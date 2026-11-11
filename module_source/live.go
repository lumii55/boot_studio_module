package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type liveEvent struct {
	ID        string            `json:"id"`
	Type      string            `json:"type"`
	Domain    string            `json:"domain,omitempty"`
	Revision  uint64            `json:"revision,omitempty"`
	Epoch     string            `json:"epoch,omitempty"`
	Revisions map[string]uint64 `json:"revisions,omitempty"`
	Timestamp int64             `json:"timestamp"`
}

type liveStateResponse struct {
	Status    string            `json:"status"`
	Epoch     string            `json:"epoch"`
	Revisions map[string]uint64 `json:"revisions"`
}

func initLiveSync() {
	value, err := randomToken(12)
	if err != nil {
		value = strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	liveMu.Lock()
	liveEpoch = "live_" + value
	liveRevisions = make(map[string]uint64)
	liveSubscribers = make(map[chan liveEvent]struct{})
	liveMu.Unlock()
	presenceMu.Lock()
	presenceClients = make(map[string]*presenceRecord)
	presenceMu.Unlock()
	operationMu.Lock()
	activeOperation = nil
	operationMu.Unlock()
}

func cloneLiveRevisionsLocked() map[string]uint64 {
	revisions := make(map[string]uint64, len(liveRevisions))
	for domain, revision := range liveRevisions {
		revisions[domain] = revision
	}
	return revisions
}

func liveSnapshot() liveStateResponse {
	liveMu.RLock()
	defer liveMu.RUnlock()
	return liveStateResponse{Status: "ok", Epoch: liveEpoch, Revisions: cloneLiveRevisionsLocked()}
}

func liveSyncEventLocked() liveEvent {
	now := time.Now()
	return liveEvent{
		ID:        liveEpoch + "_sync_" + strconv.FormatInt(now.UnixNano(), 36),
		Type:      "sync.ready",
		Epoch:     liveEpoch,
		Revisions: cloneLiveRevisionsLocked(),
		Timestamp: now.UnixMilli(),
	}
}

func emitLiveEvent(domain, eventType string) liveEvent {
	liveMu.Lock()
	liveRevisions[domain]++
	now := time.Now()
	event := liveEvent{
		ID:        liveEpoch + "_" + strconv.FormatInt(now.UnixNano(), 36),
		Type:      eventType,
		Domain:    domain,
		Revision:  liveRevisions[domain],
		Timestamp: now.UnixMilli(),
	}
	for ch := range liveSubscribers {
		select {
		case ch <- event:
		default:
			for {
				select {
				case <-ch:
				default:
					select {
					case ch <- liveSyncEventLocked():
					default:
					}
					goto nextSubscriber
				}
			}
		}
	nextSubscriber:
	}
	liveMu.Unlock()
	return event
}

func subscribeLiveEvents() chan liveEvent {
	ch := make(chan liveEvent, 32)
	liveMu.Lock()
	liveSubscribers[ch] = struct{}{}
	liveMu.Unlock()
	return ch
}

func unsubscribeLiveEvents(ch chan liveEvent) {
	liveMu.Lock()
	delete(liveSubscribers, ch)
	liveMu.Unlock()
}

func liveRevisionsHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) || !requireAuthorization(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, liveSnapshot())
}

func clearStreamingWriteDeadline(w http.ResponseWriter) error {
	return http.NewResponseController(w).SetWriteDeadline(time.Time{})
}

func liveEventsHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) || !requireAuthorization(w, r) {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Streaming unsupported"})
		return
	}
	if err := clearStreamingWriteDeadline(w); err != nil {
		writeLog("Warning: Could not clear SSE write deadline: " + err.Error())
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Connection", "keep-alive")
	_, _ = io.WriteString(w, "retry: 1000\n\n")
	presenceKey := presenceOpen(r)
	defer presenceClose(presenceKey)
	ch := subscribeLiveEvents()
	defer unsubscribeLiveEvents(ch)
	snapshot := liveSnapshot()
	initial, _ := json.Marshal(map[string]any{"type": "sync.ready", "epoch": snapshot.Epoch, "revisions": snapshot.Revisions, "timestamp": time.Now().UnixMilli()})
	_, _ = fmt.Fprintf(w, "data: %s\n\n", initial)
	flusher.Flush()
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case event := <-ch:
			if !isAuthorized(r) {
				return
			}
			presenceTouch(presenceKey, r)
			data, err := json.Marshal(event)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "id: %s\ndata: %s\n\n", event.ID, data); err != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if !isAuthorized(r) {
				return
			}
			presenceTouch(presenceKey, r)
			if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func liveMutationHandler(domain, eventType string, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		recorder := &auditStatusWriter{ResponseWriter: w}
		handler(recorder, r)
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		if status >= 200 && status < 400 {
			emitLiveEvent(domain, eventType)
		}
	}
}

func emitLiveAuditEvent(category, action string) {
	switch action {
	case "animation.applied", "animation.restored_default":
		emitLiveEvent("animation", action)
		emitLiveEvent("history", "history.changed")
	case "history.applied":
		emitLiveEvent("animation", action)
		emitLiveEvent("history", "history.changed")
	case "history.deleted":
		emitLiveEvent("history", "history.changed")
	case "playlist.applied":
		emitLiveEvent("animation", action)
	case "rotation.configured", "rotation.next_prepared", "rotation.pause_changed":
		emitLiveEvent("rotation", "rotation.changed")
	case "queue.changed", "queue.cleared", "queue.next_skipped":
		emitLiveEvent("queue", "queue.changed")
	case "module.rescan":
		emitLiveEvent("device", "device.changed")
		emitLiveEvent("health", "health.changed")
	case "module.reset", "module.factory_reset":
		emitLiveEvent("device", "device.changed")
		emitLiveEvent("health", "health.changed")
		emitLiveEvent("animation", "animation.changed")
	case "test.applied":
		emitLiveEvent("test", "test.changed")
		emitLiveEvent("animation", "animation.changed")
		emitLiveEvent("history", "history.changed")
	default:
		if strings.TrimSpace(category) != "" {
			emitLiveEvent(category, action)
		}
	}
}
