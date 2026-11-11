package main

import (
	"net/http"
	"sort"
	"time"
)

type presenceRecord struct {
	OwnerKey    string
	Label       string
	ClientType  string
	Permission  string
	Persistent  bool
	ConnectedAt time.Time
	LastSeen    time.Time
	Streams     int
}

type presenceClientView struct {
	Label       string `json:"label"`
	ClientType  string `json:"client_type"`
	Permission  string `json:"permission"`
	Persistent  bool   `json:"persistent"`
	Current     bool   `json:"current"`
	ConnectedAt int64  `json:"connected_at"`
	LastSeenAt  int64  `json:"last_seen_at"`
}

type presenceStatusResponse struct {
	Status    string               `json:"status"`
	Clients   []presenceClientView `json:"clients"`
	Operation *operationLeaseView  `json:"operation,omitempty"`
}

func requestPresenceIdentity(r *http.Request) (presenceRecord, bool) {
	if isWebUIAuthorized(r) {
		cookie, _ := r.Cookie(webUISessionCookieName)
		ownerKey := "webui"
		if cookie != nil && cookie.Value != "" {
			ownerKey = "webui:" + trustedSessionKey(cookie.Value)[:16]
		}
		return presenceRecord{OwnerKey: ownerKey, Label: "Local Module WebUI", ClientType: "webui", Permission: "admin"}, true
	}
	if session, _, ok := trustedSessionForRequest(r); ok {
		clientType := "temporary_browser"
		if session.Persistent {
			clientType = "trusted_browser"
		}
		return presenceRecord{
			OwnerKey: session.ID, Label: cleanClientLabel(session.ClientLabel), ClientType: clientType,
			Permission: session.Permission, Persistent: session.Persistent,
		}, true
	}
	clientIP := getIP(r)
	token := r.Header.Get("X-Boot-Creator-Token")
	stateMu.Lock()
	valid := pairedIP != "" && pairedIP == clientIP && secureEqual(pairedToken, token)
	if valid {
		pairedLastSeen = time.Now()
	}
	stateMu.Unlock()
	if valid {
		return presenceRecord{OwnerKey: "legacy:" + clientIP, Label: "Legacy BAS website", ClientType: "legacy_browser", Permission: "admin"}, true
	}
	return presenceRecord{}, false
}

func presenceOpen(r *http.Request) string {
	identity, ok := requestPresenceIdentity(r)
	if !ok || identity.OwnerKey == "" {
		return ""
	}
	now := time.Now()
	presenceMu.Lock()
	record := presenceClients[identity.OwnerKey]
	if record == nil {
		record = &presenceRecord{OwnerKey: identity.OwnerKey, ConnectedAt: now}
		presenceClients[identity.OwnerKey] = record
	}
	record.Label = identity.Label
	record.ClientType = identity.ClientType
	record.Permission = identity.Permission
	record.Persistent = identity.Persistent
	record.LastSeen = now
	record.Streams++
	presenceMu.Unlock()
	emitLiveEvent("presence", "presence.changed")
	return identity.OwnerKey
}

func presenceTouch(ownerKey string, r *http.Request) {
	if ownerKey == "" {
		return
	}
	identity, ok := requestPresenceIdentity(r)
	if !ok || identity.OwnerKey != ownerKey {
		return
	}
	presenceMu.Lock()
	if record := presenceClients[ownerKey]; record != nil {
		record.Label = identity.Label
		record.ClientType = identity.ClientType
		record.Permission = identity.Permission
		record.Persistent = identity.Persistent
		record.LastSeen = time.Now()
	}
	presenceMu.Unlock()
}

func presenceClose(ownerKey string) {
	if ownerKey == "" {
		return
	}
	changed := false
	presenceMu.Lock()
	if record := presenceClients[ownerKey]; record != nil {
		record.Streams--
		if record.Streams <= 0 {
			delete(presenceClients, ownerKey)
		}
		changed = true
	}
	presenceMu.Unlock()
	if changed {
		emitLiveEvent("presence", "presence.changed")
	}
}

func presenceStatusHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) || !requireAuthorization(w, r) {
		return
	}
	current, _ := requestPresenceIdentity(r)
	clients := make([]presenceClientView, 0, 4)
	presenceMu.Lock()
	for _, record := range presenceClients {
		if record == nil || record.Streams <= 0 {
			continue
		}
		clients = append(clients, presenceClientView{
			Label: record.Label, ClientType: record.ClientType, Permission: record.Permission, Persistent: record.Persistent,
			Current:     current.OwnerKey != "" && record.OwnerKey == current.OwnerKey,
			ConnectedAt: record.ConnectedAt.UnixMilli(), LastSeenAt: record.LastSeen.UnixMilli(),
		})
	}
	presenceMu.Unlock()
	sort.SliceStable(clients, func(i, j int) bool {
		if clients[i].Current != clients[j].Current {
			return clients[i].Current
		}
		return clients[i].ConnectedAt < clients[j].ConnectedAt
	})
	now := time.Now()
	operationMu.Lock()
	expired := expireOperationLocked(now)
	operation := operationViewLocked(activeOperation, current.OwnerKey)
	operationMu.Unlock()
	if expired {
		emitLiveEvent("operation", "operation.finished")
	}
	writeJSON(w, http.StatusOK, presenceStatusResponse{Status: "ok", Clients: clients, Operation: operation})
}
