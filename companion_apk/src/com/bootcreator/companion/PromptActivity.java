package com.bootcreator.companion;

import android.app.Activity;
import android.app.AlertDialog;
import android.content.DialogInterface;
import android.os.Bundle;
import java.net.HttpURLConnection;
import java.net.URL;

public class PromptActivity extends Activity {
    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        new AlertDialog.Builder(this)
            .setTitle("✨ Boot Creator Web")
            .setMessage("A website is requesting permission to manage your Boot Creator module.\n\nAllowing this grants the site access to apply, edit, or remove your custom boot animations.\n\nDo you want to allow this connection?")
            .setPositiveButton("ALLOW", new DialogInterface.OnClickListener() {
                public void onClick(DialogInterface dialog, int which) { sendCallback(true); }
            })
            .setNegativeButton("DENY", new DialogInterface.OnClickListener() {
                public void onClick(DialogInterface dialog, int which) { sendCallback(false); }
            })
            .setCancelable(false)
            .show();
    }

    private void sendCallback(final boolean allowed) {
        new Thread(new Runnable() {
            public void run() {
                try {
                    URL url = new URL("http://127.0.0.1:4040/auth_callback?allow=" + allowed);
                    HttpURLConnection conn = (HttpURLConnection) url.openConnection();
                    conn.setRequestMethod("GET");
                    conn.getResponseCode();
                } catch (Exception e) { e.printStackTrace(); }
                finally { finish(); }
            }
        }).start();
    }
}
