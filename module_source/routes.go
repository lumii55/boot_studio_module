package main

import (
	"net"
	"net/http"
	"time"
)

// newCompanionMux owns the route table; tests can examine it without binding a port.
func newCompanionMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/webui", webUIRootHandler)
	mux.HandleFunc("/webui/", webUIRootHandler)
	mux.HandleFunc("/webui/session", webUISessionHandler)
	mux.HandleFunc("/webui/request_auth", webUIRequestAuthHandler)
	mux.HandleFunc("/webui/logout", webUILogoutHandler)
	mux.HandleFunc("/webui/logs", webUILogsHandler)
	mux.HandleFunc("/webui/logs/download", webUILogsDownloadHandler)

	mux.HandleFunc("/transport/identity", transportIdentityHandler)
	mux.HandleFunc("/trust", trustLandingHandler)
	mux.HandleFunc("/info", infoHandler)
	mux.HandleFunc("/ping", pingHandler)
	mux.HandleFunc("/live/revisions", liveRevisionsHandler)
	mux.HandleFunc("/live/events", liveEventsHandler)
	mux.HandleFunc("/presence/status", presenceStatusHandler)
	mux.HandleFunc("/request_auth", requestAuthHandler)
	mux.HandleFunc("/auth/start", authStartHandler)
	mux.HandleFunc("/auth/status", authStatusHandler)
	mux.HandleFunc("/auth/trusted/challenge", trustedAuthChallengeHandler)
	mux.HandleFunc("/auth/trusted/verify", trustedAuthVerifyHandler)
	mux.HandleFunc("/auth_callback", authCallbackHandler)
	mux.HandleFunc("/trust/clients", trustClientsHandler)
	mux.HandleFunc("/trust/sessions", trustSessionsHandler)
	mux.HandleFunc("/trust/permission", liveMutationHandler("trust", "trust.changed", trustPermissionHandler))
	mux.HandleFunc("/trust/revoke", liveMutationHandler("trust", "trust.changed", trustRevokeHandler))
	mux.HandleFunc("/trust/revoke-all", liveMutationHandler("trust", "trust.changed", trustRevokeAllHandler))
	mux.HandleFunc("/trust/session/disconnect", liveMutationHandler("sessions", "sessions.changed", trustSessionDisconnectHandler))
	mux.HandleFunc("/trust/sessions/disconnect-all", liveMutationHandler("sessions", "sessions.changed", trustSessionsDisconnectAllHandler))
	mux.HandleFunc("/audit/list", securityAuditListHandler)
	mux.HandleFunc("/audit/clear", securityAuditClearHandler)
	mux.HandleFunc("/audit/export", securityAuditExportHandler)
	mux.HandleFunc("/pair_register", pairRegisterHandler)
	mux.HandleFunc("/pair_status", pairStatusHandler)
	mux.HandleFunc("/pair_exchange", pairExchangeHandler)
	mux.HandleFunc("/disconnect", disconnectHandler)
	mux.HandleFunc("/disconnect/beacon", disconnectBeaconHandler)
	mux.HandleFunc("/upload", coordinatedOperationHandler("apply", auditedHandler("control", "animation.applied", uploadHandler)))
	mux.HandleFunc("/remove", coordinatedOperationHandler("restore", auditedHandler("control", "animation.restored_default", removeHandler)))
	mux.HandleFunc("/pull", pullHandler)
	mux.HandleFunc("/reset", coordinatedOperationHandler("rescan", auditedHandler("system", "module.reset", resetHandler)))
	mux.HandleFunc("/rescan", coordinatedOperationHandler("rescan", auditedHandler("system", "module.rescan", rescanHandler)))
	mux.HandleFunc("/factory_reset", coordinatedOperationHandler("factory_reset", auditedHandler("system", "module.factory_reset", factoryResetHandler)))
	mux.HandleFunc("/history/list", historyListHandler)
	mux.HandleFunc("/history/items", historyItemsHandler)
	mux.HandleFunc("/history/download", historyDownloadHandler)
	mux.HandleFunc("/history/preview", historyPreviewHandler)
	mux.HandleFunc("/history/apply", coordinatedOperationHandler("apply", auditedHandler("control", "history.applied", historyApplyHandler)))
	mux.HandleFunc("/history/delete", auditedHandler("control", "history.deleted", historyDeleteHandler))
	mux.HandleFunc("/playlist/list", playlistListHandler)
	mux.HandleFunc("/playlist/create", liveMutationHandler("playlist", "playlist.changed", playlistCreateHandler))
	mux.HandleFunc("/playlist/rename", liveMutationHandler("playlist", "playlist.changed", playlistRenameHandler))
	mux.HandleFunc("/playlist/duplicate", liveMutationHandler("playlist", "playlist.changed", playlistDuplicateHandler))
	mux.HandleFunc("/playlist/delete", coordinatedOperationHandler("automation_prepare", liveMutationHandler("playlist", "playlist.changed", playlistDeleteHandler)))
	mux.HandleFunc("/playlist/reorder", liveMutationHandler("playlist", "playlist.changed", playlistReorderHandler))
	mux.HandleFunc("/playlist/item/upload", liveMutationHandler("playlist", "playlist.changed", playlistUploadHandler))
	mux.HandleFunc("/playlist/item/from-history", liveMutationHandler("playlist", "playlist.changed", playlistAddHistoryHandler))
	mux.HandleFunc("/playlist/item/from-staged", liveMutationHandler("playlist", "playlist.changed", playlistAddStagedHandler))
	mux.HandleFunc("/playlist/item/remove", liveMutationHandler("playlist", "playlist.changed", playlistItemRemoveHandler))
	mux.HandleFunc("/playlist/item/reorder", liveMutationHandler("playlist", "playlist.changed", playlistItemReorderHandler))
	mux.HandleFunc("/playlist/preview", coordinatedOperationHandler("preview", playlistPreviewHandler))
	mux.HandleFunc("/playlist/download", playlistDownloadHandler)
	mux.HandleFunc("/playlist/apply", coordinatedOperationHandler("apply", auditedHandler("control", "playlist.applied", playlistApplyHandler)))
	mux.HandleFunc("/playlist/stage", coordinatedOperationHandler("preview", liveMutationHandler("test", "test.changed", playlistStageHandler)))
	mux.HandleFunc("/rotation/status", rotationStatusHandler)
	mux.HandleFunc("/rotation/configure", coordinatedOperationHandler("automation_prepare", auditedHandler("automation", "rotation.configured", rotationConfigureHandler)))
	mux.HandleFunc("/rotation/prepare-next", coordinatedOperationHandler("automation_prepare", auditedHandler("automation", "rotation.next_prepared", rotationPrepareHandler)))
	mux.HandleFunc("/rotation/pause", coordinatedOperationHandler("automation_prepare", auditedHandler("automation", "rotation.pause_changed", rotationPauseHandler)))
	mux.HandleFunc("/queue/add", coordinatedOperationHandler("automation_prepare", auditedHandler("automation", "queue.changed", bootQueueAddHandler)))
	mux.HandleFunc("/queue/use-next", coordinatedOperationHandler("automation_prepare", auditedHandler("automation", "queue.changed", bootQueueUseNextHandler)))
	mux.HandleFunc("/queue/reorder", coordinatedOperationHandler("automation_prepare", auditedHandler("automation", "queue.changed", bootQueueReorderHandler)))
	mux.HandleFunc("/queue/remove", coordinatedOperationHandler("automation_prepare", auditedHandler("automation", "queue.changed", bootQueueRemoveHandler)))
	mux.HandleFunc("/queue/clear", coordinatedOperationHandler("automation_prepare", auditedHandler("automation", "queue.cleared", bootQueueClearHandler)))
	mux.HandleFunc("/queue/skip-next", coordinatedOperationHandler("automation_prepare", auditedHandler("automation", "queue.next_skipped", bootQueueSkipNextHandler)))
	mux.HandleFunc("/activity/list", bootActivityListHandler)
	mux.HandleFunc("/activity/clear", liveMutationHandler("activity", "activity.changed", bootActivityClearHandler))
	mux.HandleFunc("/device/probes", deviceProbesHandler)
	mux.HandleFunc("/health/status", moduleHealthHandler)
	mux.HandleFunc("/health/action", coordinatedOperationHandler("maintenance", liveMutationHandler("health", "health.changed", moduleHealthActionHandler)))
	mux.HandleFunc("/test/stage", coordinatedOperationHandler("preview", liveMutationHandler("test", "test.changed", testStageHandler)))
	mux.HandleFunc("/test/status", testStatusHandler)
	mux.HandleFunc("/test/start", coordinatedOperationHandler("preview", liveMutationHandler("test", "test.changed", testStartHandler)))
	mux.HandleFunc("/test/stop", coordinatedOperationHandler("preview", liveMutationHandler("test", "test.changed", testStopHandler)))
	mux.HandleFunc("/test/apply", coordinatedOperationHandler("apply", auditedHandler("control", "test.applied", testApplyHandler)))
	mux.HandleFunc("/test/clear", coordinatedOperationHandler("preview", liveMutationHandler("test", "test.changed", testClearHandler)))
	mux.HandleFunc("/test_anim", coordinatedOperationHandler("preview", testAnimHandler))

	return mux
}

// localCompanionHTTPHandler is an additional application-layer safeguard for
// the loopback HTTP listener. The listener itself binds 127.0.0.1:4040. The
// handler also rejects spoofed Host headers (DNS rebinding) and remote peers.
// It intentionally keeps all existing local routes for the installed APK.
func localCompanionHTTPHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remoteIP := net.ParseIP(getIP(r))
		if remoteIP == nil || !remoteIP.IsLoopback() {
			http.Error(w, "Local Companion only; use HTTPS on port 4041", http.StatusForbidden)
			return
		}
		address := r.Host
		if address != "127.0.0.1:4040" && address != "localhost:4040" && address != "[::1]:4040" {
			http.Error(w, "Invalid loopback host", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func newLoopbackCompanionServer(handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              "127.0.0.1:4040",
		Handler:           localCompanionHTTPHandler(handler),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Minute,
		WriteTimeout:      30 * time.Minute,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
}
