package link.quantway.android

import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import android.os.ParcelFileDescriptor
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import engine.Engine
import engine.Network
import java.util.concurrent.atomic.AtomicInteger
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class EngineBridgeTest {
    @Test fun xrayCallsAndroidDNSAndBindsThePhysicalSocket() {
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        go.Seq.setContext(context)
        val manager = context.getSystemService(ConnectivityManager::class.java)
        val physical = requireNotNull(manager.activeNetwork)
        assertTrue(manager.getNetworkCapabilities(physical)!!.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN))
        val resolutions = AtomicInteger()
        val sockets = AtomicInteger()
        val network = object : Network {
            override fun resolve(host: String): String {
                assertEquals("localhost", host)
                resolutions.incrementAndGet()
                return "127.0.0.1"
            }
            override fun prepareSocket(fd: Long) {
                ParcelFileDescriptor.fromFd(fd.toInt()).use { physical.bindSocket(it.fileDescriptor) }
                sockets.incrementAndGet()
            }
        }
        // A VLESS outbound to a deliberately closed local port. The test proves
        // JNI callbacks and socket binding; it does not claim a working server.
        val outbound = "EiAKHnhyYXkuYXBwLnByb3h5bWFuLlNlbmRlckNvbmZpZxqBAQogeHJheS5wcm94eS52bGVzcy5vdXRib3VuZC5Db25maWcSXQpbCgsSCWxvY2FsaG9zdBAJGkoaSAoYeHJheS5wcm94eS52bGVzcy5BY2NvdW50EiwKJDExMTExMTExLTIyMjItNDMzMy04NDQ0LTU1NTU1NTU1NTU1NRoEbm9uZQ=="
        val job = JSONObject().put("job_id", "android-socket").put("engine", "xray").put("outbound", outbound)
        val result = JSONObject(Engine.probe(job.toString(), "https://control.invalid/", 1000, network))
        assertEquals("fail", result.getString("outcome"))
        assertEquals(1, resolutions.get())
        assertTrue("Android did not bind a native socket", sockets.get() > 0)
    }

    @Test fun malformedAWGConfigIsUnprobeable() {
        val network = object : Network {
            override fun resolve(host: String): String = error("Invalid config must not resolve")
            override fun prepareSocket(fd: Long) = error("Invalid config must not open sockets")
        }
        val job = JSONObject().put("job_id", "invalid-awg").put("engine", "amneziawg").put("awg_conf", "[Interface]\nPrivateKey = invalid")
        val result = JSONObject(Engine.probe(job.toString(), "https://control.invalid/", 1000, network))
        assertEquals("unprobeable", result.getString("outcome"))
    }
}
