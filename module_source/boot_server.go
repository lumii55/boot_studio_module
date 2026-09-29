package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color/palette"
	"image/draw"
	"image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	ModDir                          = "/data/adb/modules/boot_creator"
	maxBootAnimationBytes           = int64(2) << 30
	directUploadWarningBytes        = int64(25) << 20
	maxPreviewBytes                 = int64(6) << 20
	maxRequestBytes                 = maxBootAnimationBytes + maxPreviewBytes + (16 << 20)
	maxZipUncompressedSize   uint64 = 8 << 30
	maxSavedPaths                   = 64
	historyLimit                    = 5
	historyMetadataVersion          = 1
	playlistStateVersion            = 1
	rotationStateVersion            = 3
	bootActivityStateVersion        = 1
	bootActivityLimit               = 50
	apiVersion                      = 1
	companionVersionCode            = 7
	webUISessionCookieName          = "boot_creator_webui"
	webUISessionTTL                 = 8 * time.Hour
	webUILogTailBytes        int64  = 512 << 10
	bridgeIdentityFile              = "bridge_id"
	trustStateVersion               = 2
	trustClientLimit                = 24
	trustChallengeTTL               = 90 * time.Second
	trustChallengeLimit             = 64
	securityAuditVersion            = 1
	securityAuditLimit              = 200
	operationLeaseTTL               = 30 * time.Minute
)

var (
	deviceModel             = "Unknown Device"
	deviceResolution        = "Unknown"
	bridgeID                string
	stateMu                 sync.RWMutex
	pairedIP                string
	pairedToken             string
	pairedCreatedAt         time.Time
	pairedLastSeen          time.Time
	pendingAuth             *authRequest
	pendingAsyncAuth        *asyncAuthRequest
	previewMu               sync.Mutex
	previewActive           bool
	previewTargetPaths      []string
	previewCancel           chan struct{}
	historyPreviewMu        sync.Mutex
	playlistMu              sync.Mutex
	playlistPreviewMu       sync.Mutex
	rotationMu              sync.Mutex
	bootActivityMu          sync.Mutex
	pendingPair             *pairRegistration
	webUISessionMu          sync.Mutex
	webUISessions           = make(map[string]time.Time)
	trustMu                 sync.Mutex
	trustedSessionMu        sync.RWMutex
	trustedSessions         = make(map[string]*trustedSession)
	trustChallengeMu        sync.Mutex
	trustChallenges         = make(map[string]*trustChallenge)
	securityAuditMu         sync.Mutex
	liveMu                  sync.RWMutex
	liveEpoch               string
	liveRevisions           = make(map[string]uint64)
	liveSubscribers         = make(map[chan liveEvent]struct{})
	presenceMu              sync.Mutex
	presenceClients         = make(map[string]*presenceRecord)
	operationMu             sync.Mutex
	activeOperation         *operationLease
	pairTokenPattern        = regexp.MustCompile(`^[A-Za-z0-9_-]{40,64}$`)
	historyIDPattern        = regexp.MustCompile(`^[0-9]{10,19}$`)
	playlistIDPattern       = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)
	playlistObjectPattern   = regexp.MustCompile(`^[a-f0-9]{64}$`)
	resolutionPattern       = regexp.MustCompile(`[0-9]+x[0-9]+`)
	bridgeIDPattern         = regexp.MustCompile(`^bc_[A-Za-z0-9_-]{20,64}$`)
	clientIDPattern         = regexp.MustCompile(`^bc_client_[A-Za-z0-9_-]{24,48}$`)
	trustedSessionIDPattern = regexp.MustCompile(`^bas_session_[A-Za-z0-9_-]{16,64}$`)
)

var webUIHTML = "<!doctype html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n<meta name=\"viewport\" content=\"width=device-width,initial-scale=1,viewport-fit=cover\">\n<meta name=\"color-scheme\" content=\"light dark\">\n<title>Boot Animation Studio — Module WebUI</title>\n<style>\n:root{font-family:Inter,system-ui,-apple-system,BlinkMacSystemFont,\"Segoe UI\",sans-serif;color-scheme:light dark;--bg:#f5f7fb;--panel:#fff;--panel2:#f0f3f8;--text:#172033;--muted:#657087;--line:#dce2ec;--accent:#3b6df6;--danger:#c83434;--ok:#178553;--warn:#a66b00;--shadow:0 12px 34px rgba(22,34,60,.08);--radius:18px}\n@media(prefers-color-scheme:dark){:root{--bg:#10131a;--panel:#181d27;--panel2:#202633;--text:#edf1f8;--muted:#9ca8bd;--line:#313949;--accent:#7d9cff;--danger:#ff7676;--ok:#5dd49a;--warn:#efb34d;--shadow:0 14px 36px rgba(0,0,0,.28)}}\n*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--text);min-height:100vh}button,input,select{font:inherit}button{cursor:pointer}.app{max-width:1180px;margin:0 auto;padding:18px 14px 48px}.topbar{display:flex;gap:12px;align-items:flex-start;justify-content:space-between;margin-bottom:16px}.title h1{font-size:1.3rem;margin:0 0 4px}.title p{margin:0;color:var(--muted);font-size:.9rem}.top-actions{display:flex;gap:8px;align-items:center;flex-wrap:wrap;justify-content:flex-end}.status-pill{display:inline-flex;align-items:center;gap:7px;padding:7px 10px;border-radius:999px;background:var(--panel);border:1px solid var(--line);font-size:.82rem}.dot{width:8px;height:8px;border-radius:50%;background:var(--muted)}.dot.ok{background:var(--ok)}.dot.warn{background:var(--warn)}select,input[type=text],input[type=file]{background:var(--panel);color:var(--text);border:1px solid var(--line);border-radius:10px;padding:9px 10px;max-width:100%}.btn{border:1px solid var(--line);background:var(--panel);color:var(--text);border-radius:10px;padding:9px 12px;min-height:40px}.btn:hover{border-color:var(--accent)}.btn.primary{background:var(--accent);color:#fff;border-color:transparent}.btn.danger{color:var(--danger)}.btn.small{min-height:34px;padding:6px 9px;font-size:.84rem}.nav{display:flex;gap:7px;overflow:auto;padding:4px 0 12px;scrollbar-width:thin}.nav button{white-space:nowrap;border:1px solid var(--line);background:var(--panel);color:var(--muted);padding:9px 12px;border-radius:999px}.nav button.active{background:var(--accent);border-color:var(--accent);color:#fff}.panel{background:var(--panel);border:1px solid var(--line);border-radius:var(--radius);padding:16px;box-shadow:var(--shadow);margin-bottom:14px}.panel h2{font-size:1.04rem;margin:0 0 5px}.panel h3{font-size:.93rem;margin:0}.sub{color:var(--muted);font-size:.86rem;margin:0 0 13px}.grid{display:grid;gap:10px}.grid.cards{grid-template-columns:repeat(auto-fit,minmax(180px,1fr))}.card{background:var(--panel2);border:1px solid var(--line);border-radius:14px;padding:12px;min-width:0}.card .label{font-size:.74rem;text-transform:uppercase;letter-spacing:.05em;color:var(--muted);margin-bottom:5px}.card .value{font-weight:700;word-break:break-word}.row{display:flex;gap:8px;align-items:center;flex-wrap:wrap}.row.between{justify-content:space-between}.stack{display:grid;gap:9px}.muted{color:var(--muted)}.smalltxt{font-size:.82rem}.sep{height:1px;background:var(--line);margin:13px 0}.list{display:grid;gap:9px}.item{border:1px solid var(--line);border-radius:13px;padding:11px;background:var(--panel2)}.item-title{font-weight:700;word-break:break-word}.item-meta{font-size:.8rem;color:var(--muted);margin-top:4px}.actions{display:flex;gap:6px;flex-wrap:wrap;margin-top:9px}.empty{padding:18px;text-align:center;color:var(--muted);border:1px dashed var(--line);border-radius:12px}.hidden{display:none!important}.auth{max-width:560px;margin:12vh auto 0}.auth h2{margin-top:0}.auth-note{background:var(--panel2);border-radius:12px;padding:11px;color:var(--muted);font-size:.87rem}.toast{position:fixed;left:50%;bottom:22px;transform:translateX(-50%) translateY(30px);opacity:0;pointer-events:none;transition:.2s;background:var(--text);color:var(--bg);padding:10px 14px;border-radius:11px;box-shadow:var(--shadow);max-width:min(92vw,620px);z-index:50}.toast.show{transform:translateX(-50%) translateY(0);opacity:1}.toast.error{background:#8e2020;color:#fff}.toast.success{background:#176a49;color:#fff}.busy{opacity:.58;pointer-events:none}.kv{display:grid;grid-template-columns:minmax(125px,.65fr) 1fr;gap:6px 12px;font-size:.85rem}.kv div:nth-child(odd){color:var(--muted)}pre.logs{margin:0;white-space:pre-wrap;word-break:break-word;max-height:440px;overflow:auto;background:#0b0e13;color:#d8e1ef;border-radius:12px;padding:12px;font:12px/1.5 ui-monospace,SFMono-Regular,Consolas,monospace}.progress{height:4px;background:var(--line);border-radius:999px;overflow:hidden;margin-top:8px}.progress>i{display:block;width:35%;height:100%;background:var(--accent);animation:slide 1s ease-in-out infinite}@keyframes slide{0%{transform:translateX(-100%)}100%{transform:translateX(300%)}}dialog{border:1px solid var(--line);border-radius:16px;background:var(--panel);color:var(--text);padding:16px;max-width:min(92vw,460px);box-shadow:var(--shadow)}dialog::backdrop{background:rgba(0,0,0,.45)}.filebox{border:1px dashed var(--line);border-radius:12px;padding:12px}.badge{display:inline-flex;padding:3px 7px;border-radius:999px;background:var(--panel);border:1px solid var(--line);font-size:.72rem;color:var(--muted)}.badge.ok{color:var(--ok)}.badge.warn{color:var(--warn)}.preview{max-width:100%;max-height:260px;border-radius:10px;background:#000;display:block;margin-top:9px}.queue-index{font-variant-numeric:tabular-nums;color:var(--muted);font-size:.78rem}.danger-zone{border-color:color-mix(in srgb,var(--danger) 35%,var(--line))}.danger-zone h2{color:var(--danger)}\n@media(max-width:640px){.app{padding:12px 10px 36px}.topbar{display:block}.top-actions{justify-content:flex-start;margin-top:10px}.panel{padding:13px}.kv{grid-template-columns:1fr}.kv div:nth-child(odd){margin-top:5px}.actions .btn{flex:1 1 auto}.row.mobile-stack{align-items:stretch}.row.mobile-stack>*{flex:1 1 100%}}\n</style>\n</head>\n<body>\n<div class=\"app\">\n  <section id=\"auth-screen\" class=\"auth panel hidden\">\n    <h2 data-i18n=\"authTitle\">Authorize Module WebUI</h2>\n    <p class=\"sub\" data-i18n=\"authText\">This local interface requires approval from the Companion app. Authorization is separate from the Boot Animation Studio website session.</p>\n    <div class=\"auth-note\" data-i18n=\"authNote\">The WebUI is available only from this Android device. Remote access requires explicit trusted-client support.</div>\n    <div class=\"actions\"><button id=\"authorize\" class=\"btn primary\" data-i18n=\"authorize\">Request authorization</button></div>\n    <div id=\"auth-progress\" class=\"progress hidden\"><i></i></div>\n  </section>\n\n  <main id=\"main-ui\" class=\"hidden\">\n    <header class=\"topbar\">\n      <div class=\"title\"><h1>Boot Animation Studio — Module WebUI</h1><p data-i18n=\"subtitle\">Local administration for the Companion Module.</p></div>\n      <div class=\"top-actions\">\n        <span class=\"status-pill\"><span id=\"status-dot\" class=\"dot\"></span><span id=\"status-text\" data-i18n=\"loading\">Loading…</span></span>\n        <select id=\"language\" aria-label=\"Language\"><option value=\"en\">English</option><option value=\"pt\">Português</option><option value=\"es\">Español</option><option value=\"fr\">Français</option><option value=\"de\">Deutsch</option><option value=\"it\">Italiano</option><option value=\"ja\">日本語</option><option value=\"zh\">简体中文</option></select>\n        <button id=\"refresh-all\" class=\"btn small\" data-i18n=\"refresh\">Refresh</button>\n        <button id=\"logout\" class=\"btn small\" data-i18n=\"signOut\">Sign out</button>\n      </div>\n    </header>\n\n    <nav class=\"nav\" aria-label=\"Module sections\">\n      <button data-tab=\"overview\" class=\"active\" data-i18n=\"navOverview\">Overview</button>\n      <button data-tab=\"install\" data-i18n=\"navInstall\">Install & Test</button>\n      <button data-tab=\"history\" data-i18n=\"navHistory\">History</button>\n      <button data-tab=\"playlists\" data-i18n=\"navPlaylists\">Playlists</button>\n      <button data-tab=\"automation\" data-i18n=\"navAutomation\">Automation</button>\n      <button data-tab=\"device\" data-i18n=\"navDevice\">Device</button>\n      <button data-tab=\"logs\" data-i18n=\"navLogs\">Logs</button>\n    </nav>\n\n    <section data-page=\"overview\">\n      <div class=\"panel\"><h2 data-i18n=\"overviewTitle\">Module overview</h2><p class=\"sub\" data-i18n=\"overviewText\">Current bridge status and common device actions.</p><div id=\"overview-cards\" class=\"grid cards\"></div></div>\n      <div class=\"panel\"><h2 data-i18n=\"quickActions\">Quick actions</h2><div class=\"actions\">\n        <button class=\"btn\" id=\"rescan\" data-i18n=\"rescan\">Rescan boot paths</button>\n        <button class=\"btn\" id=\"pull-stock\" data-i18n=\"downloadStock\">Download stock animation</button>\n        <button class=\"btn\" id=\"pull-current\" data-i18n=\"downloadCurrent\">Download current animation</button>\n        <button class=\"btn danger\" id=\"restore-stock\" data-i18n=\"restoreStock\">Restore stock animation</button>\n      </div></div>\n    </section>\n\n    <section data-page=\"install\" class=\"hidden\">\n      <div class=\"panel\"><h2 data-i18n=\"installTitle\">Install a boot animation ZIP</h2><p class=\"sub\" data-i18n=\"installText\">Choose a bootanimation.zip. Install applies it immediately and saves it to History.</p><div class=\"filebox stack\"><input id=\"install-file\" type=\"file\" accept=\".zip,application/zip\"><div id=\"install-file-meta\" class=\"smalltxt muted\" data-i18n=\"noFile\">No file selected.</div><div class=\"actions\"><button class=\"btn primary\" id=\"install-now\" data-i18n=\"installNow\">Install now</button><button class=\"btn\" id=\"stage-test\" data-i18n=\"stageTest\">Stage for test</button></div></div></div>\n      <div class=\"panel\"><h2 data-i18n=\"testLab\">Device Test Lab</h2><p class=\"sub\" data-i18n=\"testText\">Preview the staged animation on the device without permanently applying it.</p><div id=\"test-status\" class=\"grid cards\"></div><div class=\"actions\"><button class=\"btn\" id=\"test-start\" data-i18n=\"startPreview\">Start preview</button><button class=\"btn primary\" id=\"test-apply\" data-i18n=\"applyStaged\">Apply staged</button><button class=\"btn danger\" id=\"test-clear\" data-i18n=\"clearStaged\">Clear staged</button></div></div>\n    </section>\n\n    <section data-page=\"history\" class=\"hidden\"><div class=\"panel\"><div class=\"row between\"><div><h2 data-i18n=\"historyTitle\">History</h2><p class=\"sub\" data-i18n=\"historyText\">Recently applied animations stored by the module.</p></div><button class=\"btn small\" id=\"refresh-history\" data-i18n=\"refresh\">Refresh</button></div><div id=\"history-list\" class=\"list\"></div></div>\n      <div class=\"sep\"></div>\n<div class=\"panel\"><div class=\"row between\"><div><h2 data-i18n=\"activityTitle\">Boot Activity</h2><p class=\"sub\" data-i18n=\"activityText\">Observational log of prepared and completed boot plans.</p></div><div class=\"actions\"><button class=\"btn small\" id=\"refresh-activity\" data-i18n=\"refresh\">Refresh</button><button class=\"btn small danger\" id=\"clear-activity\" data-i18n=\"clear\">Clear</button></div></div><div id=\"activity-list\" class=\"list\"></div></div></section>\n\n    <section data-page=\"playlists\" class=\"hidden\">\n      <div class=\"panel\"><div class=\"row between\"><div><h2 data-i18n=\"playlistsTitle\">Playlists</h2><p class=\"sub\" data-i18n=\"playlistsText\">Persistent curated animation sets used by Rotation and next-boot controls.</p></div><button class=\"btn small\" id=\"refresh-playlists\" data-i18n=\"refresh\">Refresh</button></div>\n        <div class=\"row mobile-stack\"><select id=\"playlist-select\"></select><button class=\"btn\" id=\"playlist-create\" data-i18n=\"create\">Create</button><button class=\"btn\" id=\"playlist-rename\" data-i18n=\"rename\">Rename</button><button class=\"btn\" id=\"playlist-duplicate\" data-i18n=\"duplicate\">Duplicate</button><button class=\"btn danger\" id=\"playlist-delete\" data-i18n=\"delete\">Delete</button></div>\n        <div class=\"sep\"></div><div class=\"filebox\"><div class=\"row mobile-stack\"><input id=\"playlist-file\" type=\"file\" accept=\".zip,application/zip\"><button class=\"btn\" id=\"playlist-add-file\" data-i18n=\"addZip\">Add ZIP</button></div></div><div class=\"sep\"></div><div id=\"playlist-items\" class=\"list\"></div>\n      </div>\n    </section>\n\n    <section data-page=\"automation\" class=\"hidden\">\n      <div class=\"panel\"><h2 data-i18n=\"rotationTitle\">Rotation</h2><p class=\"sub\" data-i18n=\"rotationText\">Prepare future boots from a Playlist without changing Queue priority.</p><div class=\"row mobile-stack\"><label class=\"row\"><input id=\"rotation-enabled\" type=\"checkbox\"><span data-i18n=\"enabled\">Enabled</span></label><select id=\"rotation-playlist\"></select><select id=\"rotation-mode\"><option value=\"sequential\" data-i18n=\"sequential\">Sequential</option><option value=\"random\" data-i18n=\"random\">Random</option><option value=\"shuffle\" data-i18n=\"shuffle\">Shuffle</option></select></div><div class=\"actions\"><button class=\"btn primary\" id=\"rotation-save\" data-i18n=\"saveRotation\">Save rotation</button><button class=\"btn\" id=\"rotation-pause\" data-i18n=\"pauseResume\">Pause / Resume</button><button class=\"btn\" id=\"rotation-prepare\" data-i18n=\"prepareNext\">Prepare next</button></div><div id=\"rotation-summary\" class=\"smalltxt muted\" style=\"margin-top:10px\"></div></div>\n      <div class=\"panel\"><div class=\"row between\"><div><h2 data-i18n=\"queueTitle\">Boot Queue</h2><p class=\"sub\" data-i18n=\"queueText\">Override and queued boots take priority over normal Rotation.</p></div><div class=\"actions\"><button class=\"btn small\" id=\"queue-skip\" data-i18n=\"skipNext\">Skip next</button><button class=\"btn small danger\" id=\"queue-clear\" data-i18n=\"clearQueue\">Clear queue</button></div></div><div id=\"queue-next\" class=\"card\"></div><div class=\"sep\"></div><div id=\"queue-list\" class=\"list\"></div></div>\n    </section>\n\n    \n\n    <section data-page=\"device\" class=\"hidden\"><div class=\"panel\"><div class=\"row between\"><div><h2 data-i18n=\"deviceTitle\">Device intelligence</h2><p class=\"sub\" data-i18n=\"deviceText\">Read-only facts collected from this Android installation.</p></div><button class=\"btn small\" id=\"refresh-device\" data-i18n=\"refreshProbes\">Refresh probes</button></div><div id=\"device-summary\" class=\"grid cards\"></div><div class=\"sep\"></div><div id=\"device-details\" class=\"stack\"></div></div>\n      <div class=\"sep\"></div>\n\n      <div class=\"panel\">\n        <div class=\"row between\"><div><h2 data-i18n=\"healthTitle\">Module health</h2><p class=\"sub\" data-i18n=\"healthText\">Current integrity, storage and recovery readiness.</p></div><button class=\"btn small\" id=\"refresh-health\" data-i18n=\"healthRun\">Run checks</button></div>\n        <div id=\"health-summary\" class=\"grid cards\"></div>\n        <div class=\"sep\"></div>\n        <h3 data-i18n=\"healthChecks\">Integrity checks</h3><div id=\"health-checks\" class=\"list\" style=\"margin-top:8px\"></div>\n        <div class=\"sep\"></div>\n        <h3 data-i18n=\"healthStorage\">Storage</h3><div id=\"health-storage\" class=\"grid cards\" style=\"margin-top:8px\"></div>\n        <div class=\"sep\"></div>\n        <h3 data-i18n=\"healthMaintenance\">Safe maintenance</h3>\n        <p class=\"sub\" data-i18n=\"healthMaintenanceText\">Only disposable or unreferenced data detected by Health appears here.</p>\n        <div class=\"list\" id=\"health-maintenance\" style=\"margin-top:8px\"></div>\n        <p class=\"sub\" data-i18n=\"healthReadOnly\" style=\"margin-top:12px\">Maintenance is explicit and narrowly scoped. Health is checked again after every action.</p>\n      </div>\n    </section>\n\n    \n\n    <section data-page=\"logs\" class=\"hidden\"><div class=\"panel\"><div class=\"row between\"><div><h2 data-i18n=\"logsTitle\">Module logs</h2><p class=\"sub\" data-i18n=\"logsText\">The same current and previous boot logs exposed by the module action script.</p></div><button class=\"btn small\" id=\"refresh-logs\" data-i18n=\"refresh\">Refresh</button></div><h3 data-i18n=\"currentBoot\">Current boot</h3><pre id=\"log-current\" class=\"logs\"></pre><div class=\"actions\"><button class=\"btn small\" data-log-download=\"current\" data-i18n=\"downloadCurrentLog\">Download current log</button></div><div class=\"sep\"></div><h3 data-i18n=\"previousBoot\">Previous boot</h3><pre id=\"log-previous\" class=\"logs\"></pre><div class=\"actions\"><button class=\"btn small\" data-log-download=\"previous\" data-i18n=\"downloadPreviousLog\">Download previous log</button><button class=\"btn small\" data-log-download=\"both\" data-i18n=\"downloadBothLogs\">Download both logs</button></div></div></section>\n  </main>\n</div>\n<div id=\"toast\" class=\"toast\"></div>\n<dialog id=\"text-dialog\"><form method=\"dialog\" class=\"stack\"><h3 id=\"dialog-title\"></h3><input id=\"dialog-input\" type=\"text\" maxlength=\"80\"><div class=\"actions\"><button value=\"cancel\" class=\"btn\" data-i18n=\"cancel\">Cancel</button><button value=\"ok\" class=\"btn primary\" data-i18n=\"save\">Save</button></div></form></dialog>\n<script>\n(() => {\n'use strict';\nconst S={\"en\":{\"authTitle\":\"Authorize Module WebUI\",\"authText\":\"This local interface requires approval from the Companion app. Authorization is separate from the Boot Animation Studio website session.\",\"authNote\":\"The WebUI is available only from this Android device. Remote access requires explicit trusted-client support.\",\"authorize\":\"Request authorization\",\"subtitle\":\"Local administration for the Companion Module.\",\"loading\":\"Loading…\",\"refresh\":\"Refresh\",\"signOut\":\"Sign out\",\"navOverview\":\"Overview\",\"navInstall\":\"Install & Test\",\"navHistory\":\"History\",\"navPlaylists\":\"Playlists\",\"navAutomation\":\"Automation\",\"navActivity\":\"Boot Activity\",\"navDevice\":\"Device\",\"navLogs\":\"Logs\",\"overviewTitle\":\"Module overview\",\"overviewText\":\"Current bridge status and common device actions.\",\"quickActions\":\"Quick actions\",\"rescan\":\"Rescan boot paths\",\"downloadStock\":\"Download stock animation\",\"downloadCurrent\":\"Download current animation\",\"restoreStock\":\"Restore stock animation\",\"installTitle\":\"Install a boot animation ZIP\",\"installText\":\"Choose a bootanimation.zip. Install applies it immediately and saves it to History.\",\"noFile\":\"No file selected.\",\"installNow\":\"Install now\",\"stageTest\":\"Stage for test\",\"testLab\":\"Device Test Lab\",\"testText\":\"Preview the staged animation on the device without permanently applying it.\",\"startPreview\":\"Start preview\",\"applyStaged\":\"Apply staged\",\"clearStaged\":\"Clear staged\",\"historyTitle\":\"History\",\"historyText\":\"Recently applied animations stored by the module.\",\"playlistsTitle\":\"Playlists\",\"playlistsText\":\"Persistent curated animation sets used by Rotation and next-boot controls.\",\"create\":\"Create\",\"rename\":\"Rename\",\"duplicate\":\"Duplicate\",\"delete\":\"Delete\",\"addZip\":\"Add ZIP\",\"rotationTitle\":\"Rotation\",\"rotationText\":\"Prepare future boots from a Playlist without changing Queue priority.\",\"enabled\":\"Enabled\",\"sequential\":\"Sequential\",\"random\":\"Random\",\"shuffle\":\"Shuffle\",\"saveRotation\":\"Save rotation\",\"pauseResume\":\"Pause / Resume\",\"prepareNext\":\"Prepare next\",\"queueTitle\":\"Boot Queue\",\"queueText\":\"Override and queued boots take priority over normal Rotation.\",\"skipNext\":\"Skip next\",\"clearQueue\":\"Clear queue\",\"activityTitle\":\"Boot Activity\",\"activityText\":\"Observational log of prepared and completed boot plans.\",\"clear\":\"Clear\",\"deviceTitle\":\"Device intelligence\",\"deviceText\":\"Read-only facts collected from this Android installation.\",\"refreshProbes\":\"Refresh probes\",\"logsTitle\":\"Module logs\",\"logsText\":\"The same current and previous boot logs exposed by the module action script.\",\"currentBoot\":\"Current boot\",\"previousBoot\":\"Previous boot\",\"downloadCurrentLog\":\"Download current log\",\"downloadPreviousLog\":\"Download previous log\",\"downloadBothLogs\":\"Download both logs\",\"cancel\":\"Cancel\",\"save\":\"Save\",\"module\":\"Module\",\"device\":\"Device\",\"customAnimation\":\"Custom animation\",\"nextBoot\":\"Next boot\",\"active\":\"Active\",\"stock\":\"Stock\",\"none\":\"None\",\"unknown\":\"Unknown\",\"yes\":\"Yes\",\"no\":\"No\",\"apply\":\"Apply\",\"download\":\"Download\",\"queue\":\"Queue\",\"useNext\":\"Use next\",\"remove\":\"Remove\",\"preview\":\"Preview\",\"moveUp\":\"Up\",\"moveDown\":\"Down\",\"test\":\"Test\",\"items\":\"items\",\"emptyHistory\":\"No History entries.\",\"emptyPlaylist\":\"This playlist is empty.\",\"noPlaylists\":\"No playlists yet.\",\"emptyQueue\":\"Queue is empty.\",\"nothingPrepared\":\"Nothing prepared.\",\"emptyActivity\":\"No Boot Activity entries.\",\"confirmRestore\":\"Restore the stock boot animation now?\",\"confirmDeleteHistory\":\"Delete this History entry?\",\"confirmDeletePlaylist\":\"Delete this playlist?\",\"confirmRemoveItem\":\"Remove this item from the playlist?\",\"confirmClearQueue\":\"Clear every queued boot?\",\"confirmClearActivity\":\"Clear Boot Activity?\",\"confirmClearStaged\":\"Clear the staged test animation?\",\"authDenied\":\"Authorization failed or was denied.\",\"requestFailed\":\"Request failed.\",\"saved\":\"Saved.\",\"done\":\"Done.\",\"connected\":\"Ready\",\"offline\":\"Authorization required\",\"fileSelected\":\"Selected: {name} · {size}\",\"staged\":\"Staged\",\"notStaged\":\"Nothing staged\",\"previewActive\":\"Preview active\",\"previewInactive\":\"Preview stopped\",\"rotationPaused\":\"Rotation paused\",\"rotationRunning\":\"Rotation active\",\"rotationOff\":\"Rotation disabled\",\"preparedBy\":\"Prepared by {source}\",\"format\":\"Format\",\"resolution\":\"Resolution\",\"android\":\"Android\",\"bootTarget\":\"Boot target\",\"bootAudio\":\"Boot audio\",\"renderer\":\"Renderer\",\"mountAccess\":\"Preview mount\",\"targets\":\"Detected targets\",\"system\":\"System\",\"boot\":\"Boot\",\"logoutDone\":\"WebUI session ended.\",\"navHealth\":\"Health\",\"healthTitle\":\"Module health\",\"healthText\":\"Current integrity, storage and recovery readiness, with narrowly scoped maintenance when needed.\",\"healthRun\":\"Run checks\",\"healthOverall\":\"Overall\",\"healthChecks\":\"Integrity checks\",\"healthStorage\":\"Storage\",\"healthReadOnly\":\"Maintenance is explicit and narrowly scoped. Health is checked again after every action.\",\"healthHealthy\":\"Healthy\",\"healthAttention\":\"Attention\",\"healthDegraded\":\"Needs attention\",\"healthOkCount\":\"OK\",\"healthWarnCount\":\"Warnings\",\"healthErrorCount\":\"Problems\",\"healthFreeSpace\":\"Free space\",\"healthModuleData\":\"Module data\",\"healthFiles\":\"files\",\"healthCode\":\"Code\",\"healthCount\":\"Count\",\"healthNoData\":\"No health data yet.\",\"healthCheck_boot_paths\":\"Boot paths\",\"healthCheck_current_animation\":\"Current animation\",\"healthCheck_stock_backup\":\"Stock backup\",\"healthCheck_history_integrity\":\"History\",\"healthCheck_playlist_integrity\":\"Playlist library\",\"healthCheck_automation_integrity\":\"Rotation & Queue\",\"healthCheck_staging_integrity\":\"Test staging\",\"healthCheck_security_state\":\"Trust & audit state\",\"healthCheck_temporary_files\":\"Temporary files\",\"healthCheck_orphan_objects\":\"Unused playlist objects\",\"healthCheck_free_space\":\"Free space\",\"healthBucket_history\":\"History\",\"healthBucket_playlist\":\"Playlist library\",\"healthBucket_backup\":\"Stock backups\",\"healthBucket_staging\":\"Test staging\",\"healthBucket_trust\":\"Trust & audit\",\"healthBucket_logs\":\"Logs\",\"healthBucket_runtime\":\"Module runtime & other\",\"healthMaintenance\":\"Safe maintenance\",\"healthMaintenanceText\":\"Only disposable or unreferenced data detected by Health appears here.\",\"healthMaintenanceEmpty\":\"No safe maintenance is needed right now.\",\"healthAction_cleanup_stale_temps\":\"Clean stale temporary files\",\"healthAction_cleanup_orphan_objects\":\"Remove unused playlist objects\",\"healthAction_clear_invalid_staging\":\"Clear invalid test staging\",\"healthActionRun\":\"Run maintenance\",\"healthActionWorking\":\"Working…\",\"healthActionConfirm\":\"Run “{name}”? Only the data described by this maintenance action will be touched.\",\"healthActionDone\":\"Maintenance finished.\",\"healthActionFailed\":\"Maintenance failed.\"},\"pt\":{\"authTitle\":\"Autorizar WebUI do módulo\",\"authText\":\"Esta interface local exige aprovação pelo aplicativo Companion. A autorização é separada da sessão do site Boot Animation Studio.\",\"authNote\":\"A WebUI só fica disponível neste próprio dispositivo Android. O acesso remoto exige suporte explícito a clientes confiáveis.\",\"authorize\":\"Solicitar autorização\",\"subtitle\":\"Administração local do Companion Module.\",\"loading\":\"Carregando…\",\"refresh\":\"Atualizar\",\"signOut\":\"Sair\",\"navOverview\":\"Visão geral\",\"navInstall\":\"Instalar e testar\",\"navHistory\":\"Histórico\",\"navPlaylists\":\"Playlists\",\"navAutomation\":\"Automação\",\"navActivity\":\"Atividade de boot\",\"navDevice\":\"Dispositivo\",\"navLogs\":\"Logs\",\"overviewTitle\":\"Visão geral do módulo\",\"overviewText\":\"Estado atual da ponte e ações comuns do dispositivo.\",\"quickActions\":\"Ações rápidas\",\"rescan\":\"Redetectar caminhos de boot\",\"downloadStock\":\"Baixar animação original\",\"downloadCurrent\":\"Baixar animação atual\",\"restoreStock\":\"Restaurar animação original\",\"installTitle\":\"Instalar uma animação de boot em ZIP\",\"installText\":\"Escolha um bootanimation.zip. Instalar aplica imediatamente e salva no Histórico.\",\"noFile\":\"Nenhum arquivo selecionado.\",\"installNow\":\"Instalar agora\",\"stageTest\":\"Preparar para teste\",\"testLab\":\"Laboratório de teste no dispositivo\",\"testText\":\"Visualize a animação preparada no dispositivo sem aplicá-la permanentemente.\",\"startPreview\":\"Iniciar preview\",\"applyStaged\":\"Aplicar preparado\",\"clearStaged\":\"Limpar preparado\",\"historyTitle\":\"Histórico\",\"historyText\":\"Animações aplicadas recentemente e salvas pelo módulo.\",\"playlistsTitle\":\"Playlists\",\"playlistsText\":\"Conjuntos persistentes de animações usados pela Rotação e controles do próximo boot.\",\"create\":\"Criar\",\"rename\":\"Renomear\",\"duplicate\":\"Duplicar\",\"delete\":\"Excluir\",\"addZip\":\"Adicionar ZIP\",\"rotationTitle\":\"Rotação\",\"rotationText\":\"Prepare boots futuros a partir de uma Playlist sem alterar a prioridade da Fila.\",\"enabled\":\"Ativada\",\"sequential\":\"Sequencial\",\"random\":\"Aleatório\",\"shuffle\":\"Embaralhar\",\"saveRotation\":\"Salvar rotação\",\"pauseResume\":\"Pausar / Retomar\",\"prepareNext\":\"Preparar próxima\",\"queueTitle\":\"Fila de boot\",\"queueText\":\"Override e boots enfileirados têm prioridade sobre a Rotação normal.\",\"skipNext\":\"Pular próximo\",\"clearQueue\":\"Limpar fila\",\"activityTitle\":\"Atividade de boot\",\"activityText\":\"Registro observacional dos planos de boot preparados e concluídos.\",\"clear\":\"Limpar\",\"deviceTitle\":\"Inteligência do dispositivo\",\"deviceText\":\"Fatos somente leitura coletados desta instalação Android.\",\"refreshProbes\":\"Atualizar probes\",\"logsTitle\":\"Logs do módulo\",\"logsText\":\"Os mesmos logs do boot atual e anterior expostos pelo script de ação do módulo.\",\"currentBoot\":\"Boot atual\",\"previousBoot\":\"Boot anterior\",\"downloadCurrentLog\":\"Baixar log atual\",\"downloadPreviousLog\":\"Baixar log anterior\",\"downloadBothLogs\":\"Baixar ambos os logs\",\"cancel\":\"Cancelar\",\"save\":\"Salvar\",\"module\":\"Módulo\",\"device\":\"Dispositivo\",\"customAnimation\":\"Animação personalizada\",\"nextBoot\":\"Próximo boot\",\"active\":\"Ativa\",\"stock\":\"Original\",\"none\":\"Nenhum\",\"unknown\":\"Desconhecido\",\"yes\":\"Sim\",\"no\":\"Não\",\"apply\":\"Aplicar\",\"download\":\"Baixar\",\"queue\":\"Fila\",\"useNext\":\"Usar próximo\",\"remove\":\"Remover\",\"preview\":\"Preview\",\"moveUp\":\"Subir\",\"moveDown\":\"Descer\",\"test\":\"Testar\",\"items\":\"itens\",\"emptyHistory\":\"Nenhuma entrada no Histórico.\",\"emptyPlaylist\":\"Esta playlist está vazia.\",\"noPlaylists\":\"Nenhuma playlist ainda.\",\"emptyQueue\":\"A fila está vazia.\",\"nothingPrepared\":\"Nada preparado.\",\"emptyActivity\":\"Nenhuma Atividade de Boot.\",\"confirmRestore\":\"Restaurar a animação de boot original agora?\",\"confirmDeleteHistory\":\"Excluir esta entrada do Histórico?\",\"confirmDeletePlaylist\":\"Excluir esta playlist?\",\"confirmRemoveItem\":\"Remover este item da playlist?\",\"confirmClearQueue\":\"Limpar todos os boots da fila?\",\"confirmClearActivity\":\"Limpar a Atividade de Boot?\",\"confirmClearStaged\":\"Limpar a animação preparada para teste?\",\"authDenied\":\"A autorização falhou ou foi negada.\",\"requestFailed\":\"A solicitação falhou.\",\"saved\":\"Salvo.\",\"done\":\"Concluído.\",\"connected\":\"Pronto\",\"offline\":\"Autorização necessária\",\"fileSelected\":\"Selecionado: {name} · {size}\",\"staged\":\"Preparado\",\"notStaged\":\"Nada preparado\",\"previewActive\":\"Preview ativo\",\"previewInactive\":\"Preview parado\",\"rotationPaused\":\"Rotação pausada\",\"rotationRunning\":\"Rotação ativa\",\"rotationOff\":\"Rotação desativada\",\"preparedBy\":\"Preparado por {source}\",\"format\":\"Formato\",\"resolution\":\"Resolução\",\"android\":\"Android\",\"bootTarget\":\"Alvo de boot\",\"bootAudio\":\"Áudio de boot\",\"renderer\":\"Renderer\",\"mountAccess\":\"Mount de preview\",\"targets\":\"Alvos detectados\",\"system\":\"Sistema\",\"boot\":\"Boot\",\"logoutDone\":\"Sessão da WebUI encerrada.\",\"navHealth\":\"Saúde\",\"healthTitle\":\"Saúde do módulo\",\"healthText\":\"Integridade atual, armazenamento e prontidão para recuperação, com manutenção específica quando necessário.\",\"healthRun\":\"Executar verificações\",\"healthOverall\":\"Estado geral\",\"healthChecks\":\"Verificações de integridade\",\"healthStorage\":\"Armazenamento\",\"healthReadOnly\":\"A manutenção é explícita e bem limitada. A saúde é verificada novamente depois de cada ação.\",\"healthHealthy\":\"Saudável\",\"healthAttention\":\"Atenção\",\"healthDegraded\":\"Precisa de atenção\",\"healthOkCount\":\"OK\",\"healthWarnCount\":\"Avisos\",\"healthErrorCount\":\"Problemas\",\"healthFreeSpace\":\"Espaço livre\",\"healthModuleData\":\"Dados do módulo\",\"healthFiles\":\"arquivos\",\"healthCode\":\"Código\",\"healthCount\":\"Quantidade\",\"healthNoData\":\"Nenhum diagnóstico disponível ainda.\",\"healthCheck_boot_paths\":\"Caminhos de boot\",\"healthCheck_current_animation\":\"Animação atual\",\"healthCheck_stock_backup\":\"Backup original\",\"healthCheck_history_integrity\":\"Histórico\",\"healthCheck_playlist_integrity\":\"Biblioteca de playlists\",\"healthCheck_automation_integrity\":\"Rotação e Fila\",\"healthCheck_staging_integrity\":\"Preparação de teste\",\"healthCheck_security_state\":\"Estado de confiança e auditoria\",\"healthCheck_temporary_files\":\"Arquivos temporários\",\"healthCheck_orphan_objects\":\"Objetos de playlist não usados\",\"healthCheck_free_space\":\"Espaço livre\",\"healthBucket_history\":\"Histórico\",\"healthBucket_playlist\":\"Biblioteca de playlists\",\"healthBucket_backup\":\"Backups originais\",\"healthBucket_staging\":\"Preparação de teste\",\"healthBucket_trust\":\"Confiança e auditoria\",\"healthBucket_logs\":\"Logs\",\"healthBucket_runtime\":\"Runtime do módulo e outros\",\"healthMaintenance\":\"Manutenção segura\",\"healthMaintenanceText\":\"Só aparecem aqui dados descartáveis ou sem referência detectados pela Saúde.\",\"healthMaintenanceEmpty\":\"Nenhuma manutenção segura é necessária agora.\",\"healthAction_cleanup_stale_temps\":\"Limpar arquivos temporários antigos\",\"healthAction_cleanup_orphan_objects\":\"Remover objetos de playlist sem uso\",\"healthAction_clear_invalid_staging\":\"Limpar preparação de teste inválida\",\"healthActionRun\":\"Executar manutenção\",\"healthActionWorking\":\"Executando…\",\"healthActionConfirm\":\"Executar “{name}”? Só os dados descritos por esta ação de manutenção serão alterados.\",\"healthActionDone\":\"Manutenção concluída.\",\"healthActionFailed\":\"Falha na manutenção.\"},\"es\":{\"authTitle\":\"Autorizar WebUI del módulo\",\"authText\":\"Esta interfaz local requiere aprobación de la aplicación Companion. La autorización es independiente de la sesión del sitio Boot Animation Studio.\",\"authNote\":\"La WebUI solo está disponible desde este mismo dispositivo Android. El acceso remoto requiere soporte explícito para clientes de confianza.\",\"authorize\":\"Solicitar autorización\",\"subtitle\":\"Administración local del Companion Module.\",\"loading\":\"Cargando…\",\"refresh\":\"Actualizar\",\"signOut\":\"Cerrar sesión\",\"navOverview\":\"Resumen\",\"navInstall\":\"Instalar y probar\",\"navHistory\":\"Historial\",\"navPlaylists\":\"Playlists\",\"navAutomation\":\"Automatización\",\"navActivity\":\"Actividad de arranque\",\"navDevice\":\"Dispositivo\",\"navLogs\":\"Registros\",\"overviewTitle\":\"Resumen del módulo\",\"overviewText\":\"Estado actual del puente y acciones comunes del dispositivo.\",\"quickActions\":\"Acciones rápidas\",\"rescan\":\"Volver a detectar rutas de arranque\",\"downloadStock\":\"Descargar animación original\",\"downloadCurrent\":\"Descargar animación actual\",\"restoreStock\":\"Restaurar animación original\",\"installTitle\":\"Instalar una animación de arranque ZIP\",\"installText\":\"Elige un bootanimation.zip. Instalar lo aplica inmediatamente y lo guarda en Historial.\",\"noFile\":\"Ningún archivo seleccionado.\",\"installNow\":\"Instalar ahora\",\"stageTest\":\"Preparar para prueba\",\"testLab\":\"Laboratorio de prueba del dispositivo\",\"testText\":\"Previsualiza la animación preparada en el dispositivo sin aplicarla permanentemente.\",\"startPreview\":\"Iniciar vista previa\",\"applyStaged\":\"Aplicar preparada\",\"clearStaged\":\"Limpiar preparada\",\"historyTitle\":\"Historial\",\"historyText\":\"Animaciones aplicadas recientemente y guardadas por el módulo.\",\"playlistsTitle\":\"Playlists\",\"playlistsText\":\"Conjuntos persistentes usados por Rotación y los controles del próximo arranque.\",\"create\":\"Crear\",\"rename\":\"Renombrar\",\"duplicate\":\"Duplicar\",\"delete\":\"Eliminar\",\"addZip\":\"Agregar ZIP\",\"rotationTitle\":\"Rotación\",\"rotationText\":\"Prepara futuros arranques desde una Playlist sin cambiar la prioridad de la Cola.\",\"enabled\":\"Activada\",\"sequential\":\"Secuencial\",\"random\":\"Aleatorio\",\"shuffle\":\"Barajar\",\"saveRotation\":\"Guardar rotación\",\"pauseResume\":\"Pausar / Reanudar\",\"prepareNext\":\"Preparar siguiente\",\"queueTitle\":\"Cola de arranque\",\"queueText\":\"La anulación y los arranques en cola tienen prioridad sobre la Rotación normal.\",\"skipNext\":\"Saltar siguiente\",\"clearQueue\":\"Vaciar cola\",\"activityTitle\":\"Actividad de arranque\",\"activityText\":\"Registro observacional de planes de arranque preparados y completados.\",\"clear\":\"Limpiar\",\"deviceTitle\":\"Inteligencia del dispositivo\",\"deviceText\":\"Datos de solo lectura recopilados de esta instalación de Android.\",\"refreshProbes\":\"Actualizar probes\",\"logsTitle\":\"Registros del módulo\",\"logsText\":\"Los mismos registros del arranque actual y anterior expuestos por el script de acción del módulo.\",\"currentBoot\":\"Arranque actual\",\"previousBoot\":\"Arranque anterior\",\"downloadCurrentLog\":\"Descargar registro actual\",\"downloadPreviousLog\":\"Descargar registro anterior\",\"downloadBothLogs\":\"Descargar ambos registros\",\"cancel\":\"Cancelar\",\"save\":\"Guardar\",\"module\":\"Módulo\",\"device\":\"Dispositivo\",\"customAnimation\":\"Animación personalizada\",\"nextBoot\":\"Próximo arranque\",\"active\":\"Activa\",\"stock\":\"Original\",\"none\":\"Ninguno\",\"unknown\":\"Desconocido\",\"yes\":\"Sí\",\"no\":\"No\",\"apply\":\"Aplicar\",\"download\":\"Descargar\",\"queue\":\"Cola\",\"useNext\":\"Usar siguiente\",\"remove\":\"Quitar\",\"preview\":\"Vista previa\",\"moveUp\":\"Subir\",\"moveDown\":\"Bajar\",\"test\":\"Probar\",\"items\":\"elementos\",\"emptyHistory\":\"No hay entradas en Historial.\",\"emptyPlaylist\":\"Esta playlist está vacía.\",\"noPlaylists\":\"Aún no hay playlists.\",\"emptyQueue\":\"La cola está vacía.\",\"nothingPrepared\":\"Nada preparado.\",\"emptyActivity\":\"No hay Actividad de Arranque.\",\"confirmRestore\":\"¿Restaurar ahora la animación de arranque original?\",\"confirmDeleteHistory\":\"¿Eliminar esta entrada del Historial?\",\"confirmDeletePlaylist\":\"¿Eliminar esta playlist?\",\"confirmRemoveItem\":\"¿Quitar este elemento de la playlist?\",\"confirmClearQueue\":\"¿Vaciar todos los arranques en cola?\",\"confirmClearActivity\":\"¿Limpiar la Actividad de Arranque?\",\"confirmClearStaged\":\"¿Limpiar la animación preparada para prueba?\",\"authDenied\":\"La autorización falló o fue denegada.\",\"requestFailed\":\"La solicitud falló.\",\"saved\":\"Guardado.\",\"done\":\"Completado.\",\"connected\":\"Listo\",\"offline\":\"Autorización necesaria\",\"fileSelected\":\"Seleccionado: {name} · {size}\",\"staged\":\"Preparada\",\"notStaged\":\"Nada preparado\",\"previewActive\":\"Vista previa activa\",\"previewInactive\":\"Vista previa detenida\",\"rotationPaused\":\"Rotación pausada\",\"rotationRunning\":\"Rotación activa\",\"rotationOff\":\"Rotación desactivada\",\"preparedBy\":\"Preparado por {source}\",\"format\":\"Formato\",\"resolution\":\"Resolución\",\"android\":\"Android\",\"bootTarget\":\"Destino de arranque\",\"bootAudio\":\"Audio de arranque\",\"renderer\":\"Renderer\",\"mountAccess\":\"Mount de vista previa\",\"targets\":\"Destinos detectados\",\"system\":\"Sistema\",\"boot\":\"Arranque\",\"logoutDone\":\"Sesión de WebUI cerrada.\",\"navHealth\":\"Salud\",\"healthTitle\":\"Salud del módulo\",\"healthText\":\"Integridad actual, almacenamiento y preparación para recuperación, con mantenimiento específico cuando sea necesario.\",\"healthRun\":\"Ejecutar comprobaciones\",\"healthOverall\":\"Estado general\",\"healthChecks\":\"Comprobaciones de integridad\",\"healthStorage\":\"Almacenamiento\",\"healthReadOnly\":\"El mantenimiento es explícito y de alcance limitado. La salud se vuelve a comprobar después de cada acción.\",\"healthHealthy\":\"Saludable\",\"healthAttention\":\"Atención\",\"healthDegraded\":\"Necesita atención\",\"healthOkCount\":\"OK\",\"healthWarnCount\":\"Avisos\",\"healthErrorCount\":\"Problemas\",\"healthFreeSpace\":\"Espacio libre\",\"healthModuleData\":\"Datos del módulo\",\"healthFiles\":\"archivos\",\"healthCode\":\"Código\",\"healthCount\":\"Cantidad\",\"healthNoData\":\"Aún no hay datos de salud.\",\"healthCheck_boot_paths\":\"Rutas de arranque\",\"healthCheck_current_animation\":\"Animación actual\",\"healthCheck_stock_backup\":\"Copia original\",\"healthCheck_history_integrity\":\"Historial\",\"healthCheck_playlist_integrity\":\"Biblioteca de playlists\",\"healthCheck_automation_integrity\":\"Rotación y Cola\",\"healthCheck_staging_integrity\":\"Preparación de prueba\",\"healthCheck_security_state\":\"Estado de confianza y auditoría\",\"healthCheck_temporary_files\":\"Archivos temporales\",\"healthCheck_orphan_objects\":\"Objetos de playlist sin uso\",\"healthCheck_free_space\":\"Espacio libre\",\"healthBucket_history\":\"Historial\",\"healthBucket_playlist\":\"Biblioteca de playlists\",\"healthBucket_backup\":\"Copias originales\",\"healthBucket_staging\":\"Preparación de prueba\",\"healthBucket_trust\":\"Confianza y auditoría\",\"healthBucket_logs\":\"Registros\",\"healthBucket_runtime\":\"Runtime del módulo y otros\",\"healthMaintenance\":\"Mantenimiento seguro\",\"healthMaintenanceText\":\"Aquí solo aparecen datos desechables o sin referencias detectados por Salud.\",\"healthMaintenanceEmpty\":\"No se necesita mantenimiento seguro en este momento.\",\"healthAction_cleanup_stale_temps\":\"Limpiar archivos temporales antiguos\",\"healthAction_cleanup_orphan_objects\":\"Eliminar objetos de playlist sin uso\",\"healthAction_clear_invalid_staging\":\"Limpiar preparación de prueba no válida\",\"healthActionRun\":\"Ejecutar mantenimiento\",\"healthActionWorking\":\"Trabajando…\",\"healthActionConfirm\":\"¿Ejecutar “{name}”? Solo se tocarán los datos descritos por esta acción de mantenimiento.\",\"healthActionDone\":\"Mantenimiento finalizado.\",\"healthActionFailed\":\"Falló el mantenimiento.\"},\"fr\":{\"authTitle\":\"Autoriser la WebUI du module\",\"authText\":\"Cette interface locale nécessite l’approbation de l’application Companion. L’autorisation est distincte de la session du site Boot Animation Studio.\",\"authNote\":\"La WebUI est disponible uniquement depuis cet appareil Android. L’accès distant exige une prise en charge explicite des clients de confiance.\",\"authorize\":\"Demander l’autorisation\",\"subtitle\":\"Administration locale du Companion Module.\",\"loading\":\"Chargement…\",\"refresh\":\"Actualiser\",\"signOut\":\"Se déconnecter\",\"navOverview\":\"Vue d’ensemble\",\"navInstall\":\"Installer et tester\",\"navHistory\":\"Historique\",\"navPlaylists\":\"Playlists\",\"navAutomation\":\"Automatisation\",\"navActivity\":\"Activité de démarrage\",\"navDevice\":\"Appareil\",\"navLogs\":\"Journaux\",\"overviewTitle\":\"Vue d’ensemble du module\",\"overviewText\":\"État actuel du pont et actions courantes sur l’appareil.\",\"quickActions\":\"Actions rapides\",\"rescan\":\"Rescanner les chemins de démarrage\",\"downloadStock\":\"Télécharger l’animation d’origine\",\"downloadCurrent\":\"Télécharger l’animation actuelle\",\"restoreStock\":\"Restaurer l’animation d’origine\",\"installTitle\":\"Installer une animation de démarrage ZIP\",\"installText\":\"Choisissez un bootanimation.zip. Installer l’applique immédiatement et l’enregistre dans l’Historique.\",\"noFile\":\"Aucun fichier sélectionné.\",\"installNow\":\"Installer maintenant\",\"stageTest\":\"Préparer pour test\",\"testLab\":\"Laboratoire de test appareil\",\"testText\":\"Prévisualisez l’animation préparée sur l’appareil sans l’appliquer définitivement.\",\"startPreview\":\"Démarrer l’aperçu\",\"applyStaged\":\"Appliquer la préparation\",\"clearStaged\":\"Effacer la préparation\",\"historyTitle\":\"Historique\",\"historyText\":\"Animations appliquées récemment et enregistrées par le module.\",\"playlistsTitle\":\"Playlists\",\"playlistsText\":\"Ensembles persistants utilisés par Rotation et les contrôles du prochain démarrage.\",\"create\":\"Créer\",\"rename\":\"Renommer\",\"duplicate\":\"Dupliquer\",\"delete\":\"Supprimer\",\"addZip\":\"Ajouter ZIP\",\"rotationTitle\":\"Rotation\",\"rotationText\":\"Préparez les prochains démarrages depuis une Playlist sans modifier la priorité de la File.\",\"enabled\":\"Activée\",\"sequential\":\"Séquentiel\",\"random\":\"Aléatoire\",\"shuffle\":\"Mélanger\",\"saveRotation\":\"Enregistrer la rotation\",\"pauseResume\":\"Pause / Reprendre\",\"prepareNext\":\"Préparer suivant\",\"queueTitle\":\"File de démarrage\",\"queueText\":\"La substitution et les démarrages en file ont priorité sur la Rotation normale.\",\"skipNext\":\"Ignorer le suivant\",\"clearQueue\":\"Vider la file\",\"activityTitle\":\"Activité de démarrage\",\"activityText\":\"Journal d’observation des plans de démarrage préparés et terminés.\",\"clear\":\"Effacer\",\"deviceTitle\":\"Intelligence de l’appareil\",\"deviceText\":\"Informations en lecture seule collectées sur cette installation Android.\",\"refreshProbes\":\"Actualiser les probes\",\"logsTitle\":\"Journaux du module\",\"logsText\":\"Les mêmes journaux du démarrage actuel et précédent exposés par le script d’action du module.\",\"currentBoot\":\"Démarrage actuel\",\"previousBoot\":\"Démarrage précédent\",\"downloadCurrentLog\":\"Télécharger le journal actuel\",\"downloadPreviousLog\":\"Télécharger le journal précédent\",\"downloadBothLogs\":\"Télécharger les deux journaux\",\"cancel\":\"Annuler\",\"save\":\"Enregistrer\",\"module\":\"Module\",\"device\":\"Appareil\",\"customAnimation\":\"Animation personnalisée\",\"nextBoot\":\"Prochain démarrage\",\"active\":\"Active\",\"stock\":\"Origine\",\"none\":\"Aucun\",\"unknown\":\"Inconnu\",\"yes\":\"Oui\",\"no\":\"Non\",\"apply\":\"Appliquer\",\"download\":\"Télécharger\",\"queue\":\"File\",\"useNext\":\"Utiliser ensuite\",\"remove\":\"Retirer\",\"preview\":\"Aperçu\",\"moveUp\":\"Monter\",\"moveDown\":\"Descendre\",\"test\":\"Tester\",\"items\":\"éléments\",\"emptyHistory\":\"Aucune entrée dans l’Historique.\",\"emptyPlaylist\":\"Cette playlist est vide.\",\"noPlaylists\":\"Aucune playlist pour le moment.\",\"emptyQueue\":\"La file est vide.\",\"nothingPrepared\":\"Rien de préparé.\",\"emptyActivity\":\"Aucune Activité de Démarrage.\",\"confirmRestore\":\"Restaurer maintenant l’animation de démarrage d’origine ?\",\"confirmDeleteHistory\":\"Supprimer cette entrée de l’Historique ?\",\"confirmDeletePlaylist\":\"Supprimer cette playlist ?\",\"confirmRemoveItem\":\"Retirer cet élément de la playlist ?\",\"confirmClearQueue\":\"Vider tous les démarrages en file ?\",\"confirmClearActivity\":\"Effacer l’Activité de Démarrage ?\",\"confirmClearStaged\":\"Effacer l’animation préparée pour le test ?\",\"authDenied\":\"L’autorisation a échoué ou a été refusée.\",\"requestFailed\":\"La requête a échoué.\",\"saved\":\"Enregistré.\",\"done\":\"Terminé.\",\"connected\":\"Prêt\",\"offline\":\"Autorisation requise\",\"fileSelected\":\"Sélectionné : {name} · {size}\",\"staged\":\"Préparée\",\"notStaged\":\"Rien de préparé\",\"previewActive\":\"Aperçu actif\",\"previewInactive\":\"Aperçu arrêté\",\"rotationPaused\":\"Rotation en pause\",\"rotationRunning\":\"Rotation active\",\"rotationOff\":\"Rotation désactivée\",\"preparedBy\":\"Préparé par {source}\",\"format\":\"Format\",\"resolution\":\"Résolution\",\"android\":\"Android\",\"bootTarget\":\"Cible de démarrage\",\"bootAudio\":\"Audio de démarrage\",\"renderer\":\"Renderer\",\"mountAccess\":\"Mount d’aperçu\",\"targets\":\"Cibles détectées\",\"system\":\"Système\",\"boot\":\"Démarrage\",\"logoutDone\":\"Session WebUI terminée.\",\"navHealth\":\"Santé\",\"healthTitle\":\"Santé du module\",\"healthText\":\"Intégrité actuelle, stockage et préparation à la récupération, avec une maintenance ciblée si nécessaire.\",\"healthRun\":\"Lancer les vérifications\",\"healthOverall\":\"État général\",\"healthChecks\":\"Vérifications d’intégrité\",\"healthStorage\":\"Stockage\",\"healthReadOnly\":\"La maintenance est explicite et strictement ciblée. La santé est revérifiée après chaque action.\",\"healthHealthy\":\"Sain\",\"healthAttention\":\"Attention\",\"healthDegraded\":\"Nécessite une attention\",\"healthOkCount\":\"OK\",\"healthWarnCount\":\"Avertissements\",\"healthErrorCount\":\"Problèmes\",\"healthFreeSpace\":\"Espace libre\",\"healthModuleData\":\"Données du module\",\"healthFiles\":\"fichiers\",\"healthCode\":\"Code\",\"healthCount\":\"Nombre\",\"healthNoData\":\"Aucune donnée de santé pour le moment.\",\"healthCheck_boot_paths\":\"Chemins de démarrage\",\"healthCheck_current_animation\":\"Animation actuelle\",\"healthCheck_stock_backup\":\"Sauvegarde d’origine\",\"healthCheck_history_integrity\":\"Historique\",\"healthCheck_playlist_integrity\":\"Bibliothèque de playlists\",\"healthCheck_automation_integrity\":\"Rotation et File\",\"healthCheck_staging_integrity\":\"Préparation de test\",\"healthCheck_security_state\":\"État de confiance et d’audit\",\"healthCheck_temporary_files\":\"Fichiers temporaires\",\"healthCheck_orphan_objects\":\"Objets de playlist inutilisés\",\"healthCheck_free_space\":\"Espace libre\",\"healthBucket_history\":\"Historique\",\"healthBucket_playlist\":\"Bibliothèque de playlists\",\"healthBucket_backup\":\"Sauvegardes d’origine\",\"healthBucket_staging\":\"Préparation de test\",\"healthBucket_trust\":\"Confiance et audit\",\"healthBucket_logs\":\"Journaux\",\"healthBucket_runtime\":\"Runtime du module et autres\",\"healthMaintenance\":\"Maintenance sûre\",\"healthMaintenanceText\":\"Seules les données jetables ou non référencées détectées par Santé apparaissent ici.\",\"healthMaintenanceEmpty\":\"Aucune maintenance sûre n’est nécessaire pour le moment.\",\"healthAction_cleanup_stale_temps\":\"Nettoyer les anciens fichiers temporaires\",\"healthAction_cleanup_orphan_objects\":\"Supprimer les objets de playlist inutilisés\",\"healthAction_clear_invalid_staging\":\"Effacer une préparation de test invalide\",\"healthActionRun\":\"Lancer la maintenance\",\"healthActionWorking\":\"Traitement…\",\"healthActionConfirm\":\"Lancer « {name} » ? Seules les données décrites par cette action de maintenance seront modifiées.\",\"healthActionDone\":\"Maintenance terminée.\",\"healthActionFailed\":\"Échec de la maintenance.\"},\"de\":{\"authTitle\":\"Modul-WebUI autorisieren\",\"authText\":\"Diese lokale Oberfläche benötigt die Freigabe durch die Companion-App. Die Autorisierung ist von der Sitzung der Boot Animation Studio-Website getrennt.\",\"authNote\":\"Die WebUI ist nur auf diesem Android-Gerät verfügbar. Fernzugriff erfordert ausdrückliche Unterstützung für vertrauenswürdige Clients.\",\"authorize\":\"Autorisierung anfordern\",\"subtitle\":\"Lokale Verwaltung für das Companion-Modul.\",\"loading\":\"Wird geladen…\",\"refresh\":\"Aktualisieren\",\"signOut\":\"Abmelden\",\"navOverview\":\"Übersicht\",\"navInstall\":\"Installieren & testen\",\"navHistory\":\"Verlauf\",\"navPlaylists\":\"Playlists\",\"navAutomation\":\"Automatisierung\",\"navActivity\":\"Boot-Aktivität\",\"navDevice\":\"Gerät\",\"navLogs\":\"Logs\",\"overviewTitle\":\"Modulübersicht\",\"overviewText\":\"Aktueller Bridge-Status und häufige Geräteaktionen.\",\"quickActions\":\"Schnellaktionen\",\"rescan\":\"Boot-Pfade neu erkennen\",\"downloadStock\":\"Originalanimation herunterladen\",\"downloadCurrent\":\"Aktuelle Animation herunterladen\",\"restoreStock\":\"Originalanimation wiederherstellen\",\"installTitle\":\"Bootanimation-ZIP installieren\",\"installText\":\"Wähle eine bootanimation.zip. Beim Installieren wird sie sofort angewendet und im Verlauf gespeichert.\",\"noFile\":\"Keine Datei ausgewählt.\",\"installNow\":\"Jetzt installieren\",\"stageTest\":\"Für Test vorbereiten\",\"testLab\":\"Geräte-Testlabor\",\"testText\":\"Zeige die vorbereitete Animation auf dem Gerät an, ohne sie dauerhaft anzuwenden.\",\"startPreview\":\"Vorschau starten\",\"applyStaged\":\"Vorbereitete anwenden\",\"clearStaged\":\"Vorbereitung löschen\",\"historyTitle\":\"Verlauf\",\"historyText\":\"Kürzlich angewendete Animationen, die vom Modul gespeichert wurden.\",\"playlistsTitle\":\"Playlists\",\"playlistsText\":\"Dauerhafte kuratierte Animationssammlungen für Rotation und Nächster-Boot-Steuerung.\",\"create\":\"Erstellen\",\"rename\":\"Umbenennen\",\"duplicate\":\"Duplizieren\",\"delete\":\"Löschen\",\"addZip\":\"ZIP hinzufügen\",\"rotationTitle\":\"Rotation\",\"rotationText\":\"Bereite zukünftige Boots aus einer Playlist vor, ohne die Priorität der Warteschlange zu ändern.\",\"enabled\":\"Aktiviert\",\"sequential\":\"Nacheinander\",\"random\":\"Zufällig\",\"shuffle\":\"Mischen\",\"saveRotation\":\"Rotation speichern\",\"pauseResume\":\"Pausieren / Fortsetzen\",\"prepareNext\":\"Nächste vorbereiten\",\"queueTitle\":\"Boot Queue\",\"queueText\":\"Einmalige Overrides und eingereihte Boots haben Vorrang vor der normalen Rotation.\",\"skipNext\":\"Nächsten überspringen\",\"clearQueue\":\"Warteschlange leeren\",\"activityTitle\":\"Boot-Aktivität\",\"activityText\":\"Beobachtungsprotokoll vorbereiteter und abgeschlossener Boot-Pläne.\",\"clear\":\"Leeren\",\"deviceTitle\":\"Geräteinformationen\",\"deviceText\":\"Schreibgeschützte Informationen aus dieser Android-Installation.\",\"refreshProbes\":\"Probes aktualisieren\",\"logsTitle\":\"Modulprotokolle\",\"logsText\":\"Dieselben Protokolle des aktuellen und vorherigen Boots, die auch das Aktionsskript des Moduls bereitstellt.\",\"currentBoot\":\"Aktueller Boot\",\"previousBoot\":\"Vorheriger Boot\",\"downloadCurrentLog\":\"Aktuelles Protokoll herunterladen\",\"downloadPreviousLog\":\"Vorheriges Protokoll herunterladen\",\"downloadBothLogs\":\"Beide Protokolle herunterladen\",\"cancel\":\"Abbrechen\",\"save\":\"Speichern\",\"module\":\"Modul\",\"device\":\"Gerät\",\"customAnimation\":\"Benutzerdefinierte Animation\",\"nextBoot\":\"Nächster Boot\",\"active\":\"Aktiv\",\"stock\":\"Original\",\"none\":\"Kein\",\"unknown\":\"Unbekannt\",\"yes\":\"Ja\",\"no\":\"Nein\",\"apply\":\"Anwenden\",\"download\":\"Herunterladen\",\"queue\":\"Warteschlange\",\"useNext\":\"Als nächsten verwenden\",\"remove\":\"Entfernen\",\"preview\":\"Vorschau\",\"moveUp\":\"Nach oben\",\"moveDown\":\"Nach unten\",\"test\":\"Testen\",\"items\":\"Elemente\",\"emptyHistory\":\"Keine Verlaufseinträge.\",\"emptyPlaylist\":\"Diese Playlist ist leer.\",\"noPlaylists\":\"Noch keine Playlists.\",\"emptyQueue\":\"Die Warteschlange ist leer.\",\"nothingPrepared\":\"Nichts vorbereitet.\",\"emptyActivity\":\"Keine Boot-Aktivitätseinträge.\",\"confirmRestore\":\"Originale Bootanimation jetzt wiederherstellen?\",\"confirmDeleteHistory\":\"Diesen Verlaufseintrag löschen?\",\"confirmDeletePlaylist\":\"Diese Playlist löschen?\",\"confirmRemoveItem\":\"Dieses Element aus der Playlist entfernen?\",\"confirmClearQueue\":\"Alle geplanten Boots löschen?\",\"confirmClearActivity\":\"Boot-Aktivität leeren?\",\"confirmClearStaged\":\"Vorbereitete Testanimation löschen?\",\"authDenied\":\"Autorisierung fehlgeschlagen oder wurde abgelehnt.\",\"requestFailed\":\"Anfrage fehlgeschlagen.\",\"saved\":\"Gespeichert.\",\"done\":\"Fertig.\",\"connected\":\"Bereit\",\"offline\":\"Autorisierung erforderlich\",\"fileSelected\":\"Ausgewählt: {name} · {size}\",\"staged\":\"Vorbereitet\",\"notStaged\":\"Nichts vorbereitet\",\"previewActive\":\"Vorschau aktiv\",\"previewInactive\":\"Vorschau beendet\",\"rotationPaused\":\"Rotation pausiert\",\"rotationRunning\":\"Rotation aktiv\",\"rotationOff\":\"Rotation deaktiviert\",\"preparedBy\":\"Vorbereitet durch {source}\",\"format\":\"Format\",\"resolution\":\"Auflösung\",\"android\":\"Android\",\"bootTarget\":\"Boot-Ziel\",\"bootAudio\":\"Boot-Audio\",\"renderer\":\"Renderer\",\"mountAccess\":\"Vorschau-Mount\",\"targets\":\"Erkannte Ziele\",\"system\":\"System\",\"boot\":\"Boot\",\"logoutDone\":\"WebUI-Sitzung beendet.\",\"navHealth\":\"Gesundheit\",\"healthTitle\":\"Modulzustand\",\"healthText\":\"Aktuelle Integrität, Speicher und Wiederherstellungsbereitschaft mit eng begrenzter Wartung bei Bedarf.\",\"healthRun\":\"Prüfungen ausführen\",\"healthOverall\":\"Gesamtzustand\",\"healthChecks\":\"Integritätsprüfungen\",\"healthStorage\":\"Speicher\",\"healthReadOnly\":\"Wartung ist ausdrücklich und eng begrenzt. Nach jeder Aktion wird der Zustand erneut geprüft.\",\"healthHealthy\":\"Gesund\",\"healthAttention\":\"Aufmerksamkeit\",\"healthDegraded\":\"Benötigt Aufmerksamkeit\",\"healthOkCount\":\"OK\",\"healthWarnCount\":\"Warnungen\",\"healthErrorCount\":\"Probleme\",\"healthFreeSpace\":\"Freier Speicher\",\"healthModuleData\":\"Moduldaten\",\"healthFiles\":\"Dateien\",\"healthCode\":\"Code\",\"healthCount\":\"Anzahl\",\"healthNoData\":\"Noch keine Zustandsdaten.\",\"healthCheck_boot_paths\":\"Boot-Pfade\",\"healthCheck_current_animation\":\"Aktuelle Animation\",\"healthCheck_stock_backup\":\"Stock-Backup\",\"healthCheck_history_integrity\":\"Verlauf\",\"healthCheck_playlist_integrity\":\"Playlist-Bibliothek\",\"healthCheck_automation_integrity\":\"Rotation & Queue\",\"healthCheck_staging_integrity\":\"Test-Staging\",\"healthCheck_security_state\":\"Vertrauens- & Auditstatus\",\"healthCheck_temporary_files\":\"Temporäre Dateien\",\"healthCheck_orphan_objects\":\"Nicht verwendete Playlist-Objekte\",\"healthCheck_free_space\":\"Freier Speicher\",\"healthBucket_history\":\"Verlauf\",\"healthBucket_playlist\":\"Playlist-Bibliothek\",\"healthBucket_backup\":\"Stock-Backups\",\"healthBucket_staging\":\"Test-Staging\",\"healthBucket_trust\":\"Vertrauen & Audit\",\"healthBucket_logs\":\"Logs\",\"healthBucket_runtime\":\"Modul-Laufzeit & Sonstiges\",\"healthMaintenance\":\"Sichere Wartung\",\"healthMaintenanceText\":\"Hier erscheinen nur entbehrliche oder nicht referenzierte Daten, die Health erkannt hat.\",\"healthMaintenanceEmpty\":\"Derzeit ist keine sichere Wartung nötig.\",\"healthAction_cleanup_stale_temps\":\"Veraltete temporäre Dateien bereinigen\",\"healthAction_cleanup_orphan_objects\":\"Nicht verwendete Playlist-Objekte entfernen\",\"healthAction_clear_invalid_staging\":\"Ungültiges Test-Staging löschen\",\"healthActionRun\":\"Wartung ausführen\",\"healthActionWorking\":\"Wird ausgeführt…\",\"healthActionConfirm\":\"„{name}“ ausführen? Es werden nur die von dieser Wartungsaktion beschriebenen Daten verändert.\",\"healthActionDone\":\"Wartung abgeschlossen.\",\"healthActionFailed\":\"Wartung fehlgeschlagen.\"},\"it\":{\"authTitle\":\"Autorizza la WebUI del modulo\",\"authText\":\"Questa interfaccia locale richiede l’approvazione dell’app Companion. L’autorizzazione è separata dalla sessione del sito Boot Animation Studio.\",\"authNote\":\"La WebUI è disponibile solo su questo dispositivo Android. L’accesso remoto richiede il supporto esplicito dei client attendibili.\",\"authorize\":\"Richiedi autorizzazione\",\"subtitle\":\"Amministrazione locale del Companion Module.\",\"loading\":\"Caricamento…\",\"refresh\":\"Aggiorna\",\"signOut\":\"Esci\",\"navOverview\":\"Panoramica\",\"navInstall\":\"Installa e prova\",\"navHistory\":\"Cronologia\",\"navPlaylists\":\"Playlist\",\"navAutomation\":\"Automazione\",\"navActivity\":\"Attività di avvio\",\"navDevice\":\"Dispositivo\",\"navLogs\":\"Log\",\"overviewTitle\":\"Panoramica del modulo\",\"overviewText\":\"Stato attuale del bridge e azioni comuni sul dispositivo.\",\"quickActions\":\"Azioni rapide\",\"rescan\":\"Rileva di nuovo i percorsi di avvio\",\"downloadStock\":\"Scarica animazione originale\",\"downloadCurrent\":\"Scarica animazione attuale\",\"restoreStock\":\"Ripristina animazione originale\",\"installTitle\":\"Installa uno ZIP di boot animation\",\"installText\":\"Scegli una bootanimation.zip. L’installazione la applica subito e la salva nella Cronologia.\",\"noFile\":\"Nessun file selezionato.\",\"installNow\":\"Installa ora\",\"stageTest\":\"Prepara per il test\",\"testLab\":\"Laboratorio test dispositivo\",\"testText\":\"Visualizza l’animazione preparata sul dispositivo senza applicarla in modo permanente.\",\"startPreview\":\"Avvia anteprima\",\"applyStaged\":\"Applica preparata\",\"clearStaged\":\"Cancella preparazione\",\"historyTitle\":\"Cronologia\",\"historyText\":\"Animazioni applicate di recente e memorizzate dal modulo.\",\"playlistsTitle\":\"Playlist\",\"playlistsText\":\"Raccolte persistenti di animazioni usate dalla Rotazione e dai controlli del prossimo avvio.\",\"create\":\"Crea\",\"rename\":\"Rinomina\",\"duplicate\":\"Duplica\",\"delete\":\"Elimina\",\"addZip\":\"Aggiungi ZIP\",\"rotationTitle\":\"Rotazione\",\"rotationText\":\"Prepara gli avvii futuri da una playlist senza cambiare la priorità della coda.\",\"enabled\":\"Attivo\",\"sequential\":\"Sequenziale\",\"random\":\"Casuale\",\"shuffle\":\"Mescola\",\"saveRotation\":\"Salva rotazione\",\"pauseResume\":\"Pausa / Riprendi\",\"prepareNext\":\"Prepara successiva\",\"queueTitle\":\"Coda di avvio\",\"queueText\":\"Gli override e gli avvii in coda hanno priorità sulla Rotazione normale.\",\"skipNext\":\"Salta il prossimo\",\"clearQueue\":\"Svuota coda\",\"activityTitle\":\"Attività di avvio\",\"activityText\":\"Registro osservativo dei piani di avvio preparati e completati.\",\"clear\":\"Cancella\",\"deviceTitle\":\"Informazioni dispositivo\",\"deviceText\":\"Informazioni in sola lettura raccolte da questa installazione Android.\",\"refreshProbes\":\"Aggiorna sonde\",\"logsTitle\":\"Log del modulo\",\"logsText\":\"Gli stessi log dell’avvio attuale e precedente esposti dallo script azioni del modulo.\",\"currentBoot\":\"Avvio attuale\",\"previousBoot\":\"Avvio precedente\",\"downloadCurrentLog\":\"Scarica log attuale\",\"downloadPreviousLog\":\"Scarica log precedente\",\"downloadBothLogs\":\"Scarica entrambi i log\",\"cancel\":\"Annulla\",\"save\":\"Salva\",\"module\":\"Modulo\",\"device\":\"Dispositivo\",\"customAnimation\":\"Animazione personalizzata\",\"nextBoot\":\"Prossimo avvio\",\"active\":\"Attivo\",\"stock\":\"Originale\",\"none\":\"Nessuno\",\"unknown\":\"Sconosciuto\",\"yes\":\"Sì\",\"no\":\"No\",\"apply\":\"Applica\",\"download\":\"Scarica\",\"queue\":\"Coda\",\"useNext\":\"Usa come prossimo\",\"remove\":\"Rimuovi\",\"preview\":\"Anteprima\",\"moveUp\":\"Su\",\"moveDown\":\"Giù\",\"test\":\"Test\",\"items\":\"elementi\",\"emptyHistory\":\"Nessuna voce nella Cronologia.\",\"emptyPlaylist\":\"Questa playlist è vuota.\",\"noPlaylists\":\"Nessuna playlist.\",\"emptyQueue\":\"La coda è vuota.\",\"nothingPrepared\":\"Niente di preparato.\",\"emptyActivity\":\"Nessuna voce nell’Attività di avvio.\",\"confirmRestore\":\"Ripristinare ora la boot animation originale?\",\"confirmDeleteHistory\":\"Eliminare questa voce dalla Cronologia?\",\"confirmDeletePlaylist\":\"Eliminare questa playlist?\",\"confirmRemoveItem\":\"Rimuovere questo elemento dalla playlist?\",\"confirmClearQueue\":\"Svuotare tutti gli avvii in coda?\",\"confirmClearActivity\":\"Cancellare l’Attività di avvio?\",\"confirmClearStaged\":\"Cancellare l’animazione di test preparata?\",\"authDenied\":\"Autorizzazione non riuscita o negata.\",\"requestFailed\":\"Richiesta non riuscita.\",\"saved\":\"Salvato.\",\"done\":\"Fatto.\",\"connected\":\"Pronto\",\"offline\":\"Autorizzazione richiesta\",\"fileSelected\":\"Selezionato: {name} · {size}\",\"staged\":\"Preparata\",\"notStaged\":\"Niente di preparato\",\"previewActive\":\"Anteprima attiva\",\"previewInactive\":\"Anteprima terminata\",\"rotationPaused\":\"Rotazione in pausa\",\"rotationRunning\":\"Rotazione attiva\",\"rotationOff\":\"Rotazione disattivata\",\"preparedBy\":\"Preparato da {source}\",\"format\":\"Formato\",\"resolution\":\"Risoluzione\",\"android\":\"Android\",\"bootTarget\":\"Destinazione avvio\",\"bootAudio\":\"Audio di avvio\",\"renderer\":\"Renderer\",\"mountAccess\":\"Mount anteprima\",\"targets\":\"Destinazioni rilevate\",\"system\":\"Sistema\",\"boot\":\"Avvio\",\"logoutDone\":\"Sessione WebUI terminata.\",\"navHealth\":\"Salute\",\"healthTitle\":\"Stato del modulo\",\"healthText\":\"Integrità attuale, spazio di archiviazione e capacità di ripristino, con manutenzione strettamente mirata quando necessaria.\",\"healthRun\":\"Esegui controlli\",\"healthOverall\":\"Stato generale\",\"healthChecks\":\"Controlli integrità\",\"healthStorage\":\"Spazio\",\"healthReadOnly\":\"La manutenzione è esplicita e strettamente mirata. Lo stato viene ricontrollato dopo ogni azione.\",\"healthHealthy\":\"Sano\",\"healthAttention\":\"Attenzione\",\"healthDegraded\":\"Richiede attenzione\",\"healthOkCount\":\"OK\",\"healthWarnCount\":\"Avvisi\",\"healthErrorCount\":\"Problemi\",\"healthFreeSpace\":\"Spazio libero\",\"healthModuleData\":\"Dati del modulo\",\"healthFiles\":\"file\",\"healthCode\":\"Codice\",\"healthCount\":\"Conteggio\",\"healthNoData\":\"Nessun dato sullo stato disponibile.\",\"healthCheck_boot_paths\":\"Percorsi di avvio\",\"healthCheck_current_animation\":\"Animazione corrente\",\"healthCheck_stock_backup\":\"Backup stock\",\"healthCheck_history_integrity\":\"Cronologia\",\"healthCheck_playlist_integrity\":\"Libreria playlist\",\"healthCheck_automation_integrity\":\"Rotazione e Coda\",\"healthCheck_staging_integrity\":\"Staging test\",\"healthCheck_security_state\":\"Stato attendibilità e audit\",\"healthCheck_temporary_files\":\"File temporanei\",\"healthCheck_orphan_objects\":\"Oggetti playlist inutilizzati\",\"healthCheck_free_space\":\"Spazio libero\",\"healthBucket_history\":\"Cronologia\",\"healthBucket_playlist\":\"Libreria playlist\",\"healthBucket_backup\":\"Backup stock\",\"healthBucket_staging\":\"Staging test\",\"healthBucket_trust\":\"Attendibilità e audit\",\"healthBucket_logs\":\"Log\",\"healthBucket_runtime\":\"Runtime modulo e altro\",\"healthMaintenance\":\"Manutenzione sicura\",\"healthMaintenanceText\":\"Qui compaiono solo dati eliminabili o non referenziati rilevati da Health.\",\"healthMaintenanceEmpty\":\"Al momento non è necessaria alcuna manutenzione sicura.\",\"healthAction_cleanup_stale_temps\":\"Pulisci file temporanei obsoleti\",\"healthAction_cleanup_orphan_objects\":\"Rimuovi oggetti playlist inutilizzati\",\"healthAction_clear_invalid_staging\":\"Cancella staging test non valido\",\"healthActionRun\":\"Esegui manutenzione\",\"healthActionWorking\":\"Operazione in corso…\",\"healthActionConfirm\":\"Eseguire “{name}”? Verranno modificati solo i dati descritti da questa azione di manutenzione.\",\"healthActionDone\":\"Manutenzione completata.\",\"healthActionFailed\":\"Manutenzione non riuscita.\"},\"ja\":{\"authTitle\":\"モジュール WebUI を許可\",\"authText\":\"このローカル画面を使用するには Companion アプリでの承認が必要です。認証は Boot Animation Studio サイトのセッションとは別です。\",\"authNote\":\"WebUI はこの Android 端末上でのみ利用できます。リモートアクセスには信頼済みクライアントの明示的な対応が必要です。\",\"authorize\":\"認証をリクエスト\",\"subtitle\":\"Companion Module のローカル管理。\",\"loading\":\"読み込み中…\",\"refresh\":\"更新\",\"signOut\":\"サインアウト\",\"navOverview\":\"概要\",\"navInstall\":\"インストールとテスト\",\"navHistory\":\"履歴\",\"navPlaylists\":\"プレイリスト\",\"navAutomation\":\"自動化\",\"navActivity\":\"起動アクティビティ\",\"navDevice\":\"デバイス\",\"navLogs\":\"ログ\",\"overviewTitle\":\"モジュール概要\",\"overviewText\":\"現在のブリッジ状態と一般的な端末操作。\",\"quickActions\":\"クイック操作\",\"rescan\":\"起動パスを再検出\",\"downloadStock\":\"標準アニメーションをダウンロード\",\"downloadCurrent\":\"現在のアニメーションをダウンロード\",\"restoreStock\":\"標準アニメーションを復元\",\"installTitle\":\"ブートアニメーション ZIP をインストール\",\"installText\":\"bootanimation.zip を選択してください。インストールするとすぐに適用され、履歴に保存されます。\",\"noFile\":\"ファイルが選択されていません。\",\"installNow\":\"今すぐインストール\",\"stageTest\":\"テスト用に準備\",\"testLab\":\"端末テストラボ\",\"testText\":\"準備したアニメーションを永続的に適用せず端末でプレビューします。\",\"startPreview\":\"プレビュー開始\",\"applyStaged\":\"準備済みを適用\",\"clearStaged\":\"準備をクリア\",\"historyTitle\":\"履歴\",\"historyText\":\"モジュールに保存された最近の適用済みアニメーション。\",\"playlistsTitle\":\"プレイリスト\",\"playlistsText\":\"ローテーションと次回起動の制御に使う永続的なアニメーションセット。\",\"create\":\"作成\",\"rename\":\"名前を変更\",\"duplicate\":\"複製\",\"delete\":\"削除\",\"addZip\":\"ZIP を追加\",\"rotationTitle\":\"ローテーション\",\"rotationText\":\"キューの優先順位を変えずに、プレイリストから今後の起動を準備します。\",\"enabled\":\"有効\",\"sequential\":\"順番\",\"random\":\"ランダム\",\"shuffle\":\"シャッフル\",\"saveRotation\":\"ローテーションを保存\",\"pauseResume\":\"一時停止 / 再開\",\"prepareNext\":\"次を準備\",\"queueTitle\":\"Boot Queue\",\"queueText\":\"一回限りの上書きとキュー内の起動は通常のローテーションより優先されます。\",\"skipNext\":\"次をスキップ\",\"clearQueue\":\"キューを消去\",\"activityTitle\":\"起動アクティビティ\",\"activityText\":\"準備済みおよび完了した起動プランの観測ログ。\",\"clear\":\"クリア\",\"deviceTitle\":\"端末情報\",\"deviceText\":\"この Android インストールから収集した読み取り専用情報。\",\"refreshProbes\":\"プローブを更新\",\"logsTitle\":\"モジュールログ\",\"logsText\":\"モジュールのアクションスクリプトで公開される現在および前回の起動ログと同じ内容です。\",\"currentBoot\":\"現在の起動\",\"previousBoot\":\"前回のブート\",\"downloadCurrentLog\":\"現在のログをダウンロード\",\"downloadPreviousLog\":\"前回のログをダウンロード\",\"downloadBothLogs\":\"両方のログをダウンロード\",\"cancel\":\"キャンセル\",\"save\":\"保存\",\"module\":\"モジュール\",\"device\":\"デバイス\",\"customAnimation\":\"カスタムアニメーション\",\"nextBoot\":\"次回ブート\",\"active\":\"アクティブ\",\"stock\":\"標準\",\"none\":\"なし\",\"unknown\":\"不明\",\"yes\":\"はい\",\"no\":\"いいえ\",\"apply\":\"適用\",\"download\":\"ダウンロード\",\"queue\":\"キュー\",\"useNext\":\"次回に使用\",\"remove\":\"削除\",\"preview\":\"プレビュー\",\"moveUp\":\"上へ\",\"moveDown\":\"下へ\",\"test\":\"テスト\",\"items\":\"項目\",\"emptyHistory\":\"履歴エントリはありません。\",\"emptyPlaylist\":\"このプレイリストは空です。\",\"noPlaylists\":\"プレイリストはまだありません。\",\"emptyQueue\":\"キューは空です。\",\"nothingPrepared\":\"準備されているものはありません。\",\"emptyActivity\":\"起動アクティビティのエントリはありません。\",\"confirmRestore\":\"標準のブートアニメーションを今すぐ復元しますか？\",\"confirmDeleteHistory\":\"この履歴エントリを削除しますか？\",\"confirmDeletePlaylist\":\"このプレイリストを削除しますか？\",\"confirmRemoveItem\":\"この項目をプレイリストから削除しますか？\",\"confirmClearQueue\":\"キュー内のすべてのブートを消去しますか？\",\"confirmClearActivity\":\"起動アクティビティをクリアしますか？\",\"confirmClearStaged\":\"準備済みのテストアニメーションをクリアしますか？\",\"authDenied\":\"認証に失敗したか、拒否されました。\",\"requestFailed\":\"リクエストに失敗しました。\",\"saved\":\"保存しました。\",\"done\":\"完了。\",\"connected\":\"準備完了\",\"offline\":\"認証が必要です\",\"fileSelected\":\"選択済み: {name} · {size}\",\"staged\":\"準備済み\",\"notStaged\":\"準備なし\",\"previewActive\":\"プレビュー実行中\",\"previewInactive\":\"プレビュー停止\",\"rotationPaused\":\"ローテーション一時停止中\",\"rotationRunning\":\"ローテーション有効\",\"rotationOff\":\"ローテーション無効\",\"preparedBy\":\"準備元: {source}\",\"format\":\"形式\",\"resolution\":\"解像度\",\"android\":\"Android\",\"bootTarget\":\"ブート対象\",\"bootAudio\":\"ブート音声\",\"renderer\":\"レンダラー\",\"mountAccess\":\"プレビューマウント\",\"targets\":\"検出された対象\",\"system\":\"システム\",\"boot\":\"ブート\",\"logoutDone\":\"WebUI セッションを終了しました。\",\"navHealth\":\"ヘルス\",\"healthTitle\":\"モジュールの状態\",\"healthText\":\"現在の整合性、ストレージ、復旧準備状況を確認し、必要な場合のみ限定的なメンテナンスを行います。\",\"healthRun\":\"チェックを実行\",\"healthOverall\":\"総合状態\",\"healthChecks\":\"整合性チェック\",\"healthStorage\":\"ストレージ\",\"healthReadOnly\":\"メンテナンスは明示的かつ限定的です。各操作の後に状態を再確認します。\",\"healthHealthy\":\"正常\",\"healthAttention\":\"注意\",\"healthDegraded\":\"要確認\",\"healthOkCount\":\"OK\",\"healthWarnCount\":\"警告\",\"healthErrorCount\":\"問題\",\"healthFreeSpace\":\"空き容量\",\"healthModuleData\":\"モジュールデータ\",\"healthFiles\":\"ファイル\",\"healthCode\":\"コード\",\"healthCount\":\"件数\",\"healthNoData\":\"状態データはまだありません。\",\"healthCheck_boot_paths\":\"ブートパス\",\"healthCheck_current_animation\":\"現在のアニメーション\",\"healthCheck_stock_backup\":\"純正バックアップ\",\"healthCheck_history_integrity\":\"履歴\",\"healthCheck_playlist_integrity\":\"プレイリストライブラリ\",\"healthCheck_automation_integrity\":\"ローテーションとQueue\",\"healthCheck_staging_integrity\":\"テストステージング\",\"healthCheck_security_state\":\"信頼と監査状態\",\"healthCheck_temporary_files\":\"一時ファイル\",\"healthCheck_orphan_objects\":\"未使用プレイリストオブジェクト\",\"healthCheck_free_space\":\"空き容量\",\"healthBucket_history\":\"履歴\",\"healthBucket_playlist\":\"プレイリストライブラリ\",\"healthBucket_backup\":\"純正バックアップ\",\"healthBucket_staging\":\"テストステージング\",\"healthBucket_trust\":\"信頼と監査\",\"healthBucket_logs\":\"ログ\",\"healthBucket_runtime\":\"モジュールランタイムとその他\",\"healthMaintenance\":\"安全なメンテナンス\",\"healthMaintenanceText\":\"Health が検出した破棄可能または未参照のデータだけがここに表示されます。\",\"healthMaintenanceEmpty\":\"現在必要な安全メンテナンスはありません。\",\"healthAction_cleanup_stale_temps\":\"古い一時ファイルを削除\",\"healthAction_cleanup_orphan_objects\":\"未使用プレイリストオブジェクトを削除\",\"healthAction_clear_invalid_staging\":\"無効なテストステージングを消去\",\"healthActionRun\":\"メンテナンスを実行\",\"healthActionWorking\":\"実行中…\",\"healthActionConfirm\":\"「{name}」を実行しますか？このメンテナンス操作で説明されているデータだけが変更されます。\",\"healthActionDone\":\"メンテナンスが完了しました。\",\"healthActionFailed\":\"メンテナンスに失敗しました。\"},\"zh\":{\"authTitle\":\"授权模块 WebUI\",\"authText\":\"此本地界面需要 Companion 应用批准。该授权与 Boot Animation Studio 网站会话相互独立。\",\"authNote\":\"WebUI 仅可在此 Android 设备上使用。远程访问需要明确支持受信任客户端。\",\"authorize\":\"请求授权\",\"subtitle\":\"Companion Module 的本地管理。\",\"loading\":\"正在加载…\",\"refresh\":\"刷新\",\"signOut\":\"退出\",\"navOverview\":\"概览\",\"navInstall\":\"安装与测试\",\"navHistory\":\"历史记录\",\"navPlaylists\":\"播放列表\",\"navAutomation\":\"自动化\",\"navActivity\":\"启动活动\",\"navDevice\":\"设备\",\"navLogs\":\"日志\",\"overviewTitle\":\"模块概览\",\"overviewText\":\"当前桥接状态和常用设备操作。\",\"quickActions\":\"快捷操作\",\"rescan\":\"重新扫描启动路径\",\"downloadStock\":\"下载原始动画\",\"downloadCurrent\":\"下载当前动画\",\"restoreStock\":\"恢复原始动画\",\"installTitle\":\"安装启动动画 ZIP\",\"installText\":\"选择 bootanimation.zip。安装后会立即应用并保存到历史记录。\",\"noFile\":\"未选择文件。\",\"installNow\":\"立即安装\",\"stageTest\":\"准备测试\",\"testLab\":\"设备测试实验室\",\"testText\":\"在设备上预览已准备的动画，而不会永久应用。\",\"startPreview\":\"开始预览\",\"applyStaged\":\"应用已准备内容\",\"clearStaged\":\"清除准备内容\",\"historyTitle\":\"历史记录\",\"historyText\":\"模块保存的最近已应用动画。\",\"playlistsTitle\":\"播放列表\",\"playlistsText\":\"用于轮换和下次启动控制的持久动画集合。\",\"create\":\"创建\",\"rename\":\"重命名\",\"duplicate\":\"复制\",\"delete\":\"删除\",\"addZip\":\"添加 ZIP\",\"rotationTitle\":\"轮换\",\"rotationText\":\"从播放列表准备未来启动，而不改变队列优先级。\",\"enabled\":\"已启用\",\"sequential\":\"顺序\",\"random\":\"随机\",\"shuffle\":\"洗牌\",\"saveRotation\":\"保存轮换\",\"pauseResume\":\"暂停 / 继续\",\"prepareNext\":\"准备下一个\",\"queueTitle\":\"启动队列\",\"queueText\":\"一次性覆盖和已排队启动优先于常规轮换。\",\"skipNext\":\"跳过下一项\",\"clearQueue\":\"清空队列\",\"activityTitle\":\"启动活动\",\"activityText\":\"已准备和已完成启动计划的观察记录。\",\"clear\":\"清除\",\"deviceTitle\":\"设备信息\",\"deviceText\":\"从此 Android 安装中收集的只读信息。\",\"refreshProbes\":\"刷新探测\",\"logsTitle\":\"模块日志\",\"logsText\":\"与模块操作脚本公开的当前及上一次启动日志相同。\",\"currentBoot\":\"当前启动\",\"previousBoot\":\"上一次启动\",\"downloadCurrentLog\":\"下载当前日志\",\"downloadPreviousLog\":\"下载上一次日志\",\"downloadBothLogs\":\"下载两个日志\",\"cancel\":\"取消\",\"save\":\"保存\",\"module\":\"模块\",\"device\":\"设备\",\"customAnimation\":\"自定义动画\",\"nextBoot\":\"下一次启动\",\"active\":\"活动\",\"stock\":\"原始\",\"none\":\"无\",\"unknown\":\"未知\",\"yes\":\"是\",\"no\":\"否\",\"apply\":\"应用\",\"download\":\"下载\",\"queue\":\"队列\",\"useNext\":\"下次使用\",\"remove\":\"移除\",\"preview\":\"预览\",\"moveUp\":\"上移\",\"moveDown\":\"下移\",\"test\":\"测试\",\"items\":\"项\",\"emptyHistory\":\"没有历史记录条目。\",\"emptyPlaylist\":\"此播放列表为空。\",\"noPlaylists\":\"还没有播放列表。\",\"emptyQueue\":\"队列为空。\",\"nothingPrepared\":\"没有已准备内容。\",\"emptyActivity\":\"没有启动活动条目。\",\"confirmRestore\":\"现在恢复原始启动动画吗？\",\"confirmDeleteHistory\":\"删除此历史记录条目吗？\",\"confirmDeletePlaylist\":\"删除此播放列表吗？\",\"confirmRemoveItem\":\"从播放列表中移除此项目吗？\",\"confirmClearQueue\":\"清空所有排队的启动吗？\",\"confirmClearActivity\":\"清除启动活动吗？\",\"confirmClearStaged\":\"清除已准备的测试动画吗？\",\"authDenied\":\"授权失败或被拒绝。\",\"requestFailed\":\"请求失败。\",\"saved\":\"已保存。\",\"done\":\"完成。\",\"connected\":\"就绪\",\"offline\":\"需要授权\",\"fileSelected\":\"已选择：{name} · {size}\",\"staged\":\"已准备\",\"notStaged\":\"没有准备内容\",\"previewActive\":\"预览中\",\"previewInactive\":\"预览已停止\",\"rotationPaused\":\"轮换已暂停\",\"rotationRunning\":\"轮换已启用\",\"rotationOff\":\"轮换已禁用\",\"preparedBy\":\"准备来源：{source}\",\"format\":\"格式\",\"resolution\":\"分辨率\",\"android\":\"Android\",\"bootTarget\":\"启动目标\",\"bootAudio\":\"启动音频\",\"renderer\":\"渲染器\",\"mountAccess\":\"预览挂载\",\"targets\":\"检测到的目标\",\"system\":\"系统\",\"boot\":\"启动\",\"logoutDone\":\"WebUI 会话已结束。\",\"navHealth\":\"健康\",\"healthTitle\":\"模块健康状态\",\"healthText\":\"检查当前完整性、存储和恢复准备状态，并在需要时提供严格限定的维护。\",\"healthRun\":\"运行检查\",\"healthOverall\":\"总体状态\",\"healthChecks\":\"完整性检查\",\"healthStorage\":\"存储\",\"healthReadOnly\":\"维护操作明确且范围严格。每次操作后都会再次检查健康状态。\",\"healthHealthy\":\"健康\",\"healthAttention\":\"注意\",\"healthDegraded\":\"需要注意\",\"healthOkCount\":\"正常\",\"healthWarnCount\":\"警告\",\"healthErrorCount\":\"问题\",\"healthFreeSpace\":\"可用空间\",\"healthModuleData\":\"模块数据\",\"healthFiles\":\"文件\",\"healthCode\":\"代码\",\"healthCount\":\"数量\",\"healthNoData\":\"暂无健康数据。\",\"healthCheck_boot_paths\":\"启动路径\",\"healthCheck_current_animation\":\"当前动画\",\"healthCheck_stock_backup\":\"原厂备份\",\"healthCheck_history_integrity\":\"历史记录\",\"healthCheck_playlist_integrity\":\"播放列表库\",\"healthCheck_automation_integrity\":\"轮换与队列\",\"healthCheck_staging_integrity\":\"测试暂存\",\"healthCheck_security_state\":\"信任与审计状态\",\"healthCheck_temporary_files\":\"临时文件\",\"healthCheck_orphan_objects\":\"未使用播放列表对象\",\"healthCheck_free_space\":\"可用空间\",\"healthBucket_history\":\"历史记录\",\"healthBucket_playlist\":\"播放列表库\",\"healthBucket_backup\":\"原厂备份\",\"healthBucket_staging\":\"测试暂存\",\"healthBucket_trust\":\"信任与审计\",\"healthBucket_logs\":\"日志\",\"healthBucket_runtime\":\"模块运行时及其他\",\"healthMaintenance\":\"安全维护\",\"healthMaintenanceText\":\"这里仅显示由 Health 检测到的可丢弃或未引用数据。\",\"healthMaintenanceEmpty\":\"目前无需安全维护。\",\"healthAction_cleanup_stale_temps\":\"清理过期临时文件\",\"healthAction_cleanup_orphan_objects\":\"移除未使用的播放列表对象\",\"healthAction_clear_invalid_staging\":\"清除无效测试暂存\",\"healthActionRun\":\"运行维护\",\"healthActionWorking\":\"正在处理…\",\"healthActionConfirm\":\"要执行“{name}”吗？只会修改此维护操作所描述的数据。\",\"healthActionDone\":\"维护已完成。\",\"healthActionFailed\":\"维护失败。\"}}\n;\nconst SUPPORTED_LANGS=[\"en\",\"pt\",\"es\",\"fr\",\"de\",\"it\",\"ja\",\"zh\"];\nconst savedLang=localStorage.getItem(\"bas-webui-lang\")||\"\";\nconst detectedLang=(navigator.language||\"en\").toLowerCase().split(\"-\")[0];\nlet lang=SUPPORTED_LANGS.includes(savedLang)?savedLang:(SUPPORTED_LANGS.includes(detectedLang)?detectedLang:\"en\");\nconst state={info:null,ping:null,probes:null,health:null,history:[],playlists:[],selectedPlaylist:'',rotation:null,activity:null,test:null,logs:null,busy:false,liveTimer:0,livePolling:false,liveDirty:false,liveEpoch:'',liveRevisions:{}};\nconst $=s=>document.querySelector(s),$$=s=>Array.from(document.querySelectorAll(s));\nconst t=(k,f='')=>S[lang]?.[k]||S.en[k]||f||k;\nfunction applyLang(){document.documentElement.lang=lang;$('#language').value=lang;$$('[data-i18n]').forEach(el=>{const k=el.dataset.i18n;if(t(k))el.textContent=t(k)});renderAll();}\nfunction toast(msg,type=''){const el=$('#toast');el.textContent=String(msg||t('done'));el.className='toast show '+type;clearTimeout(toast._t);toast._t=setTimeout(()=>el.className='toast',3000)}\nfunction bytes(n){n=Number(n)||0;if(n<1024)return `${n} B`;if(n<1048576)return `${(n/1024).toFixed(1)} KB`;if(n<1073741824)return `${(n/1048576).toFixed(1)} MB`;return `${(n/1073741824).toFixed(2)} GB`}\nfunction date(v){const n=Number(v)||0;return n?new Date(n).toLocaleString():''}\nfunction esc(v){return String(v??'').replace(/[&<>\"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','\"':'&quot;',\"'\":'&#39;'}[c]))}\nfunction setBusy(v){state.busy=!!v;document.body.classList.toggle('busy',state.busy)}\nasync function fetchAny(path,opts={}){const r=await fetch(path,{cache:'no-store',...opts});if(r.status===401){showAuth();throw new Error(t('offline'))}return r}\nasync function json(path,opts={}){const r=await fetchAny(path,opts);const d=await r.json().catch(()=>({}));if(!r.ok)throw new Error(d.message||`${t('requestFailed')} (${r.status})`);return d}\nasync function postJSON(path,body){return json(path,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body||{})})}\nasync function action(fn){if(state.busy)return;setBusy(true);try{await fn()}catch(e){toast(e.message||t('requestFailed'),'error')}finally{setBusy(false);if(state.liveDirty)scheduleLivePolling(80)}}\nfunction showAuth(){stopLivePolling();$('#main-ui').classList.add('hidden');$('#auth-screen').classList.remove('hidden');$('#status-dot').className='dot warn';$('#status-text').textContent=t('offline')}\nfunction showMain(){ $('#auth-screen').classList.add('hidden');$('#main-ui').classList.remove('hidden');$('#status-dot').className='dot ok';$('#status-text').textContent=t('connected')}\nasync function sessionCheck(){const r=await fetch('/webui/session',{cache:'no-store'});if(r.ok){showMain();return true}showAuth();return false}\nasync function authorize(){return action(async()=>{ $('#auth-progress').classList.remove('hidden');try{const d=await json('/webui/request_auth',{method:'POST'});if(d.status!=='ok')throw new Error(t('authDenied'));showMain();await refreshAll();startLivePolling()}finally{$('#auth-progress').classList.add('hidden')}})}\nasync function logout(){await action(async()=>{await json('/webui/logout',{method:'POST'});toast(t('logoutDone'));showAuth()})}\nfunction card(label,value){return `<div class=\"card\"><div class=\"label\">${esc(label)}</div><div class=\"value\">${esc(value||t('unknown'))}</div></div>`}\nfunction formatMeta(x){const p=[];if(x.width&&x.height)p.push(`${x.width}×${x.height}`);if(x.fps)p.push(`${x.fps} FPS`);if(x.frame_format)p.push(x.frame_format);if(x.size_bytes)p.push(bytes(x.size_bytes));if(x.has_audio)p.push('Audio');return p.join(' · ')}\nfunction activePlaylist(){return state.playlists.find(p=>p.id===state.selectedPlaylist)||state.playlists[0]||null}\nfunction renderOverview(){if(!state.info||!state.ping)return;const next=state.rotation?.next_name||t('nothingPrepared');$('#overview-cards').innerHTML=[card(t('module'),`${state.info.module_version||'—'} · API ${state.info.api_version||1}`),card(t('device'),state.ping.model||state.probes?.system?.model||t('unknown')),card(t('customAnimation'),state.ping.has_custom?t('active'):t('stock')),card(t('nextBoot'),next)].join('')}\nfunction renderTest(){const d=state.test||{};$('#test-status').innerHTML=[card(t('staged'),d.has_staged?`${d.width||'?'}×${d.height||'?'} · ${bytes(d.size_bytes||0)}`:t('notStaged')),card(t('preview'),d.preview_active?t('previewActive'):t('previewInactive'))].join('');['test-start','test-apply','test-clear'].forEach(id=>{$('#'+id).disabled=!d.has_staged})}\nfunction historyButtons(x){return `<div class=\"actions\"><button class=\"btn small\" data-ha=\"preview\" data-id=\"${esc(x.id)}\">${t('preview')}</button><button class=\"btn small\" data-ha=\"apply\" data-id=\"${esc(x.id)}\">${t('apply')}</button><button class=\"btn small\" data-ha=\"download\" data-id=\"${esc(x.id)}\">${t('download')}</button><button class=\"btn small\" data-ha=\"queue\" data-id=\"${esc(x.id)}\">${t('queue')}</button><button class=\"btn small\" data-ha=\"next\" data-id=\"${esc(x.id)}\">${t('useNext')}</button><button class=\"btn small danger\" data-ha=\"delete\" data-id=\"${esc(x.id)}\">${t('delete')}</button></div>`}\nfunction renderHistory(){const root=$('#history-list');if(!state.history.length){root.innerHTML=`<div class=\"empty\">${t('emptyHistory')}</div>`;return}root.innerHTML=state.history.map(x=>`<div class=\"item\"><div class=\"row between\"><div><div class=\"item-title\">${esc(date(x.created_at)||x.id)}</div><div class=\"item-meta\">${esc(formatMeta(x))}</div></div><span class=\"badge\">${esc(x.id)}</span></div><div id=\"hp-${esc(x.id)}\"></div>${historyButtons(x)}</div>`).join('');root.querySelectorAll('[data-ha]').forEach(b=>b.onclick=()=>historyAction(b.dataset.ha,b.dataset.id))}\nasync function historyAction(a,id){const x=state.history.find(i=>String(i.id)===String(id));if(!x)return;if(a==='preview'){return action(async()=>{const holder=$('#hp-'+CSS.escape(String(id)));if(holder.dataset.loaded){holder.innerHTML='';holder.dataset.loaded='';return}const r=await fetchAny('/history/preview?id='+encodeURIComponent(id));if(!r.ok)throw new Error(t('requestFailed'));const blob=await r.blob();const u=URL.createObjectURL(blob);const media=blob.type.startsWith('video/')?document.createElement('video'):document.createElement('img');media.className='preview';media.src=u;if(media.tagName==='VIDEO'){media.controls=true;media.loop=true;media.muted=true;media.playsInline=true}holder.innerHTML='';holder.append(media);holder.dataset.loaded='1'})}if(a==='apply')return action(async()=>{await json('/history/apply?id='+encodeURIComponent(id),{method:'POST'});toast(t('done'),'success');await refreshCore()});if(a==='download')return downloadPath('/history/download?id='+encodeURIComponent(id),'bootanimation-'+id+'.zip');if(a==='queue')return action(async()=>{await postJSON('/queue/add',{source:'history',history_id:String(id),name:date(x.created_at)||'History'});await refreshRotation();toast(t('done'),'success')});if(a==='next')return action(async()=>{await postJSON('/queue/use-next',{source:'history',history_id:String(id),name:date(x.created_at)||'History'});await refreshRotation();toast(t('done'),'success')});if(a==='delete'){if(!confirm(t('confirmDeleteHistory')))return;return action(async()=>{await json('/history/delete?id='+encodeURIComponent(id),{method:'POST'});await refreshHistory();toast(t('done'),'success')})}}\nfunction renderPlaylists(){const select=$('#playlist-select');const prev=state.selectedPlaylist;select.innerHTML=state.playlists.length?state.playlists.map(p=>`<option value=\"${esc(p.id)}\">${esc(p.name)} (${p.items?.length||0})</option>`).join(''):`<option value=\"\">${t('noPlaylists')}</option>`;if(state.playlists.some(p=>p.id===prev))select.value=prev;else if(state.playlists[0]){state.selectedPlaylist=state.playlists[0].id;select.value=state.selectedPlaylist}else state.selectedPlaylist='';const p=activePlaylist();const root=$('#playlist-items');if(!p){root.innerHTML=`<div class=\"empty\">${t('noPlaylists')}</div>`;return}if(!p.items?.length){root.innerHTML=`<div class=\"empty\">${t('emptyPlaylist')}</div>`;return}root.innerHTML=p.items.map((x,i)=>`<div class=\"item\"><div class=\"row between\"><div><div class=\"item-title\">${esc(x.name||'Boot animation')}</div><div class=\"item-meta\">${esc(formatMeta(x))}</div></div><span class=\"badge\">${i+1}</span></div><div class=\"actions\"><button class=\"btn small\" data-pa=\"apply\" data-id=\"${esc(x.id)}\">${t('apply')}</button><button class=\"btn small\" data-pa=\"test\" data-id=\"${esc(x.id)}\">${t('test')}</button><button class=\"btn small\" data-pa=\"download\" data-id=\"${esc(x.id)}\">${t('download')}</button><button class=\"btn small\" data-pa=\"queue\" data-id=\"${esc(x.id)}\">${t('queue')}</button><button class=\"btn small\" data-pa=\"next\" data-id=\"${esc(x.id)}\">${t('useNext')}</button><button class=\"btn small\" data-pa=\"up\" data-id=\"${esc(x.id)}\" ${i===0?'disabled':''}>${t('moveUp')}</button><button class=\"btn small\" data-pa=\"down\" data-id=\"${esc(x.id)}\" ${i===p.items.length-1?'disabled':''}>${t('moveDown')}</button><button class=\"btn small danger\" data-pa=\"remove\" data-id=\"${esc(x.id)}\">${t('remove')}</button></div></div>`).join('');root.querySelectorAll('[data-pa]').forEach(b=>b.onclick=()=>playlistItemAction(b.dataset.pa,b.dataset.id))}\nasync function playlistItemAction(a,id){const p=activePlaylist();const i=p?.items?.findIndex(x=>x.id===id)??-1;if(!p||i<0)return;const x=p.items[i];if(a==='apply')return action(async()=>{await json('/playlist/apply?object='+encodeURIComponent(x.object_id),{method:'POST'});await refreshCore();toast(t('done'),'success')});if(a==='test')return action(async()=>{await json('/playlist/stage?object='+encodeURIComponent(x.object_id),{method:'POST'});await json('/test/start',{method:'POST'});await refreshTest();toast(t('done'),'success')});if(a==='download')return downloadPath('/playlist/download?object='+encodeURIComponent(x.object_id),(x.name||'bootanimation').replace(/[^a-z0-9._-]+/gi,'_')+'.zip');if(a==='queue'||a==='next')return action(async()=>{await postJSON(a==='queue'?'/queue/add':'/queue/use-next',{source:'playlist',object_id:x.object_id,name:x.name||'Boot animation'});await refreshRotation();toast(t('done'),'success')});if(a==='remove'){if(!confirm(t('confirmRemoveItem')))return;return action(async()=>{await postJSON('/playlist/item/remove',{playlist_id:p.id,entry_id:x.id});await refreshPlaylists();toast(t('done'),'success')})}if(a==='up'||a==='down'){const j=a==='up'?i-1:i+1;if(j<0||j>=p.items.length)return;const ids=p.items.map(v=>v.id);[ids[i],ids[j]]=[ids[j],ids[i]];return action(async()=>{await postJSON('/playlist/item/reorder',{playlist_id:p.id,ids});await refreshPlaylists()})}}\nfunction renderRotation(){const r=state.rotation||{};$('#rotation-enabled').checked=!!r.enabled;const rp=$('#rotation-playlist');rp.innerHTML=state.playlists.length?state.playlists.map(p=>`<option value=\"${esc(p.id)}\">${esc(p.name)}</option>`).join(''):`<option value=\"\">${t('noPlaylists')}</option>`;if(r.playlist_id)rp.value=r.playlist_id;$('#rotation-mode').value=r.mode||'sequential';$('#rotation-pause').disabled=!r.enabled;$('#rotation-summary').textContent=!r.enabled?t('rotationOff'):r.paused?t('rotationPaused'):`${t('rotationRunning')} · ${r.playlist_name||''}`;$('#queue-next').innerHTML=`<div class=\"label\">${t('nextBoot')}</div><div class=\"value\">${esc(r.next_name||t('nothingPrepared'))}</div><div class=\"item-meta\">${r.next_source?esc(t('preparedBy').replace('{source}',r.next_source)):''}</div>`;const q=Array.isArray(r.queue)?r.queue:[];const root=$('#queue-list');root.innerHTML=q.length?q.map((x,i)=>`<div class=\"item\"><div class=\"row between\"><div><div class=\"item-title\">${esc(x.name||'Boot animation')}</div><div class=\"item-meta\">${esc(x.origin||'queue')} · ${esc(date(x.added_at))}</div></div><span class=\"queue-index\">#${i+1}</span></div><div class=\"actions\"><button class=\"btn small\" data-qa=\"up\" data-id=\"${esc(x.id)}\" ${i===0?'disabled':''}>${t('moveUp')}</button><button class=\"btn small\" data-qa=\"down\" data-id=\"${esc(x.id)}\" ${i===q.length-1?'disabled':''}>${t('moveDown')}</button><button class=\"btn small danger\" data-qa=\"remove\" data-id=\"${esc(x.id)}\">${t('remove')}</button></div></div>`).join(''):`<div class=\"empty\">${t('emptyQueue')}</div>`;root.querySelectorAll('[data-qa]').forEach(b=>b.onclick=()=>queueAction(b.dataset.qa,b.dataset.id))}\nasync function queueAction(a,id){const q=state.rotation?.queue||[];const i=q.findIndex(x=>x.id===id);if(i<0)return;if(a==='remove')return action(async()=>{await postJSON('/queue/remove',{id});await refreshRotation()});const j=a==='up'?i-1:i+1;if(j<0||j>=q.length)return;const ids=q.map(x=>x.id);[ids[i],ids[j]]=[ids[j],ids[i]];return action(async()=>{await postJSON('/queue/reorder',{ids});await refreshRotation()})}\nfunction renderActivity(){const root=$('#activity-list');const items=state.activity?.items||[];root.innerHTML=items.length?items.map(x=>`<div class=\"item\"><div class=\"row between\"><div><div class=\"item-title\">${esc(x.name||x.kind||x.status)}</div><div class=\"item-meta\">${esc(x.kind||'')} · ${esc(x.reason||x.source||'')} · ${esc(date(x.created_at))}</div></div><span class=\"badge ${x.kind==='completed'?'ok':''}\">${esc(x.status||x.kind||'')}</span></div>${x.message?`<div class=\"item-meta\">${esc(x.message)}</div>`:''}</div>`).join(''):`<div class=\"empty\">${t('emptyActivity')}</div>`}\nfunction renderDevice(){const p=state.probes;if(!p){$('#device-summary').innerHTML='';$('#device-details').innerHTML=`<div class=\"empty\">${t('unknown')}</div>`;return}const s=p.system||{},b=p.boot||{};$('#device-summary').innerHTML=[card(t('device'),[s.manufacturer,s.model].filter(Boolean).join(' ')||t('unknown')),card(t('android'),`${s.android||'?'} · SDK ${s.sdk||'?'}`),card(t('bootTarget'),b.primary_path||t('unknown')),card(t('bootAudio'),b.audio?.state||t('unknown')),card(t('renderer'),b.renderer?.present?(b.renderer.path||t('yes')):t('no')),card(t('mountAccess'),b.global_mount_access?t('yes'):t('no'))].join('');const targets=(b.targets||[]).map(x=>`<div class=\"item\"><div class=\"item-title\">${esc(x.path)}</div><div class=\"item-meta\">global=${x.global_present?'yes':'no'} · overlay=${x.module_overlay?'yes':'no'} · backup=${x.stock_backup?'yes':'no'} · verified=${x.verified_system_path?'yes':'no'}</div></div>`).join('');$('#device-details').innerHTML=`<div class=\"card\"><div class=\"label\">${t('system')}</div><div class=\"kv\"><div>Build</div><div>${esc(s.build_display||s.build_id||'—')}</div><div>Security patch</div><div>${esc(s.security_patch||'—')}</div><div>Slot</div><div>${esc(s.slot||'—')}</div><div>Density</div><div>${esc(s.density_dpi||'—')}</div></div></div><div><h3>${t('targets')}</h3><div class=\"list\" style=\"margin-top:8px\">${targets||`<div class=\"empty\">${t('none')}</div>`}</div></div>`}\nfunction healthStateLabel(v){return v==='ok'?t('healthHealthy'):v==='warn'?t('healthAttention'):v==='error'?t('healthDegraded'):v==='healthy'?t('healthHealthy'):v==='attention'?t('healthAttention'):v==='degraded'?t('healthDegraded'):t('unknown')}\nfunction healthActionName(id){return t('healthAction_'+id,id||t('unknown'))}\nfunction renderHealth(){const h=state.health;if(!h){$('#health-summary').innerHTML='';$('#health-checks').innerHTML=`<div class=\"empty\">${t('healthNoData')}</div>`;$('#health-storage').innerHTML='';$('#health-maintenance').innerHTML=`<div class=\"empty\">${t('healthNoData')}</div>`;return}const st=h.storage||{};$('#health-summary').innerHTML=[card(t('healthOverall'),healthStateLabel(h.overall)),card(t('healthOkCount'),String(h.ok_count??0)),card(t('healthWarnCount'),String(h.warning_count??0)),card(t('healthErrorCount'),String(h.error_count??0)),card(t('healthFreeSpace'),st.filesystem_available?bytes(st.filesystem_free_bytes):t('unknown')),card(t('healthModuleData'),bytes(st.module_bytes||0))].join('');const checks=Array.isArray(h.checks)?h.checks:[];$('#health-checks').innerHTML=checks.length?checks.map(x=>{const meta=[];if(x.code)meta.push(`${t('healthCode')}: ${x.code}`);if(x.count)meta.push(`${t('healthCount')}: ${x.count}`);if(x.bytes)meta.push(bytes(x.bytes));if(x.detail)meta.push(x.detail);const badgeClass=x.state==='ok'?'ok':x.state==='warn'?'warn':'';return `<div class=\"item\"><div class=\"row between\"><div><div class=\"item-title\">${esc(t('healthCheck_'+x.id,x.id||t('unknown')))}</div>${meta.length?`<div class=\"item-meta\">${esc(meta.join(' · '))}</div>`:''}</div><span class=\"badge ${badgeClass}\">${esc(healthStateLabel(x.state))}</span></div></div>`}).join(''):`<div class=\"empty\">${t('healthNoData')}</div>`;const buckets=Array.isArray(st.buckets)?st.buckets:[];$('#health-storage').innerHTML=buckets.map(x=>card(t('healthBucket_'+x.id,x.id),`${bytes(x.bytes||0)} · ${Number(x.files)||0} ${t('healthFiles')}`)).join('');const actions=Array.isArray(h.actions)?h.actions:[];const root=$('#health-maintenance');root.innerHTML=actions.length?actions.map(x=>{const meta=[];if(x.count)meta.push(`${t('healthCount')}: ${x.count}`);if(x.bytes)meta.push(bytes(x.bytes));return `<div class=\"item\"><div class=\"row between\"><div><div class=\"item-title\">${esc(healthActionName(x.id))}</div>${meta.length?`<div class=\"item-meta\">${esc(meta.join(' · '))}</div>`:''}</div><button class=\"btn small\" data-health-action=\"${esc(x.id)}\">${t('healthActionRun')}</button></div></div>`}).join(''):`<div class=\"empty\">${t('healthMaintenanceEmpty')}</div>`;root.querySelectorAll('[data-health-action]').forEach(b=>b.onclick=()=>runHealthAction(b.dataset.healthAction,b))}\nasync function runHealthAction(id,button){const name=healthActionName(id);if(!confirm(t('healthActionConfirm').replace('{name}',name)))return;const old=button.textContent;button.disabled=true;button.textContent=t('healthActionWorking');try{const d=await postJSON('/health/action',{action:id});state.health=d.health||await json('/health/status');renderHealth();toast(t('healthActionDone'),'success')}catch(e){toast(e.message||t('healthActionFailed'),'error');button.disabled=false;button.textContent=old}}\nfunction renderLogs(){const l=state.logs||{};$('#log-current').textContent=l.current||'—';$('#log-previous').textContent=l.previous||'—'}\nfunction renderAll(){if($('#main-ui').classList.contains('hidden'))return;renderOverview();renderTest();renderHistory();renderPlaylists();renderRotation();renderActivity();renderDevice();renderHealth();renderLogs()}\nasync function refreshCore(){const [info,ping]=await Promise.all([json('/info'),json('/ping')]);state.info=info;state.ping=ping;renderOverview()}\nasync function refreshHistory(){state.history=await json('/history/items');if(!Array.isArray(state.history))state.history=[];renderHistory()}\nasync function refreshPlaylists(){const d=await json('/playlist/list');state.playlists=Array.isArray(d.playlists)?d.playlists:[];if(!state.selectedPlaylist||!state.playlists.some(p=>p.id===state.selectedPlaylist))state.selectedPlaylist=state.playlists[0]?.id||'';renderPlaylists();renderRotation()}\nasync function refreshRotation(){state.rotation=await json('/rotation/status');renderRotation();renderOverview()}\nasync function refreshActivity(){state.activity=await json('/activity/list');renderActivity()}\nasync function refreshTest(){state.test=await json('/test/status');renderTest()}\nasync function refreshDevice(){state.probes=await json('/device/probes');renderDevice();renderOverview()}\nasync function refreshHealth(){state.health=await json('/health/status');renderHealth()}\nasync function refreshLogs(){state.logs=await json('/webui/logs');renderLogs()}\nasync function refreshAll(){return action(async()=>{await refreshCore();await Promise.all([refreshHistory(),refreshPlaylists(),refreshRotation(),refreshActivity(),refreshTest(),refreshDevice(),refreshHealth(),refreshLogs()]);renderAll()})}\nfunction liveSupported(){const f=Array.isArray(state.info?.features)?state.info.features:[];return f.includes('live_events')&&f.includes('state_revisions')}\nfunction stopLivePolling(){if(state.liveTimer)clearTimeout(state.liveTimer);state.liveTimer=0;state.livePolling=false;state.liveDirty=false}\nfunction scheduleLivePolling(delay=2200){if(state.liveTimer)clearTimeout(state.liveTimer);state.liveTimer=0;if(!liveSupported()||document.hidden||navigator.onLine===false||$('#main-ui').classList.contains('hidden'))return;state.liveTimer=setTimeout(()=>{state.liveTimer=0;pollLiveRevisions()},Math.max(40,Number(delay)||0))}\nfunction liveRevisionChanged(epoch,revisions){if(!state.liveEpoch)return false;if(String(epoch||'')!==state.liveEpoch)return true;const next=revisions&&typeof revisions==='object'?revisions:{};const keys=new Set([...Object.keys(state.liveRevisions||{}),...Object.keys(next)]);for(const key of keys){if(Number(state.liveRevisions?.[key]||0)!==Number(next[key]||0))return true}return false}\nasync function pollLiveRevisions(){if(state.livePolling||!liveSupported()||document.hidden||navigator.onLine===false||$('#main-ui').classList.contains('hidden')){scheduleLivePolling();return}state.livePolling=true;try{const d=await json('/live/revisions');const next=d?.revisions&&typeof d.revisions==='object'?d.revisions:{};if(liveRevisionChanged(d?.epoch,next))state.liveDirty=true;state.liveEpoch=String(d?.epoch||'');state.liveRevisions={...next};if(state.liveDirty&&!state.busy){state.liveDirty=false;await refreshAll()}}catch(e){}finally{state.livePolling=false;scheduleLivePolling()}}\nfunction startLivePolling(){stopLivePolling();state.liveEpoch='';state.liveRevisions={};if(liveSupported())scheduleLivePolling(80)}\nfunction kickLivePolling(){if(!liveSupported())return;scheduleLivePolling(60)}\nfunction bindLiveLifecycle(){document.addEventListener('visibilitychange',()=>{if(!document.hidden)kickLivePolling()});window.addEventListener('online',kickLivePolling);window.addEventListener('pageshow',kickLivePolling);window.addEventListener('offline',()=>{if(state.liveTimer)clearTimeout(state.liveTimer);state.liveTimer=0})}\nasync function downloadPath(path,name){return action(async()=>{const r=await fetchAny(path);if(!r.ok)throw new Error(t('requestFailed'));const b=await r.blob();const u=URL.createObjectURL(b);const a=document.createElement('a');a.href=u;a.download=name||'download';document.body.append(a);a.click();a.remove();setTimeout(()=>URL.revokeObjectURL(u),1500)})}\nfunction selectedInstallFile(){return $('#install-file').files?.[0]||null}\nasync function uploadFile(endpoint,file,extra={}){if(!file)throw new Error(t('noFile'));const f=new FormData();Object.entries(extra).forEach(([k,v])=>f.append(k,v));f.append('bootanimation',file,'bootanimation.zip');const r=await fetchAny(endpoint,{method:'POST',body:f});const d=await r.json().catch(()=>({}));if(!r.ok)throw new Error(d.message||t('requestFailed'));return d}\nfunction askText(title,value=''){return new Promise(resolve=>{const d=$('#text-dialog'),i=$('#dialog-input');$('#dialog-title').textContent=title;i.value=value;d.returnValue='';d.onclose=()=>resolve(d.returnValue==='ok'?i.value.trim():null);d.showModal();setTimeout(()=>{i.focus();i.select()},30)})}\nasync function createPlaylist(){const name=await askText(t('create'),'');if(!name)return;await action(async()=>{const d=await postJSON('/playlist/create',{name});state.selectedPlaylist=d.id||'';await refreshPlaylists();toast(t('saved'),'success')})}\nasync function renamePlaylist(){const p=activePlaylist();if(!p)return;const name=await askText(t('rename'),p.name);if(!name)return;await action(async()=>{await postJSON('/playlist/rename',{id:p.id,name});await refreshPlaylists()})}\nasync function duplicatePlaylist(){const p=activePlaylist();if(!p)return;const name=await askText(t('duplicate'),p.name+' copy');if(!name)return;await action(async()=>{const d=await postJSON('/playlist/duplicate',{id:p.id,name});state.selectedPlaylist=d.id||state.selectedPlaylist;await refreshPlaylists()})}\nasync function deletePlaylist(){const p=activePlaylist();if(!p||!confirm(t('confirmDeletePlaylist')))return;await action(async()=>{await postJSON('/playlist/delete',{id:p.id});state.selectedPlaylist='';await refreshPlaylists();await refreshRotation()})}\nasync function installNow(){const f=selectedInstallFile();return action(async()=>{await uploadFile('/upload',f);toast(t('done'),'success');await Promise.all([refreshCore(),refreshHistory()])})}\nasync function stageTest(){const f=selectedInstallFile();return action(async()=>{await uploadFile('/test/stage',f);toast(t('staged'),'success');await refreshTest()})}\nasync function playlistAddFile(){const p=activePlaylist(),f=$('#playlist-file').files?.[0];if(!p||!f)throw new Error(t('noFile'));return action(async()=>{await uploadFile('/playlist/item/upload',f,{playlist_id:p.id,name:f.name||'bootanimation.zip'});$('#playlist-file').value='';await refreshPlaylists();toast(t('done'),'success')})}\nasync function saveRotation(){return action(async()=>{const enabled=$('#rotation-enabled').checked;await postJSON('/rotation/configure',{enabled,playlist_id:$('#rotation-playlist').value,mode:$('#rotation-mode').value});await refreshRotation();toast(t('saved'),'success')})}\nasync function logDownload(scope){return downloadPath('/webui/logs/download?scope='+encodeURIComponent(scope),scope==='both'?'boot-creator-logs.txt':`boot-creator-${scope}.log`)}\nfunction bind(){ $$('.nav button').forEach(b=>b.onclick=()=>{$$('.nav button').forEach(x=>x.classList.toggle('active',x===b));$$('[data-page]').forEach(p=>p.classList.toggle('hidden',p.dataset.page!==b.dataset.tab))});$('#authorize').onclick=authorize;$('#logout').onclick=logout;$('#refresh-all').onclick=refreshAll;$('#language').onchange=e=>{lang=e.target.value;localStorage.setItem('bas-webui-lang',lang);applyLang()};$('#install-file').onchange=e=>{const f=e.target.files?.[0];$('#install-file-meta').textContent=f?t('fileSelected').replace('{name}',f.name).replace('{size}',bytes(f.size)):t('noFile')};$('#install-now').onclick=installNow;$('#stage-test').onclick=stageTest;$('#test-start').onclick=()=>action(async()=>{await json('/test/start',{method:'POST'});await refreshTest()});$('#test-apply').onclick=()=>action(async()=>{await json('/test/apply',{method:'POST'});await Promise.all([refreshTest(),refreshCore(),refreshHistory()]);toast(t('done'),'success')});$('#test-clear').onclick=()=>{if(confirm(t('confirmClearStaged')))action(async()=>{await json('/test/clear',{method:'POST'});await refreshTest()})};$('#refresh-history').onclick=()=>action(refreshHistory);$('#refresh-playlists').onclick=()=>action(refreshPlaylists);$('#playlist-select').onchange=e=>{state.selectedPlaylist=e.target.value;renderPlaylists()};$('#playlist-create').onclick=createPlaylist;$('#playlist-rename').onclick=renamePlaylist;$('#playlist-duplicate').onclick=duplicatePlaylist;$('#playlist-delete').onclick=deletePlaylist;$('#playlist-add-file').onclick=playlistAddFile;$('#rotation-save').onclick=saveRotation;$('#rotation-pause').onclick=()=>action(async()=>{await postJSON('/rotation/pause',{paused:!state.rotation?.paused});await refreshRotation()});$('#rotation-prepare').onclick=()=>action(async()=>{await json('/rotation/prepare-next',{method:'POST'});await refreshRotation()});$('#queue-skip').onclick=()=>action(async()=>{await postJSON('/queue/skip-next',{});await refreshRotation()});$('#queue-clear').onclick=()=>{if(confirm(t('confirmClearQueue')))action(async()=>{await postJSON('/queue/clear',{});await refreshRotation()})};$('#refresh-activity').onclick=()=>action(refreshActivity);$('#clear-activity').onclick=()=>{if(confirm(t('confirmClearActivity')))action(async()=>{await json('/activity/clear',{method:'POST'});await refreshActivity()})};$('#refresh-device').onclick=()=>action(refreshDevice);$('#refresh-health').onclick=()=>action(refreshHealth);$('#refresh-logs').onclick=()=>action(refreshLogs);$$('[data-log-download]').forEach(b=>b.onclick=()=>logDownload(b.dataset.logDownload));$('#rescan').onclick=()=>action(async()=>{await json('/rescan',{method:'POST'});await Promise.all([refreshCore(),refreshDevice()]);toast(t('done'),'success')});$('#pull-stock').onclick=()=>downloadPath('/pull?source=system','stock-bootanimation.zip');$('#pull-current').onclick=()=>downloadPath('/pull?source=module','current-bootanimation.zip');$('#restore-stock').onclick=()=>{if(confirm(t('confirmRestore')))action(async()=>{await json('/remove',{method:'POST'});await refreshCore();toast(t('done'),'success')})}}\nasync function start(){bind();bindLiveLifecycle();applyLang();if(await sessionCheck()){await refreshAll();startLivePolling()}}\nstart().catch(e=>{showAuth();toast(e.message||t('requestFailed'),'error')});\n})();\n</script>\n</body>\n</html>\n"

type webUILogsResponse struct {
	Status        string `json:"status"`
	Current       string `json:"current"`
	Previous      string `json:"previous"`
	CurrentBytes  int64  `json:"current_bytes"`
	PreviousBytes int64  `json:"previous_bytes"`
}

type webUISessionResponse struct {
	Status     string `json:"status"`
	Authorized bool   `json:"authorized"`
	ExpiresAt  int64  `json:"expires_at,omitempty"`
}

type authRequest struct {
	nonce  string
	result chan bool
}

type pairRegistration struct {
	token   string
	expires time.Time
}

type asyncAuthRequest struct {
	nonce       string
	clientIP    string
	clientID    string
	clientLabel string
	publicKey   *trustPublicKey
	status      string
	token       string
	expires     time.Time
}

type statusResponse struct {
	Status     string `json:"status"`
	Model      string `json:"model,omitempty"`
	Resolution string `json:"resolution,omitempty"`
	HasCustom  bool   `json:"has_custom,omitempty"`
	BridgeID   string `json:"bridge_id,omitempty"`
	Token      string `json:"token,omitempty"`
	Permission string `json:"permission,omitempty"`
	Message    string `json:"message,omitempty"`
}

type infoResponse struct {
	API                      int      `json:"api_version"`
	BridgeID                 string   `json:"bridge_id,omitempty"`
	Model                    string   `json:"model,omitempty"`
	ModuleVersion            string   `json:"module_version"`
	ModuleVersionCode        int      `json:"module_version_code"`
	CompanionVersionCode     int      `json:"companion_version_code"`
	Features                 []string `json:"features"`
	MaxDirectUploadBytes     int64    `json:"max_direct_upload_bytes,omitempty"`
	DirectUploadWarningBytes int64    `json:"direct_upload_warning_bytes,omitempty"`
	HistoryLimit             int      `json:"history_limit,omitempty"`
}

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

type trustSessionInput struct {
	SessionID string `json:"session_id"`
}

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

type presenceStatusResponse struct {
	Status    string               `json:"status"`
	Clients   []presenceClientView `json:"clients"`
	Operation *operationLeaseView  `json:"operation,omitempty"`
}

type securityAuditActor struct {
	Type       string `json:"type"`
	ClientID   string `json:"client_id,omitempty"`
	Label      string `json:"label,omitempty"`
	Permission string `json:"permission,omitempty"`
	ClientIP   string `json:"client_ip,omitempty"`
}

type securityAuditEvent struct {
	ID        string             `json:"id"`
	Timestamp int64              `json:"timestamp"`
	Category  string             `json:"category"`
	Action    string             `json:"action"`
	Actor     securityAuditActor `json:"actor"`
	Target    string             `json:"target,omitempty"`
	Details   map[string]string  `json:"details,omitempty"`
}

type securityAuditState struct {
	Version int                  `json:"version"`
	Events  []securityAuditEvent `json:"events"`
}

type auditStatusWriter struct {
	http.ResponseWriter
	status int
}

func (w *auditStatusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *auditStatusWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(data)
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

type trustClientInput struct {
	ClientID string `json:"client_id"`
}

type trustPermissionInput struct {
	ClientID   string `json:"client_id"`
	Permission string `json:"permission"`
}

type trustVerifyInput struct {
	ClientID    string `json:"client_id"`
	ChallengeID string `json:"challenge_id"`
	Signature   string `json:"signature"`
}

type historyMetadata struct {
	Version     int    `json:"version"`
	CreatedAt   int64  `json:"created_at"`
	SizeBytes   int64  `json:"size_bytes"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	FPS         int    `json:"fps"`
	FrameCount  int    `json:"frame_count"`
	Parts       int    `json:"parts"`
	FrameFormat string `json:"frame_format"`
	HasAudio    bool   `json:"has_audio"`
}

type historyItemResponse struct {
	ID          string `json:"id"`
	CreatedAt   int64  `json:"created_at"`
	SizeBytes   int64  `json:"size_bytes"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	FPS         int    `json:"fps"`
	FrameCount  int    `json:"frame_count"`
	Parts       int    `json:"parts"`
	FrameFormat string `json:"frame_format"`
	HasAudio    bool   `json:"has_audio"`
	HasPreview  bool   `json:"has_preview"`
	PreviewType string `json:"preview_type,omitempty"`
}

type playlistItemRef struct {
	ID       string `json:"id"`
	ObjectID string `json:"object_id"`
	Name     string `json:"name"`
	AddedAt  int64  `json:"added_at"`
}

type playlistRecord struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	CreatedAt int64             `json:"created_at"`
	UpdatedAt int64             `json:"updated_at"`
	Items     []playlistItemRef `json:"items"`
}

type playlistState struct {
	Version   int              `json:"version"`
	Playlists []playlistRecord `json:"playlists"`
}

type playlistItemResponse struct {
	ID          string `json:"id"`
	ObjectID    string `json:"object_id"`
	Name        string `json:"name"`
	AddedAt     int64  `json:"added_at"`
	SizeBytes   int64  `json:"size_bytes"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	FPS         int    `json:"fps"`
	FrameCount  int    `json:"frame_count"`
	Parts       int    `json:"parts"`
	FrameFormat string `json:"frame_format"`
	HasAudio    bool   `json:"has_audio"`
	HasPreview  bool   `json:"has_preview"`
}

type playlistResponse struct {
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	CreatedAt int64                  `json:"created_at"`
	UpdatedAt int64                  `json:"updated_at"`
	Items     []playlistItemResponse `json:"items"`
}

type playlistListResponse struct {
	Status    string             `json:"status"`
	Playlists []playlistResponse `json:"playlists"`
}

type playlistMutationRequest struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	PlaylistID string   `json:"playlist_id"`
	EntryID    string   `json:"entry_id"`
	IDs        []string `json:"ids"`
}

type bootQueueItem struct {
	ID       string `json:"id"`
	ObjectID string `json:"object_id"`
	Name     string `json:"name"`
	Origin   string `json:"origin,omitempty"`
	AddedAt  int64  `json:"added_at"`
}

type rotationState struct {
	Version             int             `json:"version"`
	Enabled             bool            `json:"enabled"`
	Paused              bool            `json:"paused,omitempty"`
	PlaylistID          string          `json:"playlist_id,omitempty"`
	Mode                string          `json:"mode,omitempty"`
	LastBootEntryID     string          `json:"last_boot_entry_id,omitempty"`
	LastBootObjectID    string          `json:"last_boot_object_id,omitempty"`
	LastBootName        string          `json:"last_boot_name,omitempty"`
	LastBootSource      string          `json:"last_boot_source,omitempty"`
	LastBootAt          int64           `json:"last_boot_at,omitempty"`
	LastRotationEntryID string          `json:"last_rotation_entry_id,omitempty"`
	NextEntryID         string          `json:"next_entry_id,omitempty"`
	NextObjectID        string          `json:"next_object_id,omitempty"`
	NextName            string          `json:"next_name,omitempty"`
	NextSource          string          `json:"next_source,omitempty"`
	NextQueueID         string          `json:"next_queue_id,omitempty"`
	PreparedAt          int64           `json:"prepared_at,omitempty"`
	NextReason          string          `json:"next_reason,omitempty"`
	Queue               []bootQueueItem `json:"queue"`
	Override            *bootQueueItem  `json:"override,omitempty"`
	ShuffleRemaining    []string        `json:"shuffle_remaining,omitempty"`
	UpdatedAt           int64           `json:"updated_at,omitempty"`
	LastError           string          `json:"last_error,omitempty"`
}

type rotationConfigureRequest struct {
	Enabled    bool   `json:"enabled"`
	PlaylistID string `json:"playlist_id"`
	Mode       string `json:"mode"`
}

type rotationPauseRequest struct {
	Paused bool `json:"paused"`
}

type bootQueueMutationRequest struct {
	Source    string   `json:"source"`
	ObjectID  string   `json:"object_id"`
	HistoryID string   `json:"history_id"`
	Name      string   `json:"name"`
	ID        string   `json:"id"`
	IDs       []string `json:"ids"`
}

type rotationResponse struct {
	Status           string          `json:"status"`
	Enabled          bool            `json:"enabled"`
	Paused           bool            `json:"paused"`
	PlaylistID       string          `json:"playlist_id,omitempty"`
	PlaylistName     string          `json:"playlist_name,omitempty"`
	Mode             string          `json:"mode,omitempty"`
	LastBootEntryID  string          `json:"last_boot_entry_id,omitempty"`
	LastBootObjectID string          `json:"last_boot_object_id,omitempty"`
	LastBootName     string          `json:"last_boot_name,omitempty"`
	LastBootSource   string          `json:"last_boot_source,omitempty"`
	LastBootAt       int64           `json:"last_boot_at,omitempty"`
	NextEntryID      string          `json:"next_entry_id,omitempty"`
	NextObjectID     string          `json:"next_object_id,omitempty"`
	NextName         string          `json:"next_name,omitempty"`
	NextSource       string          `json:"next_source,omitempty"`
	PreparedAt       int64           `json:"prepared_at,omitempty"`
	NextReason       string          `json:"next_reason,omitempty"`
	Queue            []bootQueueItem `json:"queue"`
	Override         *bootQueueItem  `json:"override,omitempty"`
	UpdatedAt        int64           `json:"updated_at,omitempty"`
	LastError        string          `json:"last_error,omitempty"`
}

type bootActivityEntry struct {
	ID              string `json:"id"`
	Kind            string `json:"kind"`
	Status          string `json:"status"`
	EntryID         string `json:"entry_id,omitempty"`
	ObjectID        string `json:"object_id,omitempty"`
	Name            string `json:"name,omitempty"`
	Source          string `json:"source,omitempty"`
	Reason          string `json:"reason,omitempty"`
	PreparedAt      int64  `json:"prepared_at,omitempty"`
	BootCompletedAt int64  `json:"boot_completed_at,omitempty"`
	CreatedAt       int64  `json:"created_at"`
	Message         string `json:"message,omitempty"`
}

type bootActivityState struct {
	Version int                 `json:"version"`
	Items   []bootActivityEntry `json:"items"`
}

type bootActivityResponse struct {
	Status string              `json:"status"`
	Limit  int                 `json:"limit"`
	Items  []bootActivityEntry `json:"items"`
}

type uploadResponse struct {
	Status    string   `json:"status"`
	HistoryID string   `json:"history_id,omitempty"`
	SizeBytes int64    `json:"size_bytes,omitempty"`
	Warnings  []string `json:"warnings,omitempty"`
}

type testStageResponse struct {
	Status        string   `json:"status"`
	HasStaged     bool     `json:"has_staged"`
	PreviewActive bool     `json:"preview_active"`
	SizeBytes     int64    `json:"size_bytes,omitempty"`
	Width         int      `json:"width,omitempty"`
	Height        int      `json:"height,omitempty"`
	FPS           int      `json:"fps,omitempty"`
	FrameCount    int      `json:"frame_count,omitempty"`
	Parts         int      `json:"parts,omitempty"`
	FrameFormat   string   `json:"frame_format,omitempty"`
	HasAudio      bool     `json:"has_audio,omitempty"`
	Warnings      []string `json:"warnings,omitempty"`
}

type deviceProbeTarget struct {
	Path               string `json:"path"`
	GlobalPresent      bool   `json:"global_present"`
	ModuleOverlay      bool   `json:"module_overlay"`
	StockBackup        bool   `json:"stock_backup"`
	VerifiedSystemPath bool   `json:"verified_system_path"`
}

type bootArchiveProbe struct {
	Status         string `json:"status"`
	Format         string `json:"format"`
	Width          int    `json:"width,omitempty"`
	Height         int    `json:"height,omitempty"`
	FPS            int    `json:"fps,omitempty"`
	FrameCount     int    `json:"frame_count,omitempty"`
	Parts          int    `json:"parts,omitempty"`
	VideoCount     int    `json:"video_count,omitempty"`
	HasAudioWav    bool   `json:"has_audio_wav,omitempty"`
	HasAudioConfig bool   `json:"has_audio_config,omitempty"`
}

type bootAudioProbe struct {
	State         string   `json:"state"`
	Evidence      []string `json:"evidence,omitempty"`
	PlaySoundProp string   `json:"play_sound_property,omitempty"`
	SetVolumeProp string   `json:"set_volume_property,omitempty"`
}

type bootRendererProbe struct {
	Path            string `json:"path,omitempty"`
	Present         bool   `json:"present"`
	AudioWavMarker  bool   `json:"audio_wav_marker"`
	AudioConfMarker bool   `json:"audio_conf_marker"`
}

type deviceSystemProbe struct {
	Manufacturer  string `json:"manufacturer,omitempty"`
	Brand         string `json:"brand,omitempty"`
	Model         string `json:"model,omitempty"`
	Device        string `json:"device,omitempty"`
	Product       string `json:"product,omitempty"`
	Android       string `json:"android,omitempty"`
	SDK           int    `json:"sdk,omitempty"`
	BuildID       string `json:"build_id,omitempty"`
	BuildDisplay  string `json:"build_display,omitempty"`
	SecurityPatch string `json:"security_patch,omitempty"`
	Slot          string `json:"slot,omitempty"`
	Resolution    string `json:"resolution,omitempty"`
	DensityDPI    int    `json:"density_dpi,omitempty"`
}

type deviceBootProbe struct {
	PrimaryPath           string              `json:"primary_path,omitempty"`
	PrimaryPathConfidence string              `json:"primary_path_confidence"`
	CustomApplied         bool                `json:"custom_applied"`
	Targets               []deviceProbeTarget `json:"targets"`
	StockArchive          bootArchiveProbe    `json:"stock_archive"`
	CurrentArchive        bootArchiveProbe    `json:"current_archive"`
	Renderer              bootRendererProbe   `json:"renderer"`
	Audio                 bootAudioProbe      `json:"audio"`
	ServerMountNamespace  string              `json:"server_mount_namespace,omitempty"`
	InitMountNamespace    string              `json:"init_mount_namespace,omitempty"`
	GlobalMountAccess     bool                `json:"global_mount_access"`
	PathSchema            string              `json:"path_schema,omitempty"`
	PathEnvironmentStatus string              `json:"path_environment_status,omitempty"`
	RescanRecommended     bool                `json:"rescan_recommended"`
}

type deviceIntelligenceResponse struct {
	Status        string            `json:"status"`
	SchemaVersion int               `json:"schema_version"`
	CollectedAt   int64             `json:"collected_at"`
	System        deviceSystemProbe `json:"system"`
	Boot          deviceBootProbe   `json:"boot"`
}

type moduleHealthCheck struct {
	ID     string `json:"id"`
	State  string `json:"state"`
	Code   string `json:"code,omitempty"`
	Detail string `json:"detail,omitempty"`
	Count  int    `json:"count,omitempty"`
	Bytes  int64  `json:"bytes,omitempty"`
}

type moduleHealthAction struct {
	ID         string `json:"id"`
	Count      int    `json:"count,omitempty"`
	Bytes      int64  `json:"bytes,omitempty"`
	Permission string `json:"required_permission"`
}

type moduleHealthActionRequest struct {
	Action string `json:"action"`
}

type moduleHealthActionResponse struct {
	Status       string               `json:"status"`
	Action       string               `json:"action"`
	RemovedCount int                  `json:"removed_count"`
	RemovedBytes int64                `json:"removed_bytes"`
	Health       moduleHealthResponse `json:"health"`
}

type moduleStorageBucket struct {
	ID    string `json:"id"`
	Bytes int64  `json:"bytes"`
	Files int    `json:"files"`
}

type moduleHealthStorage struct {
	FilesystemAvailable  bool                  `json:"filesystem_available"`
	FilesystemTotalBytes int64                 `json:"filesystem_total_bytes"`
	FilesystemFreeBytes  int64                 `json:"filesystem_free_bytes"`
	ModuleBytes          int64                 `json:"module_bytes"`
	ModuleFiles          int                   `json:"module_files"`
	OrphanObjectBytes    int64                 `json:"orphan_object_bytes,omitempty"`
	OrphanObjectCount    int                   `json:"orphan_object_count,omitempty"`
	Buckets              []moduleStorageBucket `json:"buckets"`
}

type moduleHealthResponse struct {
	Status        string               `json:"status"`
	SchemaVersion int                  `json:"schema_version"`
	CollectedAt   int64                `json:"collected_at"`
	Overall       string               `json:"overall"`
	OKCount       int                  `json:"ok_count"`
	WarningCount  int                  `json:"warning_count"`
	ErrorCount    int                  `json:"error_count"`
	Checks        []moduleHealthCheck  `json:"checks"`
	Storage       moduleHealthStorage  `json:"storage"`
	Actions       []moduleHealthAction `json:"actions,omitempty"`
}

func init() {
	out, err := exec.Command("/system/bin/getprop", "ro.product.model").Output()
	if err == nil {
		model := strings.TrimSpace(string(out))
		if model != "" {
			deviceModel = model
		}
	}

	resOut, resErr := exec.Command("/system/bin/wm", "size").Output()
	if resErr == nil {
		matches := resolutionPattern.FindAllString(string(resOut), -1)
		if len(matches) > 0 {
			deviceResolution = matches[len(matches)-1]
		}
	}
}

func readModuleMetadata() (string, int) {
	data, err := os.ReadFile(filepath.Join(ModDir, "module.prop"))
	if err != nil {
		return "unknown", 0
	}

	version := "unknown"
	versionCode := 0
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		switch strings.TrimSpace(key) {
		case "version":
			if v := strings.TrimSpace(value); v != "" {
				version = v
			}
		case "versionCode":
			if v, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
				versionCode = v
			}
		}
	}
	return version, versionCode
}

func moduleFeatures() []string {
	return []string{
		"session_auth",
		"async_auth",
		"direct_upload",
		"pull",
		"history",
		"history_webm",
		"history_details",
		"history_download",
		"history_generated_preview",
		"large_upload",
		"remove",
		"reset",
		"rescan_paths",
		"factory_reset",
		"test_animation",
		"test_staging",
		"playlists",
		"playlist_test",
		"boot_rotation",
		"boot_queue",
		"boot_activity",
		"device_resolution",
		"device_intelligence",
		"module_webui",
		"multi_device_identity",
		"trusted_clients",
		"trust_persistence_choice",
		"trust_permissions",
		"trust_permission_choice",
		"trust_session_management",
		"security_audit",
		"module_health",
		"module_health_maintenance",
		"disconnect_feedback",
		"live_events",
		"state_revisions",
		"client_presence",
		"operation_coordination",
		"multi_client_sessions",
		"update_preservation",
		"path_environment_status",
	}
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

func liveRevisionsHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) || !requireAuthorization(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, liveSnapshot())
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

func writeLog(message string) {
	logPath := ModDir + "/boot_creator.log"
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	_, _ = f.WriteString(fmt.Sprintf("[%s] %s\n", timestamp, message))
}

func showToast(msg string) {
	showToastEvent("", msg)
}

func showToastEvent(key, fallback string, args ...string) {
	go func() {
		commandArgs := []string{
			"broadcast",
			"-a", "com.bootcreator.SHOW_TOAST",
			"-n", "com.bootcreator.companion/.ToastReceiver",
			"-e", "msg", fallback,
		}
		if strings.TrimSpace(key) != "" {
			commandArgs = append(commandArgs, "-e", "key", key)
		}
		if len(args) > 0 && args[0] != "" {
			commandArgs = append(commandArgs, "-e", "arg0", args[0])
		}
		if len(args) > 1 && args[1] != "" {
			commandArgs = append(commandArgs, "-e", "arg0_type", args[1])
		}
		_ = exec.Command("/system/bin/am", commandArgs...).Run()
	}()
}

func decodeJSONRequest(r *http.Request, limit int64, target interface{}) error {
	if r.Body == nil {
		return errors.New("empty request body")
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > limit {
		return errors.New("request body too large")
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return errors.New("empty request body")
	}
	return json.Unmarshal(data, target)
}

func writeJSON(w http.ResponseWriter, statusCode int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(payload)
}

func localDeviceIPs() []net.IP {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	ips := make([]net.IP, 0, 8)
	for _, iface := range interfaces {
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			var ip net.IP
			switch value := address.(type) {
			case *net.IPNet:
				ip = value.IP
			case *net.IPAddr:
				ip = value.IP
			}
			if ip != nil {
				ips = append(ips, ip)
			}
		}
	}
	return ips
}

func isLocalDeviceIPString(value string) bool {
	ip := net.ParseIP(strings.TrimSpace(value))
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	for _, localIP := range localDeviceIPs() {
		if localIP.Equal(ip) {
			return true
		}
	}
	return false
}

func isLocalDeviceRequest(r *http.Request) bool {
	return isLocalDeviceIPString(getIP(r))
}

func isModuleWebUIOrigin(u *url.URL) bool {
	if u == nil || u.Scheme != "http" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	if u.Port() != "4040" {
		return false
	}
	host := u.Hostname()
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	return isLocalDeviceIPString(host)
}

func cleanupWebUISessionsLocked(now time.Time) {
	for token, expires := range webUISessions {
		if !expires.After(now) {
			delete(webUISessions, token)
		}
	}
	if len(webUISessions) <= 12 {
		return
	}
	type sessionExpiry struct {
		token   string
		expires time.Time
	}
	items := make([]sessionExpiry, 0, len(webUISessions))
	for token, expires := range webUISessions {
		items = append(items, sessionExpiry{token: token, expires: expires})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].expires.Before(items[j].expires) })
	for len(webUISessions) > 12 && len(items) > 0 {
		delete(webUISessions, items[0].token)
		items = items[1:]
	}
}

func createWebUISession(w http.ResponseWriter) (time.Time, error) {
	token, err := randomToken(32)
	if err != nil {
		return time.Time{}, err
	}
	expires := time.Now().Add(webUISessionTTL)
	webUISessionMu.Lock()
	cleanupWebUISessionsLocked(time.Now())
	webUISessions[token] = expires
	webUISessionMu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name:     webUISessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Expires:  expires,
		MaxAge:   int(webUISessionTTL.Seconds()),
	})
	return expires, nil
}

func webUISessionExpiry(r *http.Request) (time.Time, bool) {
	if !isLocalDeviceRequest(r) {
		return time.Time{}, false
	}
	cookie, err := r.Cookie(webUISessionCookieName)
	if err != nil || cookie.Value == "" {
		return time.Time{}, false
	}
	now := time.Now()
	webUISessionMu.Lock()
	defer webUISessionMu.Unlock()
	cleanupWebUISessionsLocked(now)
	expires, ok := webUISessions[cookie.Value]
	return expires, ok && expires.After(now)
}

func clearWebUISession(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(webUISessionCookieName); err == nil && cookie.Value != "" {
		webUISessionMu.Lock()
		delete(webUISessions, cookie.Value)
		webUISessionMu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name:     webUISessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	})
}

func isWebUIAuthorized(r *http.Request) bool {
	_, ok := webUISessionExpiry(r)
	return ok
}

func isAllowedOrigin(origin string) bool {
	if origin == "" {
		return true
	}

	u, err := url.Parse(origin)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}

	if u.Scheme == "https" && u.Host == "lumii55.github.io" {
		return true
	}

	if u.Scheme == "http" {
		host := u.Hostname()
		if host == "localhost" || host == "127.0.0.1" || host == "::1" {
			return true
		}
		if isModuleWebUIOrigin(u) {
			return true
		}
	}

	return false
}

func originDisplayLabel(origin string) string {
	if origin == "https://lumii55.github.io" {
		return origin
	}

	u, err := url.Parse(origin)
	if err == nil && u.Scheme == "http" {
		host := u.Hostname()
		if host == "localhost" || host == "127.0.0.1" || host == "::1" {
			return u.Scheme + "://" + host
		}
	}

	return "Local-client"
}

func prepareRequest(w http.ResponseWriter, r *http.Request, allowedMethods ...string) bool {
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	origin := r.Header.Get("Origin")
	if origin != "" {
		if !isAllowedOrigin(origin) {
			http.Error(w, "Origin not allowed", http.StatusForbidden)
			return false
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Boot-Creator-Token, X-Boot-Creator-Client-ID, X-Boot-Creator-Client-Name")
		w.Header().Set("Access-Control-Allow-Methods", strings.Join(allowedMethods, ", ")+
			", OPTIONS")
		if r.Header.Get("Access-Control-Request-Private-Network") == "true" {
			w.Header().Set("Access-Control-Allow-Private-Network", "true")
		}
	}

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return false
	}

	for _, method := range allowedMethods {
		if r.Method == method {
			return true
		}
	}

	w.Header().Set("Allow", strings.Join(allowedMethods, ", "))
	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	return false
}

func getIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func secureEqual(a, b string) bool {
	if len(a) == 0 || len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func randomToken(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func loadOrCreateBridgeID() string {
	path := filepath.Join(ModDir, bridgeIdentityFile)
	if data, err := os.ReadFile(path); err == nil {
		candidate := strings.TrimSpace(string(data))
		if bridgeIDPattern.MatchString(candidate) {
			return candidate
		}
		writeLog("Warning: Existing bridge identity is invalid; generating a new identity.")
	}

	random, err := randomToken(18)
	if err != nil {
		writeLog("Warning: Could not generate persistent bridge identity: " + err.Error())
		return ""
	}
	candidate := "bc_" + random
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(candidate+"\n"), 0600); err != nil {
		writeLog("Warning: Could not persist bridge identity: " + err.Error())
		return candidate
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		writeLog("Warning: Could not activate persistent bridge identity: " + err.Error())
		return candidate
	}
	_ = os.Chmod(path, 0600)
	return candidate
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func securityAuditPath() string {
	return filepath.Join(ModDir, "trust", "security_audit.json")
}

func loadSecurityAuditLocked() (securityAuditState, error) {
	data, err := os.ReadFile(securityAuditPath())
	if os.IsNotExist(err) {
		return securityAuditState{Version: securityAuditVersion, Events: []securityAuditEvent{}}, nil
	}
	if err != nil {
		return securityAuditState{}, err
	}
	var state securityAuditState
	if err := json.Unmarshal(data, &state); err != nil || state.Version != securityAuditVersion {
		return securityAuditState{}, errors.New("invalid security audit state")
	}
	if state.Events == nil {
		state.Events = []securityAuditEvent{}
	}
	if len(state.Events) > securityAuditLimit {
		state.Events = append([]securityAuditEvent(nil), state.Events[len(state.Events)-securityAuditLimit:]...)
	}
	return state, nil
}

func saveSecurityAuditLocked(state securityAuditState) error {
	if err := os.MkdirAll(filepath.Dir(securityAuditPath()), 0700); err != nil {
		return err
	}
	state.Version = securityAuditVersion
	if len(state.Events) > securityAuditLimit {
		state.Events = append([]securityAuditEvent(nil), state.Events[len(state.Events)-securityAuditLimit:]...)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	temp := securityAuditPath() + ".tmp"
	if err := os.WriteFile(temp, append(data, '\n'), 0600); err != nil {
		return err
	}
	if err := os.Rename(temp, securityAuditPath()); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return os.Chmod(securityAuditPath(), 0600)
}

func securityAuditActorForRequest(r *http.Request) securityAuditActor {
	ip := getIP(r)
	if isWebUIAuthorized(r) {
		return securityAuditActor{Type: "webui", Label: "Local Module WebUI", Permission: "admin", ClientIP: ip}
	}
	if session, _, ok := trustedSessionForRequest(r); ok {
		return securityAuditActor{Type: "trusted", ClientID: session.ClientID, Label: session.ClientLabel, Permission: session.Permission, ClientIP: session.ClientIP}
	}
	stateMu.RLock()
	legacy := pairedIP != "" && pairedIP == ip && secureEqual(pairedToken, r.Header.Get("X-Boot-Creator-Token"))
	stateMu.RUnlock()
	if legacy {
		return securityAuditActor{Type: "legacy", Label: "Legacy BAS website", Permission: "admin", ClientIP: ip}
	}
	return securityAuditActor{Type: "unknown", ClientIP: ip}
}

func appendSecurityAudit(event securityAuditEvent) {
	securityAuditMu.Lock()
	defer securityAuditMu.Unlock()
	state, err := loadSecurityAuditLocked()
	if err != nil {
		writeLog("Warning: Could not load Security Audit: " + err.Error())
		state = securityAuditState{Version: securityAuditVersion, Events: []securityAuditEvent{}}
	}
	if event.Timestamp == 0 {
		event.Timestamp = time.Now().UnixMilli()
	}
	if event.ID == "" {
		suffix, err := randomToken(6)
		if err != nil {
			suffix = strconv.FormatInt(time.Now().UnixNano(), 36)
		}
		event.ID = "audit_" + strconv.FormatInt(event.Timestamp, 10) + "_" + suffix
	}
	state.Events = append(state.Events, event)
	if len(state.Events) > securityAuditLimit {
		state.Events = append([]securityAuditEvent(nil), state.Events[len(state.Events)-securityAuditLimit:]...)
	}
	if err := saveSecurityAuditLocked(state); err != nil {
		writeLog("Warning: Could not save Security Audit: " + err.Error())
		return
	}
	emitLiveEvent("audit", "audit.changed")
}

func recordSecurityAudit(r *http.Request, category, action, target string, details map[string]string) {
	appendSecurityAudit(securityAuditEvent{Category: category, Action: action, Actor: securityAuditActorForRequest(r), Target: target, Details: details})
}

func recordCompanionSecurityAudit(action, clientID, label, clientIP string, details map[string]string) {
	appendSecurityAudit(securityAuditEvent{Category: "access", Action: action, Actor: securityAuditActor{Type: "companion", Label: "Companion approval", Permission: "admin"}, Target: clientID, Details: mergeAuditDetails(details, map[string]string{"client_label": cleanClientLabel(label), "client_ip": clientIP})})
}

func mergeAuditDetails(base, extra map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range base {
		if strings.TrimSpace(value) != "" {
			out[key] = value
		}
	}
	for key, value := range extra {
		if strings.TrimSpace(value) != "" {
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func auditedHandler(category, action string, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor := securityAuditActorForRequest(r)
		recorder := &auditStatusWriter{ResponseWriter: w}
		handler(recorder, r)
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		if status >= 200 && status < 400 {
			appendSecurityAudit(securityAuditEvent{Category: category, Action: action, Actor: actor})
			emitLiveAuditEvent(category, action)
		}
	}
}

func securityAuditListHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) || !requireAuthorization(w, r) {
		return
	}
	securityAuditMu.Lock()
	state, err := loadSecurityAuditLocked()
	securityAuditMu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not load Security Audit"})
		return
	}
	events := append([]securityAuditEvent(nil), state.Events...)
	for left, right := 0, len(events)-1; left < right; left, right = left+1, right-1 {
		events[left], events[right] = events[right], events[left]
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "version": securityAuditVersion, "limit": securityAuditLimit, "events": events})
}

func securityAuditClearHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	actor := securityAuditActorForRequest(r)
	securityAuditMu.Lock()
	err := saveSecurityAuditLocked(securityAuditState{Version: securityAuditVersion, Events: []securityAuditEvent{}})
	securityAuditMu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not clear Security Audit"})
		return
	}
	appendSecurityAudit(securityAuditEvent{Category: "security", Action: "audit.cleared", Actor: actor})
	writeJSON(w, http.StatusOK, statusResponse{Status: "cleared"})
	showToastEvent("toast_audit_cleared", "🛡️ Boot Creator: Security Audit was cleared.")
}

func securityAuditExportHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) || !requireAuthorization(w, r) {
		return
	}
	securityAuditMu.Lock()
	state, err := loadSecurityAuditLocked()
	securityAuditMu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not export Security Audit"})
		return
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not export Security Audit"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="boot-animation-studio-security-audit.json"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(append(data, '\n'))
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
	if err := json.Unmarshal(data, &state); err != nil || (state.Version != 1 && state.Version != trustStateVersion) {
		return trustState{}, errors.New("invalid trust state")
	}
	if state.Clients == nil {
		state.Clients = []trustedClientRecord{}
	}
	for i := range state.Clients {
		permission, ok := normalizeTrustPermission(state.Clients[i].Permission)
		if !ok {
			permission = "admin"
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

func trustedAuthChallengeHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	var input trustClientInput
	if err := decodeJSONRequest(r, 8<<10, &input); err != nil || !clientIDPattern.MatchString(strings.TrimSpace(input.ClientID)) {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid trusted-client identity"})
		return
	}
	challengeID, message, err := createTrustChallenge(strings.TrimSpace(input.ClientID), getIP(r))
	if errors.Is(err, os.ErrNotExist) {
		writeJSON(w, http.StatusNotFound, statusResponse{Status: "untrusted"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not create trusted-client challenge"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "challenge", "challenge_id": challengeID, "message": message})
}

func trustedAuthVerifyHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	var input trustVerifyInput
	if err := decodeJSONRequest(r, 16<<10, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid trusted-client proof"})
		return
	}
	token, err := verifyTrustChallenge(input, getIP(r))
	if err != nil {
		writeJSON(w, http.StatusForbidden, statusResponse{Status: "untrusted", Message: "Trusted-client verification failed"})
		return
	}
	permission := "admin"
	if session, _, ok := trustedSessionForToken(strings.TrimSpace(input.ClientID), getIP(r), token); ok {
		permission = session.Permission
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "token": token, "client_id": strings.TrimSpace(input.ClientID), "permission": permission,
		"model": deviceModel, "resolution": deviceResolution, "has_custom": checkHasCustomAnim(), "bridge_id": bridgeID,
	})
	client, _, _ := trustedClientByID(strings.TrimSpace(input.ClientID))
	appendSecurityAudit(securityAuditEvent{Category: "access", Action: "auth.reconnected", Actor: securityAuditActor{Type: "trusted", ClientID: strings.TrimSpace(input.ClientID), Label: client.Label, Permission: permission, ClientIP: getIP(r)}})
	emitLiveEvent("sessions", "sessions.changed")
	writeLog("Trusted BAS client reconnected from IP: " + getIP(r))
}

func trustClientsHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) || !requireAuthorization(w, r) {
		return
	}
	currentID := currentTrustedClientID(r)
	trustMu.Lock()
	state, err := loadTrustStateLocked()
	trustMu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read trusted clients"})
		return
	}
	counts := make(map[string]int)
	trustedSessionMu.RLock()
	for _, session := range trustedSessions {
		counts[session.ClientID]++
	}
	trustedSessionMu.RUnlock()
	clients := make([]trustedClientView, 0, len(state.Clients))
	for _, client := range state.Clients {
		clients = append(clients, trustedClientView{
			ID: client.ID, Label: client.Label, Permission: client.Permission, CreatedAt: client.CreatedAt, LastApprovedAt: client.LastApprovedAt,
			LastSeenAt: client.LastSeenAt, LastIP: client.LastIP, ActiveSessions: counts[client.ID], Current: client.ID == currentID,
		})
	}
	sort.SliceStable(clients, func(i, j int) bool { return clients[i].LastSeenAt > clients[j].LastSeenAt })
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "version": trustStateVersion, "clients": clients})
}

func trustSessionsHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) || !requireAuthorization(w, r) {
		return
	}
	currentSessionID := ""
	if session, _, ok := trustedSessionForRequest(r); ok {
		currentSessionID = session.ID
	}
	sessions := make([]trustSessionView, 0, 8)
	trustedSessionMu.RLock()
	for _, session := range trustedSessions {
		sessions = append(sessions, trustSessionView{
			ID: session.ID, ClientID: session.ClientID, Label: session.ClientLabel, ClientIP: session.ClientIP, Permission: session.Permission,
			Persistent: session.Persistent, CreatedAt: session.CreatedAt.UnixMilli(), LastSeenAt: session.LastSeen.UnixMilli(), Current: session.ID == currentSessionID,
		})
	}
	trustedSessionMu.RUnlock()
	stateMu.RLock()
	legacyIP := pairedIP
	legacyToken := pairedToken
	legacyCreated := pairedCreatedAt
	legacySeen := pairedLastSeen
	stateMu.RUnlock()
	if legacyIP != "" && legacyToken != "" {
		currentLegacy := getIP(r) == legacyIP && secureEqual(r.Header.Get("X-Boot-Creator-Token"), legacyToken)
		sessions = append(sessions, trustSessionView{ID: "legacy", Label: "Legacy BAS website", ClientIP: legacyIP, Permission: "admin", CreatedAt: legacyCreated.UnixMilli(), LastSeenAt: legacySeen.UnixMilli(), Current: currentLegacy, Legacy: true})
	}
	sort.SliceStable(sessions, func(i, j int) bool { return sessions[i].LastSeenAt > sessions[j].LastSeenAt })
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "sessions": sessions})
}

func trustSessionDisconnectHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	actor := securityAuditActorForRequest(r)
	var input trustSessionInput
	if err := decodeJSONRequest(r, 8<<10, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid session request"})
		return
	}
	sessionID := strings.TrimSpace(input.SessionID)
	if sessionID == "legacy" {
		stateMu.Lock()
		existed := pairedIP != "" && pairedToken != ""
		pairedIP = ""
		pairedToken = ""
		pairedCreatedAt = time.Time{}
		pairedLastSeen = time.Time{}
		stateMu.Unlock()
		if !existed {
			writeJSON(w, http.StatusNotFound, statusResponse{Status: "error", Message: "Session not found"})
			return
		}
		writeJSON(w, http.StatusOK, statusResponse{Status: "disconnected"})
		showToastEvent("toast_session_disconnected", "🔌 Boot Creator: A BAS website session was disconnected.")
		appendSecurityAudit(securityAuditEvent{Category: "access", Action: "session.disconnected.admin", Actor: actor, Target: "legacy"})
		writeLog("Admin disconnected the active legacy BAS website session.")
		return
	}
	if !disconnectTrustedSessionByID(sessionID) {
		writeJSON(w, http.StatusNotFound, statusResponse{Status: "error", Message: "Session not found"})
		return
	}
	writeJSON(w, http.StatusOK, statusResponse{Status: "disconnected"})
	showToastEvent("toast_session_disconnected", "🔌 Boot Creator: A BAS website session was disconnected.")
	appendSecurityAudit(securityAuditEvent{Category: "access", Action: "session.disconnected.admin", Actor: actor, Target: sessionID})
	writeLog("Admin disconnected BAS website session: " + sessionID)
}

func trustSessionsDisconnectAllHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	actor := securityAuditActorForRequest(r)
	disconnectAllWebsiteSessions()
	writeJSON(w, http.StatusOK, statusResponse{Status: "disconnected"})
	showToastEvent("toast_sessions_disconnected", "🔌 Boot Creator: All BAS website sessions were disconnected.")
	appendSecurityAudit(securityAuditEvent{Category: "access", Action: "session.disconnected.all", Actor: actor})
	writeLog("Admin disconnected all BAS website sessions without changing persistent trust.")
}

func trustPermissionHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	var input trustPermissionInput
	if err := decodeJSONRequest(r, 8<<10, &input); err != nil || !clientIDPattern.MatchString(strings.TrimSpace(input.ClientID)) {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid trusted-client permission request"})
		return
	}
	permission, ok := normalizeTrustPermission(input.Permission)
	if !ok {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid trusted-client permission"})
		return
	}
	clientID := strings.TrimSpace(input.ClientID)
	if clientID == currentTrustedClientID(r) && permission != "admin" {
		writeJSON(w, http.StatusConflict, statusResponse{Status: "error", Message: "Use another Admin client to change this browser's access level"})
		return
	}
	trustMu.Lock()
	state, err := loadTrustStateLocked()
	found := false
	if err == nil {
		for i := range state.Clients {
			if state.Clients[i].ID == clientID {
				state.Clients[i].Permission = permission
				found = true
				break
			}
		}
		if found {
			err = saveTrustStateLocked(state)
		}
	}
	trustMu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not update trusted-client permission"})
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, statusResponse{Status: "error", Message: "Trusted client not found"})
		return
	}
	trustedSessionMu.Lock()
	for _, session := range trustedSessions {
		if session.ClientID == clientID {
			session.Permission = permission
		}
	}
	trustedSessionMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "client_id": clientID, "permission": permission})
	showToastEvent("toast_permission_changed", "🔐 Boot Creator: Trusted browser access changed to "+permission+".", permission, "permission")
	recordSecurityAudit(r, "access", "trust.permission.changed", clientID, map[string]string{"permission": permission})
	writeLog("Trusted BAS client permission changed: " + clientID + " -> " + permission)
}

func trustRevokeHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	actor := securityAuditActorForRequest(r)
	var input trustClientInput
	if err := decodeJSONRequest(r, 8<<10, &input); err != nil || !clientIDPattern.MatchString(strings.TrimSpace(input.ClientID)) {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid trusted-client identity"})
		return
	}
	clientID := strings.TrimSpace(input.ClientID)
	trustMu.Lock()
	state, err := loadTrustStateLocked()
	if err == nil {
		filtered := state.Clients[:0]
		for _, client := range state.Clients {
			if client.ID != clientID {
				filtered = append(filtered, client)
			}
		}
		state.Clients = filtered
		err = saveTrustStateLocked(state)
	}
	trustMu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not revoke trusted client"})
		return
	}
	revokeTrustedClientSessions(clientID)
	writeJSON(w, http.StatusOK, statusResponse{Status: "revoked"})
	showToastEvent("toast_trust_revoked", "🔐 Boot Creator: Trusted browser access was revoked.")
	appendSecurityAudit(securityAuditEvent{Category: "access", Action: "trust.revoked", Actor: actor, Target: clientID})
	writeLog("Trusted BAS client revoked: " + clientID)
}

func trustRevokeAllHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	actor := securityAuditActorForRequest(r)
	trustMu.Lock()
	state, loadErr := loadTrustStateLocked()
	err := loadErr
	if err == nil {
		err = saveTrustStateLocked(trustState{Version: trustStateVersion, Clients: []trustedClientRecord{}})
	}
	trustMu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not revoke trusted clients"})
		return
	}
	for _, client := range state.Clients {
		revokeTrustedClientSessions(client.ID)
	}
	writeJSON(w, http.StatusOK, statusResponse{Status: "revoked"})
	showToastEvent("toast_trust_revoked_all", "🔐 Boot Creator: All trusted browser access was revoked.")
	appendSecurityAudit(securityAuditEvent{Category: "access", Action: "trust.revoked.all", Actor: actor})
	writeLog("All persistent BAS trusted clients were revoked. Temporary and legacy sessions were left unchanged.")
}

func requestRequiredPermission(r *http.Request) string {
	path := r.URL.Path
	switch path {
	case "/ping", "/disconnect", "/live/revisions", "/live/events", "/presence/status", "/pull", "/history/list", "/history/items", "/history/download", "/history/preview", "/playlist/list", "/playlist/download", "/rotation/status", "/activity/list", "/device/probes", "/health/status", "/test/status":
		return "view"
	case "/upload", "/remove", "/history/apply", "/playlist/preview", "/playlist/apply", "/playlist/stage", "/test/stage", "/test/start", "/test/stop", "/test/apply", "/test/clear", "/test_anim":
		return "control"
	case "/reset", "/rescan", "/health/action", "/history/delete", "/playlist/create", "/playlist/rename", "/playlist/duplicate", "/playlist/delete", "/playlist/reorder", "/playlist/item/upload", "/playlist/item/from-history", "/playlist/item/from-staged", "/playlist/item/remove", "/playlist/item/reorder", "/rotation/configure", "/rotation/prepare-next", "/rotation/pause", "/queue/add", "/queue/use-next", "/queue/reorder", "/queue/remove", "/queue/clear", "/queue/skip-next", "/activity/clear":
		return "manage"
	case "/trust/clients", "/trust/sessions", "/trust/permission", "/trust/revoke", "/trust/revoke-all", "/trust/session/disconnect", "/trust/sessions/disconnect-all", "/audit/list", "/audit/clear", "/audit/export", "/factory_reset":
		return "admin"
	default:
		return "admin"
	}
}

func requestAuthorizationPermission(r *http.Request) (string, bool) {
	if isWebUIAuthorized(r) {
		return "admin", true
	}
	if session, _, ok := trustedSessionForRequest(r); ok {
		permission, valid := normalizeTrustPermission(session.Permission)
		return permission, valid
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
		return "admin", true
	}
	return "", false
}

func isAuthorized(r *http.Request) bool {
	_, ok := requestAuthorizationPermission(r)
	return ok
}

func requireAuthorization(w http.ResponseWriter, r *http.Request) bool {
	permission, ok := requestAuthorizationPermission(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, statusResponse{Status: "error", Message: "Access denied"})
		return false
	}
	required := requestRequiredPermission(r)
	if trustPermissionRank(permission) < trustPermissionRank(required) {
		writeJSON(w, http.StatusForbidden, map[string]any{"status": "forbidden", "message": "This trusted client does not have permission for this action", "permission": permission, "required_permission": required})
		return false
	}
	return true
}

func checkHasCustomAnim() bool {
	paths, err := readSavedPaths()
	if err != nil {
		return false
	}

	for _, targetPath := range paths {
		if info, err := os.Stat(ModDir + targetPath); err == nil && !info.IsDir() {
			return true
		}
	}

	return false
}

func readSavedPaths() ([]string, error) {
	data, err := os.ReadFile(ModDir + "/saved_paths.txt")
	if err != nil {
		return nil, err
	}

	paths := make([]string, 0, 8)
	seen := make(map[string]struct{})
	for _, raw := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		targetPath := strings.TrimSpace(raw)
		if targetPath == "" {
			continue
		}
		cleaned, ok := validateTargetPath(targetPath)
		if !ok {
			return nil, errors.New("invalid saved path")
		}
		if _, exists := seen[cleaned]; exists {
			continue
		}
		seen[cleaned] = struct{}{}
		paths = append(paths, cleaned)
		if len(paths) > maxSavedPaths {
			return nil, errors.New("too many saved paths")
		}
	}

	if len(paths) == 0 {
		return nil, errors.New("no valid paths")
	}

	return paths, nil
}

func validateTargetPath(targetPath string) (string, bool) {
	if len(targetPath) == 0 || len(targetPath) > 512 || !strings.HasPrefix(targetPath, "/") || strings.ContainsRune(targetPath, '\x00') || strings.Contains(targetPath, "\\") {
		return "", false
	}

	cleaned := filepath.Clean(targetPath)
	if cleaned != targetPath || filepath.Base(cleaned) != "bootanimation.zip" {
		return "", false
	}

	allowedRoots := []string{"/system/", "/vendor/", "/product/", "/oem/", "/odm/", "/system_ext/", "/apex/", "/custom/"}
	for _, root := range allowedRoots {
		if strings.HasPrefix(cleaned, root) {
			return cleaned, true
		}
	}

	return "", false
}

func runPathRescan() ([]string, error) {
	cmd := exec.Command("/system/bin/sh", ModDir+"/rescan_paths.sh")
	output, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return nil, errors.New(message)
	}
	return readSavedPaths()
}

func validatePreviewFile(previewPath string) error {
	f, err := os.Open(previewPath)
	if err != nil {
		return err
	}
	defer f.Close()

	header := make([]byte, 512)
	n, readErr := f.Read(header)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return readErr
	}
	if n < 4 {
		return errors.New("preview is empty or invalid")
	}

	contentType := http.DetectContentType(header[:n])
	if contentType != "video/webm" && !(header[0] == 0x1a && header[1] == 0x45 && header[2] == 0xdf && header[3] == 0xa3) {
		return errors.New("preview must be WebM")
	}
	return nil
}

func getPropValue(name string) string {
	if strings.TrimSpace(name) == "" {
		return ""
	}
	output, err := exec.Command("/system/bin/getprop", name).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func parsePositiveInt(value string) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed < 1 {
		return 0
	}
	return parsed
}

func readWMDensity() int {
	output, err := exec.Command("/system/bin/wm", "density").Output()
	if err != nil {
		return 0
	}
	matches := regexp.MustCompile(`[0-9]+`).FindAllString(string(output), -1)
	if len(matches) == 0 {
		return 0
	}
	return parsePositiveInt(matches[len(matches)-1])
}

func globalMountAccessAvailable() bool {
	selfNamespace := mountNamespaceID("/proc/self/ns/mnt")
	initNamespace := mountNamespaceID("/proc/1/ns/mnt")
	if selfNamespace != "unknown" && selfNamespace == initNamespace {
		return true
	}
	for _, path := range []string{"/system/bin/nsenter", "/system/bin/toybox"} {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return true
		}
	}
	if _, err := exec.LookPath("nsenter"); err == nil {
		return true
	}
	return false
}

func fileContainsMarkers(path string, markers []string) (map[string]bool, error) {
	result := make(map[string]bool, len(markers))
	if len(markers) == 0 {
		return result, nil
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return result, errors.New("file unavailable")
	}
	const maxProbeBytes int64 = 64 << 20
	if info.Size() <= 0 || info.Size() > maxProbeBytes {
		return result, errors.New("file outside probe size limit")
	}
	file, err := os.Open(path)
	if err != nil {
		return result, err
	}
	defer file.Close()

	maxMarker := 1
	for _, marker := range markers {
		if len(marker) > maxMarker {
			maxMarker = len(marker)
		}
	}
	buffer := make([]byte, 64<<10)
	overlap := make([]byte, 0, maxMarker-1)
	for {
		n, readErr := file.Read(buffer)
		if n > 0 {
			chunk := make([]byte, 0, len(overlap)+n)
			chunk = append(chunk, overlap...)
			chunk = append(chunk, buffer[:n]...)
			for _, marker := range markers {
				if !result[marker] && bytes.Contains(chunk, []byte(marker)) {
					result[marker] = true
				}
			}
			if len(chunk) >= maxMarker-1 && maxMarker > 1 {
				overlap = append(overlap[:0], chunk[len(chunk)-(maxMarker-1):]...)
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return result, readErr
		}
	}
	return result, nil
}

func findBootAnimationRenderer() bootRendererProbe {
	candidates := []string{
		"/system/bin/bootanimation",
		"/apex/com.android.bootanimation/bin/bootanimation",
		"/product/bin/bootanimation",
		"/system_ext/bin/bootanimation",
		"/vendor/bin/bootanimation",
		"/odm/bin/bootanimation",
	}
	for _, path := range candidates {
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		probe := bootRendererProbe{Path: path, Present: true}
		markers, err := fileContainsMarkers(path, []string{"audio.wav", "audio_conf.txt"})
		if err == nil {
			probe.AudioWavMarker = markers["audio.wav"]
			probe.AudioConfMarker = markers["audio_conf.txt"]
		}
		return probe
	}
	return bootRendererProbe{}
}

func archiveEntrySafe(name string) bool {
	name = strings.ReplaceAll(name, "\\", "/")
	if name == "" || strings.ContainsRune(name, '\x00') || strings.HasPrefix(name, "/") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

func inspectBootArchiveProbe(path string) bootArchiveProbe {
	probe := bootArchiveProbe{Status: "missing", Format: "unknown"}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return probe
	}
	archive, err := zip.OpenReader(path)
	if err != nil {
		probe.Status = "invalid"
		return probe
	}
	defer archive.Close()
	if len(archive.File) == 0 || len(archive.File) > 10000 {
		probe.Status = "invalid"
		return probe
	}

	probe.Status = "ok"
	var descFile *zip.File
	hasVideoDesc := false
	var totalUncompressed uint64
	for _, entry := range archive.File {
		name := strings.ReplaceAll(entry.Name, "\\", "/")
		if !archiveEntrySafe(name) {
			probe.Status = "invalid"
			probe.Format = "unknown"
			return probe
		}
		totalUncompressed += entry.UncompressedSize64
		if totalUncompressed > maxZipUncompressedSize {
			probe.Status = "invalid"
			probe.Format = "unknown"
			return probe
		}
		lower := strings.ToLower(name)
		if name == "desc.txt" {
			descFile = entry
		}
		if name == "videodesc.txt" {
			hasVideoDesc = true
		}
		if !strings.Contains(name, "/") && strings.HasSuffix(lower, ".mp4") {
			probe.VideoCount++
		}
		if strings.Contains(name, "/") && (strings.HasSuffix(lower, ".png") || strings.HasSuffix(lower, ".jpg") || strings.HasSuffix(lower, ".jpeg")) {
			probe.FrameCount++
		}
		if lower == "audio_conf.txt" {
			probe.HasAudioConfig = true
		}
		if lower == "audio.wav" || strings.HasSuffix(lower, "/audio.wav") {
			probe.HasAudioWav = true
		}
	}

	if descFile != nil && descFile.UncompressedSize64 <= 64<<10 {
		reader, openErr := descFile.Open()
		if openErr == nil {
			scanner := bufio.NewScanner(reader)
			if scanner.Scan() {
				fields := strings.Fields(scanner.Text())
				if len(fields) >= 3 {
					probe.Width, _ = strconv.Atoi(fields[0])
					probe.Height, _ = strconv.Atoi(fields[1])
					probe.FPS, _ = strconv.Atoi(fields[2])
				}
			}
			for scanner.Scan() {
				fields := strings.Fields(strings.TrimSpace(scanner.Text()))
				if len(fields) > 0 && (fields[0] == "p" || fields[0] == "c" || fields[0] == "f") {
					probe.Parts++
				}
			}
			_ = reader.Close()
		}
	}

	switch {
	case descFile != nil && probe.FrameCount > 0:
		probe.Format = "aosp_frames"
	case hasVideoDesc && probe.VideoCount > 0:
		probe.Format = "oem_video_sequence"
	case descFile != nil:
		probe.Format = "aosp_desc"
	default:
		probe.Format = "unknown_zip"
	}
	return probe
}

func collectDeviceSystemProbe() deviceSystemProbe {
	slot := strings.TrimSpace(getPropValue("ro.boot.slot_suffix"))
	if slot == "" {
		slot = strings.TrimSpace(getPropValue("ro.boot.slot"))
	}
	slot = strings.TrimPrefix(slot, "_")
	return deviceSystemProbe{
		Manufacturer:  getPropValue("ro.product.manufacturer"),
		Brand:         getPropValue("ro.product.brand"),
		Model:         getPropValue("ro.product.model"),
		Device:        getPropValue("ro.product.device"),
		Product:       getPropValue("ro.product.name"),
		Android:       getPropValue("ro.build.version.release"),
		SDK:           parsePositiveInt(getPropValue("ro.build.version.sdk")),
		BuildID:       getPropValue("ro.build.id"),
		BuildDisplay:  getPropValue("ro.build.display.id"),
		SecurityPatch: getPropValue("ro.build.version.security_patch"),
		Slot:          slot,
		Resolution:    deviceResolution,
		DensityDPI:    readWMDensity(),
	}
}

func currentPathEnvironmentKey() string {
	for _, prop := range []string{"ro.build.fingerprint", "ro.build.display.id", "ro.build.id"} {
		if value := strings.TrimSpace(getPropValue(prop)); value != "" {
			return value
		}
	}
	return ""
}

func pathEnvironmentStatus() string {
	current := currentPathEnvironmentKey()
	if current == "" {
		return "unknown"
	}
	data, err := os.ReadFile(filepath.Join(ModDir, ".paths_environment"))
	if err != nil {
		return "unknown"
	}
	saved := strings.TrimSpace(strings.SplitN(string(data), "\n", 2)[0])
	if saved == "" {
		return "unknown"
	}
	if secureEqual(saved, current) {
		return "current"
	}
	return "changed"
}

func collectDeviceBootProbe() deviceBootProbe {
	pathEnvStatus := pathEnvironmentStatus()
	probe := deviceBootProbe{
		PrimaryPathConfidence: "unknown",
		PathEnvironmentStatus: pathEnvStatus,
		RescanRecommended:     pathEnvStatus == "changed",
		CustomApplied:         checkHasCustomAnim(),
		Targets:               []deviceProbeTarget{},
		StockArchive:          bootArchiveProbe{Status: "missing", Format: "unknown"},
		CurrentArchive:        bootArchiveProbe{Status: "missing", Format: "unknown"},
		ServerMountNamespace:  mountNamespaceID("/proc/self/ns/mnt"),
		InitMountNamespace:    mountNamespaceID("/proc/1/ns/mnt"),
		GlobalMountAccess:     globalMountAccessAvailable(),
	}
	if schemaData, err := os.ReadFile(filepath.Join(ModDir, ".paths_schema")); err == nil {
		probe.PathSchema = strings.TrimSpace(string(schemaData))
	}

	paths, err := readSavedPaths()
	if err == nil {
		for _, targetPath := range paths {
			_, overlayErr := os.Stat(ModDir + targetPath)
			_, backupErr := os.Stat(filepath.Join(ModDir, "backup", targetPath))
			globalPresent := globalRegularFileExists(targetPath)
			backupPresent := backupErr == nil
			probe.Targets = append(probe.Targets, deviceProbeTarget{
				Path:               targetPath,
				GlobalPresent:      globalPresent,
				ModuleOverlay:      overlayErr == nil,
				StockBackup:        backupPresent,
				VerifiedSystemPath: backupPresent,
			})
		}
		if len(paths) > 0 {
			probe.PrimaryPath = paths[0]
			primary := probe.Targets[0]
			switch {
			case primary.VerifiedSystemPath:
				probe.PrimaryPathConfidence = "confirmed"
			case primary.GlobalPresent:
				probe.PrimaryPathConfidence = "observed"
			default:
				probe.PrimaryPathConfidence = "fallback"
			}
		}
	}

	stockPath := ""
	currentPath := ""
	if probe.PrimaryPath != "" {
		backupPath := filepath.Join(ModDir, "backup", probe.PrimaryPath)
		if info, err := os.Stat(backupPath); err == nil && !info.IsDir() {
			stockPath = backupPath
		} else if !probe.CustomApplied {
			if info, err := os.Stat(probe.PrimaryPath); err == nil && !info.IsDir() {
				stockPath = probe.PrimaryPath
			}
		}
		overlayPath := ModDir + probe.PrimaryPath
		if info, err := os.Stat(overlayPath); err == nil && !info.IsDir() {
			currentPath = overlayPath
		} else if stockPath != "" {
			currentPath = stockPath
		}
	}
	if stockPath != "" {
		probe.StockArchive = inspectBootArchiveProbe(stockPath)
	}
	if currentPath != "" {
		probe.CurrentArchive = inspectBootArchiveProbe(currentPath)
	}

	probe.Renderer = findBootAnimationRenderer()
	audio := bootAudioProbe{
		State:         "unknown",
		Evidence:      []string{},
		PlaySoundProp: getPropValue("persist.sys.bootanim.play_sound"),
		SetVolumeProp: getPropValue("ro.bootanim.set_volume"),
	}
	if probe.Renderer.AudioWavMarker {
		audio.State = "observed"
		audio.Evidence = append(audio.Evidence, "renderer_audio_wav_marker")
	}
	if probe.StockArchive.HasAudioWav {
		audio.State = "observed"
		audio.Evidence = append(audio.Evidence, "stock_archive_audio_wav")
	}
	if probe.Renderer.AudioConfMarker {
		audio.Evidence = append(audio.Evidence, "renderer_audio_conf_marker")
	}
	probe.Audio = audio
	return probe
}

func collectPathUsage(root string) (int64, int) {
	var total int64
	files := 0
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if !info.IsDir() {
			total += info.Size()
			files++
		}
		return nil
	})
	return total, files
}

func healthFilesystemUsage() (int64, int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(ModDir, &stat); err != nil {
		return 0, 0, err
	}
	blockSize := int64(stat.Bsize)
	return int64(stat.Blocks) * blockSize, int64(stat.Bavail) * blockSize, nil
}

func healthFileState(path string, expectedVersion int, target interface{}) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, target); err != nil {
		return err
	}
	_ = expectedVersion
	return nil
}

func collectModuleHealth() moduleHealthResponse {
	checks := make([]moduleHealthCheck, 0, 10)
	add := func(check moduleHealthCheck) { checks = append(checks, check) }

	bootProbe := collectDeviceBootProbe()
	paths, pathsErr := readSavedPaths()
	switch {
	case pathsErr != nil:
		add(moduleHealthCheck{ID: "boot_paths", State: "error", Code: "unreadable", Detail: pathsErr.Error()})
	case len(paths) == 0:
		add(moduleHealthCheck{ID: "boot_paths", State: "error", Code: "empty"})
	default:
		add(moduleHealthCheck{ID: "boot_paths", State: "ok", Code: "detected", Detail: paths[0], Count: len(paths)})
	}

	if bootProbe.CustomApplied {
		if bootProbe.CurrentArchive.Status == "ok" {
			add(moduleHealthCheck{ID: "current_animation", State: "ok", Code: "custom_valid", Detail: bootProbe.CurrentArchive.Format})
		} else {
			add(moduleHealthCheck{ID: "current_animation", State: "error", Code: "custom_invalid", Detail: bootProbe.CurrentArchive.Status})
		}
	} else {
		add(moduleHealthCheck{ID: "current_animation", State: "ok", Code: "stock_active", Detail: bootProbe.CurrentArchive.Format})
	}

	if bootProbe.CustomApplied {
		if bootProbe.StockArchive.Status == "ok" {
			add(moduleHealthCheck{ID: "stock_backup", State: "ok", Code: "available", Detail: bootProbe.StockArchive.Format})
		} else {
			add(moduleHealthCheck{ID: "stock_backup", State: "warn", Code: "missing_or_invalid", Detail: bootProbe.StockArchive.Status})
		}
	} else {
		add(moduleHealthCheck{ID: "stock_backup", State: "ok", Code: "not_required"})
	}

	historyInvalid := 0
	historyCount := 0
	if entries, err := os.ReadDir(filepath.Join(ModDir, "history")); err == nil {
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".zip") {
				continue
			}
			id := strings.TrimSuffix(entry.Name(), ".zip")
			if !validHistoryID(id) {
				historyInvalid++
				continue
			}
			historyCount++
			if data, err := os.ReadFile(historyMetadataPath(id)); err == nil {
				var meta historyMetadata
				if json.Unmarshal(data, &meta) != nil || meta.Version != historyMetadataVersion {
					historyInvalid++
				}
			} else if !os.IsNotExist(err) {
				historyInvalid++
			}
		}
	} else if !os.IsNotExist(err) {
		historyInvalid++
	}
	if historyInvalid > 0 {
		add(moduleHealthCheck{ID: "history_integrity", State: "warn", Code: "invalid_entries", Count: historyInvalid})
	} else {
		add(moduleHealthCheck{ID: "history_integrity", State: "ok", Code: "clean", Count: historyCount})
	}

	playlistStateValue := playlistState{Version: playlistStateVersion, Playlists: []playlistRecord{}}
	playlistErr := healthFileState(playlistStatePath(), playlistStateVersion, &playlistStateValue)
	missingObjects := 0
	referenced := make(map[string]struct{})
	if playlistErr == nil {
		if playlistStateValue.Version != 0 && playlistStateValue.Version != playlistStateVersion {
			playlistErr = errors.New("unsupported playlist state version")
		} else {
			for _, playlist := range playlistStateValue.Playlists {
				for _, item := range playlist.Items {
					if !playlistObjectPattern.MatchString(item.ObjectID) {
						missingObjects++
						continue
					}
					referenced[item.ObjectID] = struct{}{}
					info, err := os.Stat(playlistObjectZipPath(item.ObjectID))
					if err != nil || info.IsDir() || info.Size() <= 0 {
						missingObjects++
					}
				}
			}
		}
	}
	if playlistErr != nil {
		add(moduleHealthCheck{ID: "playlist_integrity", State: "error", Code: "state_invalid", Detail: playlistErr.Error()})
	} else if missingObjects > 0 {
		add(moduleHealthCheck{ID: "playlist_integrity", State: "warn", Code: "missing_objects", Count: missingObjects})
	} else {
		add(moduleHealthCheck{ID: "playlist_integrity", State: "ok", Code: "clean", Count: len(referenced)})
	}

	rotationValue := rotationState{Version: rotationStateVersion, Queue: []bootQueueItem{}}
	rotationErr := healthFileState(rotationStatePath(), rotationStateVersion, &rotationValue)
	automationMissing := 0
	if rotationErr == nil && rotationValue.Version != 0 {
		if rotationValue.Version < 1 || rotationValue.Version > rotationStateVersion {
			rotationErr = errors.New("unsupported rotation state version")
		} else {
			if rotationValue.PlaylistID != "" {
				found := false
				for _, playlist := range playlistStateValue.Playlists {
					if playlist.ID == rotationValue.PlaylistID {
						found = true
						break
					}
				}
				if !found {
					automationMissing++
				}
			}
			for _, item := range rotationValue.Queue {
				if item.ObjectID != "" {
					referenced[item.ObjectID] = struct{}{}
					if info, err := os.Stat(playlistObjectZipPath(item.ObjectID)); err != nil || info.IsDir() || info.Size() <= 0 {
						automationMissing++
					}
				}
			}
			if rotationValue.Override != nil && rotationValue.Override.ObjectID != "" {
				referenced[rotationValue.Override.ObjectID] = struct{}{}
				if info, err := os.Stat(playlistObjectZipPath(rotationValue.Override.ObjectID)); err != nil || info.IsDir() || info.Size() <= 0 {
					automationMissing++
				}
			}
			if rotationValue.NextObjectID != "" && (rotationValue.NextSource == "queue" || rotationValue.NextSource == "override") {
				referenced[rotationValue.NextObjectID] = struct{}{}
			}
		}
	}
	if rotationErr != nil {
		add(moduleHealthCheck{ID: "automation_integrity", State: "error", Code: "state_invalid", Detail: rotationErr.Error()})
	} else if automationMissing > 0 {
		add(moduleHealthCheck{ID: "automation_integrity", State: "warn", Code: "missing_references", Count: automationMissing})
	} else {
		add(moduleHealthCheck{ID: "automation_integrity", State: "ok", Code: "clean", Count: len(rotationValue.Queue)})
	}

	stagingInvalid := false
	var stagingInvalidBytes int64
	if info, err := os.Stat(stagedAnimationPath()); err == nil && !info.IsDir() {
		probe := inspectBootArchiveProbe(stagedAnimationPath())
		if probe.Status == "ok" {
			add(moduleHealthCheck{ID: "staging_integrity", State: "ok", Code: "staged_valid", Bytes: info.Size()})
		} else {
			stagingInvalid = true
			stagingInvalidBytes = info.Size()
			add(moduleHealthCheck{ID: "staging_integrity", State: "warn", Code: "staged_invalid", Detail: probe.Status, Bytes: info.Size()})
		}
	} else {
		add(moduleHealthCheck{ID: "staging_integrity", State: "ok", Code: "empty"})
	}

	trustMu.Lock()
	_, trustErr := loadTrustStateLocked()
	trustMu.Unlock()
	securityAuditMu.Lock()
	_, auditErr := loadSecurityAuditLocked()
	securityAuditMu.Unlock()
	if trustErr != nil || auditErr != nil {
		detail := ""
		if trustErr != nil {
			detail = trustErr.Error()
		} else {
			detail = auditErr.Error()
		}
		add(moduleHealthCheck{ID: "security_state", State: "error", Code: "state_invalid", Detail: detail})
	} else {
		add(moduleHealthCheck{ID: "security_state", State: "ok", Code: "clean"})
	}

	staleTemps := 0
	var staleTempBytes int64
	_ = filepath.Walk(ModDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if strings.HasSuffix(info.Name(), ".tmp") && time.Since(info.ModTime()) > 5*time.Minute {
			staleTemps++
			staleTempBytes += info.Size()
		}
		return nil
	})
	if staleTemps > 0 {
		add(moduleHealthCheck{ID: "temporary_files", State: "warn", Code: "stale", Count: staleTemps})
	} else {
		add(moduleHealthCheck{ID: "temporary_files", State: "ok", Code: "clean"})
	}

	orphanCount := 0
	var orphanBytes int64
	if entries, err := os.ReadDir(playlistObjectsDir()); err == nil {
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".zip") {
				continue
			}
			objectID := strings.TrimSuffix(entry.Name(), ".zip")
			if !playlistObjectPattern.MatchString(objectID) {
				continue
			}
			if _, used := referenced[objectID]; used {
				continue
			}
			if info, err := entry.Info(); err == nil {
				orphanBytes += info.Size()
			}
			orphanCount++
		}
	}
	if orphanCount > 0 {
		add(moduleHealthCheck{ID: "orphan_objects", State: "warn", Code: "found", Count: orphanCount, Bytes: orphanBytes})
	} else {
		add(moduleHealthCheck{ID: "orphan_objects", State: "ok", Code: "none"})
	}

	totalFS, freeFS, filesystemErr := healthFilesystemUsage()
	storageState := "ok"
	storageCode := "space_ok"
	storageDetail := ""
	if filesystemErr != nil {
		storageState, storageCode, storageDetail = "warn", "space_unknown", filesystemErr.Error()
	} else if freeFS < 64<<20 {
		storageState, storageCode = "error", "space_critical"
	} else if freeFS < 256<<20 {
		storageState, storageCode = "warn", "space_low"
	}
	add(moduleHealthCheck{ID: "free_space", State: storageState, Code: storageCode, Detail: storageDetail, Bytes: freeFS})

	bucketPaths := []struct{ id, path string }{
		{"history", filepath.Join(ModDir, "history")},
		{"playlist", playlistRootDir()},
		{"backup", filepath.Join(ModDir, "backup")},
		{"staging", filepath.Join(ModDir, "staging")},
		{"trust", filepath.Join(ModDir, "trust")},
	}
	buckets := make([]moduleStorageBucket, 0, len(bucketPaths)+2)
	var categorized int64
	for _, item := range bucketPaths {
		bytes, files := collectPathUsage(item.path)
		buckets = append(buckets, moduleStorageBucket{ID: item.id, Bytes: bytes, Files: files})
		categorized += bytes
	}
	logBytes := int64(0)
	logFiles := 0
	for _, path := range []string{filepath.Join(ModDir, "boot_creator.log"), filepath.Join(ModDir, "boot_creator.previous.log")} {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			logBytes += info.Size()
			logFiles++
		}
	}
	buckets = append(buckets, moduleStorageBucket{ID: "logs", Bytes: logBytes, Files: logFiles})
	categorized += logBytes
	moduleBytes, moduleFiles := collectPathUsage(ModDir)
	runtimeBytes := moduleBytes - categorized
	if runtimeBytes < 0 {
		runtimeBytes = 0
	}
	buckets = append(buckets, moduleStorageBucket{ID: "runtime", Bytes: runtimeBytes, Files: 0})

	okCount, warningCount, errorCount := 0, 0, 0
	for _, check := range checks {
		switch check.State {
		case "error":
			errorCount++
		case "warn":
			warningCount++
		default:
			okCount++
		}
	}
	overall := "healthy"
	if errorCount > 0 {
		overall = "degraded"
	} else if warningCount > 0 {
		overall = "attention"
	}
	actions := make([]moduleHealthAction, 0, 3)
	if staleTemps > 0 {
		actions = append(actions, moduleHealthAction{ID: "cleanup_stale_temps", Count: staleTemps, Bytes: staleTempBytes, Permission: "manage"})
	}
	if orphanCount > 0 {
		actions = append(actions, moduleHealthAction{ID: "cleanup_orphan_objects", Count: orphanCount, Bytes: orphanBytes, Permission: "manage"})
	}
	if stagingInvalid {
		actions = append(actions, moduleHealthAction{ID: "clear_invalid_staging", Count: 1, Bytes: stagingInvalidBytes, Permission: "manage"})
	}
	return moduleHealthResponse{
		Status: "ok", SchemaVersion: 2, CollectedAt: time.Now().UnixMilli(), Overall: overall,
		OKCount: okCount, WarningCount: warningCount, ErrorCount: errorCount, Checks: checks,
		Storage: moduleHealthStorage{FilesystemAvailable: filesystemErr == nil, FilesystemTotalBytes: totalFS, FilesystemFreeBytes: freeFS, ModuleBytes: moduleBytes, ModuleFiles: moduleFiles, OrphanObjectBytes: orphanBytes, OrphanObjectCount: orphanCount, Buckets: buckets},
		Actions: actions,
	}
}

func cleanupHealthStaleTemps() (int, int64, error) {
	removed := 0
	var removedBytes int64
	err := filepath.Walk(ModDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".tmp") || time.Since(info.ModTime()) <= 5*time.Minute {
			return nil
		}
		if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
			return removeErr
		}
		removed++
		removedBytes += info.Size()
		return nil
	})
	return removed, removedBytes, err
}

func cleanupHealthOrphanObjects() (int, int64, error) {
	rotationMu.Lock()
	defer rotationMu.Unlock()
	playlistMu.Lock()
	defer playlistMu.Unlock()
	state, err := loadPlaylistStateLocked()
	if err != nil {
		return 0, 0, err
	}
	automationRefs := automationObjectReferencesFromDisk()
	entries, err := os.ReadDir(playlistObjectsDir())
	if os.IsNotExist(err) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	removed := 0
	var removedBytes int64
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".zip") {
			continue
		}
		objectID := strings.TrimSuffix(entry.Name(), ".zip")
		if !playlistObjectPattern.MatchString(objectID) || playlistObjectReferencedLocked(state, objectID) {
			continue
		}
		if _, used := automationRefs[objectID]; used {
			continue
		}
		paths := []string{playlistObjectZipPath(objectID), playlistObjectMetadataPath(objectID), playlistObjectPreviewPath(objectID)}
		for _, path := range paths {
			if info, statErr := os.Stat(path); statErr == nil && !info.IsDir() {
				removedBytes += info.Size()
			}
			if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
				return removed, removedBytes, removeErr
			}
		}
		removed++
	}
	return removed, removedBytes, nil
}

func cleanupHealthInvalidStaging() (int, int64, error) {
	path := stagedAnimationPath()
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	if info.IsDir() {
		return 0, 0, errors.New("invalid staging path")
	}
	if inspectBootArchiveProbe(path).Status == "ok" {
		return 0, 0, errors.New("staged animation is valid")
	}
	bytes := info.Size()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return 0, 0, err
	}
	cleanupPreviewShadows()
	return 1, bytes, nil
}

func moduleHealthActionHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	var input moduleHealthActionRequest
	if err := decodeJSONRequest(r, 32<<10, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid health maintenance request"})
		return
	}
	action := strings.TrimSpace(input.Action)
	removedCount := 0
	var removedBytes int64
	var err error
	switch action {
	case "cleanup_stale_temps":
		removedCount, removedBytes, err = cleanupHealthStaleTemps()
	case "cleanup_orphan_objects":
		removedCount, removedBytes, err = cleanupHealthOrphanObjects()
	case "clear_invalid_staging":
		removedCount, removedBytes, err = cleanupHealthInvalidStaging()
	default:
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Unsupported health maintenance action"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: err.Error()})
		return
	}
	recordSecurityAudit(r, "maintenance", "health."+action, action, map[string]string{"removed_count": strconv.Itoa(removedCount), "removed_bytes": strconv.FormatInt(removedBytes, 10)})
	writeLog("Health maintenance completed: " + action + " removed=" + strconv.Itoa(removedCount) + " bytes=" + strconv.FormatInt(removedBytes, 10))
	showToastEvent("toast_health_maintenance", "🩺 Boot Creator: Module health maintenance completed.")
	writeJSON(w, http.StatusOK, moduleHealthActionResponse{Status: "ok", Action: action, RemovedCount: removedCount, RemovedBytes: removedBytes, Health: collectModuleHealth()})
}

func moduleHealthHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) || !requireAuthorization(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, collectModuleHealth())
}

func deviceProbesHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}
	response := deviceIntelligenceResponse{
		Status:        "ok",
		SchemaVersion: 1,
		CollectedAt:   time.Now().UnixMilli(),
		System:        collectDeviceSystemProbe(),
		Boot:          collectDeviceBootProbe(),
	}
	writeJSON(w, http.StatusOK, response)
}

func webUIRootHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/webui" && r.URL.Path != "/webui/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !isLocalDeviceRequest(r) {
		http.Error(w, "Module WebUI is local-device only", http.StatusForbidden)
		return
	}
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' blob: data:; media-src 'self' blob:; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	_, _ = io.WriteString(w, webUIHTML)
}

func webUISessionHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) {
		return
	}
	if !isLocalDeviceRequest(r) {
		writeJSON(w, http.StatusForbidden, webUISessionResponse{Status: "local_only", Authorized: false})
		return
	}
	expires, ok := webUISessionExpiry(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, webUISessionResponse{Status: "auth_required", Authorized: false})
		return
	}
	writeJSON(w, http.StatusOK, webUISessionResponse{Status: "ok", Authorized: true, ExpiresAt: expires.UnixMilli()})
}

func webUIRequestAuthHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	if !isLocalDeviceRequest(r) {
		writeJSON(w, http.StatusForbidden, statusResponse{Status: "error", Message: "Module WebUI is local-device only"})
		return
	}

	clientIP := getIP(r)
	nonce, err := randomToken(24)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not create WebUI authorization request"})
		return
	}
	request := &authRequest{nonce: nonce, result: make(chan bool, 1)}
	stateMu.Lock()
	if pendingAuth != nil {
		stateMu.Unlock()
		writeJSON(w, http.StatusConflict, statusResponse{Status: "busy", Message: "Another authorization request is active"})
		return
	}
	pendingAuth = request
	stateMu.Unlock()
	defer func() {
		stateMu.Lock()
		if pendingAuth == request {
			pendingAuth = nil
		}
		stateMu.Unlock()
	}()

	command := "am start -n com.bootcreator.companion/.PromptActivity --es nonce " + shellQuote(nonce) + " --es requester_ip " + shellQuote(clientIP) + " --es request_origin " + shellQuote("Module WebUI")
	if err := exec.Command("su", "2000", "-c", command).Run(); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not open WebUI authorization prompt"})
		writeLog("Error: Failed to launch Module WebUI authorization prompt.")
		return
	}

	select {
	case allowed := <-request.result:
		if !allowed {
			writeJSON(w, http.StatusForbidden, statusResponse{Status: "denied"})
			writeLog("Module WebUI authorization denied on device.")
			return
		}
		expires, err := createWebUISession(w)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not create WebUI session"})
			return
		}
		writeLog("Module WebUI authorized locally until " + expires.Format(time.RFC3339) + ".")
		writeJSON(w, http.StatusOK, statusResponse{Status: "ok"})
	case <-time.After(30 * time.Second):
		writeJSON(w, http.StatusRequestTimeout, statusResponse{Status: "timeout"})
		writeLog("Module WebUI authorization prompt timed out.")
	}
}

func webUILogoutHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	if !isLocalDeviceRequest(r) {
		writeJSON(w, http.StatusForbidden, statusResponse{Status: "error", Message: "Module WebUI is local-device only"})
		return
	}
	clearWebUISession(w, r)
	writeJSON(w, http.StatusOK, statusResponse{Status: "ok"})
	writeLog("Module WebUI local session ended.")
}

func readWebUILogTail(path string) (string, int64) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", 0
	}
	size := info.Size()
	start := int64(0)
	if size > webUILogTailBytes {
		start = size - webUILogTailBytes
	}
	f, err := os.Open(path)
	if err != nil {
		return "", size
	}
	defer f.Close()
	if start > 0 {
		_, _ = f.Seek(start, io.SeekStart)
	}
	data, err := io.ReadAll(io.LimitReader(f, webUILogTailBytes+1))
	if err != nil {
		return "", size
	}
	if start > 0 {
		if index := bytes.IndexByte(data, '\n'); index >= 0 && index+1 < len(data) {
			data = data[index+1:]
		}
	}
	return string(data), size
}

func webUILogsHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) || !requireAuthorization(w, r) {
		return
	}
	current, currentBytes := readWebUILogTail(filepath.Join(ModDir, "boot_creator.log"))
	previous, previousBytes := readWebUILogTail(filepath.Join(ModDir, "boot_creator.previous.log"))
	writeJSON(w, http.StatusOK, webUILogsResponse{Status: "ok", Current: current, Previous: previous, CurrentBytes: currentBytes, PreviousBytes: previousBytes})
}

func webUILogsDownloadHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) || !requireAuthorization(w, r) {
		return
	}
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = "both"
	}
	current, _ := readWebUILogTail(filepath.Join(ModDir, "boot_creator.log"))
	previous, _ := readWebUILogTail(filepath.Join(ModDir, "boot_creator.previous.log"))
	var body, filename string
	switch scope {
	case "current":
		body, filename = current, "boot-creator-current.log"
	case "previous":
		body, filename = previous, "boot-creator-previous.log"
	case "both":
		body = "CURRENT BOOT\n===================================\n" + current + "\n\nPREVIOUS BOOT\n===================================\n" + previous
		filename = "boot-creator-logs.txt"
	default:
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid log scope"})
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename="+filename)
	_, _ = io.WriteString(w, body)
}

func infoHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) {
		return
	}

	version, versionCode := readModuleMetadata()
	writeJSON(w, http.StatusOK, infoResponse{
		API:                      apiVersion,
		BridgeID:                 bridgeID,
		Model:                    deviceModel,
		ModuleVersion:            version,
		ModuleVersionCode:        versionCode,
		CompanionVersionCode:     companionVersionCode,
		Features:                 moduleFeatures(),
		MaxDirectUploadBytes:     maxBootAnimationBytes,
		DirectUploadWarningBytes: directUploadWarningBytes,
		HistoryLimit:             historyLimit,
	})
}

func pingHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) {
		return
	}

	if !isAuthorized(r) {
		writeJSON(w, http.StatusOK, statusResponse{Status: "auth_required"})
		return
	}

	permission, _ := requestAuthorizationPermission(r)
	writeJSON(w, http.StatusOK, statusResponse{
		Status:     "ok",
		Model:      deviceModel,
		Resolution: deviceResolution,
		HasCustom:  checkHasCustomAnim(),
		BridgeID:   bridgeID,
		Permission: permission,
	})
}

func authStartHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}

	approval, hasTrustIdentity, err := readOptionalTrustApproval(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid trusted-client identity"})
		return
	}
	clientID := ""
	clientLabel := ""
	var publicKey *trustPublicKey
	if hasTrustIdentity {
		clientID = approval.ClientID
		clientLabel = approval.Label
		publicKey = approval.PublicKey
	}

	clientIP := getIP(r)
	origin := r.Header.Get("Origin")
	now := time.Now()

	stateMu.Lock()
	if pendingAsyncAuth != nil && now.After(pendingAsyncAuth.expires) {
		pendingAsyncAuth = nil
	}
	if pendingAsyncAuth != nil {
		existing := pendingAsyncAuth
		sameRequester := existing.clientIP == clientIP && existing.clientID == clientID
		if sameRequester && (existing.status == "pending" || existing.status == "processing" || existing.status == "ok") {
			requestID := existing.nonce
			status := existing.status
			if status == "processing" {
				status = "pending"
			}
			stateMu.Unlock()
			writeJSON(w, http.StatusOK, map[string]any{"status": status, "request_id": requestID})
			return
		}
		if sameRequester {
			pendingAsyncAuth = nil
		} else {
			stateMu.Unlock()
			writeJSON(w, http.StatusConflict, statusResponse{Status: "busy"})
			return
		}
	}
	if pendingAuth != nil {
		stateMu.Unlock()
		writeJSON(w, http.StatusConflict, statusResponse{Status: "busy"})
		return
	}

	nonce, err := randomToken(24)
	if err != nil {
		stateMu.Unlock()
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not create authorization request"})
		return
	}
	request := &asyncAuthRequest{
		nonce: nonce, clientIP: clientIP, clientID: clientID, clientLabel: clientLabel, publicKey: publicKey,
		status: "pending", expires: now.Add(45 * time.Second),
	}
	pendingAsyncAuth = request
	stateMu.Unlock()

	originLabel := originDisplayLabel(origin)
	command := "am start -n com.bootcreator.companion/.PromptActivity --es nonce " + shellQuote(nonce) + " --es requester_ip " + shellQuote(clientIP) + " --es request_origin " + shellQuote(originLabel)
	if hasTrustIdentity {
		command += " --es client_label " + shellQuote(clientLabel) + " --es client_id " + shellQuote(clientID) + " --ez trust_choice_supported true --ez permission_choice_supported true"
	}
	if err := exec.Command("su", "2000", "-c", command).Run(); err != nil {
		stateMu.Lock()
		if pendingAsyncAuth == request {
			pendingAsyncAuth = nil
		}
		stateMu.Unlock()
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not open authorization prompt"})
		writeLog("Error: Failed to launch asynchronous authorization prompt for IP: " + clientIP)
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{"status": "pending", "request_id": nonce})
	if hasTrustIdentity {
		writeLog("Trusted-client approval requested by " + clientLabel + " from IP: " + clientIP)
	} else {
		writeLog("Asynchronous connection approval requested by IP: " + clientIP)
	}
}

func authStatusHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) {
		return
	}

	clientIP := getIP(r)
	requestID := strings.TrimSpace(r.URL.Query().Get("request_id"))
	if requestID == "" {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Missing authorization request id"})
		return
	}

	stateMu.Lock()
	request := pendingAsyncAuth
	if request == nil || request.clientIP != clientIP || !secureEqual(request.nonce, requestID) {
		stateMu.Unlock()
		writeJSON(w, http.StatusNotFound, statusResponse{Status: "error", Message: "Authorization request not found"})
		return
	}
	if time.Now().After(request.expires) && request.status == "pending" {
		request.status = "timeout"
	}
	status := request.status
	token := request.token
	if status == "ok" && request.clientID != "" && pendingAsyncAuth == request {
		pendingAsyncAuth = nil
	}
	stateMu.Unlock()

	switch status {
	case "pending", "processing", "denied", "timeout":
		if status == "processing" {
			status = "pending"
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": status, "request_id": requestID})
	case "ok":
		permission := "admin"
		if request.clientID != "" {
			if session, _, ok := trustedSessionForToken(request.clientID, request.clientIP, token); ok {
				permission = session.Permission
			}
		}
		writeJSON(w, http.StatusOK, statusResponse{
			Status:     "ok",
			Model:      deviceModel,
			Resolution: deviceResolution,
			HasCustom:  checkHasCustomAnim(),
			BridgeID:   bridgeID,
			Token:      token,
			Permission: permission,
		})
	default:
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Invalid authorization state"})
	}
}

func requestAuthHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}

	clientIP := getIP(r)
	origin := r.Header.Get("Origin")
	nonce, err := randomToken(24)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not create authorization request"})
		writeLog("Error: Failed to generate authorization nonce.")
		return
	}

	request := &authRequest{
		nonce:  nonce,
		result: make(chan bool, 1),
	}

	stateMu.Lock()
	if pendingAuth != nil || pendingAsyncAuth != nil {
		stateMu.Unlock()
		writeJSON(w, http.StatusConflict, statusResponse{Status: "busy"})
		return
	}
	pendingAuth = request
	stateMu.Unlock()

	defer func() {
		stateMu.Lock()
		if pendingAuth == request {
			pendingAuth = nil
		}
		stateMu.Unlock()
	}()

	originLabel := originDisplayLabel(origin)

	command := "am start -n com.bootcreator.companion/.PromptActivity --es nonce " + shellQuote(nonce) + " --es requester_ip " + shellQuote(clientIP) + " --es request_origin " + shellQuote(originLabel)
	if err := exec.Command("su", "2000", "-c", command).Run(); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not open authorization prompt"})
		writeLog("Error: Failed to launch authorization prompt for IP: " + clientIP)
		return
	}

	select {
	case allowed := <-request.result:
		if !allowed {
			writeJSON(w, http.StatusForbidden, statusResponse{Status: "denied"})
			showToastEvent("toast_connection_denied", "❌ Boot Creator: Connection denied by user.")
			recordCompanionSecurityAudit("auth.denied", "legacy", "Legacy BAS website", clientIP, nil)
			writeLog("Connection denied by user from IP: " + clientIP)
			return
		}

		token, err := randomToken(32)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not create session"})
			writeLog("Error: Failed to generate session token for IP: " + clientIP)
			return
		}

		stateMu.Lock()
		pairedIP = clientIP
		pairedToken = token
		pairedCreatedAt = time.Now()
		pairedLastSeen = pairedCreatedAt
		stateMu.Unlock()

		writeJSON(w, http.StatusOK, statusResponse{
			Status:     "ok",
			Model:      deviceModel,
			Resolution: deviceResolution,
			HasCustom:  checkHasCustomAnim(),
			BridgeID:   bridgeID,
			Token:      token,
		})
		showToastEvent("toast_connected", "✨ Boot Creator: Website connected successfully!")
		recordCompanionSecurityAudit("auth.approved", "legacy", "Legacy BAS website", clientIP, map[string]string{"permission": "admin", "persistent": "false"})
		emitLiveEvent("sessions", "sessions.changed")
		writeLog("Connection allowed by user from IP: " + clientIP)
	case <-time.After(30 * time.Second):
		writeJSON(w, http.StatusRequestTimeout, statusResponse{Status: "timeout"})
		writeLog("Connection prompt timed out for IP: " + clientIP)
	}
}

func authCallbackHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	clientIP := net.ParseIP(getIP(r))
	if clientIP == nil || !clientIP.IsLoopback() {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	nonce := r.URL.Query().Get("nonce")
	allowValue := r.URL.Query().Get("allow")
	persistValue := r.URL.Query().Get("persist")
	permissionValue := strings.TrimSpace(r.URL.Query().Get("permission"))
	if permissionValue == "" {
		permissionValue = "admin"
	}
	permissionValue, permissionValid := normalizeTrustPermission(permissionValue)
	if nonce == "" || (allowValue != "true" && allowValue != "false") || (persistValue != "" && persistValue != "true" && persistValue != "false") || !permissionValid {
		http.Error(w, "Invalid callback", http.StatusBadRequest)
		return
	}

	stateMu.Lock()
	asyncRequest := pendingAsyncAuth
	if asyncRequest != nil && secureEqual(asyncRequest.nonce, nonce) {
		if time.Now().After(asyncRequest.expires) {
			if asyncRequest.status == "pending" {
				asyncRequest.status = "timeout"
			}
			stateMu.Unlock()
			http.Error(w, "Authorization request expired", http.StatusGone)
			return
		}
		if asyncRequest.status != "pending" {
			stateMu.Unlock()
			http.Error(w, "Authorization request already completed", http.StatusConflict)
			return
		}
		if allowValue == "false" {
			deniedIP := asyncRequest.clientIP
			asyncRequest.status = "denied"
			stateMu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			showToastEvent("toast_connection_denied", "❌ Boot Creator: Connection denied by user.")
			recordCompanionSecurityAudit("auth.denied", asyncRequest.clientID, asyncRequest.clientLabel, deniedIP, nil)
			writeLog("Asynchronous connection denied by user from IP: " + deniedIP)
			return
		}

		asyncRequest.status = "processing"
		approvedIP := asyncRequest.clientIP
		approvedClientID := asyncRequest.clientID
		approvedLabel := asyncRequest.clientLabel
		approvedKey := asyncRequest.publicKey
		stateMu.Unlock()

		var token string
		var err error
		if approvedClientID != "" && approvedKey != nil {
			persistTrust := persistValue == "" || persistValue == "true"
			approval := trustApprovalInput{ClientID: approvedClientID, Label: approvedLabel, PublicKey: approvedKey}
			permission := permissionValue
			if persistTrust {
				if err = upsertTrustedClient(approval, approvedIP, permission); err == nil {
					token, err = issueTrustedSession(approvedClientID, approvedLabel, approvedIP, permission, true)
				}
			} else {
				token, err = issueTrustedSession(approvedClientID, approvedLabel, approvedIP, permission, false)
			}
		} else {
			token, err = randomToken(32)
			if err == nil {
				stateMu.Lock()
				pairedIP = approvedIP
				pairedToken = token
				pairedCreatedAt = time.Now()
				pairedLastSeen = pairedCreatedAt
				stateMu.Unlock()
			}
		}
		if err != nil {
			stateMu.Lock()
			if pendingAsyncAuth == asyncRequest {
				asyncRequest.status = "error"
			}
			stateMu.Unlock()
			http.Error(w, "Could not create session", http.StatusInternalServerError)
			writeLog("Error: Failed to create approved website session: " + err.Error())
			return
		}

		stateMu.Lock()
		if pendingAsyncAuth == asyncRequest {
			asyncRequest.token = token
			asyncRequest.status = "ok"
			asyncRequest.expires = time.Now().Add(60 * time.Second)
		}
		stateMu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		showToastEvent("toast_connected", "✨ Boot Creator: Website connected successfully!")
		if approvedClientID != "" {
			persistAudit := "true"
			if persistValue == "false" {
				persistAudit = "false"
			}
			recordCompanionSecurityAudit("auth.approved", approvedClientID, approvedLabel, approvedIP, map[string]string{"permission": permissionValue, "persistent": persistAudit})
			if persistValue == "false" {
				writeLog("BAS client approved for this session only: " + cleanClientLabel(approvedLabel) + " from IP: " + approvedIP)
			} else {
				writeLog("Trusted BAS client approved persistently: " + cleanClientLabel(approvedLabel) + " from IP: " + approvedIP)
			}
		} else {
			writeLog("Asynchronous connection allowed by user from IP: " + approvedIP)
		}
		emitLiveEvent("sessions", "sessions.changed")
		return
	}

	request := pendingAuth
	valid := request != nil && secureEqual(request.nonce, nonce)
	stateMu.Unlock()

	if !valid {
		http.Error(w, "Authorization request not found", http.StatusForbidden)
		return
	}

	select {
	case request.result <- allowValue == "true":
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "Authorization request already completed", http.StatusConflict)
	}
}

func validPairToken(token string) bool {
	return pairTokenPattern.MatchString(token)
}

func localIPv4() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range interfaces {
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			ipNet, ok := address.(*net.IPNet)
			if !ok || ipNet.IP.IsLoopback() {
				continue
			}
			ip := ipNet.IP.To4()
			if ip != nil && ip.IsPrivate() {
				return ip.String()
			}
		}
	}
	return ""
}

func pairRegisterHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	clientIP := net.ParseIP(getIP(r))
	if clientIP == nil || !clientIP.IsLoopback() {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	token := r.URL.Query().Get("token")
	if !validPairToken(token) {
		http.Error(w, "Invalid pairing token", http.StatusBadRequest)
		return
	}

	stateMu.Lock()
	pendingPair = &pairRegistration{token: token, expires: time.Now().Add(2 * time.Minute)}
	stateMu.Unlock()

	writeJSON(w, http.StatusOK, statusResponse{Status: "ready"})
	phoneIP := localIPv4()
	if phoneIP != "" {
		showToastEvent("toast_qr_approved_ip", "👀 Boot Creator: QR approved. Return to the other device. Phone IP: "+phoneIP, phoneIP)
	} else {
		showToastEvent("toast_qr_approved", "👀 Boot Creator: QR approved. Return to the other device to finish pairing.")
	}
	writeLog("QR pairing token registered locally.")
}

func pairStatusHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) {
		return
	}

	token := r.URL.Query().Get("token")
	if !validPairToken(token) {
		writeJSON(w, http.StatusOK, statusResponse{Status: "waiting"})
		return
	}

	stateMu.Lock()
	pair := pendingPair
	if pair != nil && time.Now().After(pair.expires) {
		pendingPair = nil
		pair = nil
	}
	ready := pair != nil && secureEqual(pair.token, token)
	stateMu.Unlock()

	if ready {
		writeJSON(w, http.StatusOK, statusResponse{Status: "ready"})
		return
	}
	writeJSON(w, http.StatusOK, statusResponse{Status: "waiting"})
}

func pairExchangeHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}

	token := r.URL.Query().Get("token")
	if !validPairToken(token) {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid pairing token"})
		return
	}

	stateMu.Lock()
	pair := pendingPair
	if pair != nil && time.Now().After(pair.expires) {
		pendingPair = nil
		pair = nil
	}
	if pair == nil || !secureEqual(pair.token, token) {
		stateMu.Unlock()
		writeJSON(w, http.StatusForbidden, statusResponse{Status: "error", Message: "Pairing token not registered"})
		return
	}
	if pendingAuth != nil {
		stateMu.Unlock()
		writeJSON(w, http.StatusConflict, statusResponse{Status: "busy"})
		return
	}

	nonce, err := randomToken(24)
	if err != nil {
		stateMu.Unlock()
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not create pairing request"})
		return
	}

	request := &authRequest{nonce: nonce, result: make(chan bool, 1)}
	pendingAuth = request
	stateMu.Unlock()

	defer func() {
		stateMu.Lock()
		if pendingAuth == request {
			pendingAuth = nil
		}
		stateMu.Unlock()
	}()

	clientIP := getIP(r)
	originLabel := originDisplayLabel(r.Header.Get("Origin"))
	command := "am start -n com.bootcreator.companion/.PromptActivity --ez auto_pair true --es nonce " + shellQuote(nonce) + " --es pair_token " + shellQuote(token) + " --es requester_ip " + shellQuote(clientIP) + " --es request_origin " + shellQuote(originLabel)
	if err := exec.Command("su", "2000", "-c", command).Run(); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not verify pairing approval"})
		writeLog("Error: Failed to verify QR pairing for IP: " + clientIP)
		return
	}

	select {
	case allowed := <-request.result:
		if !allowed {
			writeJSON(w, http.StatusForbidden, statusResponse{Status: "denied"})
			writeLog("QR pairing verification failed for IP: " + clientIP)
			return
		}

		session, err := randomToken(32)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not create session"})
			return
		}

		stateMu.Lock()
		pairedIP = clientIP
		pairedToken = session
		pairedCreatedAt = time.Now()
		pairedLastSeen = pairedCreatedAt
		pendingPair = nil
		stateMu.Unlock()

		writeJSON(w, http.StatusOK, statusResponse{
			Status:     "ok",
			Model:      deviceModel,
			Resolution: deviceResolution,
			HasCustom:  checkHasCustomAnim(),
			BridgeID:   bridgeID,
			Token:      session,
		})
		showToastEvent("toast_qr_completed", "✨ Boot Creator: QR pairing completed successfully!")
		emitLiveEvent("sessions", "sessions.changed")
		writeLog("QR pairing completed for IP: " + clientIP)
	case <-time.After(12 * time.Second):
		writeJSON(w, http.StatusRequestTimeout, statusResponse{Status: "timeout"})
		writeLog("QR pairing verification timed out for IP: " + clientIP)
	}
}

func normalizeDisconnectReason(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "page_unload":
		return "page_unload"
	default:
		return "manual"
	}
}

func disconnectFeedback(reason string) (string, string, string) {
	if normalizeDisconnectReason(reason) == "page_unload" {
		return "toast_disconnected_page", "🔌 Boot Creator: Website closed or reloaded. Session ended.", "session.disconnected.page_unload"
	}
	return "toast_disconnected_manual", "🔌 Boot Creator: Website disconnected manually.", "session.disconnected.self"
}

func disconnectWebsiteSessionByToken(clientIP, token string) (securityAuditActor, string, bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return securityAuditActor{}, "", false
	}
	key := trustedSessionKey(token)
	trustedSessionMu.Lock()
	if session := trustedSessions[key]; session != nil && session.ClientIP == clientIP {
		actor := securityAuditActor{Type: "trusted", ClientID: session.ClientID, Label: session.ClientLabel, Permission: session.Permission, ClientIP: session.ClientIP}
		delete(trustedSessions, key)
		trustedSessionMu.Unlock()
		return actor, "trusted", true
	}
	trustedSessionMu.Unlock()

	stateMu.Lock()
	legacy := pairedIP != "" && pairedIP == clientIP && secureEqual(pairedToken, token)
	if legacy {
		pairedIP = ""
		pairedToken = ""
		pairedCreatedAt = time.Time{}
		pairedLastSeen = time.Time{}
		pendingAsyncAuth = nil
	}
	stateMu.Unlock()
	if legacy {
		return securityAuditActor{Type: "legacy", Label: "Legacy BAS website", Permission: "admin", ClientIP: clientIP}, "legacy", true
	}
	return securityAuditActor{}, "", false
}

func disconnectBeaconHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid disconnect request"})
		return
	}
	actor, kind, ok := disconnectWebsiteSessionByToken(getIP(r), r.FormValue("token"))
	if !ok {
		writeJSON(w, http.StatusUnauthorized, statusResponse{Status: "error", Message: "Access denied"})
		return
	}
	if kind == "legacy" {
		if err := clearStagedTestState(); err != nil {
			writeLog("Warning: Could not fully clear Test Lab staging during page disconnect: " + err.Error())
		}
	}
	key, fallback, action := disconnectFeedback(r.FormValue("reason"))
	w.WriteHeader(http.StatusNoContent)
	showToastEvent(key, fallback)
	appendSecurityAudit(securityAuditEvent{Category: "access", Action: action, Actor: actor, Target: kind, Details: map[string]string{"reason": normalizeDisconnectReason(r.FormValue("reason"))}})
	emitLiveEvent("sessions", "sessions.changed")
	writeLog("BAS website session ended through unload-safe disconnect (" + kind + ").")
}

func disconnectHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}
	actor := securityAuditActorForRequest(r)
	reason := normalizeDisconnectReason(r.URL.Query().Get("reason"))
	toastKey, toastFallback, auditAction := disconnectFeedback(reason)

	if revokeTrustedSessionFromRequest(r) {
		writeJSON(w, http.StatusOK, statusResponse{Status: "disconnected"})
		showToastEvent(toastKey, toastFallback)
		appendSecurityAudit(securityAuditEvent{Category: "access", Action: auditAction, Actor: actor, Details: map[string]string{"reason": reason}})
		emitLiveEvent("sessions", "sessions.changed")
		if reason == "page_unload" {
			writeLog("Trusted BAS client session ended because the website was closed or reloaded.")
		} else {
			writeLog("Trusted BAS client session disconnected manually without affecting other clients.")
		}
		return
	}

	if err := clearStagedTestState(); err != nil {
		writeLog("Warning: Could not fully clear Test Lab staging during disconnect: " + err.Error())
	}

	stateMu.Lock()
	pairedIP = ""
	pairedToken = ""
	pairedCreatedAt = time.Time{}
	pairedLastSeen = time.Time{}
	pendingAsyncAuth = nil
	stateMu.Unlock()

	writeJSON(w, http.StatusOK, statusResponse{Status: "disconnected"})
	showToastEvent(toastKey, toastFallback)
	appendSecurityAudit(securityAuditEvent{Category: "access", Action: auditAction, Actor: actor, Target: "legacy", Details: map[string]string{"reason": reason}})
	emitLiveEvent("sessions", "sessions.changed")
	if reason == "page_unload" {
		writeLog("Legacy BAS website session ended because the website was closed or reloaded. Test Lab staging cleared.")
	} else {
		writeLog("Website disconnected manually by user. Test Lab staging cleared.")
	}
}

func cleanHistory(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	var zips []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".zip") {
			zips = append(zips, entry.Name())
		}
	}

	if len(zips) <= historyLimit {
		return
	}

	sort.Strings(zips)
	for i := 0; i < len(zips)-historyLimit; i++ {
		base := strings.TrimSuffix(zips[i], ".zip")
		_ = os.Remove(filepath.Join(dir, base+".zip"))
		_ = os.Remove(filepath.Join(dir, base+".webm"))
		_ = os.Remove(filepath.Join(dir, base+".gif"))
		_ = os.Remove(filepath.Join(dir, base+".json"))
	}
	showToastEvent("toast_history_cleanup", "🧹 Boot Creator: Automatic cleanup removed old animations from history.")
	writeLog("Automatic history cleanup performed. Oldest animations deleted.")
}

func newHistoryID(dir string) string {
	id := time.Now().UnixMilli()
	for {
		candidate := strconv.FormatInt(id, 10)
		if _, err := os.Stat(filepath.Join(dir, candidate+".zip")); os.IsNotExist(err) {
			return candidate
		}
		id++
	}
}

func copyFile(source, destination string, mode os.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()

	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}

	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func linkOrCopyFile(source, destination string, mode os.FileMode) error {
	_ = os.Remove(destination)
	if err := os.Link(source, destination); err == nil {
		return os.Chmod(destination, mode)
	}
	return copyFile(source, destination, mode)
}

func saveMultipartFile(file io.Reader, destination string, limit int64) error {
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}

	limited := io.LimitReader(file, limit+1)
	written, copyErr := io.Copy(output, limited)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written > limit {
		_ = os.Remove(destination)
		return errors.New("file too large")
	}
	return nil
}

func historyCreatedAt(id string) int64 {
	value, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return 0
	}
	if len(id) < 13 {
		value *= 1000
	}
	return value
}

func inspectBootAnimation(zipPath, id string) (historyMetadata, error) {
	archive, err := zip.OpenReader(zipPath)
	if err != nil {
		return historyMetadata{}, errors.New("invalid zip file")
	}
	defer archive.Close()

	if len(archive.File) == 0 || len(archive.File) > 10000 {
		return historyMetadata{}, errors.New("invalid zip contents")
	}

	info, err := os.Stat(zipPath)
	if err != nil || info.IsDir() {
		return historyMetadata{}, errors.New("could not inspect zip file")
	}

	meta := historyMetadata{
		Version:   historyMetadataVersion,
		CreatedAt: historyCreatedAt(id),
		SizeBytes: info.Size(),
	}
	if meta.CreatedAt == 0 {
		meta.CreatedAt = info.ModTime().UnixMilli()
	}

	var descFile *zip.File
	var totalUncompressed uint64
	formats := make(map[string]struct{})

	for _, entry := range archive.File {
		name := strings.ReplaceAll(entry.Name, "\\", "/")
		if strings.ContainsRune(name, '\x00') || strings.HasPrefix(name, "/") {
			return historyMetadata{}, errors.New("unsafe zip path")
		}
		for _, part := range strings.Split(name, "/") {
			if part == ".." {
				return historyMetadata{}, errors.New("unsafe zip path")
			}
		}

		totalUncompressed += entry.UncompressedSize64
		if totalUncompressed > maxZipUncompressedSize {
			return historyMetadata{}, errors.New("zip expands beyond the allowed size")
		}

		if name == "desc.txt" {
			descFile = entry
		}
		lower := strings.ToLower(name)
		if strings.Contains(name, "/") {
			switch {
			case strings.HasSuffix(lower, ".png"):
				meta.FrameCount++
				formats["PNG"] = struct{}{}
			case strings.HasSuffix(lower, ".jpg"), strings.HasSuffix(lower, ".jpeg"):
				meta.FrameCount++
				formats["JPEG"] = struct{}{}
			}
		}
		if strings.HasSuffix(lower, "/audio.wav") || lower == "audio.wav" {
			meta.HasAudio = true
		}
	}

	if descFile == nil {
		return historyMetadata{}, errors.New("desc.txt not found")
	}
	if meta.FrameCount == 0 {
		return historyMetadata{}, errors.New("no animation frames found")
	}
	if descFile.UncompressedSize64 > 64<<10 {
		return historyMetadata{}, errors.New("desc.txt is too large")
	}

	descReader, err := descFile.Open()
	if err != nil {
		return historyMetadata{}, errors.New("could not read desc.txt")
	}
	defer descReader.Close()

	scanner := bufio.NewScanner(descReader)
	if !scanner.Scan() {
		return historyMetadata{}, errors.New("desc.txt is empty")
	}
	fields := strings.Fields(scanner.Text())
	if len(fields) < 3 {
		return historyMetadata{}, errors.New("invalid desc.txt header")
	}

	width, widthErr := strconv.Atoi(fields[0])
	height, heightErr := strconv.Atoi(fields[1])
	fps, fpsErr := strconv.Atoi(fields[2])
	if widthErr != nil || heightErr != nil || fpsErr != nil || width < 1 || height < 1 || fps < 1 || width > 16384 || height > 16384 || fps > 240 {
		return historyMetadata{}, errors.New("invalid desc.txt dimensions or fps")
	}
	meta.Width = width
	meta.Height = height
	meta.FPS = fps

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) > 0 && (parts[0] == "p" || parts[0] == "c") {
			meta.Parts++
		}
	}
	if err := scanner.Err(); err != nil {
		return historyMetadata{}, errors.New("could not parse desc.txt")
	}

	switch len(formats) {
	case 1:
		for format := range formats {
			meta.FrameFormat = format
		}
	default:
		meta.FrameFormat = "Mixed"
	}
	return meta, nil
}

func validateBootAnimation(zipPath string) error {
	_, err := inspectBootAnimation(zipPath, "")
	return err
}

func historyMetadataPath(id string) string {
	return filepath.Join(ModDir, "history", id+".json")
}

func writeHistoryMetadata(id string, meta historyMetadata) error {
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	path := historyMetadataPath(id)
	temp := path + ".tmp"
	if err := os.WriteFile(temp, append(data, '\n'), 0644); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

func ensureHistoryMetadata(id string) (historyMetadata, error) {
	path := historyMetadataPath(id)
	if data, err := os.ReadFile(path); err == nil {
		var meta historyMetadata
		if json.Unmarshal(data, &meta) == nil && meta.Version == historyMetadataVersion && meta.SizeBytes >= 0 && meta.Width > 0 && meta.Height > 0 && meta.FPS > 0 {
			return meta, nil
		}
	}

	zipPath := filepath.Join(ModDir, "history", id+".zip")
	meta, err := inspectBootAnimation(zipPath, id)
	if err != nil {
		return historyMetadata{}, err
	}
	if err := writeHistoryMetadata(id, meta); err != nil {
		writeLog("Warning: Could not cache history metadata for ID: " + id)
	}
	return meta, nil
}

func historyPreviewState(id string) (bool, string) {
	for _, candidate := range []struct {
		ext  string
		kind string
	}{
		{".webm", "webm"},
		{".gif", "gif"},
	} {
		path := filepath.Join(ModDir, "history", id+candidate.ext)
		if info, err := os.Stat(path); err == nil && !info.IsDir() && info.Size() > 0 {
			return true, candidate.kind
		}
	}
	return false, ""
}

func resizeNearest(source image.Image, maxWidth, maxHeight int) image.Image {
	bounds := source.Bounds()
	sourceWidth := bounds.Dx()
	sourceHeight := bounds.Dy()
	if sourceWidth <= 0 || sourceHeight <= 0 {
		return source
	}
	targetWidth := sourceWidth
	targetHeight := sourceHeight
	if targetWidth > maxWidth {
		targetHeight = targetHeight * maxWidth / targetWidth
		targetWidth = maxWidth
	}
	if targetHeight > maxHeight {
		targetWidth = targetWidth * maxHeight / targetHeight
		targetHeight = maxHeight
	}
	if targetWidth < 1 {
		targetWidth = 1
	}
	if targetHeight < 1 {
		targetHeight = 1
	}
	if targetWidth == sourceWidth && targetHeight == sourceHeight {
		return source
	}

	output := image.NewRGBA(image.Rect(0, 0, targetWidth, targetHeight))
	for y := 0; y < targetHeight; y++ {
		sy := bounds.Min.Y + y*sourceHeight/targetHeight
		for x := 0; x < targetWidth; x++ {
			sx := bounds.Min.X + x*sourceWidth/targetWidth
			output.Set(x, y, source.At(sx, sy))
		}
	}
	return output
}

func generateHistoryGIF(zipPath, outputPath string) error {
	archive, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer archive.Close()

	frames := make([]*zip.File, 0, 32)
	for _, entry := range archive.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		lower := strings.ToLower(entry.Name)
		if strings.Contains(entry.Name, "/") && (strings.HasSuffix(lower, ".png") || strings.HasSuffix(lower, ".jpg") || strings.HasSuffix(lower, ".jpeg")) {
			frames = append(frames, entry)
		}
	}
	if len(frames) == 0 {
		return errors.New("no previewable frames")
	}
	sort.Slice(frames, func(i, j int) bool { return frames[i].Name < frames[j].Name })

	const maxFrames = 8
	sampleCount := len(frames)
	if sampleCount > maxFrames {
		sampleCount = maxFrames
	}
	animation := &gif.GIF{LoopCount: 0}
	for i := 0; i < sampleCount; i++ {
		index := 0
		if sampleCount > 1 {
			index = i * (len(frames) - 1) / (sampleCount - 1)
		}
		entry := frames[index]

		configReader, err := entry.Open()
		if err != nil {
			continue
		}
		config, _, configErr := image.DecodeConfig(configReader)
		_ = configReader.Close()
		if configErr != nil || config.Width < 1 || config.Height < 1 || int64(config.Width)*int64(config.Height) > 24_000_000 {
			continue
		}

		reader, err := entry.Open()
		if err != nil {
			continue
		}
		decoded, _, decodeErr := image.Decode(reader)
		_ = reader.Close()
		if decodeErr != nil {
			continue
		}
		small := resizeNearest(decoded, 180, 240)
		paletted := image.NewPaletted(image.Rect(0, 0, small.Bounds().Dx(), small.Bounds().Dy()), palette.Plan9)
		draw.Draw(paletted, paletted.Rect, small, small.Bounds().Min, draw.Src)
		animation.Image = append(animation.Image, paletted)
		animation.Delay = append(animation.Delay, 8)
	}
	if len(animation.Image) == 0 {
		return errors.New("could not decode preview frames")
	}

	temp := outputPath + ".tmp"
	output, err := os.OpenFile(temp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	encodeErr := gif.EncodeAll(output, animation)
	closeErr := output.Close()
	if encodeErr != nil {
		_ = os.Remove(temp)
		return encodeErr
	}
	if closeErr != nil {
		_ = os.Remove(temp)
		return closeErr
	}
	return os.Rename(temp, outputPath)
}

func moveFileOrCopy(source, destination string, mode os.FileMode) error {
	if err := os.Rename(source, destination); err == nil {
		return os.Chmod(destination, mode)
	}
	if err := copyFile(source, destination, mode); err != nil {
		return err
	}
	return os.Remove(source)
}

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	multipartReader, err := r.MultipartReader()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Upload must be multipart/form-data"})
		return
	}

	tempFile, err := os.CreateTemp("/data/local/tmp", "boot_creator_upload_*.zip")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not create temporary file"})
		return
	}
	tempPath := tempFile.Name()
	_ = tempFile.Close()
	defer os.Remove(tempPath)

	previewTemp, err := os.CreateTemp("/data/local/tmp", "boot_creator_preview_*.webm")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not create temporary preview file"})
		return
	}
	previewTempPath := previewTemp.Name()
	_ = previewTemp.Close()
	_ = os.Remove(previewTempPath)
	defer os.Remove(previewTempPath)

	hasAnimation := false
	hasPreview := false
	for {
		part, nextErr := multipartReader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Could not read multipart upload"})
			return
		}

		switch part.FormName() {
		case "bootanimation":
			if hasAnimation {
				_ = part.Close()
				writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Only one bootanimation file is allowed"})
				return
			}
			if err := saveMultipartFile(part, tempPath, maxBootAnimationBytes); err != nil {
				_ = part.Close()
				writeJSON(w, http.StatusRequestEntityTooLarge, statusResponse{Status: "error", Message: "bootanimation.zip exceeds the transport safety limit"})
				writeLog("Error: bootanimation.zip exceeded the transport safety limit.")
				return
			}
			hasAnimation = true
		case "preview":
			if !hasPreview {
				if err := saveMultipartFile(part, previewTempPath, maxPreviewBytes); err == nil {
					hasPreview = true
				} else {
					_ = os.Remove(previewTempPath)
					writeLog("Warning: Preview file was rejected because it exceeded the allowed size.")
				}
			}
		default:
			_, _ = io.Copy(io.Discard, io.LimitReader(part, 1<<20))
		}
		_ = part.Close()
	}

	if !hasAnimation {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Failed to receive bootanimation.zip"})
		return
	}
	if err := validateBootAnimation(tempPath); err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: err.Error()})
		writeLog("Error: Invalid boot animation upload: " + err.Error())
		return
	}
	if hasPreview {
		if err := validatePreviewFile(previewTempPath); err != nil {
			_ = os.Remove(previewTempPath)
			hasPreview = false
			writeLog("Warning: Preview file was rejected because it was not a valid WebM file.")
		}
	}

	historyDir := ModDir + "/history"
	if err := os.MkdirAll(historyDir, 0755); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not prepare history"})
		return
	}
	id := newHistoryID(historyDir)
	historyZipPath := filepath.Join(historyDir, id+".zip")
	if err := moveFileOrCopy(tempPath, historyZipPath, 0644); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not save history item"})
		return
	}

	meta, err := inspectBootAnimation(historyZipPath, id)
	if err != nil {
		_ = os.Remove(historyZipPath)
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not inspect history item"})
		return
	}
	_ = writeHistoryMetadata(id, meta)

	previewPath := ""
	if hasPreview {
		previewPath = filepath.Join(historyDir, id+".webm")
		if err := moveFileOrCopy(previewTempPath, previewPath, 0644); err != nil {
			previewPath = ""
			writeLog("Warning: Could not persist the supplied history preview.")
		}
	}

	cmd := exec.Command("/system/bin/sh", ModDir+"/inject.sh", historyZipPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(historyZipPath)
		_ = os.Remove(historyMetadataPath(id))
		if previewPath != "" {
			_ = os.Remove(previewPath)
		}
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Failed to inject boot animation"})
		writeLog("Error: Failed to inject new boot animation via inject.sh: " + strings.TrimSpace(string(output)))
		return
	}

	cleanHistory(historyDir)
	warnings := []string{}
	if meta.SizeBytes > directUploadWarningBytes {
		warnings = append(warnings, "large_boot_animation")
	}
	writeJSON(w, http.StatusOK, uploadResponse{Status: "success", HistoryID: id, SizeBytes: meta.SizeBytes, Warnings: warnings})
	showToastEvent("toast_animation_applied", "🚀 Boot Creator: New animation injected and ready for the next boot!")
	writeLog("Success: New boot animation injected and saved to history (ID: " + id + ").")
}

func removeHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}

	cmd := exec.Command("/system/bin/sh", ModDir+"/clean.sh")
	if err := cmd.Run(); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Error running clean.sh"})
		writeLog("Error: Failed to run clean.sh during removal.")
		return
	}

	writeJSON(w, http.StatusOK, statusResponse{Status: "success", Message: "Animation successfully removed"})
	showToastEvent("toast_animation_removed", "🗑️ Boot Creator: Custom animation removed. Original boot restored!")
	writeLog("Success: Custom boot animation removed. Restored to stock.")
}

func pullHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}

	source := r.URL.Query().Get("source")
	if source != "module" && source != "system" {
		http.Error(w, "Invalid source", http.StatusBadRequest)
		return
	}

	paths, err := readSavedPaths()
	if err != nil {
		http.Error(w, "No valid paths found", http.StatusNotFound)
		writeLog("Error: Attempted to pull animation, but no valid saved paths were found.")
		return
	}

	targetPath := paths[0]
	fileToServe := targetPath

	if source == "module" {
		fileToServe = ModDir + targetPath
		showToastEvent("toast_pull_custom", "📥 Boot Creator: The website extracted your custom animation from the module!")
		writeLog("Animation pulled by website (Source: Module).")
	} else {
		backupPath := ModDir + "/backup" + targetPath
		if _, err := os.Stat(backupPath); err == nil {
			fileToServe = backupPath
		}
		showToastEvent("toast_pull_stock", "📥 Boot Creator: The website extracted your stock animation!")
		writeLog("Animation pulled by website (Source: Stock System).")
	}

	info, err := os.Stat(fileToServe)
	if err != nil || info.IsDir() {
		http.Error(w, "File not found", http.StatusNotFound)
		writeLog("Error: Pulled animation file not found at " + fileToServe)
		return
	}

	w.Header().Set("Content-Disposition", "attachment; filename=pulled_bootanimation.zip")
	w.Header().Set("Content-Type", "application/zip")
	http.ServeFile(w, r, fileToServe)
}

func performRescan(w http.ResponseWriter, r *http.Request, legacy bool) {
	paths, err := runPathRescan()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not rescan module paths"})
		writeLog("Error: Module path rescan failed: " + err.Error())
		return
	}

	message := fmt.Sprintf("Module paths rescanned successfully (%d found)", len(paths))
	writeJSON(w, http.StatusOK, statusResponse{Status: "success", Message: message, HasCustom: checkHasCustomAnim()})
	showToastEvent("toast_rescan", "🔎 Boot Creator: Boot animation paths rescanned successfully.")
	if legacy {
		writeLog("Success: Legacy /reset request performed a non-destructive path rescan.")
	} else {
		writeLog(fmt.Sprintf("Success: Module paths rescanned. %d valid paths cached.", len(paths)))
	}
}

func rescanHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}
	performRescan(w, r, false)
}

func resetHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}
	performRescan(w, r, true)
}

func factoryResetHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}
	if r.URL.Query().Get("confirm") != "erase_module_data" {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Factory reset confirmation required"})
		return
	}

	if paths, err := readSavedPaths(); err == nil {
		for _, targetPath := range paths {
			_ = os.Remove(ModDir + targetPath)
		}
	}

	files := []string{
		ModDir + "/saved_paths.txt",
		ModDir + "/.paths_schema",
		ModDir + "/system.prop",
		ModDir + "/boot_creator.log",
		ModDir + "/boot_creator.previous.log",
		ModDir + "/.preview_mounts",
	}
	dirs := []string{
		ModDir + "/system",
		ModDir + "/product",
		ModDir + "/oem",
		ModDir + "/vendor",
		ModDir + "/odm",
		ModDir + "/system_ext",
		ModDir + "/apex",
		ModDir + "/custom",
		ModDir + "/history",
		ModDir + "/playlist",
		ModDir + "/staging",
		ModDir + "/backup",
	}

	for _, filePath := range files {
		if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
			writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Error resetting the module"})
			writeLog("Error: Failed to reset module file: " + filePath)
			return
		}
	}

	for _, dirPath := range dirs {
		if err := os.RemoveAll(dirPath); err != nil {
			writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Error resetting the module"})
			writeLog("Error: Failed to reset module directory: " + dirPath)
			return
		}
	}

	writeJSON(w, http.StatusOK, statusResponse{Status: "success", Message: "Module data reset successfully"})
	showToastEvent("toast_reset", "⚠️ Boot Creator: Module data was reset for troubleshooting.")
	writeLog("Success: Destructive module factory reset executed.")
}

func validHistoryID(id string) bool {
	return historyIDPattern.MatchString(id)
}

func historyListHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}

	historyDir := ModDir + "/history"
	entries, err := os.ReadDir(historyDir)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, []string{})
			return
		}
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read history"})
		return
	}

	var ids []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".zip") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".zip")
		if validHistoryID(id) {
			ids = append(ids, id)
		}
	}

	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	writeJSON(w, http.StatusOK, ids)
}

func historyItemsHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}

	historyDir := filepath.Join(ModDir, "history")
	entries, err := os.ReadDir(historyDir)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, []historyItemResponse{})
			return
		}
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read history"})
		return
	}

	ids := make([]string, 0, historyLimit)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".zip") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".zip")
		if validHistoryID(id) {
			ids = append(ids, id)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))

	items := make([]historyItemResponse, 0, len(ids))
	for _, id := range ids {
		meta, err := ensureHistoryMetadata(id)
		if err != nil {
			writeLog("Warning: Could not inspect history item (ID: " + id + "): " + err.Error())
			meta = historyMetadata{Version: historyMetadataVersion, CreatedAt: historyCreatedAt(id)}
			if info, statErr := os.Stat(filepath.Join(historyDir, id+".zip")); statErr == nil && !info.IsDir() {
				meta.SizeBytes = info.Size()
				if meta.CreatedAt == 0 {
					meta.CreatedAt = info.ModTime().UnixMilli()
				}
			}
		}
		hasPreview, previewType := historyPreviewState(id)
		items = append(items, historyItemResponse{
			ID:          id,
			CreatedAt:   meta.CreatedAt,
			SizeBytes:   meta.SizeBytes,
			Width:       meta.Width,
			Height:      meta.Height,
			FPS:         meta.FPS,
			FrameCount:  meta.FrameCount,
			Parts:       meta.Parts,
			FrameFormat: meta.FrameFormat,
			HasAudio:    meta.HasAudio,
			HasPreview:  hasPreview,
			PreviewType: previewType,
		})
	}
	writeJSON(w, http.StatusOK, items)
}

func historyDownloadHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}
	id := r.URL.Query().Get("id")
	if !validHistoryID(id) {
		http.Error(w, "Invalid history ID", http.StatusBadRequest)
		return
	}
	path := filepath.Join(ModDir, "history", id+".zip")
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		http.Error(w, "History item not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=bootanimation-"+id+".zip")
	http.ServeFile(w, r, path)
}

func historyPreviewHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}

	id := r.URL.Query().Get("id")
	if !validHistoryID(id) {
		http.Error(w, "Invalid history ID", http.StatusBadRequest)
		return
	}

	previewPath := filepath.Join(ModDir, "history", id+".webm")
	contentType := "video/webm"
	if info, err := os.Stat(previewPath); err != nil || info.IsDir() || info.Size() == 0 {
		previewPath = filepath.Join(ModDir, "history", id+".gif")
		contentType = "image/gif"
		if info, err = os.Stat(previewPath); err != nil || info.IsDir() || info.Size() == 0 {
			historyPreviewMu.Lock()
			info, statErr := os.Stat(previewPath)
			if statErr != nil || info.IsDir() || info.Size() == 0 {
				zipPath := filepath.Join(ModDir, "history", id+".zip")
				if err := generateHistoryGIF(zipPath, previewPath); err != nil {
					historyPreviewMu.Unlock()
					writeLog("Warning: Could not generate history preview for ID: " + id + ": " + err.Error())
					http.Error(w, "Preview not available", http.StatusNotFound)
					return
				}
			}
			historyPreviewMu.Unlock()
		}
	}

	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("Content-Type", contentType)
	http.ServeFile(w, r, previewPath)
}

func historyApplyHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}

	id := r.URL.Query().Get("id")
	if !validHistoryID(id) {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid history ID"})
		return
	}
	historyPath := filepath.Join(ModDir, "history", id+".zip")
	if info, err := os.Stat(historyPath); err != nil || info.IsDir() {
		writeJSON(w, http.StatusNotFound, statusResponse{Status: "error", Message: "History item not found"})
		return
	}
	if err := validateBootAnimation(historyPath); err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "History item is invalid: " + err.Error()})
		writeLog("Error: Refused to restore invalid history item (ID: " + id + ").")
		return
	}

	cmd := exec.Command("/system/bin/sh", ModDir+"/inject.sh", historyPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Error injecting history"})
		writeLog("Error: Failed to restore animation from history (ID: " + id + "): " + strings.TrimSpace(string(output)))
		return
	}

	writeJSON(w, http.StatusOK, statusResponse{Status: "success", Message: "Animation restored from history"})
	showToastEvent("toast_history_restored", "⏳ Boot Creator: Past animation restored successfully!")
	writeLog("Success: Animation restored from history (ID: " + id + ").")
}

func historyDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}

	id := r.URL.Query().Get("id")
	if !validHistoryID(id) {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid history ID"})
		return
	}

	zipPath := filepath.Join(ModDir, "history", id+".zip")
	previewWebMPath := filepath.Join(ModDir, "history", id+".webm")
	previewLegacyPath := filepath.Join(ModDir, "history", id+".gif")
	metadataPath := historyMetadataPath(id)
	removed := false

	if err := os.Remove(zipPath); err == nil {
		removed = true
	} else if !os.IsNotExist(err) {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not delete history item"})
		return
	}

	for _, previewPath := range []string{previewWebMPath, previewLegacyPath, metadataPath} {
		if err := os.Remove(previewPath); err != nil && !os.IsNotExist(err) {
			writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not delete history preview"})
			return
		}
	}

	if !removed {
		writeJSON(w, http.StatusNotFound, statusResponse{Status: "error", Message: "History item not found"})
		return
	}

	writeJSON(w, http.StatusOK, statusResponse{Status: "success"})
	showToastEvent("toast_history_deleted", "🧹 Boot Creator: An old animation was deleted from history.")
	writeLog("History item deleted manually by user (ID: " + id + ").")
}

func playlistRootDir() string {
	return filepath.Join(ModDir, "playlist")
}

func playlistObjectsDir() string {
	return filepath.Join(playlistRootDir(), "objects")
}

func playlistPreviewsDir() string {
	return filepath.Join(playlistRootDir(), "previews")
}

func playlistStatePath() string {
	return filepath.Join(playlistRootDir(), "playlists.json")
}

func ensurePlaylistStorage() error {
	if err := os.MkdirAll(playlistObjectsDir(), 0755); err != nil {
		return err
	}
	return os.MkdirAll(playlistPreviewsDir(), 0755)
}

func playlistObjectZipPath(objectID string) string {
	return filepath.Join(playlistObjectsDir(), objectID+".zip")
}

func playlistObjectMetadataPath(objectID string) string {
	return filepath.Join(playlistObjectsDir(), objectID+".json")
}

func playlistObjectPreviewPath(objectID string) string {
	return filepath.Join(playlistPreviewsDir(), objectID+".gif")
}

func cleanPlaylistName(value string, fallback string) string {
	name := strings.TrimSpace(value)
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		name = fallback
	}
	if len([]rune(name)) > 80 {
		name = string([]rune(name)[:80])
	}
	return name
}

func newPlaylistIdentifier(prefix string) (string, error) {
	token, err := randomToken(9)
	if err != nil {
		return "", err
	}
	return prefix + token, nil
}

func loadPlaylistStateLocked() (playlistState, error) {
	if err := ensurePlaylistStorage(); err != nil {
		return playlistState{}, err
	}
	data, err := os.ReadFile(playlistStatePath())
	if os.IsNotExist(err) {
		return playlistState{Version: playlistStateVersion, Playlists: []playlistRecord{}}, nil
	}
	if err != nil {
		return playlistState{}, err
	}
	var state playlistState
	if err := json.Unmarshal(data, &state); err != nil || state.Version != playlistStateVersion {
		return playlistState{}, errors.New("invalid playlist state")
	}
	if state.Playlists == nil {
		state.Playlists = []playlistRecord{}
	}
	return state, nil
}

func savePlaylistStateLocked(state playlistState) error {
	if err := ensurePlaylistStorage(); err != nil {
		return err
	}
	state.Version = playlistStateVersion
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	temp := playlistStatePath() + ".tmp"
	if err := os.WriteFile(temp, append(data, '\n'), 0644); err != nil {
		return err
	}
	return os.Rename(temp, playlistStatePath())
}

func playlistObjectHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func writePlaylistObjectMetadata(objectID string, meta historyMetadata) error {
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	temp := playlistObjectMetadataPath(objectID) + ".tmp"
	if err := os.WriteFile(temp, append(data, '\n'), 0644); err != nil {
		return err
	}
	return os.Rename(temp, playlistObjectMetadataPath(objectID))
}

func ensurePlaylistObjectMetadata(objectID string) (historyMetadata, error) {
	if !playlistObjectPattern.MatchString(objectID) {
		return historyMetadata{}, errors.New("invalid playlist object")
	}
	if data, err := os.ReadFile(playlistObjectMetadataPath(objectID)); err == nil {
		var meta historyMetadata
		if json.Unmarshal(data, &meta) == nil && meta.SizeBytes >= 0 && meta.Width > 0 && meta.Height > 0 && meta.FPS > 0 {
			return meta, nil
		}
	}
	meta, err := inspectBootAnimation(playlistObjectZipPath(objectID), "")
	if err != nil {
		return historyMetadata{}, err
	}
	_ = writePlaylistObjectMetadata(objectID, meta)
	return meta, nil
}

func ensurePlaylistObject(sourcePath string) (string, historyMetadata, error) {
	if err := validateBootAnimation(sourcePath); err != nil {
		return "", historyMetadata{}, err
	}
	if err := ensurePlaylistStorage(); err != nil {
		return "", historyMetadata{}, err
	}
	objectID, err := playlistObjectHash(sourcePath)
	if err != nil {
		return "", historyMetadata{}, err
	}
	destination := playlistObjectZipPath(objectID)
	if info, statErr := os.Stat(destination); statErr != nil || info.IsDir() {
		if err := linkOrCopyFile(sourcePath, destination, 0644); err != nil {
			return "", historyMetadata{}, err
		}
	}
	meta, err := ensurePlaylistObjectMetadata(objectID)
	if err != nil {
		_ = os.Remove(destination)
		return "", historyMetadata{}, err
	}
	return objectID, meta, nil
}

func playlistObjectReferencedLocked(state playlistState, objectID string) bool {
	for _, playlist := range state.Playlists {
		for _, item := range playlist.Items {
			if item.ObjectID == objectID {
				return true
			}
		}
	}
	return false
}

func automationObjectReferencesFromDisk() map[string]struct{} {
	references := make(map[string]struct{})
	data, err := os.ReadFile(rotationStatePath())
	if err != nil {
		return references
	}
	var state rotationState
	if json.Unmarshal(data, &state) != nil {
		return references
	}
	for _, item := range state.Queue {
		if playlistObjectPattern.MatchString(item.ObjectID) {
			references[item.ObjectID] = struct{}{}
		}
	}
	if state.Override != nil && playlistObjectPattern.MatchString(state.Override.ObjectID) {
		references[state.Override.ObjectID] = struct{}{}
	}
	if playlistObjectPattern.MatchString(state.NextObjectID) && (state.NextSource == "queue" || state.NextSource == "override") {
		references[state.NextObjectID] = struct{}{}
	}
	return references
}

func garbageCollectPlaylistObjectsLocked(state playlistState) {
	entries, err := os.ReadDir(playlistObjectsDir())
	if err != nil {
		return
	}
	automationRefs := automationObjectReferencesFromDisk()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".zip") {
			continue
		}
		objectID := strings.TrimSuffix(entry.Name(), ".zip")
		if !playlistObjectPattern.MatchString(objectID) || playlistObjectReferencedLocked(state, objectID) {
			continue
		}
		if _, usedByAutomation := automationRefs[objectID]; usedByAutomation {
			continue
		}
		_ = os.Remove(playlistObjectZipPath(objectID))
		_ = os.Remove(playlistObjectMetadataPath(objectID))
		_ = os.Remove(playlistObjectPreviewPath(objectID))
	}
}
func playlistResponseLocked(record playlistRecord) playlistResponse {
	response := playlistResponse{ID: record.ID, Name: record.Name, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt, Items: []playlistItemResponse{}}
	for _, ref := range record.Items {
		meta, err := ensurePlaylistObjectMetadata(ref.ObjectID)
		if err != nil {
			continue
		}
		preview := false
		if info, statErr := os.Stat(playlistObjectPreviewPath(ref.ObjectID)); statErr == nil && !info.IsDir() && info.Size() > 0 {
			preview = true
		}
		response.Items = append(response.Items, playlistItemResponse{ID: ref.ID, ObjectID: ref.ObjectID, Name: ref.Name, AddedAt: ref.AddedAt, SizeBytes: meta.SizeBytes, Width: meta.Width, Height: meta.Height, FPS: meta.FPS, FrameCount: meta.FrameCount, Parts: meta.Parts, FrameFormat: meta.FrameFormat, HasAudio: meta.HasAudio, HasPreview: preview})
	}
	return response
}

func findPlaylistLocked(state *playlistState, id string) (*playlistRecord, int) {
	for index := range state.Playlists {
		if state.Playlists[index].ID == id {
			return &state.Playlists[index], index
		}
	}
	return nil, -1
}

func addPlaylistItemLocked(state *playlistState, playlistID, objectID, name string) (playlistItemRef, error) {
	playlist, _ := findPlaylistLocked(state, playlistID)
	if playlist == nil {
		return playlistItemRef{}, errors.New("playlist not found")
	}
	entryID, err := newPlaylistIdentifier("it_")
	if err != nil {
		return playlistItemRef{}, err
	}
	now := time.Now().UnixMilli()
	displayName := strings.TrimSpace(name)
	if strings.EqualFold(filepath.Ext(displayName), ".zip") {
		displayName = strings.TrimSuffix(displayName, filepath.Ext(displayName))
	}
	item := playlistItemRef{ID: entryID, ObjectID: objectID, Name: cleanPlaylistName(displayName, "Boot animation"), AddedAt: now}
	playlist.Items = append(playlist.Items, item)
	playlist.UpdatedAt = now
	return item, nil
}

func decodePlaylistMutation(w http.ResponseWriter, r *http.Request) (playlistMutationRequest, bool) {
	var request playlistMutationRequest
	if err := decodeJSONRequest(r, 64<<10, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid request"})
		return playlistMutationRequest{}, false
	}
	return request, true
}

func playlistListHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) || !requireAuthorization(w, r) {
		return
	}
	playlistMu.Lock()
	defer playlistMu.Unlock()
	state, err := loadPlaylistStateLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read playlists"})
		return
	}
	result := playlistListResponse{Status: "success", Playlists: make([]playlistResponse, 0, len(state.Playlists))}
	for _, playlist := range state.Playlists {
		result.Playlists = append(result.Playlists, playlistResponseLocked(playlist))
	}
	writeJSON(w, http.StatusOK, result)
}

func playlistCreateHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	request, ok := decodePlaylistMutation(w, r)
	if !ok {
		return
	}
	playlistMu.Lock()
	defer playlistMu.Unlock()
	state, err := loadPlaylistStateLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read playlists"})
		return
	}
	id, err := newPlaylistIdentifier("pl_")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not create playlist"})
		return
	}
	now := time.Now().UnixMilli()
	record := playlistRecord{ID: id, Name: cleanPlaylistName(request.Name, "New playlist"), CreatedAt: now, UpdatedAt: now, Items: []playlistItemRef{}}
	state.Playlists = append(state.Playlists, record)
	if err := savePlaylistStateLocked(state); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not save playlist"})
		return
	}
	writeJSON(w, http.StatusOK, playlistResponseLocked(record))
	showToastEvent("toast_playlist_created", "📚 Boot Creator: Playlist created.")
	writeLog("Playlist created: " + record.Name)
}

func playlistRenameHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	request, ok := decodePlaylistMutation(w, r)
	if !ok || !playlistIDPattern.MatchString(request.ID) {
		if ok {
			writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid playlist"})
		}
		return
	}
	playlistMu.Lock()
	defer playlistMu.Unlock()
	state, err := loadPlaylistStateLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read playlists"})
		return
	}
	playlist, _ := findPlaylistLocked(&state, request.ID)
	if playlist == nil {
		writeJSON(w, http.StatusNotFound, statusResponse{Status: "error", Message: "Playlist not found"})
		return
	}
	playlist.Name = cleanPlaylistName(request.Name, playlist.Name)
	playlist.UpdatedAt = time.Now().UnixMilli()
	if err := savePlaylistStateLocked(state); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not save playlist"})
		return
	}
	writeJSON(w, http.StatusOK, statusResponse{Status: "success"})
	showToastEvent("toast_playlist_renamed", "📚 Boot Creator: Playlist renamed.")
}

func playlistDuplicateHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	request, ok := decodePlaylistMutation(w, r)
	if !ok || !playlistIDPattern.MatchString(request.ID) {
		if ok {
			writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid playlist"})
		}
		return
	}
	playlistMu.Lock()
	defer playlistMu.Unlock()
	state, err := loadPlaylistStateLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read playlists"})
		return
	}
	source, _ := findPlaylistLocked(&state, request.ID)
	if source == nil {
		writeJSON(w, http.StatusNotFound, statusResponse{Status: "error", Message: "Playlist not found"})
		return
	}
	newID, err := newPlaylistIdentifier("pl_")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not duplicate playlist"})
		return
	}
	now := time.Now().UnixMilli()
	copyRecord := playlistRecord{ID: newID, Name: cleanPlaylistName(request.Name, source.Name+" copy"), CreatedAt: now, UpdatedAt: now, Items: make([]playlistItemRef, 0, len(source.Items))}
	for _, sourceItem := range source.Items {
		entryID, idErr := newPlaylistIdentifier("it_")
		if idErr != nil {
			writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not duplicate playlist"})
			return
		}
		copyRecord.Items = append(copyRecord.Items, playlistItemRef{ID: entryID, ObjectID: sourceItem.ObjectID, Name: sourceItem.Name, AddedAt: now})
	}
	state.Playlists = append(state.Playlists, copyRecord)
	if err := savePlaylistStateLocked(state); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not save playlist"})
		return
	}
	writeJSON(w, http.StatusOK, playlistResponseLocked(copyRecord))
	showToastEvent("toast_playlist_duplicated", "📚 Boot Creator: Playlist duplicated.")
}

func playlistDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	request, ok := decodePlaylistMutation(w, r)
	if !ok || !playlistIDPattern.MatchString(request.ID) {
		if ok {
			writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid playlist"})
		}
		return
	}
	playlistMu.Lock()
	state, err := loadPlaylistStateLocked()
	if err != nil {
		playlistMu.Unlock()
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read playlists"})
		return
	}
	_, index := findPlaylistLocked(&state, request.ID)
	if index < 0 {
		playlistMu.Unlock()
		writeJSON(w, http.StatusNotFound, statusResponse{Status: "error", Message: "Playlist not found"})
		return
	}
	state.Playlists = append(state.Playlists[:index], state.Playlists[index+1:]...)
	if err := savePlaylistStateLocked(state); err != nil {
		playlistMu.Unlock()
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not save playlist"})
		return
	}
	garbageCollectPlaylistObjectsLocked(state)
	playlistMu.Unlock()
	disableRotationForDeletedPlaylist(request.ID)
	writeJSON(w, http.StatusOK, statusResponse{Status: "success"})
	showToastEvent("toast_playlist_deleted", "📚 Boot Creator: Playlist deleted.")
}

func playlistReorderHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	request, ok := decodePlaylistMutation(w, r)
	if !ok {
		return
	}
	playlistMu.Lock()
	defer playlistMu.Unlock()
	state, err := loadPlaylistStateLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read playlists"})
		return
	}
	if len(request.IDs) != len(state.Playlists) {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid playlist order"})
		return
	}
	lookup := make(map[string]playlistRecord, len(state.Playlists))
	for _, playlist := range state.Playlists {
		lookup[playlist.ID] = playlist
	}
	reordered := make([]playlistRecord, 0, len(state.Playlists))
	seen := make(map[string]struct{}, len(request.IDs))
	for _, id := range request.IDs {
		playlist, exists := lookup[id]
		if !exists {
			writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid playlist order"})
			return
		}
		if _, duplicate := seen[id]; duplicate {
			writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid playlist order"})
			return
		}
		seen[id] = struct{}{}
		reordered = append(reordered, playlist)
	}
	state.Playlists = reordered
	if err := savePlaylistStateLocked(state); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not save playlist order"})
		return
	}
	writeJSON(w, http.StatusOK, statusResponse{Status: "success"})
}

func playlistAddSourceLocked(state *playlistState, playlistID, sourcePath, name string) (playlistItemRef, error) {
	if playlist, _ := findPlaylistLocked(state, playlistID); playlist == nil {
		return playlistItemRef{}, errors.New("playlist not found")
	}
	objectID, _, err := ensurePlaylistObject(sourcePath)
	if err != nil {
		return playlistItemRef{}, err
	}
	item, err := addPlaylistItemLocked(state, playlistID, objectID, name)
	if err != nil {
		return playlistItemRef{}, err
	}
	if err := savePlaylistStateLocked(*state); err != nil {
		if diskState, loadErr := loadPlaylistStateLocked(); loadErr == nil {
			garbageCollectPlaylistObjectsLocked(diskState)
		}
		return playlistItemRef{}, err
	}
	return item, nil
}

func playlistUploadHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBootAnimationBytes+(8<<20))
	reader, err := r.MultipartReader()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid multipart upload"})
		return
	}
	temp, err := os.CreateTemp("/data/local/tmp", "boot_creator_playlist_*.zip")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not prepare upload"})
		return
	}
	tempPath := temp.Name()
	_ = temp.Close()
	defer os.Remove(tempPath)
	playlistID := ""
	name := ""
	hasAnimation := false
	for {
		part, nextErr := reader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Could not read multipart upload"})
			return
		}
		switch part.FormName() {
		case "playlist_id":
			data, _ := io.ReadAll(io.LimitReader(part, 256))
			playlistID = strings.TrimSpace(string(data))
		case "name":
			data, _ := io.ReadAll(io.LimitReader(part, 512))
			name = strings.TrimSpace(string(data))
		case "bootanimation":
			if !hasAnimation {
				if err := saveMultipartFile(part, tempPath, maxBootAnimationBytes); err != nil {
					_ = part.Close()
					writeJSON(w, http.StatusRequestEntityTooLarge, statusResponse{Status: "error", Message: "bootanimation.zip exceeds the transport safety limit"})
					return
				}
				hasAnimation = true
			}
		}
		_ = part.Close()
	}
	if !playlistIDPattern.MatchString(playlistID) || !hasAnimation {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Missing playlist or animation"})
		return
	}
	playlistMu.Lock()
	defer playlistMu.Unlock()
	state, err := loadPlaylistStateLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read playlists"})
		return
	}
	item, err := playlistAddSourceLocked(&state, playlistID, tempPath, name)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, item)
	showToastEvent("toast_playlist_item_added", "📚 Boot Creator: Animation added to playlist.")
	writeLog("Animation added to playlist from upload.")
}

func playlistAddHistoryHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	request, ok := decodePlaylistMutation(w, r)
	if !ok {
		return
	}
	historyID := strings.TrimSpace(request.EntryID)
	if !playlistIDPattern.MatchString(request.PlaylistID) || !historyIDPattern.MatchString(historyID) {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid playlist or history item"})
		return
	}
	source := filepath.Join(ModDir, "history", historyID+".zip")
	if info, err := os.Stat(source); err != nil || info.IsDir() {
		writeJSON(w, http.StatusNotFound, statusResponse{Status: "error", Message: "History item not found"})
		return
	}
	name := request.Name
	if name == "" {
		name = "History " + historyID
	}
	playlistMu.Lock()
	defer playlistMu.Unlock()
	state, err := loadPlaylistStateLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read playlists"})
		return
	}
	item, err := playlistAddSourceLocked(&state, request.PlaylistID, source, name)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, item)
	showToastEvent("toast_playlist_item_added", "📚 Boot Creator: Animation added to playlist.")
}

func playlistAddStagedHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	request, ok := decodePlaylistMutation(w, r)
	if !ok {
		return
	}
	if !playlistIDPattern.MatchString(request.PlaylistID) {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid playlist"})
		return
	}
	source := stagedAnimationPath()
	if err := validateBootAnimation(source); err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "No valid staged animation"})
		return
	}
	playlistMu.Lock()
	defer playlistMu.Unlock()
	state, err := loadPlaylistStateLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read playlists"})
		return
	}
	item, err := playlistAddSourceLocked(&state, request.PlaylistID, source, request.Name)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, item)
	showToastEvent("toast_playlist_item_added", "📚 Boot Creator: Animation added to playlist.")
}

func playlistItemRemoveHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	request, ok := decodePlaylistMutation(w, r)
	if !ok {
		return
	}
	if !playlistIDPattern.MatchString(request.PlaylistID) || !playlistIDPattern.MatchString(request.EntryID) {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid playlist item"})
		return
	}
	playlistMu.Lock()
	defer playlistMu.Unlock()
	state, err := loadPlaylistStateLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read playlists"})
		return
	}
	playlist, _ := findPlaylistLocked(&state, request.PlaylistID)
	if playlist == nil {
		writeJSON(w, http.StatusNotFound, statusResponse{Status: "error", Message: "Playlist not found"})
		return
	}
	index := -1
	for i, item := range playlist.Items {
		if item.ID == request.EntryID {
			index = i
			break
		}
	}
	if index < 0 {
		writeJSON(w, http.StatusNotFound, statusResponse{Status: "error", Message: "Playlist item not found"})
		return
	}
	playlist.Items = append(playlist.Items[:index], playlist.Items[index+1:]...)
	playlist.UpdatedAt = time.Now().UnixMilli()
	if err := savePlaylistStateLocked(state); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not save playlist"})
		return
	}
	garbageCollectPlaylistObjectsLocked(state)
	writeJSON(w, http.StatusOK, statusResponse{Status: "success"})
	showToastEvent("toast_playlist_item_removed", "📚 Boot Creator: Animation removed from playlist.")
}

func playlistItemReorderHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	request, ok := decodePlaylistMutation(w, r)
	if !ok {
		return
	}
	if !playlistIDPattern.MatchString(request.PlaylistID) {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid playlist"})
		return
	}
	playlistMu.Lock()
	defer playlistMu.Unlock()
	state, err := loadPlaylistStateLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read playlists"})
		return
	}
	playlist, _ := findPlaylistLocked(&state, request.PlaylistID)
	if playlist == nil {
		writeJSON(w, http.StatusNotFound, statusResponse{Status: "error", Message: "Playlist not found"})
		return
	}
	if len(request.IDs) != len(playlist.Items) {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid item order"})
		return
	}
	lookup := make(map[string]playlistItemRef, len(playlist.Items))
	for _, item := range playlist.Items {
		lookup[item.ID] = item
	}
	reordered := make([]playlistItemRef, 0, len(playlist.Items))
	seen := make(map[string]struct{}, len(request.IDs))
	for _, id := range request.IDs {
		item, exists := lookup[id]
		if !exists {
			writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid item order"})
			return
		}
		if _, duplicate := seen[id]; duplicate {
			writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid item order"})
			return
		}
		seen[id] = struct{}{}
		reordered = append(reordered, item)
	}
	playlist.Items = reordered
	playlist.UpdatedAt = time.Now().UnixMilli()
	if err := savePlaylistStateLocked(state); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not save item order"})
		return
	}
	writeJSON(w, http.StatusOK, statusResponse{Status: "success"})
}

func playlistPreviewHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) || !requireAuthorization(w, r) {
		return
	}
	objectID := strings.TrimSpace(r.URL.Query().Get("object"))
	if !playlistObjectPattern.MatchString(objectID) {
		http.Error(w, "Invalid playlist object", http.StatusBadRequest)
		return
	}
	zipPath := playlistObjectZipPath(objectID)
	if info, err := os.Stat(zipPath); err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	previewPath := playlistObjectPreviewPath(objectID)
	if info, err := os.Stat(previewPath); err != nil || info.IsDir() || info.Size() == 0 {
		playlistPreviewMu.Lock()
		info, statErr := os.Stat(previewPath)
		if statErr != nil || info.IsDir() || info.Size() == 0 {
			if err := generateHistoryGIF(zipPath, previewPath); err != nil {
				playlistPreviewMu.Unlock()
				http.Error(w, "Preview unavailable", http.StatusUnprocessableEntity)
				return
			}
		}
		playlistPreviewMu.Unlock()
	}
	w.Header().Set("Content-Type", "image/gif")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, previewPath)
}

func playlistDownloadHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) || !requireAuthorization(w, r) {
		return
	}
	objectID := strings.TrimSpace(r.URL.Query().Get("object"))
	if !playlistObjectPattern.MatchString(objectID) {
		http.Error(w, "Invalid playlist object", http.StatusBadRequest)
		return
	}
	path := playlistObjectZipPath(objectID)
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="bootanimation.zip"`)
	http.ServeFile(w, r, path)
}

func applyPlaylistObject(objectID string) (uploadResponse, error) {
	path := playlistObjectZipPath(objectID)
	if !playlistObjectPattern.MatchString(objectID) {
		return uploadResponse{}, errors.New("invalid playlist object")
	}
	if err := validateBootAnimation(path); err != nil {
		return uploadResponse{}, errors.New("playlist animation is invalid")
	}
	historyDir := filepath.Join(ModDir, "history")
	if err := os.MkdirAll(historyDir, 0755); err != nil {
		return uploadResponse{}, err
	}
	id := newHistoryID(historyDir)
	historyPath := filepath.Join(historyDir, id+".zip")
	if err := linkOrCopyFile(path, historyPath, 0644); err != nil {
		return uploadResponse{}, err
	}
	meta, err := inspectBootAnimation(historyPath, id)
	if err != nil {
		_ = os.Remove(historyPath)
		return uploadResponse{}, err
	}
	_ = writeHistoryMetadata(id, meta)
	cmd := exec.Command("/system/bin/sh", ModDir+"/inject.sh", historyPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(historyPath)
		_ = os.Remove(historyMetadataPath(id))
		return uploadResponse{}, errors.New(strings.TrimSpace(string(output)))
	}
	cleanHistory(historyDir)
	warnings := []string{}
	if meta.SizeBytes > directUploadWarningBytes {
		warnings = append(warnings, "large_boot_animation")
	}
	return uploadResponse{Status: "success", HistoryID: id, SizeBytes: meta.SizeBytes, Warnings: warnings}, nil
}

func playlistApplyHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	objectID := strings.TrimSpace(r.URL.Query().Get("object"))
	result, err := applyPlaylistObject(objectID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
	showToastEvent("toast_playlist_applied", "Boot Animation Studio: Playlist animation applied.")
	writeLog("Playlist animation applied and saved to history.")
}

func stagePlaylistObject(objectID string) (historyMetadata, error) {
	if !playlistObjectPattern.MatchString(objectID) {
		return historyMetadata{}, errors.New("invalid playlist object")
	}
	source := playlistObjectZipPath(objectID)
	meta, err := inspectBootAnimation(source, "")
	if err != nil {
		return historyMetadata{}, err
	}
	if err := os.MkdirAll(filepath.Dir(stagedAnimationPath()), 0755); err != nil {
		return historyMetadata{}, err
	}
	temp := stagedAnimationPath() + ".tmp"
	_ = os.Remove(temp)
	if err := linkOrCopyFile(source, temp, 0644); err != nil {
		return historyMetadata{}, err
	}
	if err := setSystemFileContext(temp); err != nil {
		_ = os.Remove(temp)
		return historyMetadata{}, err
	}
	_ = os.Remove(stagedAnimationPath())
	if err := os.Rename(temp, stagedAnimationPath()); err != nil {
		_ = os.Remove(temp)
		return historyMetadata{}, err
	}
	return meta, nil
}

func playlistStageHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	objectID := strings.TrimSpace(r.URL.Query().Get("object"))
	meta, err := stagePlaylistObject(objectID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: err.Error()})
		return
	}
	warnings := []string{}
	if meta.SizeBytes > directUploadWarningBytes {
		warnings = append(warnings, "large_boot_animation")
	}
	writeJSON(w, http.StatusOK, testStageResponse{Status: "success", HasStaged: true, PreviewActive: false, SizeBytes: meta.SizeBytes, Width: meta.Width, Height: meta.Height, FPS: meta.FPS, FrameCount: meta.FrameCount, Parts: meta.Parts, FrameFormat: meta.FrameFormat, HasAudio: meta.HasAudio, Warnings: warnings})
}

func rotationStatePath() string {
	return filepath.Join(playlistRootDir(), "rotation.json")
}

func bootActivityStatePath() string {
	return filepath.Join(playlistRootDir(), "boot_activity.json")
}

func loadBootActivityStateLocked() (bootActivityState, error) {
	if err := os.MkdirAll(playlistRootDir(), 0755); err != nil {
		return bootActivityState{}, err
	}
	data, err := os.ReadFile(bootActivityStatePath())
	if os.IsNotExist(err) {
		return bootActivityState{Version: bootActivityStateVersion, Items: []bootActivityEntry{}}, nil
	}
	if err != nil {
		return bootActivityState{}, err
	}
	var state bootActivityState
	if json.Unmarshal(data, &state) != nil || state.Version != bootActivityStateVersion {
		return bootActivityState{}, errors.New("invalid boot activity state")
	}
	if state.Items == nil {
		state.Items = []bootActivityEntry{}
	}
	return state, nil
}

func saveBootActivityStateLocked(state bootActivityState) error {
	if err := os.MkdirAll(playlistRootDir(), 0755); err != nil {
		return err
	}
	state.Version = bootActivityStateVersion
	if len(state.Items) > bootActivityLimit {
		state.Items = append([]bootActivityEntry(nil), state.Items[len(state.Items)-bootActivityLimit:]...)
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	temp := bootActivityStatePath() + ".tmp"
	if err := os.WriteFile(temp, append(data, '\n'), 0644); err != nil {
		return err
	}
	return os.Rename(temp, bootActivityStatePath())
}

func appendBootActivity(entry bootActivityEntry) {
	bootActivityMu.Lock()
	defer bootActivityMu.Unlock()
	state, err := loadBootActivityStateLocked()
	if err != nil {
		writeLog("Boot Activity could not read its state: " + err.Error())
		return
	}
	now := time.Now().UnixMilli()
	if entry.CreatedAt <= 0 {
		entry.CreatedAt = now
	}
	if entry.ID == "" {
		entry.ID = fmt.Sprintf("%d-%06d", now, rotationRandomIndex(1000000))
	}
	state.Items = append(state.Items, entry)
	if err := saveBootActivityStateLocked(state); err != nil {
		writeLog("Boot Activity could not save its state: " + err.Error())
	}
}

func recordBootActivityError(name, source, reason string, err error) {
	if err == nil {
		return
	}
	appendBootActivity(bootActivityEntry{
		Kind:      "diagnostic",
		Status:    "error",
		Name:      cleanPlaylistName(name, "Boot animation"),
		Source:    normalizeNextSource(source),
		Reason:    strings.TrimSpace(reason),
		CreatedAt: time.Now().UnixMilli(),
		Message:   err.Error(),
	})
}

func recordBootActivityPrepared(entryID, objectID, name, source, reason string, preparedAt int64) {
	appendBootActivity(bootActivityEntry{
		Kind:       "prepared",
		Status:     "prepared",
		EntryID:    entryID,
		ObjectID:   objectID,
		Name:       cleanPlaylistName(name, "Boot animation"),
		Source:     normalizeNextSource(source),
		Reason:     strings.TrimSpace(reason),
		PreparedAt: preparedAt,
		CreatedAt:  preparedAt,
	})
}

func bootActivityListHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) || !requireAuthorization(w, r) {
		return
	}
	bootActivityMu.Lock()
	defer bootActivityMu.Unlock()
	state, err := loadBootActivityStateLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read Boot Activity"})
		return
	}
	items := append([]bootActivityEntry(nil), state.Items...)
	for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
		items[left], items[right] = items[right], items[left]
	}
	writeJSON(w, http.StatusOK, bootActivityResponse{Status: "success", Limit: bootActivityLimit, Items: items})
}

func bootActivityClearHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	bootActivityMu.Lock()
	defer bootActivityMu.Unlock()
	if err := saveBootActivityStateLocked(bootActivityState{Version: bootActivityStateVersion, Items: []bootActivityEntry{}}); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not clear Boot Activity"})
		return
	}
	writeJSON(w, http.StatusOK, statusResponse{Status: "success"})
	showToastEvent("toast_boot_activity_cleared", "🧹 Boot Creator: Boot Activity was cleared.")
	writeLog("Boot Activity cleared by user.")
}

func normalizeRotationMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "sequential", "random", "shuffle":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "sequential"
	}
}

func normalizeNextSource(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "rotation", "queue", "override":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func loadRotationStateLocked() (rotationState, error) {
	if err := ensurePlaylistStorage(); err != nil {
		return rotationState{}, err
	}
	data, err := os.ReadFile(rotationStatePath())
	if os.IsNotExist(err) {
		return rotationState{Version: rotationStateVersion, Mode: "sequential", Queue: []bootQueueItem{}, ShuffleRemaining: []string{}}, nil
	}
	if err != nil {
		return rotationState{}, err
	}
	var state rotationState
	if err := json.Unmarshal(data, &state); err != nil || state.Version < 1 || state.Version > rotationStateVersion {
		return rotationState{}, errors.New("invalid rotation state")
	}
	if state.Version == 1 && state.LastRotationEntryID == "" {
		state.LastRotationEntryID = state.LastBootEntryID
	}
	state.Version = rotationStateVersion
	state.Mode = normalizeRotationMode(state.Mode)
	state.NextSource = normalizeNextSource(state.NextSource)
	if state.NextSource == "" && state.NextEntryID != "" {
		state.NextSource = "rotation"
	}
	if state.Queue == nil {
		state.Queue = []bootQueueItem{}
	}
	if state.ShuffleRemaining == nil {
		state.ShuffleRemaining = []string{}
	}
	return state, nil
}

func saveRotationStateLocked(state rotationState) error {
	if err := ensurePlaylistStorage(); err != nil {
		return err
	}
	state.Version = rotationStateVersion
	state.Mode = normalizeRotationMode(state.Mode)
	state.NextSource = normalizeNextSource(state.NextSource)
	state.UpdatedAt = time.Now().UnixMilli()
	if state.Queue == nil {
		state.Queue = []bootQueueItem{}
	}
	if state.ShuffleRemaining == nil {
		state.ShuffleRemaining = []string{}
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	temp := rotationStatePath() + ".tmp"
	if err := os.WriteFile(temp, append(data, '\n'), 0644); err != nil {
		return err
	}
	return os.Rename(temp, rotationStatePath())
}

func rotationPlaylistSnapshot(playlistID string) (playlistRecord, bool, error) {
	playlistMu.Lock()
	defer playlistMu.Unlock()
	state, err := loadPlaylistStateLocked()
	if err != nil {
		return playlistRecord{}, false, err
	}
	playlist, _ := findPlaylistLocked(&state, playlistID)
	if playlist == nil {
		return playlistRecord{}, false, nil
	}
	copyRecord := *playlist
	copyRecord.Items = append([]playlistItemRef(nil), playlist.Items...)
	return copyRecord, true, nil
}

func rotationUsableItems(playlist playlistRecord) []playlistItemRef {
	items := make([]playlistItemRef, 0, len(playlist.Items))
	for _, item := range playlist.Items {
		if !playlistIDPattern.MatchString(item.ID) || !playlistObjectPattern.MatchString(item.ObjectID) {
			continue
		}
		path := playlistObjectZipPath(item.ObjectID)
		info, err := os.Stat(path)
		if err != nil || info.IsDir() || info.Size() <= 0 {
			continue
		}
		items = append(items, item)
	}
	return items
}

func rotationRandomIndex(max int) int {
	if max <= 1 {
		return 0
	}
	value, err := rand.Int(rand.Reader, big.NewInt(int64(max)))
	if err != nil {
		return int(time.Now().UnixNano() % int64(max))
	}
	return int(value.Int64())
}

func rotationShuffleIDs(ids []string) []string {
	result := append([]string(nil), ids...)
	for i := len(result) - 1; i > 0; i-- {
		j := rotationRandomIndex(i + 1)
		result[i], result[j] = result[j], result[i]
	}
	return result
}

func rotationFindEntry(items []playlistItemRef, entryID string) int {
	for index, item := range items {
		if item.ID == entryID {
			return index
		}
	}
	return -1
}

func chooseRotationItem(state *rotationState, items []playlistItemRef, referenceEntryID string) (playlistItemRef, error) {
	if len(items) == 0 {
		return playlistItemRef{}, errors.New("rotation playlist has no available animations")
	}
	switch normalizeRotationMode(state.Mode) {
	case "random":
		candidates := items
		if len(items) > 1 && referenceEntryID != "" {
			filtered := make([]playlistItemRef, 0, len(items)-1)
			for _, item := range items {
				if item.ID != referenceEntryID {
					filtered = append(filtered, item)
				}
			}
			if len(filtered) > 0 {
				candidates = filtered
			}
		}
		return candidates[rotationRandomIndex(len(candidates))], nil
	case "shuffle":
		lookup := make(map[string]playlistItemRef, len(items))
		validIDs := make([]string, 0, len(items))
		for _, item := range items {
			lookup[item.ID] = item
			validIDs = append(validIDs, item.ID)
		}
		remaining := make([]string, 0, len(state.ShuffleRemaining))
		seen := make(map[string]struct{})
		for _, id := range state.ShuffleRemaining {
			if _, ok := lookup[id]; !ok {
				continue
			}
			if _, duplicate := seen[id]; duplicate {
				continue
			}
			seen[id] = struct{}{}
			remaining = append(remaining, id)
		}
		if len(remaining) == 0 {
			remaining = rotationShuffleIDs(validIDs)
			if len(remaining) > 1 && referenceEntryID != "" && remaining[0] == referenceEntryID {
				remaining[0], remaining[1] = remaining[1], remaining[0]
			}
		}
		selectedID := remaining[0]
		state.ShuffleRemaining = append([]string(nil), remaining[1:]...)
		return lookup[selectedID], nil
	default:
		index := rotationFindEntry(items, referenceEntryID)
		if index < 0 {
			return items[0], nil
		}
		return items[(index+1)%len(items)], nil
	}
}

func clearPreparedNext(state *rotationState) {
	state.NextEntryID = ""
	state.NextObjectID = ""
	state.NextName = ""
	state.NextSource = ""
	state.NextQueueID = ""
	state.PreparedAt = 0
	state.NextReason = ""
}

func injectAutomationObject(objectID string) error {
	if !playlistObjectPattern.MatchString(objectID) {
		return errors.New("invalid automation object")
	}
	objectPath := playlistObjectZipPath(objectID)
	if err := validateBootAnimation(objectPath); err != nil {
		return errors.New("selected boot animation is invalid")
	}
	cmd := exec.Command("/system/bin/sh", filepath.Join(ModDir, "inject.sh"), objectPath)
	cmd.Env = append(os.Environ(), "BAS_SILENT_INJECT=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return errors.New(message)
	}
	return nil
}

func prepareQueueItemLocked(state *rotationState, item bootQueueItem, source, reason string) error {
	if !playlistIDPattern.MatchString(item.ID) || !playlistObjectPattern.MatchString(item.ObjectID) {
		return errors.New("queue item is invalid")
	}
	if err := injectAutomationObject(item.ObjectID); err != nil {
		recordBootActivityError(item.Name, source, reason, err)
		return err
	}
	state.NextEntryID = item.ID
	state.NextObjectID = item.ObjectID
	state.NextName = cleanPlaylistName(item.Name, "Boot animation")
	state.NextSource = source
	state.NextQueueID = item.ID
	state.PreparedAt = time.Now().UnixMilli()
	state.NextReason = reason
	state.LastError = ""
	if err := saveRotationStateLocked(*state); err != nil {
		recordBootActivityError(item.Name, source, reason, err)
		return err
	}
	recordBootActivityPrepared(state.NextEntryID, state.NextObjectID, state.NextName, state.NextSource, state.NextReason, state.PreparedAt)
	writeLog("Boot automation prepared next boot: " + state.NextName + " [" + source + ", " + reason + "].")
	return nil
}

func prepareRotationNextLocked(state *rotationState, referenceEntryID string, reason string) error {
	if !state.Enabled {
		return errors.New("rotation is disabled")
	}
	if state.Paused {
		return errors.New("rotation is paused")
	}
	if !playlistIDPattern.MatchString(state.PlaylistID) {
		return errors.New("rotation playlist is invalid")
	}
	playlist, found, err := rotationPlaylistSnapshot(state.PlaylistID)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("rotation playlist was not found")
	}
	items := rotationUsableItems(playlist)
	item, err := chooseRotationItem(state, items, referenceEntryID)
	if err != nil {
		return err
	}
	if err := injectAutomationObject(item.ObjectID); err != nil {
		recordBootActivityError(item.Name, "rotation", reason, err)
		return err
	}
	state.NextEntryID = item.ID
	state.NextObjectID = item.ObjectID
	state.NextName = cleanPlaylistName(item.Name, "Boot animation")
	state.NextSource = "rotation"
	state.NextQueueID = ""
	state.PreparedAt = time.Now().UnixMilli()
	state.NextReason = reason
	state.LastError = ""
	if err := saveRotationStateLocked(*state); err != nil {
		recordBootActivityError(item.Name, "rotation", reason, err)
		return err
	}
	recordBootActivityPrepared(state.NextEntryID, state.NextObjectID, state.NextName, state.NextSource, state.NextReason, state.PreparedAt)
	writeLog("Rotation prepared next boot: " + state.NextName + " [" + normalizeRotationMode(state.Mode) + ", " + reason + "].")
	return nil
}

func prepareAutomationNextLocked(state *rotationState, reason string) error {
	if state.Override != nil {
		return prepareQueueItemLocked(state, *state.Override, "override", reason)
	}
	if len(state.Queue) > 0 {
		return prepareQueueItemLocked(state, state.Queue[0], "queue", reason)
	}
	if state.Enabled && !state.Paused {
		return prepareRotationNextLocked(state, state.LastRotationEntryID, reason)
	}
	clearPreparedNext(state)
	state.LastError = ""
	return saveRotationStateLocked(*state)
}

func rotationResponseLocked(state rotationState) rotationResponse {
	response := rotationResponse{
		Status:           "success",
		Enabled:          state.Enabled,
		Paused:           state.Paused,
		PlaylistID:       state.PlaylistID,
		Mode:             normalizeRotationMode(state.Mode),
		LastBootEntryID:  state.LastBootEntryID,
		LastBootObjectID: state.LastBootObjectID,
		LastBootName:     state.LastBootName,
		LastBootSource:   state.LastBootSource,
		LastBootAt:       state.LastBootAt,
		NextEntryID:      state.NextEntryID,
		NextObjectID:     state.NextObjectID,
		NextName:         state.NextName,
		NextSource:       state.NextSource,
		PreparedAt:       state.PreparedAt,
		NextReason:       state.NextReason,
		Queue:            append([]bootQueueItem(nil), state.Queue...),
		UpdatedAt:        state.UpdatedAt,
		LastError:        state.LastError,
	}
	if state.Override != nil {
		copyItem := *state.Override
		response.Override = &copyItem
	}
	if playlistIDPattern.MatchString(state.PlaylistID) {
		if playlist, found, err := rotationPlaylistSnapshot(state.PlaylistID); err == nil && found {
			response.PlaylistName = playlist.Name
		}
	}
	return response
}

func rotationStatusHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) || !requireAuthorization(w, r) {
		return
	}
	rotationMu.Lock()
	defer rotationMu.Unlock()
	state, err := loadRotationStateLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read rotation state"})
		return
	}
	writeJSON(w, http.StatusOK, rotationResponseLocked(state))
}

func rotationConfigureHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	var request rotationConfigureRequest
	if err := decodeJSONRequest(r, 64<<10, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid rotation request"})
		return
	}
	rotationMu.Lock()
	defer rotationMu.Unlock()
	state, err := loadRotationStateLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read rotation state"})
		return
	}
	if !request.Enabled {
		state.Enabled = false
		state.Paused = false
		state.ShuffleRemaining = []string{}
		state.LastError = ""
		if state.NextSource == "rotation" {
			clearPreparedNext(&state)
		}
		if err := saveRotationStateLocked(state); err != nil {
			writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not save rotation state"})
			return
		}
		if state.Override != nil || len(state.Queue) > 0 {
			if err := prepareAutomationNextLocked(&state, "rotation-disabled"); err != nil {
				state.LastError = err.Error()
				_ = saveRotationStateLocked(state)
			}
		}
		writeLog("Boot rotation disabled. Queue and one-shot controls were preserved.")
		writeJSON(w, http.StatusOK, rotationResponseLocked(state))
		showToastEvent("toast_rotation_disabled", "🔄 Boot Creator: Boot Rotation disabled.")
		return
	}
	playlistID := strings.TrimSpace(request.PlaylistID)
	mode := normalizeRotationMode(request.Mode)
	playlist, found, err := rotationPlaylistSnapshot(playlistID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read playlists"})
		return
	}
	if !found || len(rotationUsableItems(playlist)) == 0 {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Choose a playlist that contains at least one animation"})
		return
	}
	changed := !state.Enabled || state.PlaylistID != playlistID || normalizeRotationMode(state.Mode) != mode
	state.Enabled = true
	state.PlaylistID = playlistID
	state.Mode = mode
	state.LastError = ""
	if changed {
		state.ShuffleRemaining = []string{}
		if state.NextSource == "rotation" {
			clearPreparedNext(&state)
		}
	}
	if state.Override != nil || len(state.Queue) > 0 {
		if err := saveRotationStateLocked(state); err != nil {
			writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not save rotation state"})
			return
		}
		writeJSON(w, http.StatusOK, rotationResponseLocked(state))
		showToastEvent("toast_rotation_configured", "🔄 Boot Creator: Boot Rotation settings updated.")
		return
	}
	if !changed && state.NextSource == "rotation" && state.NextEntryID != "" {
		if err := saveRotationStateLocked(state); err != nil {
			writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not save rotation state"})
			return
		}
		writeJSON(w, http.StatusOK, rotationResponseLocked(state))
		showToastEvent("toast_rotation_configured", "🔄 Boot Creator: Boot Rotation settings updated.")
		return
	}
	if state.Paused {
		if err := saveRotationStateLocked(state); err != nil {
			writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not save rotation state"})
			return
		}
		writeJSON(w, http.StatusOK, rotationResponseLocked(state))
		showToastEvent("toast_rotation_configured", "🔄 Boot Creator: Boot Rotation settings updated.")
		return
	}
	if err := prepareRotationNextLocked(&state, state.LastRotationEntryID, "configuration"); err != nil {
		state.LastError = err.Error()
		_ = saveRotationStateLocked(state)
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, rotationResponseLocked(state))
	showToastEvent("toast_rotation_configured", "🔄 Boot Creator: Boot Rotation settings updated.")
}

func rotationPrepareHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	rotationMu.Lock()
	defer rotationMu.Unlock()
	state, err := loadRotationStateLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read rotation state"})
		return
	}
	if !state.Enabled {
		writeJSON(w, http.StatusConflict, statusResponse{Status: "error", Message: "Boot rotation is disabled"})
		return
	}
	if state.Paused {
		writeJSON(w, http.StatusConflict, statusResponse{Status: "error", Message: "Boot rotation is paused"})
		return
	}
	if state.NextSource != "" && state.NextSource != "rotation" {
		writeJSON(w, http.StatusConflict, statusResponse{Status: "error", Message: "The next boot is controlled by Boot Queue"})
		return
	}
	reference := state.NextEntryID
	if reference == "" {
		reference = state.LastRotationEntryID
	}
	if err := prepareRotationNextLocked(&state, reference, "manual"); err != nil {
		state.LastError = err.Error()
		_ = saveRotationStateLocked(state)
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, rotationResponseLocked(state))
	showToastEvent("toast_rotation_prepared", "🔄 Boot Creator: Next Rotation animation prepared.")
}

func rotationPauseHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	var request rotationPauseRequest
	if err := decodeJSONRequest(r, 64<<10, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid pause request"})
		return
	}
	rotationMu.Lock()
	defer rotationMu.Unlock()
	state, err := loadRotationStateLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read rotation state"})
		return
	}
	if !state.Enabled {
		writeJSON(w, http.StatusConflict, statusResponse{Status: "error", Message: "Boot rotation is disabled"})
		return
	}
	state.Paused = request.Paused
	state.LastError = ""
	if !state.Paused && state.Override == nil && len(state.Queue) == 0 && state.NextSource == "" {
		if err := prepareRotationNextLocked(&state, state.LastRotationEntryID, "resume"); err != nil {
			state.LastError = err.Error()
			_ = saveRotationStateLocked(state)
			writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: err.Error()})
			return
		}
	} else if err := saveRotationStateLocked(state); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not save rotation state"})
		return
	}
	writeLog("Boot rotation pause state changed.")
	writeJSON(w, http.StatusOK, rotationResponseLocked(state))
	if state.Paused {
		showToastEvent("toast_rotation_paused", "⏸️ Boot Creator: Boot Rotation paused.")
	} else {
		showToastEvent("toast_rotation_resumed", "▶️ Boot Creator: Boot Rotation resumed.")
	}
}

func decodeBootQueueMutation(w http.ResponseWriter, r *http.Request) (bootQueueMutationRequest, bool) {
	var request bootQueueMutationRequest
	if err := decodeJSONRequest(r, 64<<10, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid Boot Queue request"})
		return bootQueueMutationRequest{}, false
	}
	return request, true
}

func resolveBootQueueSource(request bootQueueMutationRequest) (bootQueueItem, error) {
	source := strings.ToLower(strings.TrimSpace(request.Source))
	name := strings.TrimSpace(request.Name)
	objectID := ""
	switch source {
	case "playlist":
		objectID = strings.TrimSpace(request.ObjectID)
		if !playlistObjectPattern.MatchString(objectID) {
			return bootQueueItem{}, errors.New("invalid playlist animation")
		}
		if info, err := os.Stat(playlistObjectZipPath(objectID)); err != nil || info.IsDir() || info.Size() <= 0 {
			return bootQueueItem{}, errors.New("playlist animation not found")
		}
	case "history":
		historyID := strings.TrimSpace(request.HistoryID)
		if !historyIDPattern.MatchString(historyID) {
			return bootQueueItem{}, errors.New("invalid History animation")
		}
		sourcePath := filepath.Join(ModDir, "history", historyID+".zip")
		if info, err := os.Stat(sourcePath); err != nil || info.IsDir() {
			return bootQueueItem{}, errors.New("History animation not found")
		}
		playlistMu.Lock()
		var err error
		objectID, _, err = ensurePlaylistObject(sourcePath)
		playlistMu.Unlock()
		if err != nil {
			return bootQueueItem{}, err
		}
	case "staged":
		sourcePath := stagedAnimationPath()
		if err := validateBootAnimation(sourcePath); err != nil {
			return bootQueueItem{}, errors.New("no valid staged animation")
		}
		playlistMu.Lock()
		var err error
		objectID, _, err = ensurePlaylistObject(sourcePath)
		playlistMu.Unlock()
		if err != nil {
			return bootQueueItem{}, err
		}
	default:
		return bootQueueItem{}, errors.New("unsupported Boot Queue source")
	}
	id, err := newPlaylistIdentifier("q_")
	if err != nil {
		return bootQueueItem{}, err
	}
	return bootQueueItem{ID: id, ObjectID: objectID, Name: cleanPlaylistName(name, "Boot animation"), Origin: source, AddedAt: time.Now().UnixMilli()}, nil
}

func garbageCollectAutomationObjectsLocked() {
	playlistMu.Lock()
	defer playlistMu.Unlock()
	state, err := loadPlaylistStateLocked()
	if err == nil {
		garbageCollectPlaylistObjectsLocked(state)
	}
}

func bootQueueAddHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	request, ok := decodeBootQueueMutation(w, r)
	if !ok {
		return
	}
	rotationMu.Lock()
	defer rotationMu.Unlock()
	state, err := loadRotationStateLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read Boot Queue"})
		return
	}
	item, err := resolveBootQueueSource(request)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: err.Error()})
		return
	}
	state.Queue = append(state.Queue, item)
	state.LastError = ""
	if state.Override == nil && state.NextSource != "queue" {
		if err := prepareQueueItemLocked(&state, state.Queue[0], "queue", "queue-added"); err != nil {
			state.Queue = state.Queue[:len(state.Queue)-1]
			state.LastError = err.Error()
			_ = saveRotationStateLocked(state)
			garbageCollectAutomationObjectsLocked()
			writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: err.Error()})
			return
		}
	} else if err := saveRotationStateLocked(state); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not save Boot Queue"})
		return
	}
	writeLog("Animation added to Boot Queue: " + item.Name)
	writeJSON(w, http.StatusOK, rotationResponseLocked(state))
	showToastEvent("toast_queue_added", "📥 Boot Creator: Animation added to Boot Queue.")
}

func bootQueueUseNextHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	request, ok := decodeBootQueueMutation(w, r)
	if !ok {
		return
	}
	rotationMu.Lock()
	defer rotationMu.Unlock()
	state, err := loadRotationStateLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read Boot Queue"})
		return
	}
	item, err := resolveBootQueueSource(request)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: err.Error()})
		return
	}
	state.Override = &item
	if err := prepareQueueItemLocked(&state, item, "override", "use-next-boot"); err != nil {
		state.Override = nil
		state.LastError = err.Error()
		_ = saveRotationStateLocked(state)
		garbageCollectAutomationObjectsLocked()
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: err.Error()})
		return
	}
	garbageCollectAutomationObjectsLocked()
	writeLog("Next boot override prepared: " + item.Name)
	writeJSON(w, http.StatusOK, rotationResponseLocked(state))
	showToastEvent("toast_queue_next", "1️⃣ Boot Creator: One-time next boot prepared.")
}

func bootQueueReorderHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	request, ok := decodeBootQueueMutation(w, r)
	if !ok {
		return
	}
	rotationMu.Lock()
	defer rotationMu.Unlock()
	state, err := loadRotationStateLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read Boot Queue"})
		return
	}
	if len(request.IDs) != len(state.Queue) {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid Boot Queue order"})
		return
	}
	lookup := make(map[string]bootQueueItem, len(state.Queue))
	for _, item := range state.Queue {
		lookup[item.ID] = item
	}
	seen := make(map[string]struct{}, len(request.IDs))
	reordered := make([]bootQueueItem, 0, len(state.Queue))
	for _, id := range request.IDs {
		item, exists := lookup[id]
		if !exists {
			writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid Boot Queue order"})
			return
		}
		if _, duplicate := seen[id]; duplicate {
			writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid Boot Queue order"})
			return
		}
		seen[id] = struct{}{}
		reordered = append(reordered, item)
	}
	originalQueue := append([]bootQueueItem(nil), state.Queue...)
	state.Queue = reordered
	if state.Override == nil && len(state.Queue) > 0 && (state.NextSource != "queue" || state.NextQueueID != state.Queue[0].ID) {
		if err := prepareQueueItemLocked(&state, state.Queue[0], "queue", "queue-reordered"); err != nil {
			state.Queue = originalQueue
			state.LastError = err.Error()
			_ = saveRotationStateLocked(state)
			writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: err.Error()})
			return
		}
	} else if err := saveRotationStateLocked(state); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not save Boot Queue"})
		return
	}
	writeJSON(w, http.StatusOK, rotationResponseLocked(state))
}

func bootQueueRemoveHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	request, ok := decodeBootQueueMutation(w, r)
	if !ok {
		return
	}
	rotationMu.Lock()
	defer rotationMu.Unlock()
	state, err := loadRotationStateLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read Boot Queue"})
		return
	}
	index := -1
	for i, item := range state.Queue {
		if item.ID == request.ID {
			index = i
			break
		}
	}
	if index < 0 {
		writeJSON(w, http.StatusNotFound, statusResponse{Status: "error", Message: "Boot Queue item not found"})
		return
	}
	wasNext := state.NextSource == "queue" && state.NextQueueID == state.Queue[index].ID
	state.Queue = append(state.Queue[:index], state.Queue[index+1:]...)
	if wasNext {
		clearPreparedNext(&state)
		if err := prepareAutomationNextLocked(&state, "queue-remove"); err != nil {
			state.LastError = err.Error()
			_ = saveRotationStateLocked(state)
			writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: err.Error()})
			return
		}
	} else if err := saveRotationStateLocked(state); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not save Boot Queue"})
		return
	}
	garbageCollectAutomationObjectsLocked()
	writeJSON(w, http.StatusOK, rotationResponseLocked(state))
	showToastEvent("toast_queue_removed", "📤 Boot Creator: Animation removed from Boot Queue.")
}

func bootQueueClearHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	rotationMu.Lock()
	defer rotationMu.Unlock()
	state, err := loadRotationStateLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read Boot Queue"})
		return
	}
	wasQueueNext := state.NextSource == "queue"
	state.Queue = []bootQueueItem{}
	if wasQueueNext {
		clearPreparedNext(&state)
		if err := prepareAutomationNextLocked(&state, "queue-cleared"); err != nil {
			state.LastError = err.Error()
			_ = saveRotationStateLocked(state)
			writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: err.Error()})
			return
		}
	} else if err := saveRotationStateLocked(state); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not save Boot Queue"})
		return
	}
	garbageCollectAutomationObjectsLocked()
	writeJSON(w, http.StatusOK, rotationResponseLocked(state))
	showToastEvent("toast_queue_cleared", "🧹 Boot Creator: Boot Queue cleared.")
}

func bootQueueSkipNextHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	rotationMu.Lock()
	defer rotationMu.Unlock()
	state, err := loadRotationStateLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read Boot Queue"})
		return
	}
	switch state.NextSource {
	case "override":
		state.Override = nil
		clearPreparedNext(&state)
		if err := prepareAutomationNextLocked(&state, "override-skipped"); err != nil {
			state.LastError = err.Error()
			_ = saveRotationStateLocked(state)
			writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: err.Error()})
			return
		}
	case "queue":
		index := -1
		for i, item := range state.Queue {
			if item.ID == state.NextQueueID {
				index = i
				break
			}
		}
		if index >= 0 {
			state.Queue = append(state.Queue[:index], state.Queue[index+1:]...)
		}
		clearPreparedNext(&state)
		if err := prepareAutomationNextLocked(&state, "queue-skipped"); err != nil {
			state.LastError = err.Error()
			_ = saveRotationStateLocked(state)
			writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: err.Error()})
			return
		}
	case "rotation":
		if !state.Enabled {
			writeJSON(w, http.StatusConflict, statusResponse{Status: "error", Message: "Boot rotation is disabled"})
			return
		}
		reference := state.NextEntryID
		wasPaused := state.Paused
		state.Paused = false
		if err := prepareRotationNextLocked(&state, reference, "skip-next"); err != nil {
			state.Paused = wasPaused
			state.LastError = err.Error()
			_ = saveRotationStateLocked(state)
			writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: err.Error()})
			return
		}
		state.Paused = wasPaused
		if err := saveRotationStateLocked(state); err != nil {
			writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not save rotation state"})
			return
		}
	default:
		writeJSON(w, http.StatusConflict, statusResponse{Status: "error", Message: "Nothing is prepared for the next boot"})
		return
	}
	garbageCollectAutomationObjectsLocked()
	writeJSON(w, http.StatusOK, rotationResponseLocked(state))
	showToastEvent("toast_queue_skipped", "⏭️ Boot Creator: Prepared next boot skipped.")
}

func disableRotationForDeletedPlaylist(playlistID string) {
	rotationMu.Lock()
	defer rotationMu.Unlock()
	state, err := loadRotationStateLocked()
	if err != nil || !state.Enabled || state.PlaylistID != playlistID {
		return
	}
	state.Enabled = false
	state.Paused = false
	state.PlaylistID = ""
	state.ShuffleRemaining = []string{}
	state.LastError = "rotation playlist was deleted"
	if state.NextSource == "rotation" {
		clearPreparedNext(&state)
		_ = prepareAutomationNextLocked(&state, "rotation-playlist-deleted")
	} else {
		_ = saveRotationStateLocked(state)
	}
	writeLog("Boot rotation disabled because its playlist was deleted.")
}

func prepareRotationAfterBoot() error {
	rotationMu.Lock()
	defer rotationMu.Unlock()
	state, err := loadRotationStateLocked()
	if err != nil {
		return err
	}
	if state.NextName != "" {
		completedAt := time.Now().UnixMilli()
		appendBootActivity(bootActivityEntry{
			Kind:            "boot",
			Status:          "completed",
			EntryID:         state.NextEntryID,
			ObjectID:        state.NextObjectID,
			Name:            state.NextName,
			Source:          state.NextSource,
			Reason:          state.NextReason,
			PreparedAt:      state.PreparedAt,
			BootCompletedAt: completedAt,
			CreatedAt:       completedAt,
		})
		state.LastBootEntryID = state.NextEntryID
		state.LastBootObjectID = state.NextObjectID
		state.LastBootName = state.NextName
		state.LastBootSource = state.NextSource
		state.LastBootAt = time.Now().UnixMilli()
	}
	switch state.NextSource {
	case "rotation":
		state.LastRotationEntryID = state.NextEntryID
	case "queue":
		index := -1
		for i, item := range state.Queue {
			if item.ID == state.NextQueueID {
				index = i
				break
			}
		}
		if index >= 0 {
			state.Queue = append(state.Queue[:index], state.Queue[index+1:]...)
		}
	case "override":
		state.Override = nil
	}
	clearPreparedNext(&state)
	if err := prepareAutomationNextLocked(&state, "post-boot"); err != nil {
		state.LastError = err.Error()
		_ = saveRotationStateLocked(state)
		return err
	}
	garbageCollectAutomationObjectsLocked()
	return nil
}

func stagedAnimationPath() string {
	return filepath.Join(ModDir, "staging", "bootanimation.zip")
}

func stagedMetadata() (historyMetadata, error) {
	path := stagedAnimationPath()
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		return historyMetadata{}, errors.New("no staged animation")
	}
	return inspectBootAnimation(path, "")
}

func setSystemFileContext(path string) error {
	command := "chcon u:object_r:system_file:s0 " + shellQuote(path)
	output, err := exec.Command("/system/bin/sh", "-c", command).CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return errors.New(message)
	}
	return nil
}

func previewShadowPath(targetPath string) string {
	return ModDir + targetPath + ".bas-test-preview"
}

func cleanupPreviewShadows() {
	paths, err := readSavedPaths()
	if err != nil {
		return
	}
	for _, targetPath := range paths {
		_ = os.Remove(previewShadowPath(targetPath))
		_ = os.Remove(previewShadowPath(targetPath) + ".tmp")
	}
}

func prepareStagedPreviewSource() (string, error) {
	paths, err := readSavedPaths()
	if err != nil {
		return "", errors.New("no valid saved path found")
	}
	stagePath := stagedAnimationPath()
	if err := validateBootAnimation(stagePath); err != nil {
		return "", errors.New("no valid staged animation")
	}

	shadowPath := previewShadowPath(paths[0])
	if err := os.MkdirAll(filepath.Dir(shadowPath), 0755); err != nil {
		return "", errors.New("could not prepare preview source directory")
	}
	tempPath := shadowPath + ".tmp"
	_ = os.Remove(tempPath)
	_ = os.Remove(shadowPath)
	if err := linkOrCopyFile(stagePath, tempPath, 0644); err != nil {
		return "", errors.New("could not prepare staged preview source")
	}
	if err := setSystemFileContext(tempPath); err != nil {
		_ = os.Remove(tempPath)
		return "", errors.New("could not label staged preview for boot animation service")
	}
	if err := os.Rename(tempPath, shadowPath); err != nil {
		_ = os.Remove(tempPath)
		return "", errors.New("could not commit staged preview source")
	}
	if err := os.Chmod(shadowPath, 0644); err != nil {
		_ = os.Remove(shadowPath)
		return "", errors.New("could not set staged preview permissions")
	}
	return shadowPath, nil
}

func mountNamespaceID(path string) string {
	value, err := os.Readlink(path)
	if err != nil {
		return "unknown"
	}
	return value
}

func globalMountCommand(args ...string) ([]byte, error) {
	if len(args) == 0 {
		return nil, errors.New("missing global mount command")
	}
	selfNamespace := mountNamespaceID("/proc/self/ns/mnt")
	initNamespace := mountNamespaceID("/proc/1/ns/mnt")
	if selfNamespace != "unknown" && selfNamespace == initNamespace {
		return exec.Command(args[0], args[1:]...).CombinedOutput()
	}
	if info, err := os.Stat("/system/bin/nsenter"); err == nil && !info.IsDir() {
		cmdArgs := append([]string{"-t", "1", "-m"}, args...)
		return exec.Command("/system/bin/nsenter", cmdArgs...).CombinedOutput()
	}
	if info, err := os.Stat("/system/bin/toybox"); err == nil && !info.IsDir() {
		cmdArgs := append([]string{"nsenter", "-t", "1", "-m"}, args...)
		return exec.Command("/system/bin/toybox", cmdArgs...).CombinedOutput()
	}
	if nsenterPath, err := exec.LookPath("nsenter"); err == nil {
		cmdArgs := append([]string{"-t", "1", "-m"}, args...)
		return exec.Command(nsenterPath, cmdArgs...).CombinedOutput()
	}
	return nil, errors.New("global mount namespace unavailable")
}

func runGlobalMountCommand(args ...string) error {
	output, err := globalMountCommand(args...)
	if err == nil {
		return nil
	}
	message := strings.TrimSpace(string(output))
	if message == "" {
		message = err.Error()
	}
	return errors.New(message)
}

func decodeMountInfoPath(value string) string {
	replacer := strings.NewReplacer(`\\040`, " ", `\\011`, "\t", `\\012`, "\n", `\\134`, `\\`)
	return replacer.Replace(value)
}

func globalMountCount(targetPath string) (int, error) {
	output, err := globalMountCommand("/system/bin/cat", "/proc/self/mountinfo")
	if err != nil {
		return 0, err
	}
	count := 0
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 5 && decodeMountInfoPath(fields[4]) == targetPath {
			count++
		}
	}
	return count, nil
}

func globalRegularFileExists(path string) bool {
	return runGlobalMountCommand("/system/bin/test", "-f", path) == nil
}

func globalFilesMatch(sourcePath, targetPath string) bool {
	return runGlobalMountCommand("/system/bin/cmp", "-s", sourcePath, targetPath) == nil
}

func previewMountMarkerPath() string {
	return filepath.Join(ModDir, ".preview_mounts")
}

func writePreviewMountMarker(paths []string) {
	if len(paths) == 0 {
		_ = os.Remove(previewMountMarkerPath())
		return
	}
	_ = os.WriteFile(previewMountMarkerPath(), []byte(strings.Join(paths, "\n")+"\n"), 0600)
}

func readPreviewMountMarker() []string {
	data, err := os.ReadFile(previewMountMarkerPath())
	if err != nil {
		return nil
	}
	paths := make([]string, 0, 4)
	seen := make(map[string]struct{})
	for _, raw := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		path := strings.TrimSpace(raw)
		cleaned, ok := validateTargetPath(path)
		if !ok {
			continue
		}
		if _, exists := seen[cleaned]; exists {
			continue
		}
		seen[cleaned] = struct{}{}
		paths = append(paths, cleaned)
	}
	return paths
}

func unmountPreviewTargets(paths []string) {
	for i := len(paths) - 1; i >= 0; i-- {
		_ = runGlobalMountCommand("/system/bin/umount", paths[i])
	}
	writePreviewMountMarker(nil)
}

func cleanupOrphanPreviewMounts() {
	paths := readPreviewMountMarker()
	if len(paths) > 0 {
		unmountPreviewTargets(paths)
	}
}

func currentPreviewState() (bool, []string) {
	previewMu.Lock()
	defer previewMu.Unlock()
	return previewActive, append([]string(nil), previewTargetPaths...)
}

func acquirePreview(targetPaths []string) (chan struct{}, bool) {
	previewMu.Lock()
	defer previewMu.Unlock()
	if previewActive {
		return nil, false
	}
	previewActive = true
	previewTargetPaths = append([]string(nil), targetPaths...)
	previewCancel = make(chan struct{})
	return previewCancel, true
}

func releasePreview(cancel chan struct{}) {
	previewMu.Lock()
	if previewActive && previewCancel == cancel {
		previewActive = false
		previewTargetPaths = nil
		previewCancel = nil
	}
	previewMu.Unlock()
}

func cleanupPreview(targetPaths []string, cancel chan struct{}) {
	_ = exec.Command("/system/bin/setprop", "ctl.stop", "bootanim").Run()
	time.Sleep(1 * time.Second)
	unmountPreviewTargets(targetPaths)
	releasePreview(cancel)
}

func cancelActivePreview() bool {
	previewMu.Lock()
	if !previewActive || previewCancel == nil {
		previewMu.Unlock()
		return false
	}
	cancel := previewCancel
	select {
	case <-cancel:
	default:
		close(cancel)
	}
	previewMu.Unlock()
	return true
}

func waitForPreviewStop(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		active, _ := currentPreviewState()
		if !active {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func clearStagedTestState() error {
	if cancelActivePreview() {
		waitForPreviewStop(3 * time.Second)
	}
	cleanupOrphanPreviewMounts()
	cleanupPreviewShadows()
	if err := os.Remove(stagedAnimationPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func startPreviewFromFile(sourcePath string, cleanupSource func()) error {
	finishSource := func() {
		if cleanupSource != nil {
			cleanupSource()
		}
	}

	paths, err := readSavedPaths()
	if err != nil {
		finishSource()
		return errors.New("no valid saved path found")
	}
	if info, err := os.Stat(sourcePath); err != nil || info.IsDir() {
		finishSource()
		return errors.New("preview animation not found")
	}

	targetPaths := make([]string, 0, len(paths))
	for _, targetPath := range paths {
		if globalRegularFileExists(targetPath) {
			targetPaths = append(targetPaths, targetPath)
		}
	}
	if len(targetPaths) == 0 {
		finishSource()
		return errors.New("system animation path not found in global mount namespace")
	}

	cancel, ok := acquirePreview(targetPaths)
	if !ok {
		finishSource()
		return errors.New("preview busy")
	}

	mounted := make([]string, 0, len(targetPaths))
	rollback := func() {
		unmountPreviewTargets(mounted)
		releasePreview(cancel)
		finishSource()
	}
	for _, targetPath := range targetPaths {
		beforeCount, countErr := globalMountCount(targetPath)
		if countErr != nil {
			rollback()
			return errors.New("could not inspect global preview mount namespace")
		}
		if err := runGlobalMountCommand("/system/bin/mount", "-o", "bind", sourcePath, targetPath); err != nil {
			rollback()
			return errors.New("could not mount preview animation in global namespace: " + err.Error())
		}
		mounted = append(mounted, targetPath)
		writePreviewMountMarker(mounted)
		afterCount, countErr := globalMountCount(targetPath)
		if countErr != nil || afterCount <= beforeCount || !globalFilesMatch(sourcePath, targetPath) {
			rollback()
			return errors.New("preview global mount verification failed")
		}
	}

	if err := exec.Command("/system/bin/setprop", "ctl.start", "bootanim").Run(); err != nil {
		rollback()
		return errors.New("could not start boot animation preview")
	}
	writeLog("Preview mounted in global namespace on: " + strings.Join(mounted, ", "))
	go func() {
		select {
		case <-time.After(15 * time.Second):
		case <-cancel:
		}
		cleanupPreview(mounted, cancel)
		finishSource()
		showToastEvent("toast_preview_finished", "🏁 Boot Creator: Preview finished!")
		writeLog("Preview finished and global mounts were removed.")
	}()
	return nil
}

func testStageHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}
	if active, _ := currentPreviewState(); active {
		writeJSON(w, http.StatusConflict, statusResponse{Status: "busy", Message: "Stop the current preview before staging another animation"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBootAnimationBytes+(8<<20))
	multipartReader, err := r.MultipartReader()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Upload must be multipart/form-data"})
		return
	}
	tempFile, err := os.CreateTemp("/data/local/tmp", "boot_creator_stage_*.zip")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not create temporary file"})
		return
	}
	tempPath := tempFile.Name()
	_ = tempFile.Close()
	defer os.Remove(tempPath)

	hasAnimation := false
	for {
		part, nextErr := multipartReader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Could not read multipart upload"})
			return
		}
		if part.FormName() == "bootanimation" && !hasAnimation {
			if err := saveMultipartFile(part, tempPath, maxBootAnimationBytes); err != nil {
				_ = part.Close()
				writeJSON(w, http.StatusRequestEntityTooLarge, statusResponse{Status: "error", Message: "bootanimation.zip exceeds the transport safety limit"})
				return
			}
			hasAnimation = true
		} else {
			_, _ = io.Copy(io.Discard, io.LimitReader(part, 1<<20))
		}
		_ = part.Close()
	}
	if !hasAnimation {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Failed to receive bootanimation.zip"})
		return
	}
	if err := validateBootAnimation(tempPath); err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: err.Error()})
		return
	}
	meta, err := inspectBootAnimation(tempPath, "")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: err.Error()})
		return
	}
	stageDir := filepath.Dir(stagedAnimationPath())
	if err := os.MkdirAll(stageDir, 0755); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not prepare test staging"})
		return
	}
	stageTemp := stagedAnimationPath() + ".tmp"
	_ = os.Remove(stageTemp)
	if err := copyFile(tempPath, stageTemp, 0644); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not stage boot animation"})
		return
	}
	if err := os.Rename(stageTemp, stagedAnimationPath()); err != nil {
		_ = os.Remove(stageTemp)
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not commit staged boot animation"})
		return
	}
	if err := os.Chmod(stagedAnimationPath(), 0644); err != nil {
		_ = os.Remove(stagedAnimationPath())
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not set staged animation permissions"})
		return
	}
	if err := setSystemFileContext(stagedAnimationPath()); err != nil {
		_ = os.Remove(stagedAnimationPath())
		writeLog("Error: Could not label staged animation for boot preview: " + err.Error())
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not prepare staged animation for boot preview"})
		return
	}
	warnings := []string{}
	if meta.SizeBytes > directUploadWarningBytes {
		warnings = append(warnings, "large_boot_animation")
	}
	writeLog(fmt.Sprintf("Test animation staged (%d bytes).", meta.SizeBytes))
	writeJSON(w, http.StatusOK, testStageResponse{Status: "success", HasStaged: true, PreviewActive: false, SizeBytes: meta.SizeBytes, Width: meta.Width, Height: meta.Height, FPS: meta.FPS, FrameCount: meta.FrameCount, Parts: meta.Parts, FrameFormat: meta.FrameFormat, HasAudio: meta.HasAudio, Warnings: warnings})
}

func testStatusHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}
	active, _ := currentPreviewState()
	meta, err := stagedMetadata()
	if err != nil {
		writeJSON(w, http.StatusOK, testStageResponse{Status: "success", HasStaged: false, PreviewActive: active})
		return
	}
	warnings := []string{}
	if meta.SizeBytes > directUploadWarningBytes {
		warnings = append(warnings, "large_boot_animation")
	}
	writeJSON(w, http.StatusOK, testStageResponse{Status: "success", HasStaged: true, PreviewActive: active, SizeBytes: meta.SizeBytes, Width: meta.Width, Height: meta.Height, FPS: meta.FPS, FrameCount: meta.FrameCount, Parts: meta.Parts, FrameFormat: meta.FrameFormat, HasAudio: meta.HasAudio, Warnings: warnings})
}

func testStartHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}
	path := stagedAnimationPath()
	if err := validateBootAnimation(path); err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "No valid staged animation"})
		return
	}
	previewSource, err := prepareStagedPreviewSource()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: err.Error()})
		return
	}
	if err := startPreviewFromFile(previewSource, func() { _ = os.Remove(previewSource) }); err != nil {
		status := http.StatusInternalServerError
		if err.Error() == "preview busy" {
			status = http.StatusConflict
		}
		writeJSON(w, status, statusResponse{Status: "error", Message: err.Error()})
		return
	}
	showToastEvent("toast_preview_staged", "👀 Boot Creator: Showing staged preview. Look at your screen!")
	writeLog("Staged preview started on device.")
	writeJSON(w, http.StatusOK, statusResponse{Status: "success", Message: "Preview started"})
}

func testStopHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}
	if !cancelActivePreview() {
		writeJSON(w, http.StatusOK, statusResponse{Status: "success", Message: "No preview was running"})
		return
	}
	waitForPreviewStop(3 * time.Second)
	writeJSON(w, http.StatusOK, statusResponse{Status: "success", Message: "Preview stopped"})
}

func testClearHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}
	if err := clearStagedTestState(); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not clear staged animation"})
		return
	}
	writeJSON(w, http.StatusOK, statusResponse{Status: "success"})
	showToastEvent("toast_test_staging_cleared", "🧪 Boot Creator: Test staging cleared.")
}

func testApplyHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}
	if active, _ := currentPreviewState(); active {
		writeJSON(w, http.StatusConflict, statusResponse{Status: "busy", Message: "Stop the preview before applying the staged animation"})
		return
	}
	stagePath := stagedAnimationPath()
	if err := validateBootAnimation(stagePath); err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "No valid staged animation"})
		return
	}
	historyDir := filepath.Join(ModDir, "history")
	if err := os.MkdirAll(historyDir, 0755); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not prepare history"})
		return
	}
	id := newHistoryID(historyDir)
	historyZipPath := filepath.Join(historyDir, id+".zip")
	if err := linkOrCopyFile(stagePath, historyZipPath, 0644); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not save staged animation to history"})
		return
	}
	meta, err := inspectBootAnimation(historyZipPath, id)
	if err != nil {
		_ = os.Remove(historyZipPath)
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not inspect staged animation"})
		return
	}
	_ = writeHistoryMetadata(id, meta)
	cmd := exec.Command("/system/bin/sh", ModDir+"/inject.sh", historyZipPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(historyZipPath)
		_ = os.Remove(historyMetadataPath(id))
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Failed to inject staged boot animation"})
		writeLog("Error: Failed to apply staged animation: " + strings.TrimSpace(string(output)))
		return
	}
	_ = os.Remove(stagePath)
	cleanHistory(historyDir)
	warnings := []string{}
	if meta.SizeBytes > directUploadWarningBytes {
		warnings = append(warnings, "large_boot_animation")
	}
	writeJSON(w, http.StatusOK, uploadResponse{Status: "success", HistoryID: id, SizeBytes: meta.SizeBytes, Warnings: warnings})
	showToastEvent("toast_test_applied", "🚀 Boot Creator: Tested animation applied and saved to history!")
	writeLog("Success: Staged animation applied and saved to history (ID: " + id + ").")
}

func testAnimHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}
	paths, err := readSavedPaths()
	if err != nil {
		writeJSON(w, http.StatusNotFound, statusResponse{Status: "error", Message: "No valid saved path found"})
		return
	}
	modulePath := ""
	for _, targetPath := range paths {
		candidate := ModDir + targetPath
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			modulePath = candidate
			break
		}
	}
	if modulePath == "" {
		writeJSON(w, http.StatusNotFound, statusResponse{Status: "error", Message: "Custom animation not found in module"})
		return
	}
	if err := startPreviewFromFile(modulePath, nil); err != nil {
		status := http.StatusInternalServerError
		if err.Error() == "preview busy" {
			status = http.StatusConflict
		}
		writeJSON(w, status, statusResponse{Status: "error", Message: err.Error()})
		return
	}
	showToastEvent("toast_preview_device", "👀 Boot Creator: Showing device preview. Look at your screen!")
	writeLog("Preview triggered on device using bind mount.")
	writeJSON(w, http.StatusOK, statusResponse{Status: "success", Message: "Preview started"})
}

func main() {
	bridgeID = loadOrCreateBridgeID()
	initLiveSync()
	if len(os.Args) > 1 && os.Args[1] == "--prepare-rotation-after-boot" {
		if err := prepareRotationAfterBoot(); err != nil {
			writeLog("Rotation post-boot preparation failed: " + err.Error())
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		os.Exit(0)
	}
	cleanupOrphanPreviewMounts()
	cleanupPreviewShadows()
	_ = os.RemoveAll(filepath.Join(ModDir, "staging"))
	writeLog("=== Boot Creator Server Started ===")
	writeLog("Mount namespace: server=" + mountNamespaceID("/proc/self/ns/mnt") + " init=" + mountNamespaceID("/proc/1/ns/mnt"))

	http.HandleFunc("/webui", webUIRootHandler)
	http.HandleFunc("/webui/", webUIRootHandler)
	http.HandleFunc("/webui/session", webUISessionHandler)
	http.HandleFunc("/webui/request_auth", webUIRequestAuthHandler)
	http.HandleFunc("/webui/logout", webUILogoutHandler)
	http.HandleFunc("/webui/logs", webUILogsHandler)
	http.HandleFunc("/webui/logs/download", webUILogsDownloadHandler)

	http.HandleFunc("/info", infoHandler)
	http.HandleFunc("/ping", pingHandler)
	http.HandleFunc("/live/revisions", liveRevisionsHandler)
	http.HandleFunc("/live/events", liveEventsHandler)
	http.HandleFunc("/presence/status", presenceStatusHandler)
	http.HandleFunc("/request_auth", requestAuthHandler)
	http.HandleFunc("/auth/start", authStartHandler)
	http.HandleFunc("/auth/status", authStatusHandler)
	http.HandleFunc("/auth/trusted/challenge", trustedAuthChallengeHandler)
	http.HandleFunc("/auth/trusted/verify", trustedAuthVerifyHandler)
	http.HandleFunc("/auth_callback", authCallbackHandler)
	http.HandleFunc("/trust/clients", trustClientsHandler)
	http.HandleFunc("/trust/sessions", trustSessionsHandler)
	http.HandleFunc("/trust/permission", liveMutationHandler("trust", "trust.changed", trustPermissionHandler))
	http.HandleFunc("/trust/revoke", liveMutationHandler("trust", "trust.changed", trustRevokeHandler))
	http.HandleFunc("/trust/revoke-all", liveMutationHandler("trust", "trust.changed", trustRevokeAllHandler))
	http.HandleFunc("/trust/session/disconnect", liveMutationHandler("sessions", "sessions.changed", trustSessionDisconnectHandler))
	http.HandleFunc("/trust/sessions/disconnect-all", liveMutationHandler("sessions", "sessions.changed", trustSessionsDisconnectAllHandler))
	http.HandleFunc("/audit/list", securityAuditListHandler)
	http.HandleFunc("/audit/clear", securityAuditClearHandler)
	http.HandleFunc("/audit/export", securityAuditExportHandler)
	http.HandleFunc("/pair_register", pairRegisterHandler)
	http.HandleFunc("/pair_status", pairStatusHandler)
	http.HandleFunc("/pair_exchange", pairExchangeHandler)
	http.HandleFunc("/disconnect", disconnectHandler)
	http.HandleFunc("/disconnect/beacon", disconnectBeaconHandler)
	http.HandleFunc("/upload", coordinatedOperationHandler("apply", auditedHandler("control", "animation.applied", uploadHandler)))
	http.HandleFunc("/remove", coordinatedOperationHandler("restore", auditedHandler("control", "animation.restored_default", removeHandler)))
	http.HandleFunc("/pull", pullHandler)
	http.HandleFunc("/reset", coordinatedOperationHandler("rescan", auditedHandler("system", "module.reset", resetHandler)))
	http.HandleFunc("/rescan", coordinatedOperationHandler("rescan", auditedHandler("system", "module.rescan", rescanHandler)))
	http.HandleFunc("/factory_reset", coordinatedOperationHandler("factory_reset", auditedHandler("system", "module.factory_reset", factoryResetHandler)))
	http.HandleFunc("/history/list", historyListHandler)
	http.HandleFunc("/history/items", historyItemsHandler)
	http.HandleFunc("/history/download", historyDownloadHandler)
	http.HandleFunc("/history/preview", historyPreviewHandler)
	http.HandleFunc("/history/apply", coordinatedOperationHandler("apply", auditedHandler("control", "history.applied", historyApplyHandler)))
	http.HandleFunc("/history/delete", auditedHandler("control", "history.deleted", historyDeleteHandler))
	http.HandleFunc("/playlist/list", playlistListHandler)
	http.HandleFunc("/playlist/create", liveMutationHandler("playlist", "playlist.changed", playlistCreateHandler))
	http.HandleFunc("/playlist/rename", liveMutationHandler("playlist", "playlist.changed", playlistRenameHandler))
	http.HandleFunc("/playlist/duplicate", liveMutationHandler("playlist", "playlist.changed", playlistDuplicateHandler))
	http.HandleFunc("/playlist/delete", coordinatedOperationHandler("automation_prepare", liveMutationHandler("playlist", "playlist.changed", playlistDeleteHandler)))
	http.HandleFunc("/playlist/reorder", liveMutationHandler("playlist", "playlist.changed", playlistReorderHandler))
	http.HandleFunc("/playlist/item/upload", liveMutationHandler("playlist", "playlist.changed", playlistUploadHandler))
	http.HandleFunc("/playlist/item/from-history", liveMutationHandler("playlist", "playlist.changed", playlistAddHistoryHandler))
	http.HandleFunc("/playlist/item/from-staged", liveMutationHandler("playlist", "playlist.changed", playlistAddStagedHandler))
	http.HandleFunc("/playlist/item/remove", liveMutationHandler("playlist", "playlist.changed", playlistItemRemoveHandler))
	http.HandleFunc("/playlist/item/reorder", liveMutationHandler("playlist", "playlist.changed", playlistItemReorderHandler))
	http.HandleFunc("/playlist/preview", coordinatedOperationHandler("preview", playlistPreviewHandler))
	http.HandleFunc("/playlist/download", playlistDownloadHandler)
	http.HandleFunc("/playlist/apply", coordinatedOperationHandler("apply", auditedHandler("control", "playlist.applied", playlistApplyHandler)))
	http.HandleFunc("/playlist/stage", coordinatedOperationHandler("preview", liveMutationHandler("test", "test.changed", playlistStageHandler)))
	http.HandleFunc("/rotation/status", rotationStatusHandler)
	http.HandleFunc("/rotation/configure", coordinatedOperationHandler("automation_prepare", auditedHandler("automation", "rotation.configured", rotationConfigureHandler)))
	http.HandleFunc("/rotation/prepare-next", coordinatedOperationHandler("automation_prepare", auditedHandler("automation", "rotation.next_prepared", rotationPrepareHandler)))
	http.HandleFunc("/rotation/pause", coordinatedOperationHandler("automation_prepare", auditedHandler("automation", "rotation.pause_changed", rotationPauseHandler)))
	http.HandleFunc("/queue/add", coordinatedOperationHandler("automation_prepare", auditedHandler("automation", "queue.changed", bootQueueAddHandler)))
	http.HandleFunc("/queue/use-next", coordinatedOperationHandler("automation_prepare", auditedHandler("automation", "queue.changed", bootQueueUseNextHandler)))
	http.HandleFunc("/queue/reorder", coordinatedOperationHandler("automation_prepare", auditedHandler("automation", "queue.changed", bootQueueReorderHandler)))
	http.HandleFunc("/queue/remove", coordinatedOperationHandler("automation_prepare", auditedHandler("automation", "queue.changed", bootQueueRemoveHandler)))
	http.HandleFunc("/queue/clear", coordinatedOperationHandler("automation_prepare", auditedHandler("automation", "queue.cleared", bootQueueClearHandler)))
	http.HandleFunc("/queue/skip-next", coordinatedOperationHandler("automation_prepare", auditedHandler("automation", "queue.next_skipped", bootQueueSkipNextHandler)))
	http.HandleFunc("/activity/list", bootActivityListHandler)
	http.HandleFunc("/activity/clear", liveMutationHandler("activity", "activity.changed", bootActivityClearHandler))
	http.HandleFunc("/device/probes", deviceProbesHandler)
	http.HandleFunc("/health/status", moduleHealthHandler)
	http.HandleFunc("/health/action", coordinatedOperationHandler("maintenance", liveMutationHandler("health", "health.changed", moduleHealthActionHandler)))
	http.HandleFunc("/test/stage", coordinatedOperationHandler("preview", liveMutationHandler("test", "test.changed", testStageHandler)))
	http.HandleFunc("/test/status", testStatusHandler)
	http.HandleFunc("/test/start", coordinatedOperationHandler("preview", liveMutationHandler("test", "test.changed", testStartHandler)))
	http.HandleFunc("/test/stop", coordinatedOperationHandler("preview", liveMutationHandler("test", "test.changed", testStopHandler)))
	http.HandleFunc("/test/apply", coordinatedOperationHandler("apply", auditedHandler("control", "test.applied", testApplyHandler)))
	http.HandleFunc("/test/clear", coordinatedOperationHandler("preview", liveMutationHandler("test", "test.changed", testClearHandler)))
	http.HandleFunc("/test_anim", coordinatedOperationHandler("preview", testAnimHandler))

	server := &http.Server{
		Addr:              "0.0.0.0:4040",
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Minute,
		WriteTimeout:      30 * time.Minute,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}

	fmt.Println("✨ Boot Creator server running on port 4040...")
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		writeLog("Server stopped with error: " + err.Error())
	}
}
