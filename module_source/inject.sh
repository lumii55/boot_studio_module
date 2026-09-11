#!/system/bin/sh

MODDIR=${0%/*}
CACHE_FILE="$MODDIR/saved_paths.txt"
NEW_ZIP="$1"

if [ ! -f "$CACHE_FILE" ]; then
    echo "Error: Scan cache not found!"
    exit 1
fi

if [ -z "$NEW_ZIP" ] || [ ! -f "$NEW_ZIP" ]; then
    echo "Error: Boot animation file not found!"
    exit 1
fi

APPLIED=0

while read -r TARGET_PATH; do
    if [ -n "$TARGET_PATH" ]; then
        case "$TARGET_PATH" in
            /*) ;;
            *) TARGET_PATH="/$TARGET_PATH" ;;
        esac

        TARGET_DIR=$(dirname "$TARGET_PATH")
        if ! mkdir -p "$MODDIR$TARGET_DIR"; then
            echo "Error: Could not create target directory."
            exit 1
        fi

        if ! cp "$NEW_ZIP" "$MODDIR$TARGET_PATH"; then
            echo "Error: Could not copy boot animation."
            exit 1
        fi

        chmod 644 "$MODDIR$TARGET_PATH" 2>/dev/null
        chcon u:object_r:system_file:s0 "$MODDIR$TARGET_PATH" 2>/dev/null
        APPLIED=1
    fi
done < "$CACHE_FILE"

if [ "$APPLIED" -ne 1 ]; then
    echo "Error: No valid target paths were available."
    exit 1
fi

if ! printf '%s\n' "persist.sys.bootanim.play_sound=1" "ro.bootanim.set_volume=1" > "$MODDIR/system.prop"; then
    echo "Error: Could not create system.prop."
    exit 1
fi

chmod 644 "$MODDIR/system.prop"
rm -f "$NEW_ZIP"

cmd notification post -S bigtext -t "✨ Boot Creator" "tag" "Injection successful! Reboot to see your new animation!" >/dev/null 2>&1

echo "Success! Directory paths unlocked, Magic Mount applied and sound enabled!"
exit 0
