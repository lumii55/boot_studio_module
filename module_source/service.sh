#!/system/bin/sh

MODDIR=${0%/*}
LOGFILE="$MODDIR/boot_creator.log"
PREVIOUS_LOGFILE="$MODDIR/boot_creator.previous.log"
CACHE_FILE="$MODDIR/saved_paths.txt"
TEMP_LIST="$MODDIR/temp_paths.txt"
PKG_NAME="com.bootcreator.companion"
APK_PATH="$MODDIR/companion.apk"
EXPECTED_COMPANION_VERSION=6

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

if [ -f "$CACHE_FILE" ]; then
    log_msg "Cache found. Reading file..."
    while read -r p; do
        log_msg "Path loaded from cache: $p"
    done < "$CACHE_FILE"
else
    log_msg "Cache not found. Looking for bootanimation.zip"
    rm -f "$TEMP_LIST"

    log_msg "[Step 1] Checking priority paths..."
    for P in /apex/com.android.bootanimation/etc/bootanimation.zip \
             /product/media/bootanimation.zip \
             /oem/media/bootanimation.zip \
             /system_ext/media/bootanimation.zip \
             /system/media/bootanimation.zip; do
        if [ -f "$P" ]; then
            echo "$P" >> "$TEMP_LIST"
            log_msg "[Step 1] Found: $P"
        fi
    done

    log_msg "[Step 2] Searching in system folders..."
    find /system /vendor /product /oem /odm /system_ext /apex -maxdepth 4 -type f -name "bootanimation.zip" 2>/dev/null >> "$TEMP_LIST"

    if [ -f "$TEMP_LIST" ]; then
        sort -u "$TEMP_LIST" > "$CACHE_FILE"
        rm -f "$TEMP_LIST"
    fi

    if [ -s "$CACHE_FILE" ]; then
        log_msg "[Step 1 & 2] Paths found and saved to cache."
    else
        log_msg "No bootanimation was found. Activating recovery paths."
        echo "/system/media/bootanimation.zip" > "$CACHE_FILE"
        echo "/product/media/bootanimation.zip" >> "$CACHE_FILE"
        echo "/oem/media/bootanimation.zip" >> "$CACHE_FILE"
        echo "/system_ext/media/bootanimation.zip" >> "$CACHE_FILE"
        echo "/apex/com.android.bootanimation/etc/bootanimation.zip" >> "$CACHE_FILE"
    fi
fi

log_msg "Verifying backups in the vault..."
while read -r TARGET_PATH; do
    if [ -n "$TARGET_PATH" ] && [ -f "$TARGET_PATH" ]; then
        BACKUP_PATH="$MODDIR/backup$TARGET_PATH"
        if [ ! -f "$BACKUP_PATH" ]; then
            mkdir -p "$(dirname "$BACKUP_PATH")"
            cp "$TARGET_PATH" "$BACKUP_PATH"
            log_msg "Backup created for: $TARGET_PATH"
        fi
    fi
done < "$CACHE_FILE"

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
