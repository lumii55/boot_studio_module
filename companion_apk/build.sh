#!/data/data/com.termux/files/usr/bin/bash
set -e

echo "📜 Atualizando o Manifesto com o Ouvinte e Permissão Flutuante... :3"
cat << 'XML_EOF' > AndroidManifest.xml
<?xml version="1.0" encoding="utf-8"?>
<manifest xmlns:android="http://schemas.android.com/apk/res/android"
    package="com.bootcreator.companion"
    android:versionCode="3"
    android:versionName="1.2">
    
    <uses-sdk android:minSdkVersion="24" android:targetSdkVersion="28" />
    <uses-permission android:name="android.permission.INTERNET" />
    
    <!-- ✨ A permissão pra desenhar o Toast Fofo em qualquer lugar! ✨ -->
    <uses-permission android:name="android.permission.SYSTEM_ALERT_WINDOW" />
    
    <application android:usesCleartextTraffic="true" android:label="Boot Creator" android:theme="@android:style/Theme.DeviceDefault.Light.Dialog.NoActionBar">
        <activity android:name="com.bootcreator.companion.PromptActivity"
            android:exported="true" android:excludeFromRecents="true">
            <intent-filter>
                <action android:name="android.intent.action.MAIN" />
            </intent-filter>
        </activity>

        <receiver android:name="com.bootcreator.companion.ToastReceiver" android:exported="true">
            <intent-filter>
                <action android:name="com.bootcreator.SHOW_TOAST" />
            </intent-filter>
        </receiver>
    </application>
</manifest>
XML_EOF

if [ ! -f "android.jar" ]; then
    echo "📥 Baixando android.jar..."
    curl -L -o platform.zip "https://dl.google.com/android/repository/platform-28_r06.zip"
    unzip -j platform.zip "android-9/android.jar" -d .
    rm platform.zip
fi

echo "☕ 1. Compilando o código Java com javac..."
javac -source 1.8 -target 1.8 -cp android.jar -d . src/com/bootcreator/companion/*.java

echo "⚡ 2. Convertendo classes para DEX..."
d8 --lib android.jar --output . com/bootcreator/companion/*.class

echo "📦 3. Empacotando APK com AAPT..."
aapt package -f -M AndroidManifest.xml -I android.jar -F companion_unsigned.apk
aapt add companion_unsigned.apk classes.dex > /dev/null

echo "🔑 4. Criando chave e Assinando o APK..."
if [ ! -f "debug.keystore" ]; then
    keytool -genkey -v -keystore debug.keystore -storepass android -alias androiddebugkey -keypass android -keyalg RSA -keysize 2048 -validity 10000 -dname "CN=Android Debug,O=Android,C=US"
fi
apksigner sign --ks debug.keystore --ks-pass pass:android --out companion.apk companion_unsigned.apk

echo "🎉 SUCESSO! Instalando no sistema e dando a permissão suprema..."
# 🔥 Instalamos E já damos a permissão de "Sobrepor outros apps" direto pelo root!
su -c "cp $PWD/companion.apk /data/local/tmp/ && pm install -r /data/local/tmp/companion.apk && appops set com.bootcreator.companion SYSTEM_ALERT_WINDOW allow"
echo "✨ APK Atualizado com SUCESSO! owo"
