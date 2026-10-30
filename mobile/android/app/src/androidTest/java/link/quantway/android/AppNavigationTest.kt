package link.quantway.android

import android.content.Intent
import android.graphics.Bitmap
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.test.platform.app.InstrumentationRegistry
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.Before
import java.net.HttpURLConnection
import java.net.URL
import java.io.ByteArrayOutputStream

class AppNavigationTest {
    @get:Rule val ui=createAndroidComposeRule<MainActivity>()
    @Before fun waitForStartup(){ui.waitUntil(15_000){ui.onAllNodesWithTag("nav-account").fetchSemanticsNodes().isNotEmpty()}}
    private val instrumentation get()=InstrumentationRegistry.getInstrumentation()
    private val context get()=instrumentation.targetContext
    private val app get()=context.applicationContext as QuantwayApplication
    private val origin get()=InstrumentationRegistry.getArguments().getString("testbed")?:error("testbed required")
    private fun request(path:String):JSONObject {val c=URL(origin+path).openConnection() as HttpURLConnection;return try{JSONObject(c.inputStream.bufferedReader().use{it.readText()})}finally{c.disconnect()}}
    private fun waitFor(message:String,timeout:Long=45_000,test:()->Boolean){try{ui.waitUntil(timeout){test()}}catch(e:Exception){throw AssertionError(message,e)}}
    private fun screenshot(name:String){
        ui.waitForIdle()
        val bitmap=instrumentation.uiAutomation.takeScreenshot()?:error("screenshot unavailable")
        val bytes=ByteArrayOutputStream();bitmap.compress(Bitmap.CompressFormat.PNG,100,bytes);bitmap.recycle()
        val c=app.physical.state.value.network!!.openConnection(URL("$origin/screenshots/$name")) as HttpURLConnection
        try{c.requestMethod="POST";c.doOutput=true;c.setRequestProperty("Content-Type","image/png");c.outputStream.use{it.write(bytes.toByteArray())};assertEquals(204,c.responseCode)}finally{c.disconnect()}
    }
    @Test fun navigationRealSubscriptionProbeVPNAndPreferences(){
        android.os.ParcelFileDescriptor.AutoCloseInputStream(instrumentation.uiAutomation.executeShellCommand("appops set link.quantway.android ACTIVATE_VPN allow")).use{it.readBytes()}
        android.os.ParcelFileDescriptor.AutoCloseInputStream(instrumentation.uiAutomation.executeShellCommand("svc wifi enable")).use{it.readBytes()}
        waitFor("Network not ready before UI test",60_000){app.physical.state.value.network!=null}
        val fixture=request("/fixture");val link=fixture.getString("subscription_url")
        ui.onNodeWithTag("nav-account").performClick();ui.onNodeWithTag("manage-subscriptions").performScrollTo().performClick()
        ui.onNodeWithTag("add-subscription").assertIsDisplayed().performClick()
        ui.onNodeWithTag("subscription-link").performTextInput(link)
        ui.onNodeWithTag("import-submit").performClick()
        waitFor("subscription did not finish importing"){ui.onAllNodesWithTag("vpn-power").fetchSemanticsNodes().isNotEmpty()&&ui.onAllNodesWithText("Всё готово").fetchSemanticsNodes().isNotEmpty()}
        val autoModel=androidx.lifecycle.ViewModelProvider(ui.activity)[ProbeViewModel::class.java]
        waitFor("automatic route check did not finish",120_000){!autoModel.state.value.running && autoModel.hasFreshRecommendation()}
        assertTrue("automatic check did not select its recommendation",autoModel.state.value.automatic && autoModel.state.value.selectedId==autoModel.state.value.recommendedId)
        ui.onNodeWithTag("home-check-routes").performScrollTo().performClick()
        ui.onNodeWithTag("recommended-route").assertIsDisplayed()
        ui.onNodeWithTag("use-recommended-route").assertDoesNotExist()
        ui.onNodeWithText("Выбран",substring=false).assertExists()
        val recommendedId=autoModel.state.value.recommendedId
        val anotherId=autoModel.state.value.profiles.first{it.optString("job_id")!=recommendedId}.getString("job_id")
        ui.runOnIdle{autoModel.select(anotherId)}
        ui.onNodeWithTag("use-recommended-route").assertIsDisplayed()
        ui.onNodeWithTag("use-recommended-route").performClick()
        assertEquals(recommendedId,autoModel.state.value.selectedId)
        val stored=app.subscriptions.read();assertTrue(stored.getJSONArray("sources").length()>0)
        val raw=java.io.File(context.noBackupFilesDir,"subscriptions.enc").readBytes().toString(Charsets.ISO_8859_1)
        assertFalse("device token persisted without encryption",raw.contains(fixture.getString("token")))
        val activeId=stored.getString("active");val sources=stored.getJSONArray("sources");val source=(0 until sources.length()).map{sources.getJSONObject(it)}.first{it.getString("id")==activeId}
        val data=source.getJSONObject("cache");assertEquals(30,data.getJSONObject("subscription").getInt("days_available"))
        assertTrue(data.getJSONObject("routing").getJSONArray("direct_cidrs").length()>0)
        val profiles=data.getJSONArray("profiles");assertTrue("catalog must exceed six routes",profiles.length()>6);val awg=(0 until profiles.length()).map{profiles.getJSONObject(it)}.first{it.getString("engine")=="amneziawg"}
        ui.onNodeWithTag("nav-routes").performClick();ui.onNodeWithTag("routes-check").assertIsDisplayed();ui.onNodeWithTag("routes-filters").assertIsDisplayed().performClick();ui.onNodeWithTag("filter-show-all").performClick();ui.onNodeWithTag("route-${awg.getString("job_id")}").performScrollTo().assertExists()
        ui.onNodeWithTag("route-${awg.getString("job_id")}").performScrollTo().performClick()
        ui.onNodeWithTag("vpn-power").performClick()
        waitFor("AWG VPN did not connect: ${QuantwayVpnService.state.value}",60_000){QuantwayVpnService.phase.value=="connected"&&QuantwayVpnService.activeEngine.value=="amneziawg"}
        ui.onNodeWithText("Вы в сети").assertExists();assertTrue(app.preferences.state.value.split)
        val manager=context.getSystemService(android.net.ConnectivityManager::class.java)
        waitFor("Android did not activate the system VPN"){manager.getNetworkCapabilities(manager.activeNetwork)?.hasTransport(android.net.NetworkCapabilities.TRANSPORT_VPN)==true&&manager.getLinkProperties(manager.activeNetwork)?.dnsServers?.any{it.hostAddress=="10.66.0.1"}==true}
        assertTrue("private GeoIP bypass failed through system VPN",request("/status").has("run_requests"))
        val c=URL("https://example.com/?quantway-ui=awg-split").openConnection() as HttpURLConnection;c.connectTimeout=10_000;c.readTimeout=10_000
        try{assertEquals("AWG routing engine did not carry system HTTPS",200,c.responseCode);c.inputStream.close()}finally{c.disconnect()}
        assertTrue("native outbound health check failed",engine.Engine.checkVPN("https://example.com/"))
        assertTrue("system VPN path health check failed",engine.Engine.checkSystemVPN("https://example.com/"))
        screenshot("home")
        ui.onNodeWithTag("nav-routes").performClick()
        val other=(0 until profiles.length()).map{profiles.getJSONObject(it)}.first{it.getString("engine")=="xray"&&it.optString("transport")=="xhttp"}
        val previous=QuantwayVpnService.selected!!.getString("job_id")
        ui.onNodeWithTag("route-${other.getString("job_id")}").performScrollTo().performClick()
        ui.onNodeWithText("Сменить маршрут?").assertExists()
        assertEquals("route changed before confirmation",previous,QuantwayVpnService.selected!!.getString("job_id"))
        ui.onNodeWithTag("confirm-action").performClick()
        waitFor("confirmed route change failed"){QuantwayVpnService.phase.value=="connected"&&QuantwayVpnService.activeProfile.value?.getString("job_id")==other.getString("job_id")}
        ui.onNodeWithTag("nav-settings").performClick();ui.onNodeWithTag("theme-light").performScrollTo().performClick();assertEquals("light",app.preferences.state.value.theme)
        ui.onNodeWithTag("theme-dark").performClick()
        ui.onNodeWithText("Обход VPN").performScrollTo().performClick()
        ui.onNodeWithText("Мои исключения").performScrollTo().performClick()
        ui.onNodeWithTag("domain-input").performTextInput("direct.example")
        ui.onNodeWithText("Добавить исключение").performClick();assertTrue(app.preferences.state.value.exceptions.contains("direct.example"))
        ui.onNodeWithContentDescription("Удалить direct.example").performScrollTo().performClick();assertFalse(app.preferences.state.value.exceptions.contains("direct.example"))
        ui.onNodeWithTag("nav-settings").performClick();screenshot("settings")
        ui.onNodeWithTag("nav-vpn").performClick();ui.onNodeWithText("Проверить маршруты").performScrollTo().performClick();ui.onNodeWithContentDescription("Делиться результатами").performScrollTo().performClick();request("/arm-delayed-report");ui.onNodeWithTag("start-probe").performScrollTo().performClick()
        ui.onNodeWithTag("probe-progress").assertIsDisplayed()
        ui.onNodeWithTag("probe-percent").assertIsDisplayed()
        val model=androidx.lifecycle.ViewModelProvider(ui.activity)[ProbeViewModel::class.java]
        waitFor("probe did not return recommendation",120_000){!model.state.value.running&&model.state.value.recommendedId.isNotEmpty()}
        ui.onNodeWithText("Есть рабочие маршруты").assertIsDisplayed()
        waitFor("slow report did not start"){request("/status").optBoolean("report_in_flight")}
        assertFalse("slow report blocked completed probe",model.state.value.running)
        assertTrue("recommendation waited for report upload",model.state.value.recommendedId.isNotBlank())
        ui.onNodeWithTag("recommended-route").assertIsDisplayed()
        if(model.state.value.recommendedId==QuantwayVpnService.activeProfile.value?.optString("job_id"))ui.onNodeWithTag("use-recommended-route").assertDoesNotExist() else ui.onNodeWithTag("use-recommended-route").assertIsDisplayed()
        ui.onNodeWithTag("probe-progress").assertDoesNotExist()
        model.state.value.rows.forEach { ui.onNodeWithText(it).assertDoesNotExist() }
        assertEquals("probe did not check the entire catalog",profiles.length(),model.state.value.completed)
        assertEquals("probe lost results",profiles.length(),model.state.value.observations.size)
        assertTrue("same-country masks are indistinguishable",model.state.value.rows.distinct().size==model.state.value.rows.size)
        assertEquals("probe silently changed active route",other.getString("job_id"),QuantwayVpnService.activeProfile.value!!.getString("job_id"))
        val afterProbe=URL("https://example.com/?quantway-ui=after-parallel-probe").openConnection() as HttpURLConnection
        afterProbe.connectTimeout=10_000;afterProbe.readTimeout=10_000
        try{assertEquals("VPN internet stopped after parallel probing",200,afterProbe.responseCode);afterProbe.inputStream.close()}finally{afterProbe.disconnect()}
        waitFor("queued report was not delivered",20_000){!request("/status").optBoolean("report_in_flight")&&model.state.value.pending==0}
        ui.onNodeWithTag("nav-routes").performClick();ui.onNodeWithTag("route-${other.getString("job_id")}").assertExists();screenshot("routes");ui.onNodeWithTag("nav-vpn").performClick();ui.onNodeWithText("Вы в сети").assertExists();screenshot("home")
        val suggestions=model.state.value.profiles.filter{p->p.optString("job_id")!=other.getString("job_id")&&model.state.value.observations[p.optString("job_id")]?.let{it.epoch==app.physical.state.value.epoch&&it.outcome=="ok"}==true}.sortedBy{model.state.value.observations[it.getString("job_id")]!!.elapsedMS}.take(3)
        assertEquals("home should have three measured alternatives",3,suggestions.size)
        suggestions.forEach{ui.onNodeWithTag("home-route-choice-${it.getString("job_id")}").performScrollTo().assertIsDisplayed()}
        val alternative=suggestions.first().getString("job_id")
        ui.onNodeWithTag("home-route-choice-$alternative").performScrollTo().performClick()
        ui.onNodeWithText("Сменить маршрут?").assertExists()
        assertEquals("home choice changed active route before confirmation",other.getString("job_id"),QuantwayVpnService.activeProfile.value!!.getString("job_id"))
        ui.onNodeWithTag("confirm-action").performClick()
        waitFor("home choice did not reconnect VPN"){QuantwayVpnService.phase.value=="connected"&&QuantwayVpnService.activeProfile.value?.getString("job_id")==alternative}
        ui.onNodeWithTag("nav-settings").performClick();ui.onNodeWithText("Диагностика",substring=false).performScrollTo().performClick();ui.onNodeWithText("Подготовить отчёт").performScrollTo().performClick()
        val report=ui.onNodeWithTag("diagnostic-report").fetchSemanticsNode().config[androidx.compose.ui.semantics.SemanticsProperties.Text].joinToString()
        assertFalse(report.contains(fixture.getString("token")));assertFalse(report.contains("PrivateKey"));assertFalse(report.contains("vless://"))
        ui.onNodeWithTag("nav-account").performClick();ui.onNodeWithTag("manage-subscriptions").performScrollTo().performClick();ui.onNodeWithTag("add-subscription").performScrollTo().performClick()
        ui.onNodeWithTag("subscription-link").performTextInput("$origin/external-subscription")
        ui.onNodeWithTag("import-submit").performClick()
        waitFor("external subscription did not import"){app.subscriptions.read().getJSONArray("sources").let{a->(0 until a.length()).any{!a.getJSONObject(it).getBoolean("api")}}}
        assertEquals("import disrupted active VPN",alternative,QuantwayVpnService.activeProfile.value!!.getString("job_id"))
        ui.onNodeWithTag("nav-vpn").performClick();ui.onNodeWithTag("vpn-power").performScrollTo().performClick()
        waitFor("VPN did not stop"){QuantwayVpnService.current==null}
        assertEquals("dark",app.preferences.state.value.theme)
    }
}
