#!/system/bin/sh

MODDIR=${0%/*}
CACHE_FILE="$MODDIR/saved_paths.txt"

. "$MODDIR/path_utils.sh" || exit 1

if ! saved_paths_are_valid "$CACHE_FILE"; then
    echo "Error: Nothing safe to clean!"
    exit 1
fi

while IFS= read -r TARGET_PATH || [ -n "$TARGET_PATH" ]; do
    [ -n "$TARGET_PATH" ] || continue
    if ! is_valid_bootanimation_path "$TARGET_PATH"; then
        echo "Error: Invalid target path in scan cache."
        exit 1
    fi
    rm -f "$MODDIR$TARGET_PATH"
done < "$CACHE_FILE"

rm -f "$MODDIR/system.prop"

cmd notification post -t "✨ Boot Creator" "tag" "Animation removed! 🧹 Reboot to restore original!"

echo "Cleanup complete!"
