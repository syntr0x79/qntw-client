package link.quantway.android

import android.content.res.Configuration
import android.app.UiModeManager
import android.os.ParcelFileDescriptor
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assume.assumeTrue
import org.junit.Rule
import org.junit.Test

class TvNavigationTest {
    @get:Rule val ui=createAndroidComposeRule<MainActivity>()
    private fun key(value:String){ParcelFileDescriptor.AutoCloseInputStream(InstrumentationRegistry.getInstrumentation().uiAutomation.executeShellCommand("input keyevent $value")).use{it.readBytes()};ui.waitForIdle()}
    @Test fun remoteNavigatesLoginAndReturnsFromKeyboard(){
        val app=InstrumentationRegistry.getInstrumentation().targetContext
        assumeTrue(app.getSystemService(UiModeManager::class.java).currentModeType==Configuration.UI_MODE_TYPE_TELEVISION)
        ui.waitUntil(15000){ui.onAllNodesWithTag("welcome-login").fetchSemanticsNodes().isNotEmpty()}
        Thread.sleep(1000)
        repeat(4){key("KEYCODE_DPAD_UP")}
        key("KEYCODE_DPAD_DOWN")
        key("KEYCODE_DPAD_DOWN")
        key("KEYCODE_DPAD_CENTER")
        ui.waitUntil(15000){ui.onAllNodesWithTag("account-code").fetchSemanticsNodes().isNotEmpty()}
        Thread.sleep(300)
        key("KEYCODE_DPAD_RIGHT")
        key("KEYCODE_DPAD_CENTER")
        key("KEYCODE_BACK")
        key("KEYCODE_DPAD_LEFT")
        key("KEYCODE_DPAD_UP")
        key("KEYCODE_DPAD_CENTER")
        ui.waitUntil(15000){ui.onAllNodesWithText("Нет загруженных маршрутов").fetchSemanticsNodes().isNotEmpty()}
    }
}
