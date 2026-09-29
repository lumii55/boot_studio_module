#!/system/bin/sh

OLDMOD="/data/adb/modules/boot_creator"
PRESERVED=0

. "$MODPATH/path_utils.sh" || abort "- Could not load module path validation"

preserve_file() {
    SOURCE="$1"
    DESTINATION="$2"
    if [ -f "$SOURCE" ]; then
        mkdir -p "$(dirname "$DESTINATION")"
        if cp -af "$SOURCE" "$DESTINATION"; then
            PRESERVED=1
        fi
    fi
}

preserve_dir() {
    SOURCE="$1"
    DESTINATION="$2"
    if [ -d "$SOURCE" ]; then
        mkdir -p "$DESTINATION"
        if cp -af "$SOURCE"/. "$DESTINATION"/; then
            PRESERVED=1
        fi
    fi
}

preserve_file_required() {
    SOURCE="$1"
    DESTINATION="$2"
    LABEL="$3"
    [ -f "$SOURCE" ] || return 0

    mkdir -p "$(dirname "$DESTINATION")" || abort "- Could not prepare $LABEL preservation"
    if ! cp -af "$SOURCE" "$DESTINATION"; then
        abort "- Could not preserve $LABEL; existing installation was left unchanged"
    fi
    PRESERVED=1
}

preserve_dir_required() {
    SOURCE="$1"
    DESTINATION="$2"
    LABEL="$3"
    [ -d "$SOURCE" ] || return 0

    mkdir -p "$DESTINATION" || abort "- Could not prepare $LABEL preservation"
    if ! cp -af "$SOURCE"/. "$DESTINATION"/; then
        abort "- Could not preserve $LABEL; existing installation was left unchanged"
    fi
    PRESERVED=1
}

if [ -d "$OLDMOD" ] && [ "$OLDMOD" != "$MODPATH" ]; then
    ui_print "- Preserving existing Boot Animation Studio data"

    preserve_file_required "$OLDMOD/bridge_id" "$MODPATH/bridge_id" "bridge identity"
    preserve_dir_required "$OLDMOD/trust" "$MODPATH/trust" "trusted-client state"

    preserve_file "$OLDMOD/saved_paths.txt" "$MODPATH/saved_paths.txt"
    preserve_file "$OLDMOD/.paths_schema" "$MODPATH/.paths_schema"
    preserve_file "$OLDMOD/.paths_environment" "$MODPATH/.paths_environment"
    preserve_file "$OLDMOD/system.prop" "$MODPATH/system.prop"
    preserve_file "$OLDMOD/boot_creator.log" "$MODPATH/boot_creator.log"
    preserve_file "$OLDMOD/boot_creator.previous.log" "$MODPATH/boot_creator.previous.log"
    preserve_dir "$OLDMOD/history" "$MODPATH/history"
    preserve_dir "$OLDMOD/playlist" "$MODPATH/playlist"
    preserve_dir "$OLDMOD/backup" "$MODPATH/backup"

    if saved_paths_are_valid "$OLDMOD/saved_paths.txt"; then
        while IFS= read -r TARGET_PATH || [ -n "$TARGET_PATH" ]; do
            [ -n "$TARGET_PATH" ] || continue
            is_valid_bootanimation_path "$TARGET_PATH" || continue

            SOURCE="$OLDMOD$TARGET_PATH"
            DESTINATION="$MODPATH$TARGET_PATH"

            if [ -f "$SOURCE" ]; then
                mkdir -p "$(dirname "$DESTINATION")"
                if cp -af "$SOURCE" "$DESTINATION"; then
                    PRESERVED=1
                fi
            fi
        done < "$OLDMOD/saved_paths.txt"
    fi

    if [ "$PRESERVED" -eq 1 ]; then
        ui_print "- Existing animations and module data preserved"
    else
        ui_print "- No existing persistent data found"
    fi
else
    ui_print "- Fresh installation detected"
fi
