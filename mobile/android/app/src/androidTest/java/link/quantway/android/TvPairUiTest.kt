package link.quantway.android

import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.lifecycle.ViewModelProvider
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test

class TvPairUiTest {
    @get:Rule val ui=createAndroidComposeRule<MainActivity>()
    @Test fun phoneEntersCodeAndSendsSelectedExternalSubscription(){
        val app=InstrumentationRegistry.getInstrumentation().targetContext.applicationContext as QuantwayApplication
        val origin=InstrumentationRegistry.getArguments().getString("testbed")!!
        app.cabinetTestOrigin=origin
        val probe=ViewModelProvider(ui.activity)[ProbeViewModel::class.java]
        val tv=TvPairViewModel(app)
        try{
            ui.waitUntil(15000){ui.onAllNodesWithTag("nav-account").fetchSemanticsNodes().isNotEmpty()}
            ui.waitUntil(60000){!probe.state.value.running}
            ui.runOnUiThread{probe.importLink("$origin/external-subscription")}
            ui.waitUntil(90000){!probe.state.value.running&&probe.activeSource()?.api==false}
            ui.runOnUiThread{tv.startTv(probe)}
            ui.waitUntil(30000){tv.state.value.code.isNotBlank()}
            ui.onNodeWithTag("nav-account").performClick()
            ui.onNodeWithTag("open-tv-pair").performScrollTo().performClick()
            ui.onNodeWithTag("pair-code-input").performTextInput(tv.state.value.code)
            ui.onNodeWithTag("pair-send").performScrollTo().performClick()
            ui.waitUntil(90000){tv.state.value.tvDone}
            ui.onNodeWithText("Подписка отправлена. Продолжите на телевизоре.").assertExists()
            assertEquals("$origin/external-subscription",probe.activeSource()?.url)
        }finally{ui.runOnUiThread{tv.stopTv()};app.cabinetTestOrigin=null}
    }
}
