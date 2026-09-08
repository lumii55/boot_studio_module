#!/system/bin/sh

MODDIR=${0%/*}
CACHE_FILE="$MODDIR/saved_paths.txt"

if [ ! -f "$CACHE_FILE" ]; then
    echo "Error: Nothing to clean!"
    exit 1
fi

while read -r TARGET_PATH; do
    if [ -n "$TARGET_PATH" ]; then
        case "$TARGET_PATH" in
            /*) ;;
            *) TARGET_PATH="/$TARGET_PATH" ;;
        esac
        
        rm -f "$MODDIR$TARGET_PATH"
    fi
done < "$CACHE_FILE"

rm -f "$MODDIR/system.prop"

cmd notification post -t "✨ Boot Creator" "tag" "Animation removed! 🧹 Reboot to restore original!"

echo "Cleanup complete!"
