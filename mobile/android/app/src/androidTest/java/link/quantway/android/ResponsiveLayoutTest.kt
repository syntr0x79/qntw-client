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
    @Test fun compactRouteHidesTechnicalAddressAndPlacesLatencyOnRight(){
        val technical="awg-in · 192.0.2.1:51821"
        ui.setContent{MaterialTheme{Surface{Column(Modifier.width(360.dp)){
            RouteCard(JSONObject().put("job_id","compact").put("label","AWG — Польша").put("engine","amneziawg").put("entry_label",technical),false,ProbeObservation("compact","", "ok",123,"wifi","Wi-Fi",0),"wifi"){}
        }}}}
        ui.onNodeWithText(technical).assertDoesNotExist()
        val card=ui.onNodeWithTag("route-compact").getUnclippedBoundsInRoot()
        assertTrue("ordinary route card should be compact",card.bottom-card.top<=72.dp)
        val name=ui.onNodeWithText("AWG — Польша",useUnmergedTree=true).fetchSemanticsNode().boundsInRoot
        val latency=ui.onNodeWithText("123 мс",useUnmergedTree=true).fetchSemanticsNode().boundsInRoot
        assertTrue("latency must sit to the right of the route name",latency.left>=name.right)
    }

    @Test fun redesignedThemesKeepLongRouteAndActionReadable(){
        val name="Длинное название маршрута в Соединённые Штаты Америки"
        var selected=false
        ui.setContent {
            val density=LocalDensity.current
            CompositionLocalProvider(LocalDensity provides Density(density.density,1.4f)) {
                Column(Modifier.width(320.dp).verticalScroll(rememberScrollState())) {
                    listOf(true,false).forEach { dark ->
                        QuantwayTheme(dark) {
                            Surface(color=MaterialTheme.colorScheme.background) {
                                Column(Modifier.padding(16.dp),verticalArrangement=Arrangement.spacedBy(10.dp)) {
                                    Heading(if(dark)"Тёмная тема" else "Светлая тема","Подключение и маршруты")
                                    RouteCard(JSONObject().put("job_id",if(dark)"dark-route" else "light-route").put("label",name).put("engine","xray"),false,null,"wifi") {selected=true}
                                }
                            }
                        }
                    }
                }
            }
        }
        ui.onNodeWithTag("route-dark-route").assertIsDisplayed().performClick()
        assertTrue(selected)
        ui.onNodeWithTag("route-light-route").performScrollTo().assertIsDisplayed()
    }

    @Test fun loadingScreensShowBrandAndRealProgress(){
        ui.setContent {
            QuantwayTheme(true) {
                Column(Modifier.width(320.dp).verticalScroll(rememberScrollState())) {
                    Box(Modifier.height(500.dp)) { StartupSplash() }
                    SubscriptionLoading()
                }
            }
        }
        ui.onNodeWithTag("startup-splash").assertIsDisplayed()
        ui.onNodeWithText("Quantway").assertIsDisplayed()
        ui.onNodeWithTag("subscription-loading").performScrollTo().assertIsDisplayed()
        ui.onNodeWithText("Загружаем подписку").assertIsDisplayed()
    }

    @Test fun connectionSummaryShowsMeasuredDataAndOpensRoutes(){
        var clicked=false
        ui.setContent {
            val density=LocalDensity.current
            CompositionLocalProvider(LocalDensity provides Density(density.density,1.5f)) {
                QuantwayTheme(true) {
                    ConnectionSummary(JSONObject().put("job_id","route").put("label","Нидерланды — Амстердам").put("protocol","vless").put("transport","xhttp"),true,true,"Wi-Fi",ProbeObservation("route","", "ok",123,"wifi","Wi-Fi",0),"wifi") {clicked=true}
                }
            }
        }
        ui.onNodeWithText("Нидерланды — Амстердам").assertIsDisplayed()
        ui.onNodeWithText("Wi-Fi").assertIsDisplayed()
        ui.onNodeWithText("123 мс").assertIsDisplayed()
        ui.onNodeWithText("Автоматически").assertIsDisplayed()
        ui.onNodeWithTag("selected-route").assertIsDisplayed().performClick()
        assertTrue(clicked)
    }

    @Test fun compactRouteFilterSwitchesModeAndProtocol(){
        val all=androidx.compose.runtime.mutableStateOf(true)
        val protocol=androidx.compose.runtime.mutableStateOf("Все протоколы")
        ui.setContent {
            QuantwayTheme(true) {
                RouteFilters(all.value,protocol.value,listOf("Все протоколы","AWG","Xray"),{all.value=it},{protocol.value=it})
            }
        }
        ui.onNodeWithTag("routes-filters").assertIsDisplayed().performClick()
        ui.onNodeWithTag("filter-show-best").assertIsDisplayed().performClick()
        assertFalse(all.value)
        ui.onNodeWithTag("routes-filters").performClick()
        ui.onNodeWithTag("protocol-AWG").performClick()
        assertEquals("AWG",protocol.value)
        ui.onNodeWithText("Лучшие маршруты · AWG").assertIsDisplayed()
    }

}
