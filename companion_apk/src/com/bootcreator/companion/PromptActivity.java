package com.bootcreator.companion;

import android.app.Activity;
import android.app.AlertDialog;
import android.content.DialogInterface;
import android.content.SharedPreferences;
import android.os.Bundle;
import android.view.View;
import android.widget.AdapterView;
import android.widget.ArrayAdapter;
import android.widget.CheckBox;
import android.widget.LinearLayout;
import android.widget.Spinner;
import android.widget.TextView;
import java.net.HttpURLConnection;
import java.net.URL;
import java.net.URLEncoder;
import java.util.regex.Pattern;

public class PromptActivity extends Activity {
    private static final Pattern NONCE_PATTERN = Pattern.compile("^[A-Za-z0-9_-]{32}$");
    private static final Pattern PAIR_TOKEN_PATTERN = Pattern.compile("^[A-Za-z0-9_-]{40,64}$");
    private static final String[] PERMISSION_VALUES = {"admin", "manage", "control", "view"};

    private String nonce;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);

        nonce = getIntent().getStringExtra("nonce");
        String pairToken = getIntent().getStringExtra("pair_token");
        boolean autoPair = getIntent().getBooleanExtra("auto_pair", false);
        boolean trustChoiceSupported = getIntent().getBooleanExtra("trust_choice_supported", false);
        boolean permissionChoiceSupported = getIntent().getBooleanExtra("permission_choice_supported", false);

        if (nonce == null || !NONCE_PATTERN.matcher(nonce).matches()) {
            finish();
            return;
        }

        if (autoPair) {
            sendCallback(consumeApprovedPair(pairToken), true, "admin");
            return;
        }

        String requesterIp = getIntent().getStringExtra("requester_ip");
        String requestOrigin = getIntent().getStringExtra("request_origin");
        String clientLabel = getIntent().getStringExtra("client_label");
        String clientId = getIntent().getStringExtra("client_id");

        if (requesterIp == null || requesterIp.isEmpty()) requesterIp = "Unknown";
        if (requestOrigin == null || requestOrigin.isEmpty()) requestOrigin = "Unknown";

        String message = L10n.get(this, "prompt_intro") + "\n\n"
                + L10n.get(this, "website") + ": " + requestOrigin
                + "\n" + L10n.get(this, "requester_ip") + ": " + requesterIp;

        boolean trustedClientRequest = clientLabel != null && !clientLabel.trim().isEmpty()
                && clientId != null && clientId.startsWith("bc_client_") && clientId.length() >= 18;
        if (trustedClientRequest) {
            message += "\n" + L10n.get(this, "client") + ": " + clientLabel.trim();
            message += "\n" + L10n.get(this, "client_id") + ": …" + clientId.substring(clientId.length() - 8);
            if (trustChoiceSupported) {
                message += "\n\n" + L10n.get(this, "session_only_note");
            } else {
                message += "\n\n" + L10n.get(this, "legacy_trust_note");
            }
        } else {
            message += "\n\n" + L10n.get(this, "legacy_session_note");
        }

        message += "\n\n" + L10n.get(this, "allow_question");

        final CheckBox alwaysTrust = new CheckBox(this);
        alwaysTrust.setText(L10n.get(this, "always_trust"));
        alwaysTrust.setChecked(false);

        final Spinner permissionSpinner = new Spinner(this);
        final TextView permissionDescription = new TextView(this);
        final LinearLayout optionsLayout = new LinearLayout(this);
        optionsLayout.setOrientation(LinearLayout.VERTICAL);
        int pad = (int) (20 * getResources().getDisplayMetrics().density);
        int gap = (int) (10 * getResources().getDisplayMetrics().density);
        optionsLayout.setPadding(pad, 0, pad, 0);

        if (trustedClientRequest && permissionChoiceSupported) {
            TextView permissionLabel = new TextView(this);
            permissionLabel.setText(L10n.get(this, "access_level"));
            permissionLabel.setPadding(0, 0, 0, gap / 2);
            optionsLayout.addView(permissionLabel);

            final String[] permissionLabels = {
                    L10n.get(this, "perm_admin"),
                    L10n.get(this, "perm_manage"),
                    L10n.get(this, "perm_control"),
                    L10n.get(this, "perm_view")
            };
            final String[] permissionDescriptions = {
                    L10n.get(this, "perm_admin_desc"),
                    L10n.get(this, "perm_manage_desc"),
                    L10n.get(this, "perm_control_desc"),
                    L10n.get(this, "perm_view_desc")
            };
            ArrayAdapter<String> permissionAdapter = new ArrayAdapter<>(this, android.R.layout.simple_spinner_item, permissionLabels);
            permissionAdapter.setDropDownViewResource(android.R.layout.simple_spinner_dropdown_item);
            permissionSpinner.setAdapter(permissionAdapter);
            permissionSpinner.setSelection(0);
            optionsLayout.addView(permissionSpinner);

            permissionDescription.setText(permissionDescriptions[0]);
            permissionDescription.setPadding(0, gap / 2, 0, gap);
            optionsLayout.addView(permissionDescription);
            permissionSpinner.setOnItemSelectedListener(new AdapterView.OnItemSelectedListener() {
                @Override
                public void onItemSelected(AdapterView<?> parent, View view, int position, long id) {
                    if (position >= 0 && position < permissionDescriptions.length) {
                        permissionDescription.setText(permissionDescriptions[position]);
                    }
                }

                @Override
                public void onNothingSelected(AdapterView<?> parent) {
                    permissionDescription.setText(permissionDescriptions[0]);
                }
            });
        }

        if (trustedClientRequest && trustChoiceSupported) {
            optionsLayout.addView(alwaysTrust);
        }

        AlertDialog.Builder builder = new AlertDialog.Builder(this)
                .setTitle(L10n.get(this, "prompt_title"))
                .setMessage(message)
                .setPositiveButton(L10n.get(this, "allow"), new DialogInterface.OnClickListener() {
                    public void onClick(DialogInterface dialog, int which) {
                        boolean persist = trustedClientRequest && (!trustChoiceSupported || alwaysTrust.isChecked());
                        String permission = "admin";
                        if (trustedClientRequest && permissionChoiceSupported) {
                            int position = permissionSpinner.getSelectedItemPosition();
                            if (position >= 0 && position < PERMISSION_VALUES.length) permission = PERMISSION_VALUES[position];
                        }
                        sendCallback(true, persist, permission);
                    }
                })
                .setNegativeButton(L10n.get(this, "deny"), new DialogInterface.OnClickListener() {
                    public void onClick(DialogInterface dialog, int which) {
                        sendCallback(false, false, "admin");
                    }
                })
                .setCancelable(false);

        if (trustedClientRequest && (trustChoiceSupported || permissionChoiceSupported)) builder.setView(optionsLayout);
        builder.show();
    }

    private boolean consumeApprovedPair(String pairToken) {
        if (pairToken == null || !PAIR_TOKEN_PATTERN.matcher(pairToken).matches()) return false;
        SharedPreferences prefs = getSharedPreferences("pairing", MODE_PRIVATE);
        String stored = prefs.getString("token", "");
        long expires = prefs.getLong("expires", 0L);
        boolean valid = pairToken.equals(stored) && System.currentTimeMillis() <= expires;
        prefs.edit().remove("token").remove("expires").apply();
        return valid;
    }

    private void sendCallback(final boolean allowed, final boolean persist, final String permission) {
        new Thread(new Runnable() {
            public void run() {
                HttpURLConnection connection = null;
                try {
                    String encodedNonce = URLEncoder.encode(nonce, "UTF-8");
                    String encodedPermission = URLEncoder.encode(permission == null ? "admin" : permission, "UTF-8");
                    URL url = new URL("http://127.0.0.1:4040/auth_callback?allow=" + allowed + "&persist=" + persist + "&permission=" + encodedPermission + "&nonce=" + encodedNonce);
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
