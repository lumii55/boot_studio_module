#!/system/bin/sh

MODDIR=${0%/*}
CACHE_FILE="$MODDIR/saved_paths.txt"
NEW_ZIP="$1"

. "$MODDIR/path_utils.sh" || exit 1

if ! saved_paths_are_valid "$CACHE_FILE"; then
    echo "Error: Scan cache is missing or invalid!"
    exit 1
fi

if [ -z "$NEW_ZIP" ] || [ ! -f "$NEW_ZIP" ]; then
    echo "Error: Boot animation file not found!"
    exit 1
fi

APPLIED=0
PRIMARY_APPLIED=""

while IFS= read -r TARGET_PATH || [ -n "$TARGET_PATH" ]; do
    [ -n "$TARGET_PATH" ] || continue
    if ! is_valid_bootanimation_path "$TARGET_PATH"; then
        echo "Error: Invalid target path in scan cache."
        exit 1
    fi

    TARGET_DIR=$(dirname "$TARGET_PATH")
    TARGET_FILE="$MODDIR$TARGET_PATH"
    if ! mkdir -p "$MODDIR$TARGET_DIR"; then
        echo "Error: Could not create target directory."
        exit 1
    fi

    rm -f "$TARGET_FILE"
    if [ -z "$PRIMARY_APPLIED" ]; then
        if ! cp "$NEW_ZIP" "$TARGET_FILE"; then
            echo "Error: Could not copy boot animation."
            exit 1
        fi
        PRIMARY_APPLIED="$TARGET_FILE"
    else
        if ! ln "$PRIMARY_APPLIED" "$TARGET_FILE" 2>/dev/null; then
            if ! cp "$PRIMARY_APPLIED" "$TARGET_FILE"; then
                echo "Error: Could not create boot animation target."
                exit 1
            fi
        fi
    fi

    chmod 644 "$TARGET_FILE" 2>/dev/null
    chcon u:object_r:system_file:s0 "$TARGET_FILE" 2>/dev/null
    APPLIED=1
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

if [ "${BAS_SILENT_INJECT:-0}" != "1" ]; then
    cmd notification post -S bigtext -t "✨ Boot Creator" "tag" "Injection successful! Reboot to see your new animation!" >/dev/null 2>&1
fi

echo "Success! Directory paths unlocked, Magic Mount applied and sound enabled!"
exit 0
