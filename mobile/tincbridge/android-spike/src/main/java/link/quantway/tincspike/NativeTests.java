package link.quantway.tincspike;

import android.app.Activity;
import android.app.Instrumentation;
import android.content.Intent;
import android.os.Bundle;
import java.io.File;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.util.Map;
import java.util.concurrent.TimeUnit;

/** An isolated target-35 app identity for testing packaged native executables. */
public final class NativeTests extends Instrumentation {
    private Bundle arguments;

    @Override public void onCreate(Bundle arguments) {
        super.onCreate(arguments);
        this.arguments = arguments == null ? new Bundle() : arguments;
        start();
    }

    @Override public void onStart() {
        Bundle result = new Bundle();
        int status = Activity.RESULT_CANCELED;
        Process child = null;
        Activity fixtureActivity = null;
        try {
            if ("1".equals(arguments.getString("guard.vpn", "0"))) {
                fixtureActivity = startActivitySync(new Intent(getTargetContext(), HarnessActivity.class).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK));
                if (!GuardVpnService.READY.await(10, TimeUnit.SECONDS) || GuardVpnService.instance == null)
                    throw new IllegalStateException("VPN guard startup failed: " + GuardVpnService.startupError);
            }
            File libs = new File(getTargetContext().getApplicationInfo().nativeLibraryDir);
            File cache = getTargetContext().getCacheDir();
            File log = new File(cache, "native-tests.log");
            String pattern = arguments.getString("test.run", ".*");
            ProcessBuilder command = new ProcessBuilder(
                new File(libs, "libtinctest.so").getAbsolutePath(),
                "-test.v", "-test.timeout=90s", "-test.run=" + pattern);
            Map<String, String> env = command.environment();
            env.put("TMPDIR", cache.getAbsolutePath());
            env.put("CLIENT_TINC_DAEMON", new File(libs, "libtincd.so").getAbsolutePath());
            env.put("CLIENT_TINC_CTL", new File(libs, "libtincctl.so").getAbsolutePath());
            env.put("CLIENT_TINC_FIXTURE_BIN", new File(libs, "libfixture.so").getAbsolutePath());
            if (GuardVpnService.instance != null)
                env.put("CLIENT_TINC_ANDROID_GUARD", "@" + GuardVpnService.instance.brokerName);
            command.directory(cache).redirectErrorStream(true).redirectOutput(log);
            child = command.start();
            if (!child.waitFor(110, TimeUnit.SECONDS)) {
                child.destroyForcibly();
                throw new IllegalStateException("native test process timed out");
            }
            String output = new String(Files.readAllBytes(log.toPath()), StandardCharsets.UTF_8);
            String evidence = "";
            GuardVpnService vpn = GuardVpnService.instance;
            if (vpn != null) {
                evidence = "Android actual VpnService: one TUN=" + vpn.hasTun() + ", prepared sockets=" + vpn.preparedSockets.get()
                    + ", rejected=" + vpn.rejected.get() + "\n"
                    + "Proof scope: active VPN + external socket protection/binding + raw packet-FD delivery; no user traffic routing claim.\n";
                if (!vpn.hasTun() || vpn.preparedSockets.get() == 0)
                    throw new IllegalStateException("Native test did not exercise active VPN socket broker: " + evidence);
            }
            result.putString("stream", "\nNative library directory: " + libs + "\n" + output + evidence);
            result.putInt("native_exit_code", child.exitValue());
            if (child.exitValue() == 0) status = Activity.RESULT_OK;
        } catch (Throwable error) {
            result.putString("stream", "\nNative test harness failure: " + error + "\n");
        } finally {
            if (child != null && child.isAlive()) child.destroyForcibly();
            getTargetContext().stopService(new Intent(getTargetContext(), GuardVpnService.class));
            if (fixtureActivity != null) {
                Activity finished = fixtureActivity;
                runOnMainSync(finished::finish);
            }
        }
        finish(status, result);
    }
}
