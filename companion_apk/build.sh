#!/data/data/com.termux/files/usr/bin/bash
set -euo pipefail

BUILD_ONLY="${BAS_BUILD_ONLY:-0}"
ALLOW_NEW_KEY="${BAS_ALLOW_NEW_KEY:-0}"
CACHE_DIR="${BAS_COMPANION_CACHE_DIR:-$HOME/.cache/boot-animation-studio-companion}"
ANDROID_JAR="${BAS_ANDROID_JAR:-$CACHE_DIR/android-28.jar}"
KEYSTORE="${BAS_KEYSTORE:-$PWD/debug.keystore}"
BUILD_DIR="$PWD/.build"
CLASSES_DIR="$BUILD_DIR/classes"
DEX_DIR="$BUILD_DIR/dex"
UNSIGNED_APK="$BUILD_DIR/companion_unsigned.apk"
OUTPUT_APK="$PWD/companion.apk"

fail() {
    printf 'ERROR: %s\n' "$1" >&2
    exit 1
}

need() {
    command -v "$1" >/dev/null 2>&1 || fail "Missing required command: $1"
}

for cmd in bash curl unzip javac d8 aapt keytool apksigner mkdir rm cp; do
    need "$cmd"
done

[ -f AndroidManifest.xml ] || fail "AndroidManifest.xml is missing."
compgen -G 'src/com/bootcreator/companion/*.java' >/dev/null || fail "Companion Java sources are missing."

mkdir -p "$CACHE_DIR"
if [ ! -f "$ANDROID_JAR" ]; then
    printf 'Downloading Android 9 platform jar...\n'
    PLATFORM_ZIP="$CACHE_DIR/platform-28.zip"
    rm -f "$PLATFORM_ZIP"
    curl -fL --retry 3 --retry-delay 2 -o "$PLATFORM_ZIP" "https://dl.google.com/android/repository/platform-28_r06.zip"
    unzip -p "$PLATFORM_ZIP" "android-9/android.jar" > "$ANDROID_JAR"
    rm -f "$PLATFORM_ZIP"
fi
[ -s "$ANDROID_JAR" ] || fail "android.jar is missing or empty: $ANDROID_JAR"

if [ ! -f "$KEYSTORE" ]; then
    [ "$ALLOW_NEW_KEY" = "1" ] || fail "Signing key not found: $KEYSTORE. Set BAS_KEYSTORE or explicitly set BAS_ALLOW_NEW_KEY=1."
    mkdir -p "$(dirname "$KEYSTORE")"
    printf 'Creating a new companion signing key...\n'
    keytool -genkeypair -v -keystore "$KEYSTORE" -storepass android -alias androiddebugkey -keypass android -keyalg RSA -keysize 2048 -validity 10000 -dname "CN=Android Debug,O=Android,C=US"
    chmod 600 "$KEYSTORE"
fi

rm -rf "$BUILD_DIR" "$OUTPUT_APK"
mkdir -p "$CLASSES_DIR" "$DEX_DIR"

printf 'Compiling Java sources...\n'
javac -source 1.8 -target 1.8 -cp "$ANDROID_JAR" -d "$CLASSES_DIR" src/com/bootcreator/companion/*.java

printf 'Converting classes to DEX...\n'
d8 --lib "$ANDROID_JAR" --output "$DEX_DIR" "$CLASSES_DIR"/com/bootcreator/companion/*.class
[ -s "$DEX_DIR/classes.dex" ] || fail "classes.dex was not generated."

printf 'Packaging APK...\n'
aapt package -f -M AndroidManifest.xml -I "$ANDROID_JAR" -F "$UNSIGNED_APK"
(
    cd "$DEX_DIR"
    aapt add "$UNSIGNED_APK" classes.dex >/dev/null
)

printf 'Signing APK...\n'
apksigner sign --ks "$KEYSTORE" --ks-pass pass:android --key-pass pass:android --out "$OUTPUT_APK" "$UNSIGNED_APK"
apksigner verify "$OUTPUT_APK" >/dev/null
[ -s "$OUTPUT_APK" ] || fail "companion.apk was not generated."

if [ "$BUILD_ONLY" = "1" ]; then
    printf 'Companion APK built successfully (build-only mode).\n'
    exit 0
fi

if [ -d "../module_source" ]; then
    cp -f "$OUTPUT_APK" ../module_source/companion.apk
fi

for cmd in su pm appops; do
    need "$cmd"
done

printf 'Installing companion APK...\n'
su -c "cp '$OUTPUT_APK' /data/local/tmp/boot_creator_companion.apk && pm install -r /data/local/tmp/boot_creator_companion.apk && appops set com.bootcreator.companion SYSTEM_ALERT_WINDOW allow"
printf 'Companion APK installed successfully.\n'
