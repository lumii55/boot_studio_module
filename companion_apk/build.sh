#!/data/data/com.termux/files/usr/bin/bash
set -e

echo "📜 Updating AndroidManifest.xml..."
cat << 'XML_EOF' > AndroidManifest.xml
<?xml version="1.0" encoding="utf-8"?>
<manifest xmlns:android="http://schemas.android.com/apk/res/android"
    package="com.bootcreator.companion"
    android:versionCode="6"
    android:versionName="1.5">

    <uses-sdk android:minSdkVersion="24" android:targetSdkVersion="28" />
    <uses-permission android:name="android.permission.INTERNET" />
    <uses-permission android:name="android.permission.SYSTEM_ALERT_WINDOW" />

    <application android:usesCleartextTraffic="true" android:label="Boot Animation Studio Module" android:theme="@android:style/Theme.DeviceDefault.Light.Dialog.NoActionBar">
        <activity android:name="com.bootcreator.companion.PromptActivity"
            android:exported="true" android:excludeFromRecents="true" android:noHistory="true" android:permission="android.permission.DUMP">
            <intent-filter>
                <action android:name="android.intent.action.MAIN" />
            </intent-filter>
        </activity>


        <activity android:name="com.bootcreator.companion.PairActivity"
            android:exported="true" android:excludeFromRecents="true" android:noHistory="true">
            <intent-filter>
                <action android:name="android.intent.action.VIEW" />
                <category android:name="android.intent.category.DEFAULT" />
                <category android:name="android.intent.category.BROWSABLE" />
                <data android:scheme="bootstudio" android:host="pair" />
            </intent-filter>
        </activity>

        <receiver android:name="com.bootcreator.companion.ToastReceiver" android:exported="true" android:permission="android.permission.DUMP">
            <intent-filter>
                <action android:name="com.bootcreator.SHOW_TOAST" />
            </intent-filter>
        </receiver>
    </application>
</manifest>
XML_EOF

if [ ! -f "android.jar" ]; then
    echo "📥 Downloading android.jar..."
    curl -L -o platform.zip "https://dl.google.com/android/repository/platform-28_r06.zip"
    unzip -j platform.zip "android-9/android.jar" -d .
    rm platform.zip
fi

echo "☕ Compiling Java sources..."
javac -source 1.8 -target 1.8 -cp android.jar -d . src/com/bootcreator/companion/*.java

echo "⚡ Converting classes to DEX..."
d8 --lib android.jar --output . com/bootcreator/companion/*.class

echo "📦 Packaging APK..."
aapt package -f -M AndroidManifest.xml -I android.jar -F companion_unsigned.apk
aapt add companion_unsigned.apk classes.dex > /dev/null

echo "🔑 Signing APK..."
if [ ! -f "debug.keystore" ]; then
    keytool -genkey -v -keystore debug.keystore -storepass android -alias androiddebugkey -keypass android -keyalg RSA -keysize 2048 -validity 10000 -dname "CN=Android Debug,O=Android,C=US"
fi
apksigner sign --ks debug.keystore --ks-pass pass:android --out companion.apk companion_unsigned.apk

if [ -d "../module_source" ]; then
    cp -f companion.apk ../module_source/companion.apk
    echo "📦 Companion APK copied to module_source."
fi

echo "📲 Installing companion APK..."
su -c "cp '$PWD/companion.apk' /data/local/tmp/boot_creator_companion.apk && pm install -r /data/local/tmp/boot_creator_companion.apk && appops set com.bootcreator.companion SYSTEM_ALERT_WINDOW allow"
echo "✅ Companion APK installed successfully."
