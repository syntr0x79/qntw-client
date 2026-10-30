package link.quantway.android

import android.app.UiModeManager
import android.content.res.Configuration
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assume.assumeTrue
import org.junit.Rule
import org.junit.Test

class TvPairTvScreenTest {
    @get:Rule val ui=createAndroidComposeRule<MainActivity>()
    @Test fun televisionDisplaysShortCodeFromServer(){
        val context=InstrumentationRegistry.getInstrumentation().targetContext
        assumeTrue(context.getSystemService(UiModeManager::class.java).currentModeType==Configuration.UI_MODE_TYPE_TELEVISION)
        val app=context.applicationContext as QuantwayApplication
        app.cabinetTestOrigin=InstrumentationRegistry.getArguments().getString("testbed")!!
        try{
            ui.waitUntil(15000){ui.onAllNodesWithTag("tv-pair-start").fetchSemanticsNodes().isNotEmpty()}
            Thread.sleep(1000)
            fun key(value:String){android.os.ParcelFileDescriptor.AutoCloseInputStream(InstrumentationRegistry.getInstrumentation().uiAutomation.executeShellCommand("input keyevent $value")).use{it.readBytes()};ui.waitForIdle()}
            repeat(4){key("KEYCODE_DPAD_UP")}
            key("KEYCODE_DPAD_RIGHT");key("KEYCODE_DPAD_CENTER")
            ui.waitUntil(30000){ui.onAllNodesWithTag("tv-pair-code").fetchSemanticsNodes().isNotEmpty()}
            ui.onNodeWithText("Код подключения").assertExists()
            val image=InstrumentationRegistry.getInstrumentation().uiAutomation.takeScreenshot()
            val bytes=java.io.ByteArrayOutputStream();image.compress(android.graphics.Bitmap.CompressFormat.PNG,100,bytes);image.recycle()
            val connection=java.net.URL(app.cabinetTestOrigin+"/screenshots/tv-pair").openConnection() as java.net.HttpURLConnection
            try{connection.requestMethod="POST";connection.doOutput=true;connection.outputStream.use{it.write(bytes.toByteArray())};check(connection.responseCode==204)}finally{connection.disconnect()}
            ui.onNodeWithTag("nav-vpn").performClick()
            Thread.sleep(500)
        }finally{app.cabinetTestOrigin=null}
    }
}
