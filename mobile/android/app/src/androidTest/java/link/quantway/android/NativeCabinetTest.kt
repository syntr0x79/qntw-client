package link.quantway.android

import android.graphics.Bitmap
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.lifecycle.ViewModelProvider
import androidx.compose.ui.unit.dp
import androidx.test.platform.app.InstrumentationRegistry
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.FixMethodOrder
import org.junit.runners.MethodSorters
import org.junit.Rule
import org.junit.Test
import java.net.HttpURLConnection
import java.net.URL

@FixMethodOrder(MethodSorters.NAME_ASCENDING)
class NativeCabinetTest {
    @get:Rule val ui=createAndroidComposeRule<MainActivity>()
    private val instrumentation get()=InstrumentationRegistry.getInstrumentation()
    private val app get()=instrumentation.targetContext.applicationContext as QuantwayApplication
    private val origin get()=InstrumentationRegistry.getArguments().getString("testbed")!!
    private fun fixture():JSONObject {val c=URL("$origin/fixture").openConnection() as HttpURLConnection;return try{JSONObject(c.inputStream.bufferedReader().use{it.readText()})}finally{c.disconnect()}}
    private fun waitFor(message:String,test:()->Boolean){try{ui.waitUntil(45_000){test()}}catch(e:Exception){throw AssertionError(message,e)}}
    private fun account()=ViewModelProvider(ui.activity)[CabinetViewModel::class.java]
    private fun ready(){waitFor("cabinet request did not finish"){!account().state.value.busy}}
    private fun nav(){ui.onNodeWithTag("nav-account").performClick();ready()}
    private fun confirm(){ui.onNodeWithText("Подтвердить",substring=false).performClick();ready()}
    private fun screenshot(name:String){
        ui.waitForIdle();Thread.sleep(350)
        val bitmap=instrumentation.uiAutomation.takeScreenshot()!!;val bytes=java.io.ByteArrayOutputStream();bitmap.compress(Bitmap.CompressFormat.PNG,100,bytes);bitmap.recycle()
        val c=URL("$origin/screenshots/$name").openConnection() as HttpURLConnection
        try{c.requestMethod="POST";c.doOutput=true;c.outputStream.use{it.write(bytes.toByteArray())};assertEquals(204,c.responseCode)}finally{c.disconnect()}
    }
    @Test fun a_onboardingCreatesPhoneWithoutManualSubscription(){
        app.cabinetTestOrigin=origin;app.cabinet.clear()
        val f=fixture();val probe=ViewModelProvider(ui.activity)[ProbeViewModel::class.java]
        waitFor("subscription refresh did not settle"){!probe.state.value.running}
        probe.state.value.sources.toList().forEach{probe.removeSource(it.id)}
        ui.onNodeWithTag("nav-vpn").performClick();ui.onNodeWithTag("welcome-login").performScrollTo().performClick();ready()
        ui.onNodeWithTag("account-code").performTextInput(f.getString("cabinet_code"));ui.onNodeWithTag("account-login").performClick()
        waitFor("onboarding login failed"){account().state.value.authenticated&&!account().state.value.busy}
        ui.onNodeWithTag("connect-account-phone").performScrollTo().performClick()
        waitFor("login did not create this phone and load routes"){!probe.state.value.running&&probe.state.value.profiles.size==10&&account().state.value.devices.optJSONArray("devices")?.length()==2}
        nav()
        val createdId=account().state.value.phoneId
        assertTrue("new app device was not bound",createdId.isNotBlank()&&createdId!=f.getString("device_id"))
        ui.onNodeWithTag("account-devices").performScrollTo().performClick();ready()
        ui.onNodeWithTag("device-card-$createdId").assertExists();ui.onNodeWithTag("current-account-device").assertExists()
        screenshot("devices-new")
        ui.onNodeWithText("← Кабинет").performScrollTo().performClick()
        ui.onNodeWithTag("account-logout").performScrollTo().performClick();confirm()
        app.cabinetTestOrigin=null
    }
    @Test fun b_nativeLoginDevicesSubscriptionInvoicesAndSupport(){
        app.cabinetTestOrigin=origin
        app.cabinet.clear()
        val f=fixture();val code=f.getString("cabinet_code")
        val unauthorized=URL("$origin/api/web/v1/account/me").openConnection() as HttpURLConnection
        try{unauthorized.setRequestProperty("Authorization","Bearer "+f.getString("token"));assertEquals("subscription token granted account access",401,unauthorized.responseCode)}finally{unauthorized.disconnect()}
        val probe=ViewModelProvider(ui.activity)[ProbeViewModel::class.java]
        ui.waitForIdle();waitFor("initial subscription refresh did not settle"){!probe.state.value.running}
        val importedLink=f.getString("subscription_url")+"?source=android"
        probe.importLink(importedLink)
        waitFor("existing subscription import failed"){!probe.state.value.running&&probe.state.value.profiles.size==10&&probe.activeSource()?.url==importedLink}
        nav()
        ui.onNodeWithTag("account-code").performTextInput("WRONG-CODE")
        ui.onNodeWithTag("account-login").performClick();ready()
        assertFalse(account().state.value.authenticated)
        ui.onNodeWithTag("account-code").performTextClearance();ui.onNodeWithTag("account-code").performTextInput(code)
        ui.onNodeWithTag("account-login").performClick()
        waitFor("native login failed"){account().state.value.authenticated&&!account().state.value.busy&&account().state.value.me.has("subscription")}
        assertEquals(30,account().state.value.me.getJSONObject("subscription").getInt("days_left"))
        assertTrue("session did not survive client recreation",CabinetApi(app).request("account/me").has("subscription"))
        val encrypted=java.io.File(app.noBackupFilesDir,"cabinet.enc").readBytes().toString(Charsets.ISO_8859_1)
        assertFalse("login code saved in plaintext",encrypted.contains(code));assertFalse(encrypted.contains("qw_session="))
        assertEquals("imported phone was not recognized on login",f.getString("device_id"),account().state.value.phoneId)
        val before=account().state.value.devices.getJSONArray("devices").length()
        ui.onNodeWithTag("connect-account-phone").performScrollTo().performClick()
        waitFor("account did not import its VPN subscription"){!probe.state.value.running&&probe.state.value.profiles.size==10&&ui.onAllNodesWithTag("vpn-power").fetchSemanticsNodes().isNotEmpty()}
        nav();assertEquals("existing phone consumed another paid slot",before,account().state.value.devices.getJSONArray("devices").length())
        ui.onNodeWithTag("account-devices").performScrollTo().performClick();ready()
        ui.onNodeWithTag("current-account-device").assertExists()
        val selfBounds=ui.onNodeWithTag("device-card-${f.getString("device_id")}").getUnclippedBoundsInRoot()
        assertTrue("device card is too tall",selfBounds.bottom-selfBounds.top<=100.dp)
        ui.onNodeWithText("Удалить устройство",substring=false).assertDoesNotExist()
        ui.onNodeWithTag("device-menu-${f.getString("device_id")}").performScrollTo().performClick()
        ui.onNodeWithTag("rename-device-${f.getString("device_id")}").performClick()
        ui.onNode(hasSetTextAction()).performTextClearance();ui.onNode(hasSetTextAction()).performTextInput("Мой Android")
        ui.onNodeWithText("Сохранить",substring=false).performClick();ready();ui.onNodeWithText("Мой Android").assertExists()
        ui.onNodeWithTag("device-menu-${f.getString("device_id")}").performScrollTo().performClick();ui.onNodeWithTag("block-device-${f.getString("device_id")}").performClick();confirm();ui.onNodeWithText("Заблокировано",substring=false).assertExists()
        ui.onNodeWithTag("device-menu-${f.getString("device_id")}").performScrollTo().performClick();ui.onNodeWithTag("block-device-${f.getString("device_id")}").performClick();confirm()
        screenshot("devices")
        ui.onNodeWithText("← Кабинет").performScrollTo().performClick()
        ui.onNodeWithTag("account-pause").performScrollTo().performClick();confirm()
        assertEquals("paused",account().state.value.me.getJSONObject("subscription").getString("status"))
        ui.onNodeWithTag("account-pause").performScrollTo().performClick();confirm()
        assertEquals("active",account().state.value.me.getJSONObject("subscription").getString("status"))
        ui.onNodeWithText("Мой кабинет",substring=false).performScrollTo();ui.waitForIdle();Thread.sleep(350)
        val bitmap=instrumentation.uiAutomation.takeScreenshot()!!;val bytes=java.io.ByteArrayOutputStream();bitmap.compress(Bitmap.CompressFormat.PNG,100,bytes);bitmap.recycle()
        val shot=URL("$origin/screenshots/account").openConnection() as HttpURLConnection
        try{shot.requestMethod="POST";shot.doOutput=true;shot.outputStream.use{it.write(bytes.toByteArray())};assertEquals(204,shot.responseCode)}finally{shot.disconnect()}
        ui.onNodeWithTag("account-plans").performScrollTo().performClick();ready()
        ui.onNodeWithTag("account-create-invoice").performScrollTo().performClick();ready()
        assertEquals("invoice",account().state.value.page);assertTrue(account().state.value.data.getJSONObject("invoice").optString("address").isNotBlank())
        ui.onNodeWithText("← Кабинет").performScrollTo().performClick()
        ui.onNodeWithTag("account-support").performScrollTo().performClick();ready()
        ui.onNodeWithTag("account-support-message").performScrollTo().performTextInput("Тест нативного кабинета")
        ui.onNodeWithTag("account-support-send").performScrollTo().performClick();ready()
        assertTrue(account().state.value.data.getJSONObject("current").getJSONArray("messages").length()>0)
        ui.onNodeWithText("← Кабинет").performScrollTo().performClick()
        ui.onNodeWithTag("account-logout").performScrollTo().performClick();confirm()
        assertFalse(account().state.value.authenticated);assertFalse(app.cabinet.signedIn)
        assertTrue("logout removed VPN subscriptions",probe.state.value.sources.isNotEmpty())
        app.cabinetTestOrigin=null
    }
}
