🔌 API Compatibility

- Added the "/info" endpoint for module version and capability discovery.
- The module now reports:
  - API version
  - Module version
  - Module version code
  - Companion version code
  - Supported capabilities
- Module version information is read automatically from "module.prop".
- Added capability reporting so the website can detect which features are supported by the installed module.
- Introduced API versioning to improve compatibility between different website and module releases.
- Existing supported website versions remain compatible with the updated module.
- Added compatibility support for future module features without requiring immediate protocol changes.

📱 Companion Improvements

- Updated the companion app branding to Boot Animation Studio Module.
- Reworked module notifications into a custom top-screen overlay.
- Added redesigned privileged-action notifications with:
  - Dark translucent card
  - Rounded corners
  - Module icon
  - "ROOT" badge
  - Status-specific accents
  - Success, error, warning and information states
  - Smooth enter and exit animations
- Module notifications are now displayed at the top of the screen to avoid overlapping website notifications.
- Overlay notifications can be dismissed by tapping them.
- Added automatic fallback to native Android Toast notifications when overlays are unavailable.
- Removed the unnecessary legacy "TOAST_WINDOW" app-op.
- Improved companion build tooling so the compiled APK is automatically copied into "module_source".
- Updated companion version requirements and verification for the new companion release.

📷 QR Pairing

- Added secure QR-based pairing support.
- Added a dedicated companion pairing Activity.
- The companion can now receive and approve temporary pairing requests opened through a QR code.
- QR pairing uses randomized 256-bit temporary tokens.
- Pairing tokens expire automatically and are removed after successful use.
- QR pairing does not directly grant privileged access.
- Successful QR approval is exchanged for the module's normal authenticated session.
- Pairing registration is restricted to local requests.
- Added internal verification to ensure the pairing token approved on the phone matches the session being created.
- Added "qr_pairing" to the module capability list.
- Manual IP connection and existing authentication methods remain supported.

🌐 Network & Pairing Integration

- Improved integration between the module server and companion during local-network pairing.
- Added module-side support for secure pairing discovery.
- Added pairing status feedback through the companion's custom overlay.
- The companion can display the device's local IP after QR approval to provide a manual fallback when automatic discovery is unavailable.
- Existing privileged session authentication remains unchanged after pairing.
