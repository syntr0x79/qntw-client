package link.quantway.tincspike;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.content.Intent;
import android.net.ConnectivityManager;
import android.net.LocalServerSocket;
import android.net.LocalSocket;
import android.net.Network;
import android.net.NetworkCapabilities;
import android.net.VpnService;
import android.os.ParcelFileDescriptor;
import android.system.Os;
import java.io.FileDescriptor;
import java.io.IOException;
import java.util.UUID;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.atomic.AtomicInteger;

/** Actual Android protect/bind broker; the test TUN carries no user default route. */
public final class GuardVpnService extends VpnService {
    static final CountDownLatch READY = new CountDownLatch(1);
    static volatile GuardVpnService instance;
    static volatile Throwable startupError;
    final AtomicInteger preparedSockets = new AtomicInteger();
    final AtomicInteger rejected = new AtomicInteger();
    volatile String brokerName;
    private volatile boolean closed;
    private volatile LocalSocket active;
    private LocalServerSocket broker;
    private ParcelFileDescriptor tun;
    private Network physical;

    @Override public void onCreate() {
        super.onCreate();
        NotificationManager notices = getSystemService(NotificationManager.class);
        notices.createNotificationChannel(new NotificationChannel("tinc-spike", "Tinc spike", NotificationManager.IMPORTANCE_LOW));
        startForeground(1, new Notification.Builder(this, "tinc-spike")
            .setSmallIcon(android.R.drawable.stat_sys_upload).setContentTitle("Tinc native socket test")
            .setContentText("Isolated test network").setOngoing(true).build());
        try {
            if (VpnService.prepare(this) != null) throw new IOException("VPN authorization missing; grant ACTIVATE_VPN to test package");
            ConnectivityManager connectivity = getSystemService(ConnectivityManager.class);
            // Capture the physical network before creating the VPN.
            Network defaultNetwork = connectivity.getActiveNetwork();
            NetworkCapabilities defaultCaps = connectivity.getNetworkCapabilities(defaultNetwork);
            if (defaultCaps != null && defaultCaps.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
                && defaultCaps.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN)) physical = defaultNetwork;
            for (Network candidate : connectivity.getAllNetworks()) {
                if (physical != null) break;
                NetworkCapabilities caps = connectivity.getNetworkCapabilities(candidate);
                if (caps != null && caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
                    && caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN)
                    && caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_VALIDATED)) {
                    physical = candidate;
                    break;
                }
            }
            if (physical == null) throw new IOException("No physical network");
            android.util.Log.i("TincSpike", "Physical network before VPN: " + physical);
            tun = new Builder().setSession("Tinc spike isolated TUN").setMtu(1280)
                .addAddress("198.18.71.2", 32).addRoute("198.18.71.0", 24)
                .setUnderlyingNetworks(new Network[]{physical}).setBlocking(false).establish();
            if (tun == null) throw new IOException("VPN TUN establishment failed");
            brokerName = "tinc-spike-" + UUID.randomUUID();
            broker = new LocalServerSocket(brokerName);
            instance = this;
            new Thread(this::serve, "tinc-android-socket-guard").start();
        } catch (Throwable error) {
            startupError = error;
            stopSelf();
        } finally {
            READY.countDown();
        }
    }

    @Override public int onStartCommand(Intent intent, int flags, int startId) { return START_NOT_STICKY; }

    private void serve() {
        while (!closed) {
            try (LocalSocket connection = broker.accept()) {
                active = connection;
                connection.setSoTimeout(5_000);
                int request = connection.getInputStream().read();
                FileDescriptor[] received = connection.getAncillaryFileDescriptors();
                int ack = 1;
                try {
                    if (request == 1 && received != null && received.length == 1 && !closed) {
                        // The Go SocketGuard validates AF_INET/AF_INET6 and
                        // unconnected TCP/UDP before forwarding this descriptor.
                        try (ParcelFileDescriptor duplicate = ParcelFileDescriptor.dup(received[0])) {
                            if (!protect(duplicate.getFd())) throw new IOException("VpnService.protect failed");
                            physical.bindSocket(received[0]);
                        }
                        preparedSockets.incrementAndGet();
                        ack = 0;
                    }
                } catch (Exception error) {
                    android.util.Log.w("TincSpike", "Socket preparation denied", error);
                } finally {
                    if (received != null) for (FileDescriptor descriptor : received) {
                        try { Os.close(descriptor); } catch (Exception ignored) { }
                    }
                }
                if (ack != 0) rejected.incrementAndGet();
                connection.getOutputStream().write(ack);
            } catch (IOException error) {
                if (!closed) android.util.Log.w("TincSpike", "Broker connection failed", error);
            } finally {
                active = null;
            }
        }
    }

    boolean hasTun() { return tun != null && tun.getFileDescriptor().valid(); }

    @Override public void onRevoke() { stopSelf(); }

    @Override public void onDestroy() {
        closed = true;
        try { if (active != null) active.close(); } catch (IOException ignored) { }
        try { if (broker != null) broker.close(); } catch (IOException ignored) { }
        try { if (tun != null) tun.close(); } catch (IOException ignored) { }
        instance = null;
        super.onDestroy();
    }
}
