package com.bootcreator.companion;

import android.app.Activity;
import android.app.AlertDialog;
import android.content.DialogInterface;
import android.content.SharedPreferences;
import android.os.Bundle;
import java.net.HttpURLConnection;
import java.net.URL;
import java.net.URLEncoder;

public class PromptActivity extends Activity {
    private String nonce;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);

        nonce = getIntent().getStringExtra("nonce");
        String pairToken = getIntent().getStringExtra("pair_token");
        boolean autoPair = getIntent().getBooleanExtra("auto_pair", false);

        if (nonce == null || nonce.isEmpty()) {
            finish();
            return;
        }

        if (autoPair) {
            sendCallback(consumeApprovedPair(pairToken));
            return;
        }

        String requesterIp = getIntent().getStringExtra("requester_ip");
        String requestOrigin = getIntent().getStringExtra("request_origin");

        if (requesterIp == null || requesterIp.isEmpty()) requesterIp = "Unknown";
        if (requestOrigin == null || requestOrigin.isEmpty()) requestOrigin = "Unknown";

        String message = "A website is requesting permission to manage your Boot Animation Studio module.\n\nWebsite: "
                + requestOrigin
                + "\nRequester IP: "
                + requesterIp
                + "\n\nAllowing this grants the site access to apply, edit, test, or remove your custom boot animations.\n\nDo you want to allow this connection?";

        new AlertDialog.Builder(this)
                .setTitle("Boot Animation Studio")
                .setMessage(message)
                .setPositiveButton("ALLOW", new DialogInterface.OnClickListener() {
                    public void onClick(DialogInterface dialog, int which) {
                        sendCallback(true);
                    }
                })
                .setNegativeButton("DENY", new DialogInterface.OnClickListener() {
                    public void onClick(DialogInterface dialog, int which) {
                        sendCallback(false);
                    }
                })
                .setCancelable(false)
                .show();
    }

    private boolean consumeApprovedPair(String pairToken) {
        if (pairToken == null || pairToken.isEmpty()) return false;
        SharedPreferences prefs = getSharedPreferences("pairing", MODE_PRIVATE);
        String stored = prefs.getString("token", "");
        long expires = prefs.getLong("expires", 0L);
        boolean valid = pairToken.equals(stored) && System.currentTimeMillis() <= expires;
        prefs.edit().remove("token").remove("expires").apply();
        return valid;
    }

    private void sendCallback(final boolean allowed) {
        new Thread(new Runnable() {
            public void run() {
                HttpURLConnection connection = null;
                try {
                    String encodedNonce = URLEncoder.encode(nonce, "UTF-8");
                    URL url = new URL("http://127.0.0.1:4040/auth_callback?allow=" + allowed + "&nonce=" + encodedNonce);
                    connection = (HttpURLConnection) url.openConnection();
                    connection.setRequestMethod("POST");
                    connection.setConnectTimeout(5000);
                    connection.setReadTimeout(5000);
                    connection.setDoOutput(true);
                    connection.getOutputStream().close();
                    connection.getResponseCode();
                } catch (Exception ignored) {
                } finally {
                    if (connection != null) connection.disconnect();
                    runOnUiThread(new Runnable() {
                        public void run() {
                            finish();
                        }
                    });
                }
            }
        }).start();
    }
}
