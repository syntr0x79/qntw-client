package link.quantway.android

import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.Before
import java.io.File
import java.net.HttpURLConnection
import java.net.URL

class AutomaticProbeTest {
    @get:Rule val ui=createAndroidComposeRule<MainActivity>()
    @Before fun waitForStartup(){ui.waitUntil(15_000){ui.onAllNodesWithTag("nav-account").fetchSemanticsNodes().isNotEmpty()}}
    private val instrumentation get()=InstrumentationRegistry.getInstrumentation()
    private val app get()=instrumentation.targetContext.applicationContext as QuantwayApplication
    private val origin get()=InstrumentationRegistry.getArguments().getString("testbed")!!
    private fun shell(command:String){android.os.ParcelFileDescriptor.AutoCloseInputStream(instrumentation.uiAutomation.executeShellCommand(command)).use{it.readBytes()}}
    private fun await(message:String, predicate:()->Boolean){try{ui.waitUntil(90_000){predicate()}}catch(e:Exception){throw AssertionError(message,e)}}
    @Test fun freshnessExpiresAndRequiresSameNetwork(){
        val checked=1_000_000L
        assertTrue(isProbeFresh(checked,"wifi-a","wifi-a",checked+300_000))
        assertFalse(isProbeFresh(checked,"wifi-a","wifi-a",checked+300_001))
        assertFalse(isProbeFresh(checked,"wifi-a","wifi-b",checked+1))
        assertFalse(isProbeFresh(0,"wifi-a","wifi-a",checked))
    }
    @Test fun autoChecksOnImportAndNewNetwork(){
        shell("svc wifi enable")
        await("Wi-Fi unavailable"){app.physical.state.value.label=="Wi-Fi"}
        val model=androidx.lifecycle.ViewModelProvider(ui.activity)[ProbeViewModel::class.java]
        await("subscription refresh did not settle"){!model.state.value.running}
        ui.runOnUiThread { model.state.value.sources.toList().forEach{model.removeSource(it.id)} }
        ui.onNodeWithTag("nav-account").performClick()
        ui.onNodeWithTag("manage-subscriptions").performScrollTo().performClick()
        ui.onNodeWithTag("add-subscription").performScrollTo().performClick()
        ui.onNodeWithTag("subscription-link").performTextInput("$origin/parallel-subscription")
        ui.onNodeWithTag("import-submit").performClick()
        await("compact progress did not appear on the home screen"){ui.onAllNodesWithTag("home-probe-progress").fetchSemanticsNodes().isNotEmpty()}
        ui.onNodeWithTag("probe-progress").assertDoesNotExist()
        await("automatic check did not finish"){!model.state.value.running && model.hasFreshRecommendation() && model.state.value.total==56}
        val first=model.state.value
        assertEquals(56, first.completed)
        assertEquals(first.recommendedId,first.selectedId)
        assertNull(QuantwayVpnService.current)
        val other=first.profiles.first{it.optString("job_id")!=first.selectedId}.getString("job_id")
        ui.runOnUiThread { model.select(other) }
        val epoch=app.physical.state.value.epoch
        try {
            shell("svc wifi disable")
            await("network changed without a fresh check"){app.physical.state.value.epoch!=epoch && !model.state.value.running && model.hasFreshRecommendation() && model.state.value.checkedEpoch==app.physical.state.value.epoch}
            assertEquals("best route was not selected after the network changed",model.state.value.recommendedId,model.state.value.selectedId)
            assertTrue("automatic mode was not restored",model.state.value.automatic)
            assertNull(QuantwayVpnService.current)
        } finally {shell("svc wifi enable")}
    }
}
