package main

import (
	"net/http"
	"strconv"
	"time"
)

type operationLease struct {
	ID         string
	Type       string
	OwnerKey   string
	OwnerLabel string
	OwnerType  string
	Permission string
	StartedAt  time.Time
	ExpiresAt  time.Time
}

type operationLeaseView struct {
	Type         string `json:"type"`
	OwnerLabel   string `json:"owner_label"`
	OwnerType    string `json:"owner_type"`
	Permission   string `json:"permission"`
	CurrentOwner bool   `json:"current_owner"`
	StartedAt    int64  `json:"started_at"`
	ExpiresAt    int64  `json:"expires_at"`
}

func operationViewLocked(lease *operationLease, currentOwnerKey string) *operationLeaseView {
	if lease == nil {
		return nil
	}
	return &operationLeaseView{
		Type: lease.Type, OwnerLabel: lease.OwnerLabel, OwnerType: lease.OwnerType, Permission: lease.Permission,
		CurrentOwner: currentOwnerKey != "" && lease.OwnerKey == currentOwnerKey,
		StartedAt:    lease.StartedAt.UnixMilli(), ExpiresAt: lease.ExpiresAt.UnixMilli(),
	}
}

func expireOperationLocked(now time.Time) bool {
	if activeOperation != nil && !activeOperation.ExpiresAt.After(now) {
		activeOperation = nil
		return true
	}
	return false
}

func acquireOperation(r *http.Request, operationType string) (*operationLease, *operationLeaseView, bool) {
	identity, ok := requestPresenceIdentity(r)
	if !ok {
		return nil, nil, false
	}
	now := time.Now()
	operationMu.Lock()
	expired := expireOperationLocked(now)
	if activeOperation != nil {
		conflict := operationViewLocked(activeOperation, identity.OwnerKey)
		operationMu.Unlock()
		if expired {
			emitLiveEvent("operation", "operation.changed")
		}
		return nil, conflict, false
	}
	id, err := randomToken(12)
	if err != nil {
		id = strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	lease := &operationLease{
		ID: "op_" + id, Type: operationType, OwnerKey: identity.OwnerKey, OwnerLabel: identity.Label,
		OwnerType: identity.ClientType, Permission: identity.Permission, StartedAt: now, ExpiresAt: now.Add(operationLeaseTTL),
	}
	activeOperation = lease
	operationMu.Unlock()
	if expired {
		emitLiveEvent("operation", "operation.changed")
	}
	emitLiveEvent("operation", "operation.started")
	return lease, nil, true
}

func releaseOperation(id string) {
	if id == "" {
		return
	}
	changed := false
	operationMu.Lock()
	if activeOperation != nil && activeOperation.ID == id {
		activeOperation = nil
		changed = true
	}
	operationMu.Unlock()
	if changed {
		emitLiveEvent("operation", "operation.finished")
	}
}

func coordinatedOperationHandler(operationType string, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		permission, authorized := requestAuthorizationPermission(r)
		if !authorized || trustPermissionRank(permission) < trustPermissionRank(requestRequiredPermission(r)) {
			handler(w, r)
			return
		}
		lease, conflict, acquired := acquireOperation(r, operationType)
		if !acquired {
			if conflict == nil {
				handler(w, r)
				return
			}
			message := "Another device operation is already in progress"
			if conflict.OwnerLabel != "" {
				message += " by " + conflict.OwnerLabel
			}
			writeJSON(w, http.StatusConflict, map[string]any{"status": "busy", "message": message, "operation": conflict})
			return
		}
		defer releaseOperation(lease.ID)
		handler(w, r)
	}
}
