package link.quantway.android

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.text.TextLayoutResult
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.unit.Density
import androidx.compose.ui.unit.dp
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test

class ResponsiveLayoutTest {
    @get:Rule val ui = createComposeRule()

    @Test fun narrowScreenWithLargeFontKeepsNamesStatusAndActionsAccessible() {
        val name = "Очень длинное название маршрута через Соединённые Штаты Америки и дополнительную маску"
        val value = "Провайдер с очень длинным названием подписки и дополнительными сведениями о подключении"
        var clicked = false
        ui.setContent {
            val density = LocalDensity.current
            CompositionLocalProvider(LocalDensity provides Density(density.density, 1.6f)) {
                MaterialTheme {
                    Surface {
                        Column(Modifier.width(320.dp).verticalScroll(rememberScrollState()).padding(16.dp), verticalArrangement=Arrangement.spacedBy(10.dp)) {
                            AccountNotice("Подписка активна.\n\n\nДоступ восстановлен.")
                            Heading("Проверка доступности маршрутов", "Длинный текст пояснения переносится без наложения на соседние элементы")
                            RouteCard(JSONObject().put("job_id", "long").put("label", name).put("engine", "xray").put("entry_label", "Маска с очень длинным названием"), false, null, "test") { clicked = true }
                            CompactDeviceRow(JSONObject().put("id", "phone").put("name", name), true, false, {}, {}, {}, {})
                            Surface(Modifier.testTag("detail")) { Detail("Подписка", value) }
                            HelpItem("Как работает подключение?", "Развёрнутый ответ справки")
                        }
                    }
                }
            }
        }
        ui.onNodeWithText("Подписка активна.\nДоступ восстановлен.").assertExists()
        ui.onNodeWithTag("route-long").performScrollTo().performClick()
        assertTrue("route remains actionable", clicked)
        ui.onNodeWithText("Не проверен").assertExists()
        ui.onNodeWithTag("device-menu-phone").performScrollTo().assertIsDisplayed().performClick()
        ui.onNodeWithTag("rename-device-phone").assertIsDisplayed()
        ui.onNodeWithTag("rename-device-phone").performClick()
        ui.onNodeWithText(value).performScrollTo().assertIsDisplayed()
        val layouts = mutableListOf<TextLayoutResult>()
        ui.onNodeWithText(value).performSemanticsAction(SemanticsActions.GetTextLayoutResult) { it(layouts) }
        assertTrue("long detail wraps", layouts.single().lineCount > 1)
        assertFalse("long detail is clipped", layouts.single().hasVisualOverflow)
        val route = ui.onNodeWithTag("route-long").fetchSemanticsNode().boundsInRoot
        val device = ui.onNodeWithTag("device-card-phone").fetchSemanticsNode().boundsInRoot
        assertTrue("cards must not overlap", route.bottom <= device.top)
        ui.onNodeWithText("Как работает подключение?").performScrollTo().performClick()
        ui.onNodeWithText("Развёрнутый ответ справки").performScrollTo().assertIsDisplayed()
    }
}
