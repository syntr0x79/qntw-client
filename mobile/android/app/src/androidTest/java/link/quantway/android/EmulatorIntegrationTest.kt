package link.quantway.android

import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import engine.Engine
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.collect
import java.net.HttpURLConnection
import java.net.URL
import java.util.UUID
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicReference
import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Assume.assumeTrue
import org.junit.FixMethodOrder
import org.junit.Test
import org.junit.runner.RunWith
import org.junit.runners.MethodSorters

@RunWith(AndroidJUnit4::class)
@FixMethodOrder(MethodSorters.NAME_ASCENDING)
class EmulatorIntegrationTest {
    private fun testbed(): String {
        val value = InstrumentationRegistry.getArguments().getString("testbed")
        assumeTrue("Run through TestAndroidEmulatorRoundTrip", value != null)
        require(value!!.startsWith("http://10.0.2.2:"))
        return value
    }
    private fun request(origin: String, path: String, body: JSONObject? = null, token: String? = null): Pair<Int, JSONObject> {
        val context=InstrumentationRegistry.getInstrumentation().targetContext
        val manager=context.getSystemService(android.net.ConnectivityManager::class.java)
        val physical=manager.allNetworks.filter { manager.getNetworkCapabilities(it)?.hasCapability(android.net.NetworkCapabilities.NET_CAPABILITY_NOT_VPN)==true }.sortedByDescending { manager.getNetworkCapabilities(it)?.hasTransport(android.net.NetworkCapabilities.TRANSPORT_WIFI)==true }.first()
        val connection = physical.openConnection(URL(origin + path)) as HttpURLConnection
        try {
            connection.connectTimeout = 10_000
            connection.readTimeout = 30_000
            connection.instanceFollowRedirects = false
            if (token != null) connection.setRequestProperty("Authorization", "Bearer $token")
            if (body != null) {
                connection.requestMethod = "POST"
                connection.doOutput = true
                connection.setRequestProperty("Content-Type", "application/json")
                connection.outputStream.use { it.write(body.toString().toByteArray()) }
            }
            val code = connection.responseCode
            val text = (if (code in 200..299) connection.inputStream else connection.errorStream)?.bufferedReader()?.use { it.readText() } ?: ""
            return code to (if (text.startsWith("{")) JSONObject(text) else JSONObject())
        } finally { connection.disconnect() }
    }
    @Test fun a_realProtocolsAndBackendReport() {
        val origin = testbed()
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        go.Seq.setContext(context)
        val fixture = request(origin, "/fixture").second
        val token = fixture.getString("token")
        PhysicalNetwork(context).use { physical ->
            val snapshot = physical.state.value
            physical.checkCurrent(snapshot)
            val response = request(origin, "/api/mobile/v1/probe/runs", JSONObject().put("network_epoch", snapshot.epoch).put("engines", JSONArray(listOf("xray", "amneziawg"))).put("active_engine", ""), token)
            assertEquals(200, response.first)
            val run = response.second
            val jobs = run.getJSONArray("jobs")
            assertEquals("Expected Reality, XHTTP and AWG", 3, jobs.length())
            val results = JSONArray()
            val names = mutableSetOf<String>()
            for (i in 0 until jobs.length()) {
                val job = jobs.getJSONObject(i)
                val result = runBlocking { ProbeExecutor(physical).execute(job, run.getString("control_url"), 20_000, snapshot) }
                android.util.Log.i("QuantwayIntegration", "${job.getString("label")}: $result")
                names += job.getString("label")
                results.put(result)
            }
            assertTrue(names.any { it.contains("Reality") })
            assertTrue(names.any { it.contains("XHTTP") })
            assertTrue(names.any { it.contains("Amnezia") || it.contains("AWG") })
            val report = JSONObject().put("report_id", UUID.randomUUID().toString()).put("run_id", run.getString("run_id")).put("network_epoch", snapshot.epoch)
                .put("app_version", BuildConfig.VERSION_NAME).put("engine_version", Engine.version()).put("results", results)
            repeat(2) {
                val accepted = request(origin, "/api/mobile/v1/probe/reports", report, token)
                assertEquals(200, accepted.first)
                assertTrue(accepted.second.getBoolean("accepted"))
            }
            val failures = (0 until results.length()).filter { results.getJSONObject(it).getString("outcome") != "ok" }
            assertTrue("Failed protocol checks: $results", failures.isEmpty())
            results.getJSONObject(0).put("outcome", "fail")
            assertEquals(409, request(origin, "/api/mobile/v1/probe/reports", report, token).first)
        }
    }
    @Test fun b_networkChangeCancelsNativeProbe() {
        val origin = testbed()
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        go.Seq.setContext(context)
        val fixture = request(origin, "/fixture").second
        PhysicalNetwork(context).use { physical ->
            shell("svc wifi enable")
            awaitCondition("Wi-Fi was not ready before cancellation test"){physical.state.value.label=="Wi-Fi"}
            val snapshot = physical.state.value
            physical.checkCurrent(snapshot)
            val done = CountDownLatch(1)
            val outcome = AtomicReference<String>()
            val failure = AtomicReference<Throwable>()
            val worker = Thread {
                try { outcome.set(runBlocking { ProbeExecutor(physical).execute(fixture.getJSONObject("cancel_job"), "https://example.com/", 25_000, snapshot) }.getString("outcome")) }
                catch (error: Throwable) { failure.set(error) }
                finally { done.countDown() }
            }
            worker.start()
            try {
                val socketDeadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(10)
                var accepted = false
                while (!accepted && System.nanoTime() < socketDeadline) {
                    accepted = request(origin, "/status").second.getLong("silent_connections") > 0
                    if (!accepted) Thread.sleep(50)
                }
                assertTrue("Native connection did not reach test server", accepted)
                android.os.ParcelFileDescriptor.AutoCloseInputStream(InstrumentationRegistry.getInstrumentation().uiAutomation.executeShellCommand("svc wifi disable")).use { it.readBytes() }
                val transitionDeadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(12)
                while (physical.state.value.epoch == snapshot.epoch && System.nanoTime() < transitionDeadline) Thread.sleep(50)
                assertTrue("Network change did not stop native probe", done.await(15, TimeUnit.SECONDS))
                failure.get()?.let { throw AssertionError("Native probe crashed", it) }
                android.util.Log.i("QuantwayIntegration", "Network change: outcome=${outcome.get()} epochChanged=${snapshot.epoch != physical.state.value.epoch}")
                assertNotEquals("Network epoch did not change", snapshot.epoch, physical.state.value.epoch)
                assertEquals("cancelled", outcome.get())
            } finally {
                Engine.cancel()
                android.os.ParcelFileDescriptor.AutoCloseInputStream(InstrumentationRegistry.getInstrumentation().uiAutomation.executeShellCommand("svc wifi enable")).use { it.readBytes() }
                worker.join(5000)
            }
        }
    }
    private fun awaitCondition(message:String,timeout:Long=30_000,condition:()->Boolean) {
        val end=System.currentTimeMillis()+timeout
        while(!condition()&&System.currentTimeMillis()<end)Thread.sleep(100)
        assertTrue(message,condition())
    }
    private fun shell(command:String) {android.os.ParcelFileDescriptor.AutoCloseInputStream(InstrumentationRegistry.getInstrumentation().uiAutomation.executeShellCommand(command)).use{it.readBytes()}}
    @Test fun c_fullDeviceVPNAndConcurrentProbes() {
        val origin=testbed()
        val instrumentation=InstrumentationRegistry.getInstrumentation()
        val context=instrumentation.targetContext
        instrumentation.startActivitySync(android.content.Intent(context,MainActivity::class.java).addFlags(android.content.Intent.FLAG_ACTIVITY_NEW_TASK))
        shell("appops set link.quantway.android ACTIVATE_VPN allow")
        assertNull("VPN consent was not granted",android.net.VpnService.prepare(context))
        val fixture=request(origin,"/fixture").second
        val token=fixture.getString("token")
        val loaded=request(origin,"/api/mobile/v1/profiles",token=token)
        assertEquals(200,loaded.first)
        val all=loaded.second.getJSONArray("profiles")
        val profiles=(0 until all.length()).map{all.getJSONObject(it)}
        val application=context.applicationContext as QuantwayApplication
        awaitCondition("Wi-Fi was not ready before VPN test"){application.physical.state.value.label=="Wi-Fi"}
        for(engine in listOf("amneziawg","xray")) {
            val profile=profiles.first{it.getString("engine")==engine}
            QuantwayVpnService.profiles=listOf(profile)
            QuantwayVpnService.selected=profile
            context.startForegroundService(android.content.Intent(context,QuantwayVpnService::class.java))
            try {
                awaitCondition("VPN did not start: ${QuantwayVpnService.state.value}"){QuantwayVpnService.activeEngine.value==engine}
                val manager=context.getSystemService(android.net.ConnectivityManager::class.java)
                awaitCondition("Android did not select the VPN network"){manager.getNetworkCapabilities(manager.activeNetwork)?.hasTransport(android.net.NetworkCapabilities.TRANSPORT_VPN)==true}
                val expectedDNS=if(engine=="amneziawg")Regex("(?im)^DNS\\s*=\\s*([^,\\s]+)").find(profile.getString("awg_conf"))!!.groupValues[1] else "1.1.1.1"
                awaitCondition("Android did not install the VPN DNS"){manager.getLinkProperties(manager.activeNetwork)?.dnsServers?.any{it.hostAddress==expectedDNS}==true}
                awaitCondition("VPN DNS did not become usable"){try{java.net.InetAddress.getAllByName("example.com").isNotEmpty()}catch(_:java.net.UnknownHostException){false}}

                // This socket is deliberately NOT protected or bound: Android must
                // deliver it through the real system TUN, including DNS resolution.
                val connection=URL("https://example.com/?quantway-vpn=$engine").openConnection() as HttpURLConnection
                connection.connectTimeout=10_000;connection.readTimeout=10_000
                try {assertEquals("System traffic failed through $engine",200,connection.responseCode);connection.inputStream.close()}finally{connection.disconnect()}
                val snapshot=application.physical.state.value
                var response:Pair<Int,JSONObject>
                do {
                    response=request(origin,"/api/mobile/v1/probe/runs",JSONObject().put("network_epoch",snapshot.epoch).put("engines",JSONArray(listOf("xray","amneziawg"))).put("active_engine",engine),token)
                    if(response.first==429)Thread.sleep(5_000)
                }while(response.first==429)
                assertEquals(200,response.first)
                val jobs=response.second.getJSONArray("jobs")
                val awgJob=(0 until jobs.length()).map{jobs.getJSONObject(it)}.first{it.getString("engine")=="amneziawg"}
                assertNotEquals("Probe reused VPN peer",profiles.first{it.getString("engine")=="amneziawg"}.getString("awg_conf"),awgJob.getString("awg_conf"))
                val probe=runBlocking {ProbeExecutor(application.physical).execute(awgJob,response.second.getString("control_url"),15_000,snapshot)}
                assertEquals("Probe failed with $engine VPN active: $probe","ok",probe.getString("outcome"))
                val browser=instrumentation.startActivitySync(android.content.Intent(context,AccountActivity::class.java).putExtra("url","https://example.com/?quantway-webview=$engine").addFlags(android.content.Intent.FLAG_ACTIVITY_NEW_TASK))
                try {
                    awaitCondition("Browser did not load through $engine",30_000){
                        val title=AtomicReference<String>("");val ready=CountDownLatch(1)
                        instrumentation.runOnMainSync {
                            val container=browser.findViewById<android.view.ViewGroup>(android.R.id.content)
                            (container.getChildAt(0) as android.webkit.WebView).evaluateJavascript("document.title"){title.set(it);ready.countDown()}
                        }
                        ready.await(2,TimeUnit.SECONDS)&&title.get().contains("Example Domain")
                    }
                }finally{instrumentation.runOnMainSync{browser.finish()}}
                val after=URL("https://example.com/?quantway-after-probe=$engine").openConnection() as HttpURLConnection
                after.connectTimeout=10_000;after.readTimeout=10_000
                try{assertEquals("Probe disrupted $engine tunnel",200,after.responseCode);after.inputStream.close()}finally{after.disconnect()}
                if(engine=="xray") {
                    val epoch=application.physical.state.value.epoch
                    android.util.Log.i("QuantwayIntegration","Before switching: ${application.physical.state.value.label}")
                    shell("svc wifi disable")
                    awaitCondition("Physical network did not change"){application.physical.state.value.epoch!=epoch}
                    awaitCondition("VPN did not reconnect on cellular",45_000){QuantwayVpnService.activeEngine.value==engine&&QuantwayVpnService.state.value.contains("Мобильная")}
                    val resumed=URL("https://example.com/?quantway-resumed").openConnection() as HttpURLConnection
                    resumed.connectTimeout=10_000;resumed.readTimeout=10_000
                    try{assertEquals(200,resumed.responseCode);resumed.inputStream.close()}finally{resumed.disconnect()}
                }
                android.util.Log.i("QuantwayIntegration","System VPN $engine and concurrent AWG probe succeeded")
            }finally{
                context.startService(android.content.Intent(context,QuantwayVpnService::class.java).setAction(QuantwayVpnService.STOP))
                awaitCondition("VPN did not stop"){QuantwayVpnService.current==null}
                Thread.sleep(500)
                shell("svc wifi enable")
                awaitCondition("Wi-Fi did not return"){application.physical.state.value.label=="Wi-Fi"}
            }
        }
    }
    @Test fun d_subscriptionLinkAndAutomaticContinuation() {
        val origin=testbed()
        val context=InstrumentationRegistry.getInstrumentation().targetContext
        val fixture=request(origin,"/fixture").second
        val model=ProbeViewModel(context.applicationContext as android.app.Application)
        model.importLink(fixture.getString("subscription_url"))
        awaitCondition("Single-link import failed: ${model.state.value.message}"){!model.state.value.running&&model.state.value.profiles.isNotEmpty()}
        val before=request(origin,"/status").second.getLong("run_requests")
        request(origin,"/arm-network-change")
        model.run(true)
        val observer=CoroutineScope(Dispatchers.IO).launch {model.state.collect{android.util.Log.i("QuantwayProbe",it.message)}}
        try {
            awaitCondition("Probe did not request jobs"){request(origin,"/status").second.getLong("run_requests")>before}
            shell("svc wifi disable")
            awaitCondition("Probe did not continue after network change: ${model.state.value.message}",60_000){!model.state.value.running&&model.state.value.message=="Рабочее подключение выбрано"}
            assertTrue("No successful results",model.state.value.rows.any{it.contains("Работает")})
        }finally{observer.cancel();model.cancel();shell("svc wifi enable")}
    }

}
