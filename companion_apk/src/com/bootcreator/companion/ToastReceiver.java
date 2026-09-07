package com.bootcreator.companion;

import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;
import android.graphics.PixelFormat;
import android.graphics.drawable.GradientDrawable;
import android.os.Build;
import android.os.Handler;
import android.os.Looper;
import android.view.Gravity;
import android.view.View;
import android.view.WindowManager;
import android.widget.TextView;

public class ToastReceiver extends BroadcastReceiver {
    
    private static View toastAtual = null;
    private static WindowManager wm = null;

    @Override
    public void onReceive(Context context, Intent intent) {
        if (intent != null && intent.hasExtra("msg")) {
            String message = intent.getStringExtra("msg");
            
            if (wm == null) {
                wm = (WindowManager) context.getSystemService(Context.WINDOW_SERVICE);
            }

            if (toastAtual != null) {
                try {
                    wm.removeView(toastAtual);
                } catch (Exception e) {}
                toastAtual = null;
            }

            TextView tv = new TextView(context);
            tv.setText(message);
            tv.setTextColor(0xFF000000);
            tv.setTextSize(14f);
            tv.setPadding(50, 25, 50, 25); 

            GradientDrawable fundo = new GradientDrawable();
            fundo.setColor(0xFFBB86FC); 
            fundo.setCornerRadius(100f);
            tv.setBackground(fundo);

            int tipoJanela = Build.VERSION.SDK_INT >= Build.VERSION_CODES.O 
                    ? WindowManager.LayoutParams.TYPE_APPLICATION_OVERLAY 
                    : WindowManager.LayoutParams.TYPE_SYSTEM_ALERT;

            WindowManager.LayoutParams params = new WindowManager.LayoutParams(
                    WindowManager.LayoutParams.WRAP_CONTENT,
                    WindowManager.LayoutParams.WRAP_CONTENT,
                    tipoJanela,
                    WindowManager.LayoutParams.FLAG_NOT_FOCUSABLE
                  | WindowManager.LayoutParams.FLAG_NOT_TOUCH_MODAL, 
                    PixelFormat.TRANSLUCENT
            );

            params.gravity = Gravity.BOTTOM | Gravity.CENTER_HORIZONTAL;
            params.y = 150;

            wm.addView(tv, params);
            toastAtual = tv;

            new Handler(Looper.getMainLooper()).postDelayed(new Runnable() {
                @Override
                public void run() {
                    if (toastAtual == tv) {
                        try {
                            wm.removeView(tv);
                            toastAtual = null;
                        } catch (Exception e) {}
                    }
                }
            }, 3000);
        }
    }
}

