#!/system/bin/sh

OLDMOD="/data/adb/modules/boot_creator"
PRESERVED=0

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

if [ -d "$OLDMOD" ] && [ "$OLDMOD" != "$MODPATH" ]; then
    ui_print "- Preserving existing Boot Animation Studio data"

    preserve_file "$OLDMOD/saved_paths.txt" "$MODPATH/saved_paths.txt"
    preserve_file "$OLDMOD/system.prop" "$MODPATH/system.prop"
    preserve_file "$OLDMOD/boot_creator.log" "$MODPATH/boot_creator.log"
    preserve_file "$OLDMOD/boot_creator.previous.log" "$MODPATH/boot_creator.previous.log"
    preserve_dir "$OLDMOD/history" "$MODPATH/history"
    preserve_dir "$OLDMOD/backup" "$MODPATH/backup"

    if [ -f "$OLDMOD/saved_paths.txt" ]; then
        while IFS= read -r TARGET_PATH || [ -n "$TARGET_PATH" ]; do
            case "$TARGET_PATH" in
                /*/bootanimation.zip)
                    case "$TARGET_PATH" in
                        *"/../"*|*/..|*"/./"*|*/.)
                            continue
                            ;;
                    esac

                    SOURCE="$OLDMOD$TARGET_PATH"
                    DESTINATION="$MODPATH$TARGET_PATH"

                    if [ -f "$SOURCE" ]; then
                        mkdir -p "$(dirname "$DESTINATION")"
                        if cp -af "$SOURCE" "$DESTINATION"; then
                            PRESERVED=1
                        fi
                    fi
                    ;;
            esac
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
