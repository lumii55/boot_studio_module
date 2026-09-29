## 🚀 Major release overview

- Expanded the module from **11 advertised capabilities to 41** while keeping **API version 1**.
- Expanded the HTTP interface from **17 registered routes to 82**.
- Kept every HTTP route that existed in v1.3 for backward compatibility.
- Added persistent device identity, trusted clients, multi-client sessions, live state synchronization, playlists, boot automation, Device Test Lab, Device Intelligence, Module Health and a local Module WebUI.
- Updated the Companion app from **1.5 (`versionCode 6`) to 1.6 (`versionCode 7`)** so upgrades from v1.3 reliably install the new Companion code.

## 🔐 Connection, trust & multi-client access

- Added an asynchronous authorization flow so the website no longer needs to keep the initial connection request blocked while waiting for approval.
- Added a persistent **bridge identity** so the same module installation can be recognized across reconnects.
- Added optional **trusted browser identities** with cryptographic P-256 challenge verification for secure reconnects.
- Added a Companion approval choice between **session-only access** and **Always trust this browser**.
- Added four permission levels for trusted clients:
  - **Admin** — full access, including trust/security and recovery controls.
  - **Manage** — persistent module settings such as playlists, rotation, queue and History.
  - **Control** — apply, upload, preview and test animations without changing persistent access/automation settings.
  - **View** — read-only status, History, playlists, downloads and device information.
- Added trusted-client management: list clients, change permissions, revoke one client or revoke all persistent trust.
- Added active-session management: list sessions, disconnect individual sessions or disconnect all sessions.
- Added unload-safe disconnect handling and clearer feedback for manual disconnects, page closes and reloads.
- Added a local **Security Audit** with list, clear and JSON export support for supported access, security, maintenance and privileged actions.
- Added live state revisions and event streaming so connected clients can refresh only when canonical module state changes.
- Added privacy-limited client presence information and multi-client session awareness.
- Added coordinated operation leases so conflicting physical-device mutations cannot run at the same time from different clients.

## 📦 Animation transfer & History

- Raised the boot-animation transport limit from **25 MiB to 2 GiB**.
- Reworked uploads to stream multipart data into temporary files instead of relying on the previous small-request multipart path.
- Kept a warning threshold for animations larger than 25 MiB so clients can flag unusually large files without rejecting them outright.
- History remains intentionally bounded to the **5 most recently applied animations**.
- Added richer History metadata, including file size, dimensions, FPS, frame count, part count, frame format and detected audio.
- Added direct History ZIP downloads for reopening or exporting previous animations.
- Added automatic metadata generation for older History entries when the richer metadata file does not exist yet.
- Added generated GIF preview fallback when a History item has no stored WebM/GIF preview and contains previewable image frames.
- Improved History validation before re-applying stored animations.
- History deletion now also cleans generated metadata and preview artifacts belonging to the deleted entry.

## 🧪 Device Test Lab

- Added a dedicated staging area for testing an animation on the real device **before** applying it persistently.
- Added separate actions to stage, start preview, stop preview, apply the staged animation and clear staging.
- Added Test Lab status reporting with animation metadata and current preview state.
- Applying a staged animation now saves it into History using the same validated flow as a normal apply.
- Playlist items can also be staged into Test Lab.
- Reworked preview mounting and cleanup, including global mount-namespace support when available, so Android's boot animation service can see temporary previews without replacing the persistent animation.
- Added cleanup for interrupted/orphaned preview mounts and temporary preview state.

## 📚 Playlists, Boot Rotation & Boot Queue

- Added persistent **Playlists** separate from the bounded History list.
- Playlist animation storage is content-addressed and deduplicated, so identical animation ZIPs can be reused without unnecessary duplicate copies.
- Added playlist create, rename, duplicate, delete and reorder actions.
- Added playlist item import from:
  - a new uploaded ZIP;
  - an existing History entry;
  - the currently staged Test Lab animation.
- Added playlist item reorder/remove, preview, download, stage and apply actions.
- Added **Boot Rotation** with **Sequential**, **Random** and **Shuffle** selection modes.
- Added rotation pause/resume and manual preparation of the next rotation item.
- Added **Boot Queue** for one-time or ordered upcoming boot selections.
- Boot Queue supports add, one-time next-boot override, reorder, remove, clear and skip-next actions.
- Added persistent **Boot Activity** records for prepared/completed automation actions and supported errors.
- The boot service now finalizes the previous prepared item after Android boots and prepares the next Queue/Rotation animation automatically.

## 📱 Device Intelligence, paths & Module Health

- Added a shared validated boot-animation path layer used by install, apply, cleanup, update preservation and rescanning.
- Added stricter path safety rules, including allowed Android roots, traversal rejection, duplicate filtering and bounded cached path count.
- Added an explicit **Rescan boot paths** flow.
- Rescanning can migrate the currently applied module animation from old detected targets to newly detected targets instead of silently losing the active custom animation.
- Added path-cache schema tracking and a build/environment marker based on Android build properties.
- When the ROM/build environment changes, the module now keeps the existing valid cache but reports that a manual rescan is recommended instead of silently changing targets.
- The legacy v1.3 `/reset` route is now intentionally **non-destructive** and performs a safe path rescan.
- Added a separate `/factory_reset` action with explicit confirmation for destructive module-data reset.
- Added **Device Intelligence** probes for manufacturer/brand/model, Android/SDK/build/security patch, slot, resolution/density, detected boot-animation targets, path confidence, active/stock archive format, renderer evidence, audio-support evidence and mount-namespace availability.
- Added **Module Health** checks for boot paths, current animation, stock backup, History, playlists, automation state, Test Lab staging, trust/security state, stale temporary files, orphan playlist objects and free storage.
- Added storage usage reporting for History, playlists, backups, staging, trust data, logs and module runtime data.
- Added supported maintenance actions to remove stale temporary files, orphaned playlist objects and invalid Test Lab staging.

## 🖥️ Local Module WebUI & localization

- Added a phone-local Module WebUI at **`http://127.0.0.1:4040/webui/`**.
- The WebUI is restricted to the local Android device and uses its own temporary authorization session.
- Added phone-side controls for module overview, install/test, History, Playlists, Boot Rotation/Queue, Boot Activity, Device Intelligence, Module Health and logs.
- Added log viewing and log download support through the local WebUI.
- Added live refresh using the module revision system, with manual Refresh retained as fallback.
- Added localized Companion prompts, pairing messages and privileged-action status messages.
- Added **English, Portuguese, Spanish, French, German, Italian, Japanese and Simplified Chinese** support for the Companion/WebUI surfaces introduced in v1.4.
- Hardened the legacy pairing deep link by validating its expected scheme, host, path and token format before accepting it.

## 🛡️ Upgrade, runtime & persistence hardening

- Module updates now preserve the persistent bridge identity and trusted-client/security state when they already exist.
- If continuity-critical bridge/trust data exists but cannot be preserved safely, installation aborts instead of silently replacing that identity.
- Upgrade preservation now also covers supported path state, path schema/environment marker, History, Playlist/automation data, backups, logs, `system.prop` and currently applied module overlays.
- Added safer cached-path validation to install, injection and cleanup scripts.
- Backup copies now use tighter file permissions where supported.
- Injection now validates every target, reuses the first applied copy through hard links when possible, and falls back to copying when necessary.
- Added silent injection support for internal automation flows that should not emit duplicate notifications.
- Added atomic/temp-file persistence patterns and rollback/error handling across trust, playlists, rotation, queue, audit and related state.
- Improved request-size enforcement, JSON parsing, temporary-file handling and cleanup behavior.
- Improved live-event subscriber bounds, server lifecycle handling and preview cancellation/cleanup.

## 🧰 Companion source & build tooling

- Companion source version is now **1.6 / `versionCode 7`**, ensuring v1.3 users with the older code-6 Companion receive the updated app during module upgrade.
- Companion prompts now expose trust persistence and permission-level choices required by the new trusted-client flow.
- Reworked the Companion build script with stricter error handling and required-tool checks.
- Android platform files are now cached instead of rewriting/downloading build inputs on every run.
- The build script no longer silently creates a new signing key unless explicitly allowed, helping preserve upgrade-compatible APK signatures.
- Added build-only support and post-signature APK verification.

## 🔌 Compatibility notes

- **Module:** v1.4 (`versionCode 5`)
- **Companion APK:** 1.6 (`versionCode 7`)
- **API:** version 1
- **Advertised capabilities:** 11 → 41
- **Registered HTTP routes:** 17 → 82
- All v1.3 HTTP routes remain registered in v1.4; new functionality is capability-gated rather than requiring an API-version break.
- The current Studio no longer advertises the old `qr_pairing` capability because the current connection flow uses the newer authorization/trust system.
- Legacy `/pair_register`, `/pair_status` and `/pair_exchange` endpoints remain available for backward compatibility.
