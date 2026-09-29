#!/system/bin/sh

MODDIR=${0%/*}
LOGFILE="$MODDIR/boot_creator.log"
PREVIOUS_LOGFILE="$MODDIR/boot_creator.previous.log"
CACHE_FILE="$MODDIR/saved_paths.txt"
SCHEMA_FILE="$MODDIR/.paths_schema"
ENV_FILE="$MODDIR/.paths_environment"
PKG_NAME="com.bootcreator.companion"
APK_PATH="$MODDIR/companion.apk"
EXPECTED_COMPANION_VERSION=7

. "$MODDIR/path_utils.sh" || exit 0

until [ "$(getprop sys.boot_completed)" = "1" ]; do
    sleep 1
done

sleep 3

if [ -f "$LOGFILE" ]; then
    mv -f "$LOGFILE" "$PREVIOUS_LOGFILE"
fi
: > "$LOGFILE"

log_msg() {
    echo "$(date '+%Y-%m-%d %H:%M:%S') - $1" >> "$LOGFILE"
}

log_msg "--- ✨ Boot Creator Module Started ✨ ---"

NEEDS_RESCAN=0
if ! saved_paths_are_valid "$CACHE_FILE"; then
    NEEDS_RESCAN=1
elif [ "$(cat "$SCHEMA_FILE" 2>/dev/null)" != "$PATH_SCHEMA_VERSION" ]; then
    NEEDS_RESCAN=1
fi

if [ "$NEEDS_RESCAN" -eq 1 ]; then
    log_msg "Path cache is missing, invalid or outdated. Running path scan..."
    if /system/bin/sh "$MODDIR/rescan_paths.sh"; then
        log_msg "Path scan completed successfully."
    else
        log_msg "Path scan failed. Secure server will remain disabled."
        exit 0
    fi
else
    log_msg "Valid path cache found."
    PATH_ENV_STATUS="$(path_environment_status "$ENV_FILE")"
    case "$PATH_ENV_STATUS" in
        changed)
            log_msg "System build changed since the last path scan. Keeping cached paths and recommending a manual rescan."
            ;;
        unknown)
            if write_path_environment_marker "$ENV_FILE"; then
                log_msg "Path environment baseline initialized for this build."
            else
                log_msg "Warning: Could not initialize path environment baseline."
            fi
            ;;
        *)
            log_msg "Path environment matches the last scan."
            ;;
    esac
fi

while IFS= read -r TARGET_PATH || [ -n "$TARGET_PATH" ]; do
    [ -n "$TARGET_PATH" ] || continue
    if is_valid_bootanimation_path "$TARGET_PATH"; then
        log_msg "Path loaded from cache: $TARGET_PATH"
    fi
done < "$CACHE_FILE"

log_msg "Verifying backups in the vault..."
while IFS= read -r TARGET_PATH || [ -n "$TARGET_PATH" ]; do
    [ -n "$TARGET_PATH" ] || continue
    is_valid_bootanimation_path "$TARGET_PATH" || continue
    if [ -f "$TARGET_PATH" ]; then
        BACKUP_PATH="$MODDIR/backup$TARGET_PATH"
        if [ ! -f "$BACKUP_PATH" ]; then
            mkdir -p "$(dirname "$BACKUP_PATH")"
            if cp "$TARGET_PATH" "$BACKUP_PATH"; then
                chmod 600 "$BACKUP_PATH" 2>/dev/null
                log_msg "Backup created for: $TARGET_PATH"
            else
                log_msg "Warning: Failed to create backup for: $TARGET_PATH"
            fi
        fi
    fi
done < "$CACHE_FILE"

if [ -f "$MODDIR/boot_server" ]; then
    chmod 755 "$MODDIR/boot_server" 2>/dev/null
    if "$MODDIR/boot_server" --prepare-rotation-after-boot >/dev/null 2>&1; then
        log_msg "Boot rotation post-boot preparation checked."
    else
        log_msg "Warning: Boot rotation could not prepare the next animation. See module log for details."
    fi
fi

INSTALLED_VERSION="$(dumpsys package "$PKG_NAME" 2>/dev/null | sed -n 's/.*versionCode=\([0-9][0-9]*\).*/\1/p' | head -n 1)"

if [ "$INSTALLED_VERSION" != "$EXPECTED_COMPANION_VERSION" ]; then
    log_msg "Installing or updating companion APK..."
    if pm install -r "$APK_PATH" >/dev/null 2>&1; then
        log_msg "Companion APK installation completed."
    else
        log_msg "Failed to install companion APK."
    fi
fi

INSTALLED_VERSION="$(dumpsys package "$PKG_NAME" 2>/dev/null | sed -n 's/.*versionCode=\([0-9][0-9]*\).*/\1/p' | head -n 1)"

if [ "$INSTALLED_VERSION" != "$EXPECTED_COMPANION_VERSION" ]; then
    log_msg "Companion APK version mismatch. Secure server will remain disabled."
    exit 0
fi

log_msg "Companion APK is up to date."
log_msg "Granting companion permissions..."
appops set "$PKG_NAME" SYSTEM_ALERT_WINDOW allow
pm grant "$PKG_NAME" android.permission.POST_NOTIFICATIONS 2>/dev/null

log_msg "Starting secure server on port 4040..."
chmod 755 "$MODDIR/boot_server"
nohup "$MODDIR/boot_server" > /dev/null 2>&1 &

log_msg "All set. Waiting for requests from the website."
