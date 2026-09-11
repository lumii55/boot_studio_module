### 🔐 Security Improvements

- Added secure session-based authentication between the website and the companion module.
- Added randomized nonces to the connection authorization flow.
- Added randomized session tokens required for privileged operations.
- Authorization now validates both the paired client and its active session token.
- Restricted the companion authorization callback to local requests only.
- Protected all sensitive API endpoints, including upload, pull, history, reset, removal and boot animation testing.
- Fixed unprotected history endpoints that could previously perform privileged operations without proper authorization.
- Added strict validation for history IDs and other request parameters.
- Removed unsafe use of client-controlled values in privileged shell commands.
- Restricted CORS to trusted/supported origins instead of allowing all origins.
- Added support for modern browser private-network requests.
- Restricted HTTP methods according to each endpoint's purpose.
- Added HTTP server limits and timeouts.
- Added upload size limits for boot animations and previews.
- Added validation for uploaded boot animation ZIPs, including:
  - Valid "desc.txt"
  - Valid animation frames
  - Resolution and FPS validation
  - Entry count limits
  - Uncompressed size limits
  - Protection against invalid or malicious paths such as "../"
- Protected the Android companion Activity and Receiver with privileged permissions.
- The server now starts only after verifying that the required companion version is installed.
- The module now fails safely if the correct companion application cannot be installed or verified.
- Prevented multiple boot animation test sessions from running simultaneously.
- Improved session invalidation when disconnecting.

### 🐛 Bug Fixes

- History previews are now stored correctly as ".webm" files instead of WebM data using a ".gif" extension.
- Maintained compatibility with legacy ".gif" history previews created by previous versions.
- Improved history ID generation to prevent collisions when multiple animations are applied within the same second.
- Failed animation injections no longer leave invalid entries in the history.
- Fixed cases where "inject.sh" could report success even when an internal copy operation failed.
- Added validation for required files and directories during animation injection.
- Removed an overly broad recursive permission change that could modify unrelated module files.
- Troubleshoot/Reset now correctly removes both current and previous diagnostic logs.

### 🛠️ Stability & Diagnostics

- Improved error handling during boot animation injection.
- Improved validation before applying animation files.
- History cleanup now runs only after a successful animation injection.
- Module logs are no longer permanently overwritten on every boot.
- The previous boot log is now preserved as "boot_creator.previous.log".
- Module actions can now display logs from both the current and previous boot sessions.
- Improved companion version verification and update handling.
- Improved server startup reliability by verifying the companion before launching the API server.
