package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type trustPublicKey struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

type trustedClientRecord struct {
	ID             string         `json:"id"`
	Label          string         `json:"label"`
	PublicKey      trustPublicKey `json:"public_key"`
	Permission     string         `json:"permission"`
	CreatedAt      int64          `json:"created_at"`
	LastApprovedAt int64          `json:"last_approved_at"`
	LastSeenAt     int64          `json:"last_seen_at"`
	LastIP         string         `json:"last_ip,omitempty"`
}

type trustState struct {
	Version int                   `json:"version"`
	Clients []trustedClientRecord `json:"clients"`
}

type trustedClientView struct {
	ID             string `json:"id"`
	Label          string `json:"label"`
	Permission     string `json:"permission"`
	CreatedAt      int64  `json:"created_at"`
	LastApprovedAt int64  `json:"last_approved_at"`
	LastSeenAt     int64  `json:"last_seen_at"`
	LastIP         string `json:"last_ip,omitempty"`
	ActiveSessions int    `json:"active_sessions"`
	Current        bool   `json:"current"`
}

type trustedSession struct {
	ID          string
	ClientID    string
	ClientLabel string
	ClientIP    string
	Permission  string
	Persistent  bool
	TokenHash   string
	CreatedAt   time.Time
	LastSeen    time.Time
}

type trustSessionView struct {
	ID         string `json:"id"`
	ClientID   string `json:"client_id,omitempty"`
	Label      string `json:"label"`
	ClientIP   string `json:"client_ip,omitempty"`
	Permission string `json:"permission"`
	Persistent bool   `json:"persistent"`
	CreatedAt  int64  `json:"created_at"`
	LastSeenAt int64  `json:"last_seen_at"`
	Current    bool   `json:"current"`
	Legacy     bool   `json:"legacy,omitempty"`
}

type trustChallenge struct {
	ID       string
	ClientID string
	ClientIP string
	Message  string
	Expires  time.Time
}

type trustApprovalInput struct {
	ClientID  string          `json:"client_id"`
	Label     string          `json:"label"`
	PublicKey *trustPublicKey `json:"public_key"`
}

func trustStatePath() string {
	return filepath.Join(ModDir, "trust", "trusted_clients.json")
}

func cleanClientLabel(value string) string {
	label := strings.TrimSpace(value)
	label = strings.Join(strings.Fields(label), " ")
	if label == "" {
		label = "Boot Animation Studio browser"
	}
	runes := []rune(label)
	if len(runes) > 80 {
		label = string(runes[:80])
	}
	return label
}

func normalizeTrustPermission(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "view":
		return "view", true
	case "control":
		return "control", true
	case "manage":
		return "manage", true
	case "admin":
		return "admin", true
	default:
		return "", false
	}
}

// Trust schema v1 predated permission tiers; its missing field meant an
// administrator-level approved client. Preserve only that documented migration.
// Every unknown, missing or malformed permission in schema v2 fails closed.
func normalizeStoredTrustPermission(version int, value string) (string, error) {
	if version == 1 && strings.TrimSpace(value) == "" {
		return "admin", nil
	}
	permission, ok := normalizeTrustPermission(value)
	if !ok {
		return "", errors.New("invalid stored trusted-client permission")
	}
	return permission, nil
}

func trustPermissionRank(value string) int {
	switch value {
	case "view":
		return 0
	case "control":
		return 1
	case "manage":
		return 2
	case "admin":
		return 3
	default:
		return -1
	}
}

func loadTrustStateLocked() (trustState, error) {
	data, err := os.ReadFile(trustStatePath())
	if os.IsNotExist(err) {
		return trustState{Version: trustStateVersion, Clients: []trustedClientRecord{}}, nil
	}
	if err != nil {
		return trustState{}, err
	}
	var state trustState
	if err := json.Unmarshal(data, &state); err != nil {
		return trustState{}, errors.New("invalid trust state")
	}
	trustVersion := classifyPersistentStateVersion(state.Version, persistentVersionPolicy{
		Domain: "trust", MinimumVersion: 1, CurrentVersion: trustStateVersion,
	})
	if trustVersion != persistentVersionCurrent && trustVersion != persistentVersionMigration {
		return trustState{}, errors.New("invalid trust state")
	}
	if state.Clients == nil {
		state.Clients = []trustedClientRecord{}
	}
	for i := range state.Clients {
		permission, permissionErr := normalizeStoredTrustPermission(state.Version, state.Clients[i].Permission)
		if permissionErr != nil {
			// Never turn an unknown persisted permission into administrator access.
			// Refuse corrupted/current-version state; the owner can restore a known-good backup.
			return trustState{}, permissionErr
		}
		state.Clients[i].Permission = permission
	}
	state.Version = trustStateVersion
	return state, nil
}

func saveTrustStateLocked(state trustState) error {
	if err := os.MkdirAll(filepath.Dir(trustStatePath()), 0700); err != nil {
		return err
	}
	state.Version = trustStateVersion
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	temp := trustStatePath() + ".tmp"
	if err := os.WriteFile(temp, append(data, '\n'), 0600); err != nil {
		return err
	}
	if err := os.Rename(temp, trustStatePath()); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return os.Chmod(trustStatePath(), 0600)
}

func trustPublicKeyValue(key trustPublicKey) (*ecdsa.PublicKey, error) {
	if key.Kty != "EC" || key.Crv != "P-256" || key.X == "" || key.Y == "" {
		return nil, errors.New("unsupported trusted-client public key")
	}
	xBytes, err := base64.RawURLEncoding.DecodeString(key.X)
	if err != nil || len(xBytes) != 32 {
		return nil, errors.New("invalid trusted-client public key")
	}
	yBytes, err := base64.RawURLEncoding.DecodeString(key.Y)
	if err != nil || len(yBytes) != 32 {
		return nil, errors.New("invalid trusted-client public key")
	}
	x := new(big.Int).SetBytes(xBytes)
	y := new(big.Int).SetBytes(yBytes)
	curve := elliptic.P256()
	if !curve.IsOnCurve(x, y) {
		return nil, errors.New("trusted-client public key is not on P-256")
	}
	return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, nil
}

func trustClientIDForKey(key trustPublicKey) (string, error) {
	if _, err := trustPublicKeyValue(key); err != nil {
		return "", err
	}
	canonical := "EC|P-256|" + key.X + "|" + key.Y
	digest := sha256.Sum256([]byte(canonical))
	encoded := base64.RawURLEncoding.EncodeToString(digest[:])
	return "bc_client_" + encoded[:32], nil
}

func validateTrustApproval(input trustApprovalInput) (trustApprovalInput, error) {
	input.ClientID = strings.TrimSpace(input.ClientID)
	input.Label = cleanClientLabel(input.Label)
	if !clientIDPattern.MatchString(input.ClientID) || input.PublicKey == nil {
		return trustApprovalInput{}, errors.New("invalid trusted-client identity")
	}
	expected, err := trustClientIDForKey(*input.PublicKey)
	if err != nil || !secureEqual(expected, input.ClientID) {
		return trustApprovalInput{}, errors.New("trusted-client identity does not match its public key")
	}
	return input, nil
}

func readOptionalTrustApproval(r *http.Request) (trustApprovalInput, bool, error) {
	if r.Body == nil || r.ContentLength == 0 {
		return trustApprovalInput{}, false, nil
	}
	defer r.Body.Close()
	var input trustApprovalInput
	if err := decodeJSONRequest(r, 32<<10, &input); err != nil {
		return trustApprovalInput{}, false, err
	}
	if strings.TrimSpace(input.ClientID) == "" && input.PublicKey == nil {
		return trustApprovalInput{}, false, nil
	}
	validated, err := validateTrustApproval(input)
	return validated, true, err
}

func upsertTrustedClient(input trustApprovalInput, clientIP, permission string) error {
	permission, ok := normalizeTrustPermission(permission)
	if !ok {
		return errors.New("invalid trusted-client permission")
	}
	now := time.Now().UnixMilli()
	trustMu.Lock()
	defer trustMu.Unlock()
	state, err := loadTrustStateLocked()
	if err != nil {
		return err
	}
	for i := range state.Clients {
		if state.Clients[i].ID == input.ClientID {
			state.Clients[i].Label = cleanClientLabel(input.Label)
			state.Clients[i].PublicKey = *input.PublicKey
			state.Clients[i].Permission = permission
			state.Clients[i].LastApprovedAt = now
			state.Clients[i].LastSeenAt = now
			state.Clients[i].LastIP = clientIP
			return saveTrustStateLocked(state)
		}
	}
	if len(state.Clients) >= trustClientLimit {
		return errors.New("trusted-client limit reached")
	}
	state.Clients = append(state.Clients, trustedClientRecord{
		ID: input.ClientID, Label: cleanClientLabel(input.Label), PublicKey: *input.PublicKey, Permission: permission,
		CreatedAt: now, LastApprovedAt: now, LastSeenAt: now, LastIP: clientIP,
	})
	return saveTrustStateLocked(state)
}

func trustedClientByID(clientID string) (trustedClientRecord, bool, error) {
	trustMu.Lock()
	defer trustMu.Unlock()
	state, err := loadTrustStateLocked()
	if err != nil {
		return trustedClientRecord{}, false, err
	}
	for _, client := range state.Clients {
		if client.ID == clientID {
			return client, true, nil
		}
	}
	return trustedClientRecord{}, false, nil
}

func touchTrustedClient(clientID, clientIP string) {
	trustMu.Lock()
	defer trustMu.Unlock()
	state, err := loadTrustStateLocked()
	if err != nil {
		return
	}
	now := time.Now().UnixMilli()
	changed := false
	for i := range state.Clients {
		if state.Clients[i].ID != clientID {
			continue
		}
		if now-state.Clients[i].LastSeenAt < 30000 && state.Clients[i].LastIP == clientIP {
			return
		}
		state.Clients[i].LastSeenAt = now
		state.Clients[i].LastIP = clientIP
		changed = true
		break
	}
	if changed {
		_ = saveTrustStateLocked(state)
	}
}

func trustedSessionKey(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

func issueTrustedSession(clientID, clientLabel, clientIP, permission string, persistent bool) (string, error) {
	token, err := randomToken(32)
	if err != nil {
		return "", err
	}
	sessionID, err := randomToken(18)
	if err != nil {
		return "", err
	}
	permission, ok := normalizeTrustPermission(permission)
	if !ok {
		return "", errors.New("invalid trusted-client permission")
	}
	if persistent {
		trustMu.Lock()
		state, err := loadTrustStateLocked()
		if err != nil {
			trustMu.Unlock()
			return "", err
		}
		found := false
		for _, client := range state.Clients {
			if client.ID != clientID {
				continue
			}
			clientLabel = client.Label
			permission = client.Permission
			found = true
			break
		}
		if !found {
			trustMu.Unlock()
			return "", errors.New("trusted client not found")
		}
		now := time.Now()
		session := &trustedSession{ID: "bas_session_" + sessionID, ClientID: clientID, ClientLabel: cleanClientLabel(clientLabel), ClientIP: clientIP, Permission: permission, Persistent: true, TokenHash: trustedSessionKey(token), CreatedAt: now, LastSeen: now}
		trustedSessionMu.Lock()
		if len(trustedSessions) >= 64 {
			oldestKey := ""
			var oldest time.Time
			for key, item := range trustedSessions {
				if oldestKey == "" || item.CreatedAt.Before(oldest) {
					oldestKey = key
					oldest = item.CreatedAt
				}
			}
			if oldestKey != "" {
				delete(trustedSessions, oldestKey)
			}
		}
		trustedSessions[session.TokenHash] = session
		trustedSessionMu.Unlock()
		trustMu.Unlock()
		touchTrustedClient(clientID, clientIP)
		return token, nil
	}
	now := time.Now()
	session := &trustedSession{ID: "bas_session_" + sessionID, ClientID: clientID, ClientLabel: cleanClientLabel(clientLabel), ClientIP: clientIP, Permission: permission, Persistent: false, TokenHash: trustedSessionKey(token), CreatedAt: now, LastSeen: now}
	trustedSessionMu.Lock()
	if len(trustedSessions) >= 64 {
		oldestKey := ""
		var oldest time.Time
		for key, item := range trustedSessions {
			if oldestKey == "" || item.CreatedAt.Before(oldest) {
				oldestKey = key
				oldest = item.CreatedAt
			}
		}
		if oldestKey != "" {
			delete(trustedSessions, oldestKey)
		}
	}
	trustedSessions[session.TokenHash] = session
	trustedSessionMu.Unlock()
	touchTrustedClient(clientID, clientIP)
	return token, nil
}

func trustedSessionForToken(clientID, clientIP, token string) (*trustedSession, string, bool) {
	if !clientIDPattern.MatchString(clientID) || token == "" {
		return nil, "", false
	}
	key := trustedSessionKey(token)
	trustedSessionMu.RLock()
	session := trustedSessions[key]
	if session == nil || session.ClientID != clientID || session.ClientIP != clientIP {
		trustedSessionMu.RUnlock()
		return nil, "", false
	}
	copy := *session
	trustedSessionMu.RUnlock()
	return &copy, key, true
}

func trustedSessionForRequest(r *http.Request) (*trustedSession, string, bool) {
	clientID := strings.TrimSpace(r.Header.Get("X-Boot-Creator-Client-ID"))
	token := r.Header.Get("X-Boot-Creator-Token")
	if !clientIDPattern.MatchString(clientID) || token == "" {
		return nil, "", false
	}
	key := trustedSessionKey(token)
	clientIP := getIP(r)
	trustedSessionMu.Lock()
	session := trustedSessions[key]
	if session == nil || session.ClientID != clientID || session.ClientIP != clientIP {
		trustedSessionMu.Unlock()
		return nil, "", false
	}
	session.LastSeen = time.Now()
	copy := *session
	trustedSessionMu.Unlock()
	touchTrustedClient(clientID, clientIP)
	return &copy, key, true
}

func revokeTrustedSessionFromRequest(r *http.Request) bool {
	clientID := strings.TrimSpace(r.Header.Get("X-Boot-Creator-Client-ID"))
	token := r.Header.Get("X-Boot-Creator-Token")
	if !clientIDPattern.MatchString(clientID) || token == "" {
		return false
	}
	key := trustedSessionKey(token)
	clientIP := getIP(r)
	trustedSessionMu.Lock()
	defer trustedSessionMu.Unlock()
	session := trustedSessions[key]
	if session == nil || session.ClientID != clientID || session.ClientIP != clientIP {
		return false
	}
	delete(trustedSessions, key)
	return true
}

func revokeTrustedClientSessions(clientID string) {
	trustedSessionMu.Lock()
	defer trustedSessionMu.Unlock()
	for key, session := range trustedSessions {
		if session.ClientID == clientID {
			delete(trustedSessions, key)
		}
	}
}

func clearTrustedSessions() {
	trustedSessionMu.Lock()
	trustedSessions = make(map[string]*trustedSession)
	trustedSessionMu.Unlock()
}

func disconnectTrustedSessionByID(sessionID string) bool {
	if !trustedSessionIDPattern.MatchString(sessionID) {
		return false
	}
	trustedSessionMu.Lock()
	defer trustedSessionMu.Unlock()
	for key, session := range trustedSessions {
		if session.ID == sessionID {
			delete(trustedSessions, key)
			return true
		}
	}
	return false
}

func disconnectAllWebsiteSessions() {
	clearTrustedSessions()
	stateMu.Lock()
	pairedIP = ""
	pairedToken = ""
	pairedCreatedAt = time.Time{}
	pairedLastSeen = time.Time{}
	stateMu.Unlock()
}

func currentTrustedClientID(r *http.Request) string {
	if session, _, ok := trustedSessionForRequest(r); ok {
		return session.ClientID
	}
	return ""
}

func createTrustChallenge(clientID, clientIP string) (string, string, error) {
	client, ok, err := trustedClientByID(clientID)
	if err != nil {
		return "", "", err
	}
	if !ok {
		return "", "", os.ErrNotExist
	}
	if _, err := trustPublicKeyValue(client.PublicKey); err != nil {
		return "", "", err
	}
	challengeID, err := randomToken(18)
	if err != nil {
		return "", "", err
	}
	nonce, err := randomToken(32)
	if err != nil {
		return "", "", err
	}
	message := "BAS-TRUST-V1\n" + bridgeID + "\n" + clientID + "\n" + challengeID + "\n" + nonce
	now := time.Now()
	trustChallengeMu.Lock()
	for id, challenge := range trustChallenges {
		if now.After(challenge.Expires) {
			delete(trustChallenges, id)
		}
	}
	if len(trustChallenges) >= trustChallengeLimit {
		oldestID := ""
		var earliest time.Time
		for id, challenge := range trustChallenges {
			if oldestID == "" || challenge.Expires.Before(earliest) {
				oldestID = id
				earliest = challenge.Expires
			}
		}
		if oldestID != "" {
			delete(trustChallenges, oldestID)
		}
	}
	trustChallenges[challengeID] = &trustChallenge{ID: challengeID, ClientID: clientID, ClientIP: clientIP, Message: message, Expires: now.Add(trustChallengeTTL)}
	trustChallengeMu.Unlock()
	return challengeID, message, nil
}

func verifyTrustChallenge(input trustVerifyInput, clientIP string) (string, error) {
	input.ClientID = strings.TrimSpace(input.ClientID)
	input.ChallengeID = strings.TrimSpace(input.ChallengeID)
	if !clientIDPattern.MatchString(input.ClientID) || input.ChallengeID == "" || input.Signature == "" {
		return "", errors.New("invalid trusted-client proof")
	}
	trustChallengeMu.Lock()
	challenge := trustChallenges[input.ChallengeID]
	delete(trustChallenges, input.ChallengeID)
	trustChallengeMu.Unlock()
	if challenge == nil || challenge.ClientID != input.ClientID || challenge.ClientIP != clientIP || time.Now().After(challenge.Expires) {
		return "", errors.New("trusted-client challenge is invalid or expired")
	}
	client, ok, err := trustedClientByID(input.ClientID)
	if err != nil || !ok {
		return "", errors.New("trusted client not found")
	}
	publicKey, err := trustPublicKeyValue(client.PublicKey)
	if err != nil {
		return "", err
	}
	signature, err := base64.RawURLEncoding.DecodeString(input.Signature)
	if err != nil || len(signature) != 64 {
		return "", errors.New("invalid trusted-client signature")
	}
	digest := sha256.Sum256([]byte(challenge.Message))
	r := new(big.Int).SetBytes(signature[:32])
	s := new(big.Int).SetBytes(signature[32:])
	if !ecdsa.Verify(publicKey, digest[:], r, s) {
		return "", errors.New("trusted-client signature verification failed")
	}
	return issueTrustedSession(input.ClientID, client.Label, clientIP, client.Permission, true)
}
