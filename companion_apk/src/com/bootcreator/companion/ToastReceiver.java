package com.bootcreator.companion;

import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;
import android.graphics.Color;
import android.graphics.PixelFormat;
import android.graphics.Typeface;
import android.graphics.drawable.GradientDrawable;
import android.os.Build;
import android.os.Handler;
import android.os.Looper;
import android.provider.Settings;
import android.view.Gravity;
import android.view.View;
import android.view.WindowManager;
import android.view.animation.DecelerateInterpolator;
import android.widget.LinearLayout;
import android.widget.TextView;
import android.widget.Toast;

public class ToastReceiver extends BroadcastReceiver {
    private static final Handler HANDLER = new Handler(Looper.getMainLooper());
    private static View currentView;
    private static WindowManager windowManager;
    private static Runnable dismissRunnable;

    @Override
    public void onReceive(Context context, Intent intent) {
        if (intent == null || !intent.hasExtra("msg")) {
            return;
        }

        String rawMessage = intent.getStringExtra("msg");
        if (rawMessage == null || rawMessage.trim().isEmpty()) {
            return;
        }

        Context appContext = context.getApplicationContext();
        String message = cleanMessage(rawMessage);

        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M && !Settings.canDrawOverlays(appContext)) {
            showFallback(appContext, message);
            return;
        }

        if (windowManager == null) {
            windowManager = (WindowManager) appContext.getSystemService(Context.WINDOW_SERVICE);
        }
        if (windowManager == null) {
            showFallback(appContext, message);
            return;
        }

        dismissCurrent(false);

        VisualStyle style = resolveStyle(rawMessage);
        LinearLayout card = createCard(appContext, message, style);
        int windowType = Build.VERSION.SDK_INT >= Build.VERSION_CODES.O
                ? WindowManager.LayoutParams.TYPE_APPLICATION_OVERLAY
                : WindowManager.LayoutParams.TYPE_SYSTEM_ALERT;

        WindowManager.LayoutParams params = new WindowManager.LayoutParams(
                WindowManager.LayoutParams.WRAP_CONTENT,
                WindowManager.LayoutParams.WRAP_CONTENT,
                windowType,
                WindowManager.LayoutParams.FLAG_NOT_FOCUSABLE
                        | WindowManager.LayoutParams.FLAG_NOT_TOUCH_MODAL,
                PixelFormat.TRANSLUCENT
        );
        params.gravity = Gravity.TOP | Gravity.CENTER_HORIZONTAL;
        params.y = dp(appContext, 18);

        try {
            windowManager.addView(card, params);
            currentView = card;
            card.setAlpha(0f);
            card.setTranslationY(-dp(appContext, 24));
            card.animate()
                    .alpha(1f)
                    .translationY(0f)
                    .setDuration(220)
                    .setInterpolator(new DecelerateInterpolator())
                    .start();
            card.setOnClickListener(new View.OnClickListener() {
                @Override
                public void onClick(View view) {
                    dismissCurrent(true);
                }
            });
            card.announceForAccessibility("Boot Animation Studio Module. " + message);
            scheduleDismiss();
        } catch (Exception e) {
            currentView = null;
            showFallback(appContext, message);
        }
    }

    private static LinearLayout createCard(Context context, String message, VisualStyle style) {
        LinearLayout card = new LinearLayout(context);
        card.setOrientation(LinearLayout.HORIZONTAL);
        card.setGravity(Gravity.CENTER_VERTICAL);
        card.setPadding(dp(context, 14), dp(context, 13), dp(context, 17), dp(context, 13));
        card.setElevation(dp(context, 12));
        card.setContentDescription("Boot Animation Studio Module: " + message);

        GradientDrawable cardBackground = new GradientDrawable();
        cardBackground.setColor(Color.rgb(27, 25, 36));
        cardBackground.setCornerRadius(dp(context, 20));
        cardBackground.setStroke(dp(context, 1), withAlpha(style.color, 110));
        card.setBackground(cardBackground);

        TextView icon = new TextView(context);
        icon.setText(style.symbol);
        icon.setTextColor(Color.WHITE);
        icon.setTextSize(18f);
        icon.setTypeface(Typeface.DEFAULT_BOLD);
        icon.setGravity(Gravity.CENTER);
        GradientDrawable iconBackground = new GradientDrawable();
        iconBackground.setShape(GradientDrawable.OVAL);
        iconBackground.setColor(style.color);
        icon.setBackground(iconBackground);
        LinearLayout.LayoutParams iconParams = new LinearLayout.LayoutParams(dp(context, 34), dp(context, 34));
        iconParams.setMarginEnd(dp(context, 12));
        card.addView(icon, iconParams);

        LinearLayout textColumn = new LinearLayout(context);
        textColumn.setOrientation(LinearLayout.VERTICAL);

        LinearLayout header = new LinearLayout(context);
        header.setOrientation(LinearLayout.HORIZONTAL);
        header.setGravity(Gravity.CENTER_VERTICAL);

        TextView title = new TextView(context);
        title.setText("Boot Animation Studio");
        title.setTextColor(Color.rgb(244, 240, 255));
        title.setTextSize(12.5f);
        title.setTypeface(Typeface.DEFAULT_BOLD);
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.LOLLIPOP) {
            title.setLetterSpacing(0.02f);
        }
        header.addView(title, new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.WRAP_CONTENT,
                LinearLayout.LayoutParams.WRAP_CONTENT
        ));

        TextView badge = new TextView(context);
        badge.setText("ROOT");
        badge.setTextSize(8.5f);
        badge.setTextColor(Color.rgb(190, 169, 255));
        badge.setTypeface(Typeface.DEFAULT_BOLD);
        badge.setGravity(Gravity.CENTER);
        badge.setPadding(dp(context, 6), dp(context, 2), dp(context, 6), dp(context, 2));
        GradientDrawable badgeBackground = new GradientDrawable();
        badgeBackground.setColor(Color.rgb(47, 40, 65));
        badgeBackground.setCornerRadius(dp(context, 8));
        badgeBackground.setStroke(dp(context, 1), Color.rgb(105, 85, 145));
        badge.setBackground(badgeBackground);
        LinearLayout.LayoutParams badgeParams = new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.WRAP_CONTENT,
                LinearLayout.LayoutParams.WRAP_CONTENT
        );
        badgeParams.setMarginStart(dp(context, 8));
        header.addView(badge, badgeParams);
        textColumn.addView(header);

        TextView body = new TextView(context);
        body.setText(message);
        body.setTextColor(Color.rgb(220, 215, 231));
        body.setTextSize(13.5f);
        body.setMaxLines(3);
        body.setMaxWidth(Math.max(dp(context, 190), Math.min(dp(context, 300), context.getResources().getDisplayMetrics().widthPixels - dp(context, 110))));
        body.setPadding(0, dp(context, 3), 0, 0);
        textColumn.addView(body);

        card.addView(textColumn, new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.WRAP_CONTENT,
                LinearLayout.LayoutParams.WRAP_CONTENT
        ));
        return card;
    }

    private static void scheduleDismiss() {
        if (dismissRunnable != null) {
            HANDLER.removeCallbacks(dismissRunnable);
        }
        dismissRunnable = new Runnable() {
            @Override
            public void run() {
                dismissCurrent(true);
            }
        };
        HANDLER.postDelayed(dismissRunnable, 3200);
    }

    private static void dismissCurrent(boolean animated) {
        if (dismissRunnable != null) {
            HANDLER.removeCallbacks(dismissRunnable);
            dismissRunnable = null;
        }
        final View view = currentView;
        currentView = null;
        if (view == null || windowManager == null) {
            return;
        }

        if (!animated) {
            removeView(view);
            return;
        }

        view.animate()
                .alpha(0f)
                .translationY(-Math.max(12f, view.getHeight() * 0.2f))
                .setDuration(180)
                .withEndAction(new Runnable() {
                    @Override
                    public void run() {
                        removeView(view);
                    }
                })
                .start();
    }

    private static void removeView(View view) {
        try {
            if (windowManager != null) {
                windowManager.removeView(view);
            }
        } catch (Exception ignored) {
        }
    }

    private static void showFallback(Context context, String message) {
        Toast.makeText(context, "Boot Animation Studio Module\n" + message, Toast.LENGTH_LONG).show();
    }

    private static VisualStyle resolveStyle(String message) {
        if (message.contains("❌")) {
            return new VisualStyle(Color.rgb(255, 107, 129), "×");
        }
        if (message.contains("⚠")) {
            return new VisualStyle(Color.rgb(255, 194, 87), "!");
        }
        if (message.contains("🚀")
                || message.contains("✨")
                || message.contains("📥")
                || message.contains("⏳")
                || message.contains("🏁")
                || message.contains("🗑")) {
            return new VisualStyle(Color.rgb(94, 215, 161), "✓");
        }
        return new VisualStyle(Color.rgb(154, 134, 253), "i");
    }

    private static String cleanMessage(String message) {
        String result = message.trim()
                .replace("Boot Creator:", "")
                .replace("Boot Animation Studio:", "")
                .trim();
        String[] prefixes = {
                "❌", "✨", "🔌", "🧹", "🚀", "🗑️", "🗑", "📥",
                "⚠️", "⚠", "⏳", "👀", "🏁"
        };
        for (String prefix : prefixes) {
            if (result.startsWith(prefix)) {
                result = result.substring(prefix.length()).trim();
                break;
            }
        }
        return result.isEmpty() ? "Module action completed." : result;
    }

    private static int dp(Context context, int value) {
        return Math.round(value * context.getResources().getDisplayMetrics().density);
    }

    private static int withAlpha(int color, int alpha) {
        return Color.argb(alpha, Color.red(color), Color.green(color), Color.blue(color));
    }

    private static final class VisualStyle {
        final int color;
        final String symbol;

        VisualStyle(int color, String symbol) {
            this.color = color;
            this.symbol = symbol;
        }
    }
}
