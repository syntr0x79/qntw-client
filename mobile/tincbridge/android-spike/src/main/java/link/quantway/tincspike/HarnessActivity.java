package link.quantway.tincspike;

import android.app.Activity;
import android.content.Intent;
import android.os.Bundle;
import android.widget.TextView;

/** Gives the isolated foreground test service an ordinary activity lifecycle. */
public final class HarnessActivity extends Activity {
    @Override public void onCreate(Bundle state) {
        super.onCreate(state);
        TextView label = new TextView(this);
        label.setText("Isolated tinc socket test. No user traffic is routed here.");
        setContentView(label);
        startForegroundService(new Intent(this, GuardVpnService.class));
    }
}
