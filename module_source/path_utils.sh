#!/system/bin/sh

PATH_SCHEMA_VERSION=2
MAX_SAVED_PATHS=64

current_path_environment_key() {
    for PROP_NAME in ro.build.fingerprint ro.build.display.id ro.build.id; do
        PROP_VALUE="$(getprop "$PROP_NAME" 2>/dev/null)"
        if [ -n "$PROP_VALUE" ]; then
            printf '%s\n' "$PROP_VALUE"
            return 0
        fi
    done
    return 1
}

path_environment_status() {
    MARKER_FILE="$1"
    CURRENT_ENV="$(current_path_environment_key 2>/dev/null)" || {
        printf '%s\n' "unknown"
        return 0
    }
    [ -s "$MARKER_FILE" ] || {
        printf '%s\n' "unknown"
        return 0
    }
    SAVED_ENV="$(sed -n '1p' "$MARKER_FILE" 2>/dev/null)"
    [ -n "$SAVED_ENV" ] || {
        printf '%s\n' "unknown"
        return 0
    }
    if [ "$SAVED_ENV" = "$CURRENT_ENV" ]; then
        printf '%s\n' "current"
    else
        printf '%s\n' "changed"
    fi
}

write_path_environment_marker() {
    MARKER_FILE="$1"
    CURRENT_ENV="$(current_path_environment_key 2>/dev/null)" || return 1
    TMP_FILE="$MARKER_FILE.tmp.$$"
    if ! printf '%s\n' "$CURRENT_ENV" > "$TMP_FILE"; then
        rm -f "$TMP_FILE"
        return 1
    fi
    chmod 600 "$TMP_FILE" 2>/dev/null
    if ! mv -f "$TMP_FILE" "$MARKER_FILE"; then
        rm -f "$TMP_FILE"
        return 1
    fi
    chmod 600 "$MARKER_FILE" 2>/dev/null
    return 0
}

is_valid_bootanimation_path() {
    TARGET_PATH="$1"
    [ -n "$TARGET_PATH" ] || return 1
    [ "${#TARGET_PATH}" -le 512 ] || return 1

    case "$TARGET_PATH" in
        /*) ;;
        *) return 1 ;;
    esac

    case "$TARGET_PATH" in
        *\\*|*//*|*"/../"*|*/..|*"/./"*|*/.) return 1 ;;
    esac

    case "$TARGET_PATH" in
        */bootanimation.zip) ;;
        *) return 1 ;;
    esac

    case "$TARGET_PATH" in
        /system/*|/vendor/*|/product/*|/oem/*|/odm/*|/system_ext/*|/apex/*|/custom/*) return 0 ;;
    esac

    return 1
}

append_unique_bootanimation_path() {
    OUTPUT_FILE="$1"
    TARGET_PATH="$2"

    is_valid_bootanimation_path "$TARGET_PATH" || return 0
    if [ -f "$OUTPUT_FILE" ] && grep -Fqx "$TARGET_PATH" "$OUTPUT_FILE" 2>/dev/null; then
        return 0
    fi

    printf '%s\n' "$TARGET_PATH" >> "$OUTPUT_FILE"
}

saved_paths_are_valid() {
    INPUT_FILE="$1"
    [ -s "$INPUT_FILE" ] || return 1

    COUNT=0
    while IFS= read -r TARGET_PATH || [ -n "$TARGET_PATH" ]; do
        [ -n "$TARGET_PATH" ] || continue
        is_valid_bootanimation_path "$TARGET_PATH" || return 1
        COUNT=$((COUNT + 1))
        [ "$COUNT" -le "$MAX_SAVED_PATHS" ] || return 1
    done < "$INPUT_FILE"

    [ "$COUNT" -gt 0 ]
}

scan_bootanimation_paths() {
    OUTPUT_FILE="$1"
    : > "$OUTPUT_FILE" || return 1

    for TARGET_PATH in \
        /apex/com.android.bootanimation/etc/bootanimation.zip \
        /product/media/bootanimation.zip \
        /oem/media/bootanimation.zip \
        /system_ext/media/bootanimation.zip \
        /vendor/media/bootanimation.zip \
        /odm/media/bootanimation.zip \
        /custom/media/bootanimation.zip \
        /system/media/bootanimation.zip; do
        if [ -f "$TARGET_PATH" ]; then
            append_unique_bootanimation_path "$OUTPUT_FILE" "$TARGET_PATH"
        fi
    done

    for ROOT_DIR in /system /vendor /product /oem /odm /system_ext /apex /custom; do
        [ -d "$ROOT_DIR" ] || continue
        find "$ROOT_DIR" -maxdepth 4 -type f -name "bootanimation.zip" 2>/dev/null | while IFS= read -r TARGET_PATH; do
            append_unique_bootanimation_path "$OUTPUT_FILE" "$TARGET_PATH"
        done
    done

    if [ ! -s "$OUTPUT_FILE" ]; then
        append_unique_bootanimation_path "$OUTPUT_FILE" "/system/media/bootanimation.zip"
        append_unique_bootanimation_path "$OUTPUT_FILE" "/product/media/bootanimation.zip"
        append_unique_bootanimation_path "$OUTPUT_FILE" "/oem/media/bootanimation.zip"
        append_unique_bootanimation_path "$OUTPUT_FILE" "/system_ext/media/bootanimation.zip"
        append_unique_bootanimation_path "$OUTPUT_FILE" "/apex/com.android.bootanimation/etc/bootanimation.zip"
    fi

    saved_paths_are_valid "$OUTPUT_FILE"
}
