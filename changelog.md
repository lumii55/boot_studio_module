# v1.5 — Security & connection update

## 🔐 Security

### Encrypted connections from other devices
- Connections from a PC now use **HTTPS (TLS 1.3) on port 4041**.
- The certificate is generated **on your phone** (ECDSA P-256, valid for one year) and stored in the module's
  private `trust/` folder with root-only permissions. It is kept across reboots and module updates.
- A new certificate is only created when it expires or when your phone gets a Wi-Fi address it doesn't cover,
  so you don't have to accept it again and again.
- **Plain HTTP (port 4040) now only accepts the phone itself.** Nobody else on your Wi-Fi can read your session
  or reach the module over unencrypted HTTP.
- Requests to the local port must use a local host name, which blocks DNS-rebinding tricks from web pages.
- If HTTPS can't start, the module **never** falls back to plain HTTP on the network; it stays local-only and
  says so in the log and the WebUI.

### Fixes
- **Authorization hijack:** while you were approving a connection, another app on the same phone could obtain
  that connection's authorization. A pending or approved request is now never handed to a second caller.
- **WebUI session misuse:** the local Module WebUI accepted its admin session from other web pages running on
  the phone (on a different port). It now only accepts requests from its own page, and a WebUI session opened
  over HTTPS can't be reused over plain HTTP.
- **Trusted browsers:** a saved trusted browser whose permission is missing or unknown is now refused instead
  of being treated as Admin.

### Safer animation files
Boot animation ZIPs are inspected **before** they are previewed, tested or applied. A ZIP is rejected when it
contains:
- paths that try to escape the animation folder, or absolute paths;
- duplicate file names or symbolic links;
- encrypted entries or unsupported compression;
- impossible sizes, too many files, or an extreme compression ratio (ZIP bombs).

## 📡 Connecting from a PC

- **Secure connection (HTTPS):** in Boot Animation Studio on another device, choose *Secure connection*. The
  first time, the browser warns that it can't verify the certificate; choose *Advanced → Continue* once. A small
  page from your phone confirms it, closes itself, and the Studio connects. Next time it's remembered.
- **The network search finds your phone again.** A tiny discovery signal on port 4042 answers the Studio's
  search with "a Companion Module is here" and nothing else: no device name, no data, no controls. It runs on
  its own port, separate from everything else. To turn it off, create an empty file named `discovery.disabled`
  in `/data/adb/modules/boot_creator/` and reboot.
- **The Module WebUI shows your phone's address** to type on the other device, plus the certificate fingerprint
  (under *Check the HTTPS certificate*) if you want to verify the connection by hand. This box is now translated
  into all 8 WebUI languages.
- Browsers mark the Studio page as "not secure" while it talks to your phone. The connection is encrypted, but a
  browser can't verify a certificate that your phone made itself. This is expected.

## 🔁 Reconnecting

- Fixed: connecting again right after disconnecting could fail with "too many requests". Authorization prompts
  and trusted-browser reconnects now have **separate limits**.
- Limits: 3 authorization requests per minute from another device and 6 from the phone itself, then a 60-second
  pause. The pause doesn't grow if you keep trying.
- The module now tells the Studio how long the pause lasts, so the website can show the exact wait time.

## 🛠️ Stability

- **Applying and rescanning are atomic:** the new file is fully written next to the old one and only
  then swapped in. A failed copy can no longer leave a broken or half-written boot animation, and temporary files
  are cleaned up.
- The module's built-in tests now run during every build, and test files are no longer included in the
  installed module.
- The server was reorganized internally for easier maintenance. Its behavior, routes and permissions are
  unchanged.

## ↩️ Updating from v1.4

- **Kept:** History, playlists, boot rotation, boot queue, boot activity, trusted browsers, the original-animation
  backup, saved boot-animation paths and your current boot animation.
- **Companion app:** unchanged (1.6), not reinstalled.
- **On the phone itself:** nothing changes.
- **On other devices:** connect once with *Secure connection* (the website guides you). Devices that knew your
  phone over plain HTTP are switched to the secure connection automatically.
- **Compatibility:** API version 1, every v1.4 feature and route is kept. The website keeps supporting v1.4 and
  older modules.