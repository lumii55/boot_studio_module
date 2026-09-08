#!/system/bin/sh

MODDIR=${0%/*}
CACHE_FILE="$MODDIR/saved_paths.txt"
NEW_ZIP="$1" 

if [ ! -f "$CACHE_FILE" ]; then
    echo "Error: Scan cache not found!"
    exit 1
fi

while read -r TARGET_PATH; do
    if [ -n "$TARGET_PATH" ]; then
        case "$TARGET_PATH" in
            /*) ;;
            *) TARGET_PATH="/$TARGET_PATH" ;;
        esac
        
        TARGET_DIR=$(dirname "$TARGET_PATH")
        mkdir -p "$MODDIR$TARGET_DIR"
        
        cp "$NEW_ZIP" "$MODDIR$TARGET_PATH"
        
        chcon u:object_r:system_file:s0 "$MODDIR$TARGET_PATH" 2>/dev/null
    fi
done < "$CACHE_FILE"

echo "persist.sys.bootanim.play_sound=1" > "$MODDIR/system.prop"
echo "ro.bootanim.set_volume=1" >> "$MODDIR/system.prop"
chmod 644 "$MODDIR/system.prop"

chown -R 0:0 "$MODDIR"

find "$MODDIR" -type d -exec chmod 755 {} \;
find "$MODDIR" -type f -exec chmod 644 {} \;

chmod 755 "$MODDIR/inject.sh"
chmod 755 "$MODDIR/clean.sh"
chmod 755 "$MODDIR/service.sh"

rm -f "$NEW_ZIP"

cmd notification post -S bigtext -t "✨ Boot Creator" "tag" "Injection successful! Reboot to see your new animation!"

echo "Success! Directory paths unlocked, Magic Mount applied and sound enabled!"
