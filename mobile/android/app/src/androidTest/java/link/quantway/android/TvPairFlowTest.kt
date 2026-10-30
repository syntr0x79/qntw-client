package link.quantway.android

import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.lifecycle.ViewModelProvider
import androidx.test.platform.app.InstrumentationRegistry
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import java.net.URL

class TvPairFlowTest {
    @get:Rule val ui=createAndroidComposeRule<MainActivity>()
    private fun waitFor(label:String,condition:()->Boolean){try{ui.waitUntil(90000){condition()}}catch(e:Exception){throw AssertionError(label,e)}}
    @Test fun sendsExternalAndCreatesSeparateQuantwayDevice(){
        val inst=InstrumentationRegistry.getInstrumentation()
        val app=inst.targetContext.applicationContext as QuantwayApplication
        val origin=InstrumentationRegistry.getArguments().getString("testbed")!!
        val fixture=JSONObject(URL("$origin/fixture").readText())
        app.cabinetTestOrigin=origin;app.cabinet.clear()
        val probe=ViewModelProvider(ui.activity)[ProbeViewModel::class.java]
        val cabinet=ViewModelProvider(ui.activity)[CabinetViewModel::class.java]
        val tv=TvPairViewModel(app);val phone=TvPairViewModel(app)
        fun transfer(source:SubscriptionSource){
            ui.runOnUiThread{tv.startTv(probe)}
            waitFor("TV did not receive a pairing code"){tv.state.value.code.isNotBlank()}
            val code=tv.state.value.code
            if(source.api){
                val foreign=source.copy(id="unrelated-account",url="$origin/sub/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
                ui.runOnUiThread{phone.send(code,foreign)}
                waitFor("mismatched account was not rejected"){!phone.state.value.sending&&phone.state.value.phoneMessage.isNotBlank()}
                assertFalse("different account was accepted",phone.state.value.sent)
                assertTrue(phone.state.value.phoneMessage,phone.state.value.phoneMessage.contains("не связана"))
            }
            ui.runOnUiThread{phone.send(code,source)}
            waitFor("phone did not submit the pairing: ${phone.state.value.phoneMessage}"){phone.state.value.sent||(!phone.state.value.sending&&phone.state.value.phoneMessage.isNotBlank())}
            assertTrue(phone.state.value.phoneMessage,phone.state.value.sent)
            waitFor("TV did not decrypt and import the subscription: ${tv.state.value.tvMessage}"){tv.state.value.tvDone}
            ui.runOnUiThread{tv.stopTv()}
        }
        try{
            waitFor("startup did not settle"){!probe.state.value.running&&!cabinet.state.value.busy}
            ui.runOnUiThread{probe.importLink("$origin/external-subscription")}
            waitFor("external subscription did not import"){!probe.state.value.running&&probe.activeSource()?.api==false}
            val external=probe.activeSource()!!
            transfer(external)
            assertEquals(external.url,probe.activeSource()!!.url)
            waitFor("external probing did not settle"){!probe.state.value.running}
            ui.runOnUiThread{probe.importLink(fixture.getString("subscription_url"))}
            waitFor("Quantway subscription did not import"){!probe.state.value.running&&probe.activeSource()?.api==true}
            val phoneSource=probe.activeSource()!!
            ui.runOnUiThread{cabinet.login(fixture.getString("cabinet_code"))}
            waitFor("cabinet login did not finish"){cabinet.state.value.authenticated&&!cabinet.state.value.busy&&cabinet.state.value.phoneId.isNotBlank()}
            val count=cabinet.state.value.devices.getJSONArray("devices").length()
            transfer(phoneSource)
            assertNotEquals("TV reused phone subscription",phoneSource.url,probe.activeSource()!!.url)
            ui.runOnUiThread{cabinet.refresh()}
            waitFor("device list did not refresh"){!cabinet.state.value.busy}
            assertEquals("TV did not occupy exactly one device slot",count+1,cabinet.state.value.devices.getJSONArray("devices").length())
        }finally{ui.runOnUiThread{tv.stopTv()};app.cabinetTestOrigin=null}
    }
}
