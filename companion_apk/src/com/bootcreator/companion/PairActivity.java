package com.bootcreator.companion;

import android.app.Activity;
import android.app.AlertDialog;
import android.content.DialogInterface;
import android.content.SharedPreferences;
import android.net.Uri;
import android.os.Bundle;
import android.widget.Toast;
import java.net.HttpURLConnection;
import java.net.URL;
import java.net.URLEncoder;
import java.util.regex.Pattern;

public class PairActivity extends Activity {
    private static final Pattern TOKEN_PATTERN = Pattern.compile("^[A-Za-z0-9_-]{40,64}$");
    private String token;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        Uri data = getIntent().getData();
        boolean validLink = data != null
                && "bootstudio".equals(data.getScheme())
                && "pair".equals(data.getHost())
                && (data.getPath() == null || data.getPath().isEmpty() || "/".equals(data.getPath()));
        token = validLink ? data.getQueryParameter("token") : null;
        if (token == null || !TOKEN_PATTERN.matcher(token).matches()) {
            Toast.makeText(this, L10n.get(this, "pair_invalid"), Toast.LENGTH_LONG).show();
            finish();
            return;
        }

        String code = token.substring(0, 3).toUpperCase() + "-" + token.substring(token.length() - 3).toUpperCase();
        String message = L10n.get(this, "pair_message", code);

        new AlertDialog.Builder(this)
                .setTitle(L10n.get(this, "pair_title"))
                .setMessage(message)
                .setPositiveButton(L10n.get(this, "pair"), new DialogInterface.OnClickListener() {
                    public void onClick(DialogInterface dialog, int which) {
                        approvePairing();
                    }
                })
                .setNegativeButton(L10n.get(this, "cancel"), new DialogInterface.OnClickListener() {
                    public void onClick(DialogInterface dialog, int which) {
                        finish();
                    }
                })
                .setCancelable(false)
                .show();
    }

    private void approvePairing() {
        SharedPreferences prefs = getSharedPreferences("pairing", MODE_PRIVATE);
        prefs.edit()
                .putString("token", token)
                .putLong("expires", System.currentTimeMillis() + 120000L)
                .apply();

        new Thread(new Runnable() {
            public void run() {
                HttpURLConnection connection = null;
                boolean ok = false;
                try {
                    String encodedToken = URLEncoder.encode(token, "UTF-8");
                    URL url = new URL("http://127.0.0.1:4040/pair_register?token=" + encodedToken);
                    connection = (HttpURLConnection) url.openConnection();
                    connection.setRequestMethod("POST");
                    connection.setConnectTimeout(5000);
                    connection.setReadTimeout(5000);
                    connection.setDoOutput(true);
                    connection.getOutputStream().close();
                    int status = connection.getResponseCode();
                    ok = status >= 200 && status < 300;
                } catch (Exception ignored) {
                } finally {
                    if (connection != null) connection.disconnect();
                    final boolean result = ok;
                    runOnUiThread(new Runnable() {
                        public void run() {
                            if (!result) {
                                getSharedPreferences("pairing", MODE_PRIVATE).edit().clear().apply();
                                Toast.makeText(PairActivity.this, L10n.get(PairActivity.this, "pair_server_error"), Toast.LENGTH_LONG).show();
                            }
                            finish();
                        }
                    });
                }
            }
        }).start();
    }

}