#!/system/bin/sh

MODDIR=${0%/*}
CACHE_FILE="$MODDIR/saved_paths.txt"
SCHEMA_FILE="$MODDIR/.paths_schema"
ENV_FILE="$MODDIR/.paths_environment"
OLD_LIST="$MODDIR/.paths-old.$$"
NEW_LIST="$MODDIR/.paths-new.$$"
CURRENT_ZIP="$MODDIR/.paths-current.$$.zip"

. "$MODDIR/path_utils.sh" || exit 1

cleanup() {
    rm -f "$OLD_LIST" "$NEW_LIST" "$CURRENT_ZIP" "$CACHE_FILE.tmp.$$"
}
trap cleanup EXIT HUP INT TERM

if saved_paths_are_valid "$CACHE_FILE"; then
    cp -f "$CACHE_FILE" "$OLD_LIST" || exit 1

    while IFS= read -r TARGET_PATH || [ -n "$TARGET_PATH" ]; do
        [ -n "$TARGET_PATH" ] || continue
        if is_valid_bootanimation_path "$TARGET_PATH" && [ -f "$MODDIR$TARGET_PATH" ]; then
            cp -f "$MODDIR$TARGET_PATH" "$CURRENT_ZIP" || exit 1
            break
        fi
    done < "$OLD_LIST"
fi

scan_bootanimation_paths "$NEW_LIST" || exit 1

if [ -s "$OLD_LIST" ]; then
    while IFS= read -r TARGET_PATH || [ -n "$TARGET_PATH" ]; do
        [ -n "$TARGET_PATH" ] || continue
        is_valid_bootanimation_path "$TARGET_PATH" || continue
        if ! grep -Fqx "$TARGET_PATH" "$NEW_LIST" 2>/dev/null; then
            rm -f "$MODDIR$TARGET_PATH"
        fi
    done < "$OLD_LIST"
fi

cp -f "$NEW_LIST" "$CACHE_FILE.tmp.$$" || exit 1
chmod 600 "$CACHE_FILE.tmp.$$" 2>/dev/null
mv -f "$CACHE_FILE.tmp.$$" "$CACHE_FILE" || exit 1
chmod 600 "$CACHE_FILE" 2>/dev/null

if [ -f "$CURRENT_ZIP" ]; then
    PRIMARY_APPLIED=""
    while IFS= read -r TARGET_PATH || [ -n "$TARGET_PATH" ]; do
        [ -n "$TARGET_PATH" ] || continue
        is_valid_bootanimation_path "$TARGET_PATH" || exit 1
        TARGET_DIR=$(dirname "$TARGET_PATH")
        TARGET_FILE="$MODDIR$TARGET_PATH"
        mkdir -p "$MODDIR$TARGET_DIR" || exit 1
        rm -f "$TARGET_FILE"
        if [ -z "$PRIMARY_APPLIED" ]; then
            cp -f "$CURRENT_ZIP" "$TARGET_FILE" || exit 1
            PRIMARY_APPLIED="$TARGET_FILE"
        elif ! ln "$PRIMARY_APPLIED" "$TARGET_FILE" 2>/dev/null; then
            cp -f "$PRIMARY_APPLIED" "$TARGET_FILE" || exit 1
        fi
        chmod 644 "$TARGET_FILE" 2>/dev/null
        chcon u:object_r:system_file:s0 "$TARGET_FILE" 2>/dev/null
    done < "$CACHE_FILE"
fi

printf '%s\n' "$PATH_SCHEMA_VERSION" > "$SCHEMA_FILE" || exit 1
chmod 600 "$SCHEMA_FILE" 2>/dev/null
write_path_environment_marker "$ENV_FILE" || exit 1
exit 0
