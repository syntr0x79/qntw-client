package link.quantway.android

import android.app.Activity
import android.content.Intent
import android.net.VpnService
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.foundation.*
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.List
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.rotate
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.toArgb
import androidx.core.view.WindowCompat
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.luminance
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.semantics.*
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.layout.ContentScale
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.viewmodel.compose.viewModel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.first
import org.json.JSONObject

class MainActivity:ComponentActivity(){
    override fun onCreate(savedInstanceState:Bundle?){
        super.onCreate(savedInstanceState)
        setContent {
            val app=application as QuantwayApplication
            val prefs by app.preferences.state.collectAsState()
            val dark=when(prefs.theme){"dark"->true;"light"->false;else->isSystemInDarkTheme()}
            var splashVisible by rememberSaveable { mutableStateOf(savedInstanceState==null) }
            LaunchedEffect(Unit){if(splashVisible){delay(850);splashVisible=false}}
            val colors=if(dark)QuantwayDark else QuantwayLight
            SideEffect {
                WindowCompat.getInsetsController(window,window.decorView).isAppearanceLightStatusBars=!dark&&!splashVisible
                WindowCompat.getInsetsController(window,window.decorView).isAppearanceLightNavigationBars=!dark&&!splashVisible
                window.decorView.setBackgroundColor((if(splashVisible) Color(0xff06101a) else colors.background).toArgb())
            }
            val television=isTelevision()
            CompositionLocalProvider(LocalTvMode provides television){QuantwayTheme(dark){if(splashVisible)StartupSplash() else QuantwayApp()}}
        }
    }
}

@Composable internal fun StartupSplash(){
    Box(Modifier.fillMaxSize().background(Color(0xff06101a)).testTag("startup-splash")){
        Image(painterResource(R.drawable.earth_splash),contentDescription=null,contentScale=ContentScale.Crop,modifier=Modifier.fillMaxSize())
        Box(Modifier.fillMaxSize().background(Brush.verticalGradient(listOf(Color(0xff07131e).copy(alpha=.45f),Color.Transparent,Color(0xff06101a).copy(alpha=.6f)))))
        Column(Modifier.fillMaxWidth().align(Alignment.Center).padding(horizontal=24.dp),horizontalAlignment=Alignment.CenterHorizontally,verticalArrangement=Arrangement.spacedBy(12.dp)){
            Image(painterResource(R.drawable.quantway_brand),contentDescription=null,contentScale=ContentScale.Crop,modifier=Modifier.size(78.dp).clip(RoundedCornerShape(22.dp)))
            Text("Quantway",fontSize=28.sp,lineHeight=34.sp,fontWeight=FontWeight.SemiBold,color=Color.White)
            Text("Безопасный доступ к нужным ресурсам",style=MaterialTheme.typography.bodyMedium,textAlign=TextAlign.Center,color=Color(0xffccdae1))
        }
    }
}

private enum class Page{VPN,ROUTES,SUBSCRIPTIONS,SETTINGS,ACCOUNT,IMPORT,PROBE,DETAILS,SPLIT,EXCEPTIONS,DIAGNOSTICS,REPORT,HELP,ABOUT,PAIR_TV,PAIR_PHONE}
private data class Confirmation(val title:String,val message:String,val action:()->Unit)

@OptIn(ExperimentalMaterial3Api::class,ExperimentalLayoutApi::class)
@Composable private fun QuantwayApp(model:ProbeViewModel=viewModel()){
    val cabinet:CabinetViewModel=viewModel()
    val pairing:TvPairViewModel=viewModel()
    val cabinetState by cabinet.state.collectAsState()
    val state by model.state.collectAsState();val prefs by model.preferences.state.collectAsState();val network by model.physical.state.collectAsState()
    val phase by QuantwayVpnService.phase.collectAsState();val active by QuantwayVpnService.activeProfile.collectAsState();val started by QuantwayVpnService.startedAt.collectAsState();val applied by QuantwayVpnService.appliedRules.collectAsState()
    val context=LocalContext.current;val app=context.applicationContext as QuantwayApplication
    val tv=LocalTvMode.current
    val tvMenuFocus=remember{listOf(Page.VPN,Page.ROUTES,Page.ACCOUNT,Page.SETTINGS).associateWith{FocusRequester()}}
    var pageName by rememberSaveable{mutableStateOf(Page.VPN.name)};val page=Page.valueOf(pageName)
    var confirmation by remember{mutableStateOf<Confirmation?>(null)}
    var link by rememberSaveable{mutableStateOf("")};var domain by rememberSaveable{mutableStateOf("")};var domainError by remember{mutableStateOf("")}
    var filter by rememberSaveable{mutableStateOf("Все протоколы")};var showAll by rememberSaveable{mutableStateOf(true)};var showUnavailable by rememberSaveable{mutableStateOf(false)}
    var importing by rememberSaveable{mutableStateOf(false)}
    var elapsed by remember{mutableLongStateOf(0)}
    val online=phase=="connected";val serviceRunning=phase!="off";val busy=state.running
    val source=model.activeSource();val inactive=source?.api==true&&model.subscriptionStatus() in listOf("paused","expired","unauthorized")
    val selected=active?:state.profiles.firstOrNull{it.optString("job_id")==state.selectedId}
    fun go(p:Page){pageName=p.name}
    fun openCabinet(){go(Page.ACCOUNT)}
    val consent=rememberLauncherForActivityResult(ActivityResultContracts.StartActivityForResult()){if(it.resultCode==Activity.RESULT_OK)model.connect(context)}
    fun connect(){
        if(inactive){openCabinet();return}
        if(!serviceRunning && !model.hasFreshRecommendation()){
            go(Page.PROBE)
            if(!model.state.value.running)model.run(prefs.shareReports,selectBest=true)
            return
        }
        val intent=VpnService.prepare(context);if(intent==null)model.connect(context)else consent.launch(intent)
    }
    fun choose(id:String?,auto:Boolean=false){
        val change={if(auto)model.selectAutomatic()else id?.let{model.select(it)};go(Page.VPN)}
        if(serviceRunning){confirmation=Confirmation("Сменить маршрут?","Текущее подключение продолжит работать до подтверждения. Затем VPN переподключится."){change();connect()}}else change()
    }
    fun back(){go(when(page){Page.EXCEPTIONS->Page.SPLIT;Page.SPLIT,Page.DIAGNOSTICS,Page.HELP,Page.ABOUT,Page.REPORT->Page.SETTINGS;Page.IMPORT->Page.SUBSCRIPTIONS;Page.SUBSCRIPTIONS,Page.PAIR_PHONE->Page.ACCOUNT;else->Page.VPN})}
    BackHandler(page !in listOf(Page.VPN)){back()}
    LaunchedEffect(cabinetState.me.optJSONObject("subscription")?.toString()){
        if(cabinetState.authenticated&&SubscriptionLink.sameDevice(source?.url?:"",app.cabinet.deviceLink)){
            if(cabinetState.me.optJSONObject("subscription")?.optString("status") in listOf("paused","expired")){
                if(model.state.value.activity=="probe")model.cancel()
                if(serviceRunning)model.disconnect(context)
            }
            model.state.first{!it.running}
            if(SubscriptionLink.sameDevice(model.activeSource()?.url?:"",app.cabinet.deviceLink))model.refresh()
        }
    }
    LaunchedEffect(started,phase){while(online&&started>0){elapsed=(android.os.SystemClock.elapsedRealtime()-started)/1000;delay(1000)};if(!online)elapsed=0}
    LaunchedEffect(state.running,state.sources.size,state.message){if(importing&&!state.running&&state.message=="Подписка добавлена"){importing=false;link="";go(if(serviceRunning)Page.SUBSCRIPTIONS else Page.VPN)}}
    val owner=LocalLifecycleOwner.current
    var foreground by remember { mutableStateOf(false) }
    var resumeGeneration by remember { mutableIntStateOf(0) }
    DisposableEffect(owner,model){
        val observer=LifecycleEventObserver{_,event->
            if(event==Lifecycle.Event.ON_RESUME){
                foreground=true
                if(model.activeSource()!=null&&!model.state.value.running)model.refresh()
                resumeGeneration++
            } else if(event==Lifecycle.Event.ON_PAUSE) foreground=false
        }
        owner.lifecycle.addObserver(observer);onDispose{owner.lifecycle.removeObserver(observer)}
    }
    LaunchedEffect(foreground,resumeGeneration,network.epoch,state.sourceId,serviceRunning){
        if(foreground && !serviceRunning && state.sourceId.isNotBlank()){
            model.state.first{!it.running}
            // Let the import flow return to the home screen before starting work.
            delay(200)
            model.state.first{!it.running}
            val current=model.state.value
            val blocked=model.activeSource()?.api==true && model.subscriptionStatus() in listOf("paused","expired","unauthorized")
            if(!blocked && QuantwayVpnService.current==null && current.profiles.isNotEmpty() && model.physical.state.value.network!=null && !model.hasFreshCheck()) model.run(prefs.shareReports,selectBest=true)
        }
    }
    val topPage=when(page){Page.ROUTES->Page.ROUTES;Page.ACCOUNT,Page.SUBSCRIPTIONS,Page.IMPORT,Page.PAIR_PHONE->Page.ACCOUNT;Page.SETTINGS,Page.SPLIT,Page.EXCEPTIONS,Page.DIAGNOSTICS,Page.REPORT,Page.HELP,Page.ABOUT->Page.SETTINGS;else->Page.VPN}
    LaunchedEffect(tv,page,if(page==Page.ACCOUNT)cabinetState.page else "") {if(tv){delay(100);tvMenuFocus.getValue(topPage).requestFocus()}}
    val pendingRules=serviceRunning&&applied!=routingKey(app)
    Scaffold(modifier=Modifier.fillMaxSize().systemBarsPadding().then(if(tv)Modifier.padding(horizontal=36.dp,vertical=20.dp)else Modifier),containerColor=MaterialTheme.colorScheme.background,topBar={
        if(!tv){
        Row(Modifier.fillMaxWidth().padding(horizontal=20.dp).heightIn(min=52.dp),verticalAlignment=Alignment.CenterVertically,horizontalArrangement=Arrangement.SpaceBetween){
            if(page !in listOf(Page.VPN,Page.ROUTES,Page.ACCOUNT,Page.SETTINGS))IconButton(onClick={back()},modifier=Modifier.testTag("back")){Icon(Icons.AutoMirrored.Filled.ArrowBack,"Назад")}else Row(verticalAlignment=Alignment.CenterVertically,horizontalArrangement=Arrangement.spacedBy(9.dp)) {
                Image(painterResource(R.drawable.quantway_brand),contentDescription=null,contentScale=ContentScale.Crop,modifier=Modifier.size(30.dp).clip(RoundedCornerShape(9.dp)))
                Text("Quantway",style=MaterialTheme.typography.titleMedium,fontWeight=FontWeight.Bold)
            }
        }
        }
    },bottomBar={
        if(!tv)NavigationBar(containerColor=MaterialTheme.colorScheme.surfaceContainerLow,tonalElevation=0.dp){listOf(Triple(Page.VPN,"Главная",Icons.Default.Home),Triple(Page.ROUTES,"Маршруты",Icons.AutoMirrored.Filled.List),Triple(Page.ACCOUNT,"Кабинет",Icons.Default.AccountCircle),Triple(Page.SETTINGS,"Настройки",Icons.Default.Settings)).forEach{(id,label,icon)->NavigationBarItem(selected=topPage==id,onClick={go(id)},icon={Icon(icon,null)},label={Text(label,fontSize=11.sp)},modifier=Modifier.testTag("nav-${id.name.lowercase()}"),colors=NavigationBarItemDefaults.colors(selectedIconColor=MaterialTheme.colorScheme.primary,selectedTextColor=MaterialTheme.colorScheme.primary,indicatorColor=MaterialTheme.colorScheme.secondaryContainer))}}
    }){padding->
        val scroll=rememberScrollState();LaunchedEffect(page,if(page==Page.ACCOUNT)cabinetState.page else ""){scroll.scrollTo(0)}
        Row(Modifier.fillMaxSize().padding(padding).consumeWindowInsets(padding)){
        if(tv)Column(Modifier.width(184.dp).fillMaxHeight().padding(end=16.dp),verticalArrangement=Arrangement.spacedBy(5.dp)){
            Row(Modifier.padding(start=12.dp,top=8.dp,bottom=28.dp),verticalAlignment=Alignment.CenterVertically,horizontalArrangement=Arrangement.spacedBy(10.dp)){
                Image(painterResource(R.drawable.quantway_brand),contentDescription=null,contentScale=ContentScale.Crop,modifier=Modifier.size(30.dp).clip(RoundedCornerShape(9.dp)))
                Text("Quantway",style=MaterialTheme.typography.titleMedium,fontWeight=FontWeight.Bold)
            }
            listOf(Triple(Page.VPN,"Главная",Icons.Default.Home),Triple(Page.ROUTES,"Маршруты",Icons.AutoMirrored.Filled.List),Triple(Page.ACCOUNT,"Кабинет",Icons.Default.AccountCircle),Triple(Page.SETTINGS,"Настройки",Icons.Default.Settings)).forEach{(id,label,icon)->
                val selected=topPage==id
                Surface(onClick={go(id)},modifier=Modifier.fillMaxWidth().focusRequester(tvMenuFocus.getValue(id)).tvFocus().testTag("nav-${id.name.lowercase()}"),shape=RoundedCornerShape(14.dp),color=if(selected)MaterialTheme.colorScheme.secondaryContainer.copy(alpha=.65f) else Color.Transparent){
                    Row(Modifier.heightIn(min=48.dp).padding(start=4.dp,end=10.dp,top=8.dp,bottom=8.dp),verticalAlignment=Alignment.CenterVertically,horizontalArrangement=Arrangement.spacedBy(10.dp)){
                        Box(Modifier.width(3.dp).height(22.dp).background(if(selected)MaterialTheme.colorScheme.primary else Color.Transparent,RoundedCornerShape(50)))
                        Icon(icon,null,Modifier.size(19.dp),tint=if(selected)MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant)
                        Text(label,style=MaterialTheme.typography.labelLarge,color=if(selected)MaterialTheme.colorScheme.onSurface else MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                }
            }
        }
        Box(Modifier.weight(1f).fillMaxHeight(),contentAlignment=Alignment.TopStart){
        Column(Modifier.fillMaxHeight().widthIn(max=760.dp).fillMaxWidth().imePadding().verticalScroll(scroll).padding(horizontal=20.dp).padding(bottom=24.dp),verticalArrangement=Arrangement.spacedBy(12.dp)){
            if(tv && page !in listOf(Page.VPN,Page.ROUTES,Page.ACCOUNT,Page.SETTINGS))TextButton(onClick={back()},modifier=Modifier.tvFocus().testTag("back")){Icon(Icons.AutoMirrored.Filled.ArrowBack,null,Modifier.size(18.dp));Spacer(Modifier.width(8.dp));Text("Назад")}
            when(page){
                Page.VPN->{
                    if(state.profiles.isEmpty()&&!inactive){
                        if(source?.api==true){
                            Spacer(Modifier.height(16.dp));Heading("Пока нет доступных маршрутов","Обновите подписку через минуту. Проверить состояние доступа можно в кабинете.")
                            Primary("Обновить маршруты",enabled=!busy){model.refresh()}
                            Secondary("Открыть кабинет"){go(Page.ACCOUNT)}
                        }else if(tv){
                        Spacer(Modifier.height(12.dp));Heading("Добро пожаловать в Quantway","Выберите способ подключения телевизора.")
                        Primary("Подключить с телефона",modifier=Modifier.testTag("tv-pair-start")){go(Page.PAIR_TV)}
                        Row(Modifier.fillMaxWidth(),horizontalArrangement=Arrangement.spacedBy(16.dp)){
                            Column(Modifier.weight(1f)){Panel{Text("Аккаунт Quantway",style=MaterialTheme.typography.titleLarge);Muted("Войдите, чтобы управлять подпиской.");Primary(if(cabinetState.authenticated)"Открыть кабинет"else "Войти в Quantway",Modifier.testTag("welcome-login")){go(Page.ACCOUNT)}}}
                            Column(Modifier.weight(1f)){Panel{Text("Другая подписка",style=MaterialTheme.typography.titleLarge);Muted("Добавьте ссылку VPN-провайдера.");Secondary("Добавить подписку",modifier=Modifier.testTag("add-first")){go(Page.IMPORT)}}}
                        }
                        }else{
                        Spacer(Modifier.height(16.dp));Heading("Добро пожаловать в Quantway","Выберите, как вы хотите подключиться.")
                        Panel {
                            Text("Аккаунт Quantway",style=MaterialTheme.typography.titleMedium)
                            Muted("Управляйте подпиской и устройствами в приложении.")
                            Primary(if(cabinetState.authenticated)"Открыть кабинет"else "Войти в Quantway",Modifier.testTag("welcome-login")){go(Page.ACCOUNT)}
                        }
                        Panel {
                            Text("Другая подписка",style=MaterialTheme.typography.titleMedium)
                            Muted("Добавьте ссылку вашего VPN-провайдера.")
                            Secondary("У меня есть ссылка подписки",modifier=Modifier.testTag("add-first")){go(Page.IMPORT)}
                        }
                        }
                    }else if(inactive){
                        Spacer(Modifier.height(16.dp));Heading(if(model.subscriptionStatus()=="paused")"Подписка на паузе"else "Продолжим?",if(model.subscriptionStatus()=="paused")"Оставшиеся дни сохраняются. Возобновите доступ в кабинете."else "Продлите подписку, чтобы снова подключиться.")
                        Panel{Text(source?.name?:"Quantway",style=MaterialTheme.typography.labelLarge);model.days()?.let{Text("$it дней",fontSize=35.sp,fontWeight=FontWeight.Medium)};Primary("Открыть кабинет"){openCabinet()}}
                    }else if(tv){
                        val title=if(state.activity=="probe"&&busy)"Ищем лучший маршрут" else when(phase){"connected"->"Вы в сети";"waiting"->"Ожидаем сеть";"reconnecting"->"Восстанавливаем связь";"connecting"->"Подключаемся";else->"Всё готово"}
                        Heading("Подключение",if(online)"Защищённое соединение работает" else "Выберите маршрут и подключите VPN")
                        Row(Modifier.fillMaxWidth(),horizontalArrangement=Arrangement.spacedBy(16.dp)){
                            Column(Modifier.weight(1f),verticalArrangement=Arrangement.spacedBy(12.dp)){
                                Panel{Text(title,modifier=Modifier.testTag("vpn-title"),style=MaterialTheme.typography.titleLarge);Muted(if(online)"VPN подключён · ${formatDuration(elapsed)}" else if(state.activity=="probe"&&busy)"Проверяем маршруты в текущей сети" else network.label)
                                    Power(online,if(online)"Отключить"else if(serviceRunning)"Отменить"else "Подключить",!busy||serviceRunning){if(serviceRunning)model.disconnect(context)else connect()}
                                    if(state.activity=="probe"&&busy)CompactProbeProgress(state.completed,state.total)
                                }
                                Secondary(if(state.activity=="probe"&&busy)"Ход проверки"else "Проверить маршруты",enabled=!busy||state.activity=="probe",modifier=Modifier.testTag("home-check-routes")){go(Page.PROBE)}
                                Secondary("Подписки",modifier=Modifier.testTag("tv-home-subscriptions")){go(Page.SUBSCRIPTIONS)}
                            }
                            Column(Modifier.weight(1f),verticalArrangement=Arrangement.spacedBy(12.dp)){
                                selected?.let{profile->ConnectionSummary(profile,online,state.automatic,network.label,state.observations[profile.optString("job_id")],network.epoch){go(Page.ROUTES)}}?:Empty("Маршрут не выбран","Дождитесь проверки или выберите маршрут.")
                                if(state.checkedAt>0)Muted("Проверено ${relativeTime(state.checkedAt)} · ${state.checkedNetwork}")
                            }
                        }
                        selected?.let{profile->HomeRoutes(profile,state.profiles,state.observations,network.epoch,onChoose={choose(it)})}
                        if(busy && state.activity!="probe"){LinearProgressIndicator(Modifier.fillMaxWidth());Muted(state.message)}
                        if(state.message.startsWith("Не ")||state.message.startsWith("Ссылка "))Text(state.message,color=MaterialTheme.colorScheme.error,style=MaterialTheme.typography.bodySmall)
                    }else{
                        val title=if(state.activity=="probe"&&busy)"Ищем лучший маршрут" else when(phase){"connected"->"Вы в сети";"waiting"->"Ожидаем сеть";"reconnecting"->"Восстанавливаем связь";"connecting"->"Подключаемся";else->"Всё готово"}
                        Surface(Modifier.fillMaxWidth(),shape=RoundedCornerShape(14.dp),color=MaterialTheme.colorScheme.surfaceContainerHigh){
                            Row(Modifier.padding(horizontal=14.dp,vertical=12.dp),verticalAlignment=Alignment.CenterVertically,horizontalArrangement=Arrangement.spacedBy(10.dp)){
                                Box(Modifier.size(9.dp).background(if(online)Color(0xff35bd91) else MaterialTheme.colorScheme.onSurfaceVariant,CircleShape))
                                Column(Modifier.weight(1f)){
                                    Text(title,style=MaterialTheme.typography.titleSmall,modifier=Modifier.testTag("vpn-title"))
                                    Text(if(online)"VPN подключён" else if(state.activity=="probe"&&busy)"Проверяем маршруты в текущей сети" else network.label,style=MaterialTheme.typography.bodySmall,color=MaterialTheme.colorScheme.onSurfaceVariant)
                                }
                                if(online)Text(formatDuration(elapsed),style=MaterialTheme.typography.labelMedium,color=MaterialTheme.colorScheme.onSurfaceVariant)
                            }
                        }
                        selected?.let { profile ->
                            ConnectionSummary(profile,online,state.automatic,network.label,state.observations[profile.optString("job_id")],network.epoch){go(Page.ROUTES)}
                        } ?: Empty("Маршрут не выбран","Выберите маршрут или дождитесь завершения проверки.")
                        Power(online,if(online)"Отключить"else if(serviceRunning)"Отменить"else "Подключить",!busy||serviceRunning){if(serviceRunning)model.disconnect(context)else connect()}
                        if(state.activity=="probe"&&busy)CompactProbeProgress(state.completed,state.total)
                        if(!serviceRunning && !state.automatic && state.recommendedId.isNotBlank() && state.recommendedId!=state.selectedId && state.checkedEpoch==network.epoch){
                            state.profiles.firstOrNull{it.optString("job_id")==state.recommendedId}?.let{best->
                                Secondary("Самый быстрый: ${best.optString("label")}",modifier=Modifier.testTag("home-recommended-route")){choose(state.recommendedId)}
                            }
                        }
                        Secondary(if(state.activity=="probe"&&busy)"Ход проверки"else "Проверить маршруты",enabled=!busy||state.activity=="probe",modifier=Modifier.testTag("home-check-routes")){go(Page.PROBE)}
                        selected?.let{profile->HomeRoutes(profile,state.profiles,state.observations,network.epoch,onChoose={choose(it)})}
                        if(state.checkedAt>0)Muted("Последняя проверка: ${relativeTime(state.checkedAt)} · ${state.checkedNetwork}")
                        if(online)Setting("О соединении","Сеть и текущий маршрут",Icons.Default.Info){go(Page.DETAILS)}
                        Setting("Подписка",listOfNotNull(source?.name,model.days()?.let{"$it дней"}).joinToString(" · "),Icons.Default.AccountCircle){go(Page.SUBSCRIPTIONS)}
                    }
                    if(busy && state.activity!="probe"){LinearProgressIndicator(Modifier.fillMaxWidth());Muted(state.message)}
                    if(state.message.startsWith("Не ")||state.message.startsWith("Ссылка "))Text(state.message,color=MaterialTheme.colorScheme.error,style=MaterialTheme.typography.bodySmall)
                }
                Page.ROUTES->{
                    Heading("Маршруты",if(state.checkedAt>0)"Проверены ${relativeTime(state.checkedAt)} · ${state.checkedNetwork}"else "Доступность в этой сети ещё не проверена")
                    if(state.profiles.isNotEmpty())Secondary(if(state.activity=="probe"&&busy)"Ход проверки"else "Проверить доступность",enabled=!busy||state.activity=="probe",modifier=Modifier.testTag("routes-check")){go(Page.PROBE)}
                    if(state.profiles.isEmpty()){Empty("Нет загруженных маршрутов","Добавьте подписку или откройте кабинет, если доступ приостановлен.");Secondary("К подпискам"){go(Page.SUBSCRIPTIONS)}}else{
                        val families=listOf("Все протоколы")+state.profiles.map{family(it)}.distinct()
                        RouteFilters(showAll,filter,families,onShowAll={showAll=it},onProtocol={filter=it})
                        val all=state.profiles.filter{filter=="Все протоколы"||family(it)==filter}
                        val failed=all.filter{state.observations[it.optString("job_id")]?.let{v->v.epoch==network.epoch&&v.outcome=="fail"}==true}
                        val available=(all-failed.toSet()).sortedBy{p->state.observations[p.optString("job_id")]?.takeIf{it.epoch==network.epoch&&it.outcome=="ok"}?.elapsedMS?:Long.MAX_VALUE}
                        val shown=if(showAll)available+failed else available.take(6)
                        val selectedRoute=active?.optString("job_id")?:state.selectedId
                        if(shown.isEmpty())Empty("Нет подходящих маршрутов","Измените фильтр или запустите проверку.")
                        if(tv)TvRouteGrid(shown,selectedRoute,state.observations,network.epoch){choose(it)} else shown.forEach{p->RouteCard(p,p.optString("job_id")==selectedRoute,state.observations[p.optString("job_id")],network.epoch){choose(p.getString("job_id"))}}
                        if(!showAll&&failed.isNotEmpty()){SecondaryAction(if(showUnavailable)"Скрыть недоступные"else "Недоступные (${failed.size})"){showUnavailable=!showUnavailable};if(showUnavailable){if(tv)TvRouteGrid(failed,selectedRoute,state.observations,network.epoch){choose(it)} else failed.forEach{p->RouteCard(p,p.optString("job_id")==selectedRoute,state.observations[p.optString("job_id")],network.epoch){choose(p.getString("job_id"))}}}}
                        Muted("Время ответа относится к проверочному запросу, а не к скорости загрузки. После смены сети замеры считаются устаревшими.")
                    }
                }
                Page.ACCOUNT->{CabinetScreen(cabinet,onSubscriptions={go(Page.SUBSCRIPTIONS)},onPairTv={go(Page.PAIR_PHONE)},onLink={url->model.importLink(url);go(if(serviceRunning)Page.SUBSCRIPTIONS else Page.VPN)})}
                Page.PAIR_TV->{TvPairReceiver(pairing,model,onDone={go(if(serviceRunning)Page.SUBSCRIPTIONS else Page.VPN)})}
                Page.PAIR_PHONE->{TvPairSender(pairing,model,cabinetState.authenticated,onAccount={go(Page.ACCOUNT)},onSubscriptions={go(Page.SUBSCRIPTIONS)})}
                Page.SUBSCRIPTIONS->{
                    Heading("Подписки","Ваш VPN и другие подключения — в одном месте.")
                    if(tv)Secondary("Получить подписку с телефона",modifier=Modifier.testTag("tv-pair-again")){go(Page.PAIR_TV)}
                    Secondary("Добавить подписку",enabled=!busy,modifier=Modifier.testTag("add-subscription")){go(Page.IMPORT)}
                    if(source!=null)Setting("Обновить маршруты","Загрузить изменения провайдера",Icons.Default.Refresh){model.refresh()}
                    state.sources.forEach{src->val selectedSource=src.id==state.sourceId;Panel(selected=selectedSource){Row(Modifier.fillMaxWidth(),horizontalArrangement=Arrangement.SpaceBetween){Text(src.name,modifier=Modifier.weight(1f).padding(end=8.dp),style=MaterialTheme.typography.titleMedium,maxLines=2,overflow=TextOverflow.Ellipsis);if(selectedSource)Tag("Выбрана")};src.cache.optJSONObject("subscription")?.let{info->Text("${info.optInt("days_available")} дней",fontSize=32.sp,fontWeight=FontWeight.Medium);Muted(when(info.optString("status")){"active"->"Подписка активна";"paused"->"На паузе · дни сохраняются";else->"Доступ закончился"})};val count=src.cache.optJSONArray("profiles")?.length()?:0;Muted("$count маршрутов · ${relativeTime(src.updatedAt)}");if(src.cache.optInt("skipped")>0)Muted("Неподдерживаемых конфигураций пропущено: ${src.cache.optInt("skipped")}")
                        Row(horizontalArrangement=Arrangement.spacedBy(8.dp)){if(!selectedSource)Button(onClick={val change={model.activate(src.id);go(Page.VPN)};if(serviceRunning)confirmation=Confirmation("Сменить подписку?","Переподключимся через выбранного провайдера. До подтверждения текущий VPN продолжает работать."){change();connect()}else change()},enabled=!busy,modifier=Modifier.weight(1f)){Text("Выбрать")};if(src.api)OutlinedButton(onClick={openCabinet()},modifier=Modifier.weight(1f)){Text("Кабинет")};IconButton(onClick={confirmation=Confirmation("Удалить подписку?","Ссылка будет удалена с этого устройства. Подписка у провайдера сохранится."){if(selectedSource&&serviceRunning)model.disconnect(context);model.removeSource(src.id)}}){Icon(Icons.Default.Delete,"Удалить подписку ${src.name}")}}
                    }}
                    if(busy)LinearProgressIndicator(Modifier.fillMaxWidth());Muted(state.message)
                }
                Page.IMPORT->{
                    if(importing && busy && state.activity=="import"){
                        SubscriptionLoading()
                    }else{
                    Heading("Добавить подписку","Вставьте полную ссылку от VPN-провайдера.")
                    OutlinedTextField(link,{link=it},label={Text("Ссылка подписки")},placeholder={Text("https://…")},singleLine=true,enabled=!busy,keyboardOptions=KeyboardOptions(keyboardType=KeyboardType.Uri),modifier=Modifier.fillMaxWidth().tvInputNavigation().tvFocus().testTag("subscription-link"))
                    Muted("Ссылка содержит данные доступа. Не передавайте её другим людям.")
                    Primary(if(busy)"Загружаем…"else "Добавить",enabled=!busy&&link.isNotBlank(),modifier=Modifier.testTag("import-submit")){importing=true;model.importLink(link)}
                    if(busy)LinearProgressIndicator(Modifier.fillMaxWidth())
                    if(importing||state.message.startsWith("Не ")||state.message.startsWith("Ссылка"))Text(state.message,style=MaterialTheme.typography.bodySmall,color=if(busy)MaterialTheme.colorScheme.onSurfaceVariant else MaterialTheme.colorScheme.error)
                    Muted("Поддерживаются ссылки Quantway и подписки со списками VLESS, Hysteria2 и AmneziaWG. Неподдерживаемые конфигурации не импортируются.")
                    }
                }
                Page.PROBE->{
                    val checking=state.activity=="probe"&&busy
                    val recommended=state.recommendedId.takeIf{state.checkedEpoch==network.epoch}?:""
                    LaunchedEffect(checking,recommended){scroll.animateScrollTo(0)}
                    Heading(if(checking)"Ищем рабочий путь"else if(recommended.isNotEmpty())"Есть рабочие маршруты"else "Проверка маршрутов")
                    if(checking){
                        ProbeProgress(state.completed,state.total)
                        if(state.message.startsWith("Ожидаем"))Muted(state.message)
                        Secondary("Остановить проверку",modifier=Modifier.testTag("cancel-probe")){model.cancel()}
                    }else if(recommended.isNotBlank()){
                        val best=state.profiles.firstOrNull{it.optString("job_id")==recommended}
                        best?.let{
                            val isSelected=selected?.optString("job_id")==recommended
                            ConnectionSummary(it,online,state.automatic,network.label,state.observations[recommended],network.epoch,modifier=Modifier.testTag("recommended-route"),header="Рекомендуемый маршрут",allowChange=false,showSelection=false,isSelected=isSelected){}
                            if(!isSelected)Primary("Использовать этот маршрут",modifier=Modifier.testTag("use-recommended-route")){choose(recommended)}
                        }
                        Muted("Проверено ${state.completed} из ${state.total}")
                    }else{
                        Muted(if(state.checkedAt>0L||state.message.startsWith("Не ")||state.message=="Проверка остановлена")state.message else "Найдём рабочий маршрут в вашей текущей сети.")
                        if(serviceRunning)Muted("VPN останется включён. Маршрут не сменится без подтверждения.")
                    }
                    if(!checking && state.message.startsWith("Активный AWG"))Muted(state.message)
                    if(!checking && recommended.isNotBlank())Secondary("Проверить снова",enabled=!busy,modifier=Modifier.testTag("start-probe")){model.run(prefs.shareReports)}else if(!checking)Primary("Начать проверку",enabled=!busy&&state.profiles.isNotEmpty(),modifier=Modifier.testTag("start-probe")){model.run(prefs.shareReports)}
                    if(!checking){
                    if(state.checkedAt>0)SecondaryAction("Все результаты"){go(Page.ROUTES)}
                    if(source?.api==true)Toggle("Делиться результатами","Доступность, время ответа и тип сети",prefs.shareReports){model.preferences.update(prefs.copy(shareReports=it))}else Muted("Для внешнего провайдера проверки выполняются только на этом устройстве, без отправки отчётов Quantway.")
                    }
                }
                Page.DETAILS->{Heading("О соединении");Secondary("Переподключиться",enabled=state.profiles.isNotEmpty()){connect()};Panel{Detail("Состояние",if(online)"Подключено"else "VPN выключен");selected?.let{Detail("Маршрут",it.optString("label"));Detail("Протокол",it.protocolLabel())};Detail("Сеть",network.label);Detail("Режим",if(state.automatic)"Автоматически"else "Вручную");if(online)Detail("Время сеанса",formatDuration(elapsed));Detail("Подписка",source?.name?:"—")};Setting("Изменить маршрут","Страна и способ подключения",Icons.AutoMirrored.Filled.List){go(Page.ROUTES)};Setting("Обход VPN","Правила маршрутизации",Icons.Default.Settings){go(Page.SPLIT)}}
                Page.SETTINGS->{Heading("Настройки");if(tv){Row(Modifier.fillMaxWidth(),horizontalArrangement=Arrangement.spacedBy(16.dp)){
                    Column(Modifier.weight(1f),verticalArrangement=Arrangement.spacedBy(10.dp)){Section("ПОДКЛЮЧЕНИЕ");Toggle("Восстанавливать VPN","После смены сети и обрыва связи",prefs.autoRestore){model.preferences.update(prefs.copy(autoRestore=it))};Setting("Обход VPN",if(prefs.split)"Россия и локальные сети — напрямую"else "Весь трафик — через VPN",Icons.Default.Settings){go(Page.SPLIT)};Setting("Диагностика","Проверки, результаты и отчёты",Icons.Default.Info){go(Page.DIAGNOSTICS)}}
                    Column(Modifier.weight(1f),verticalArrangement=Arrangement.spacedBy(10.dp)){Section("ПРИЛОЖЕНИЕ");Setting("Помощь","Если подключение не работает",Icons.Default.Info){go(Page.HELP)};Setting("О приложении","Quantway для Android TV",Icons.Default.Info){go(Page.ABOUT)};Text("Оформление",style=MaterialTheme.typography.titleSmall);FlowRow(horizontalArrangement=Arrangement.spacedBy(7.dp)){listOf("system" to "Авто","dark" to "Тёмное","light" to "Светлое").forEach{(key,label)->FilterChip(selected=prefs.theme==key,onClick={model.preferences.update(prefs.copy(theme=key))},label={Text(label)},modifier=Modifier.tvFocus().testTag("theme-$key"))}}}
                }}else{Section("ПОДКЛЮЧЕНИЕ");Toggle("Восстанавливать VPN","После смены сети и обрыва связи",prefs.autoRestore){model.preferences.update(prefs.copy(autoRestore=it))};Setting("Обход VPN",if(prefs.split)"Россия и локальные сети — напрямую"else "Весь трафик — через VPN",Icons.Default.Settings){go(Page.SPLIT)};Setting("Диагностика","Проверки, результаты и отчёты",Icons.Default.Info){go(Page.DIAGNOSTICS)};Setting("Помощь","Если подключение не работает",Icons.Default.Info){go(Page.HELP)};Section("ПРИЛОЖЕНИЕ");Text("Оформление",style=MaterialTheme.typography.titleSmall);FlowRow(horizontalArrangement=Arrangement.spacedBy(7.dp)){listOf("system" to "Авто","dark" to "Тёмное","light" to "Светлое").forEach{(key,label)->FilterChip(selected=prefs.theme==key,onClick={model.preferences.update(prefs.copy(theme=key))},label={Text(label)},modifier=Modifier.testTag("theme-$key"))}};Setting("О приложении","Quantway для Android",Icons.Default.Info){go(Page.ABOUT)}}}
                Page.SPLIT->{Heading("Обход VPN","Какие ресурсы открывать напрямую.");if(pendingRules)Primary("Применить и переподключиться"){confirmation=Confirmation("Применить правила?","Для применения новых правил VPN кратко переподключится."){connect()}};Toggle("Россия напрямую","Списки из вашей подписки Quantway",prefs.split,enabled=model.routing()!=null){model.preferences.update(prefs.copy(split=it))};Setting("Мои исключения","Добавлено: ${prefs.exceptions.size}",Icons.AutoMirrored.Filled.List){go(Page.EXCEPTIONS)};if(model.routing()!=null)Secondary("Обновить списки",enabled=!busy){model.refresh()};if(model.routing()==null)Muted("Списки появятся после загрузки подписки Quantway. Личные исключения можно добавить отдельно.");Panel{Detail("YouTube, Telegram, Discord","VPN");Detail("Локальные сети",if(prefs.split)"Напрямую"else "VPN");Detail("Российские сайты и IP",if(prefs.split)"Напрямую"else "VPN");Detail("Остальные ресурсы","VPN")};Muted("Для ресурсов из списка VPN правило по домену важнее географии IP. Личные исключения имеют самый высокий приоритет.");model.routing()?.let{p->Muted("Версия списков: ${p.optString("revision").take(12)}\n${p.optJSONArray("direct_cidrs")?.length()?:0} IP-сетей и ${p.optJSONArray("direct_domains")?.length()?:0} доменных правил")};if(!pendingRules&&!serviceRunning)Muted("Изменения применятся при следующем подключении.")}
                Page.EXCEPTIONS->{Heading("Мои исключения","Эти домены открываются напрямую.");OutlinedTextField(domain,{domain=it;domainError=""},label={Text("Домен")},placeholder={Text("example.com")},singleLine=true,modifier=Modifier.fillMaxWidth().tvInputNavigation().tvFocus().testTag("domain-input"));Secondary("Добавить исключение",enabled=domain.isNotBlank()&&prefs.exceptions.size<128){val d=runCatching{java.net.IDN.toASCII(domain.trim().lowercase())}.getOrDefault("");if(d.length>253||!Regex("^(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\\.)+[a-z]{2,63}$").matches(d))domainError="Введите домен без https:// и пути"else{model.preferences.update(prefs.copy(exceptions=(prefs.exceptions+d).distinct().sorted()));domain=""}};if(domainError.isNotEmpty())Text(domainError,color=MaterialTheme.colorScheme.error);if(pendingRules)Secondary("Применить и переподключиться"){confirmation=Confirmation("Применить исключения?","VPN кратко переподключится."){connect()}};prefs.exceptions.forEach{d->Row(Modifier.fillMaxWidth(),verticalAlignment=Alignment.CenterVertically){Text(d,Modifier.weight(1f));IconButton(onClick={model.preferences.update(prefs.copy(exceptions=prefs.exceptions-d))}){Icon(Icons.Default.Close,"Удалить $d")}}};if(prefs.exceptions.isEmpty())Empty("Пока нет исключений","Добавьте домен, который должен видеть IP вашей обычной сети.")}
                Page.DIAGNOSTICS->{Heading("Диагностика","Помогает найти рабочий маршрут.");if(state.pending>0)Secondary("Отправить отчёты (${state.pending})",enabled=!busy){model.retry()};Panel{Text("Последняя проверка",style=MaterialTheme.typography.titleMedium);Muted(if(state.checkedAt>0)"${relativeTime(state.checkedAt)} · ${state.checkedNetwork}"else "Ещё не выполнялась");Secondary("Проверить маршруты",enabled=!busy){go(Page.PROBE)}};Toggle("Отправлять результаты","Для маршрутов Quantway, без ключей и ссылок",prefs.shareReports){model.preferences.update(prefs.copy(shareReports=it))};Setting("Результаты проверки","Доступность и время ответа",Icons.AutoMirrored.Filled.List){go(Page.ROUTES)};Setting("Подготовить отчёт","Просмотрите перед отправкой",Icons.Default.Info){go(Page.REPORT)}}
                Page.REPORT->{Heading("Отчёт диагностики","Вы решаете, что отправить в поддержку.");Primary("Поделиться отчётом"){context.startActivity(Intent.createChooser(Intent(Intent.ACTION_SEND).setType("text/plain").putExtra(Intent.EXTRA_TEXT,model.diagnosticText()),"Отправить отчёт"))};Muted("В отчёте нет ссылок подписки, ключей и адресов серверов.");Panel{Text(model.diagnosticText(),style=MaterialTheme.typography.bodySmall,modifier=Modifier.testTag("diagnostic-report"))}}
                Page.HELP->{Heading("Помощь");Secondary("Подготовить отчёт"){go(Page.REPORT)};HelpItem("VPN включён, но сайт не открывается","Запустите проверку маршрутов. Новый вариант можно выбрать без остановки текущей проверки.");HelpItem("Что происходит при смене сети?","Приложение восстанавливает подключение, если эта функция включена в настройках. Доступность маршрутов зависит от оператора.");HelpItem("Банку нужен мой обычный IP","Включите обход VPN для российских ресурсов или добавьте домен в личные исключения.");HelpItem("Как добавить другого провайдера?","В разделе «Подписки» вставьте полную ссылку. Поддерживаются списки ссылок VLESS, Hysteria2 и AmneziaWG, в том числе Base64.")}
                Page.ABOUT->{Heading("Quantway","Android · версия ${BuildConfig.VERSION_NAME}");Panel{Text("Связь, когда она нужна",style=MaterialTheme.typography.titleLarge);Muted("Рабочие маршруты, восстановление соединения и ваши подписки в одном приложении.")};Muted(engine.Engine.version());Setting("Помощь","Подключение и работа приложения",Icons.Default.Info){go(Page.HELP)}}
            }
        }
        }
        }
    }
    confirmation?.let{c->AlertDialog(onDismissRequest={confirmation=null},title={Text(c.title)},text={Text(c.message)},confirmButton={TextButton(onClick={confirmation=null;c.action()},modifier=Modifier.testTag("confirm-action")){Text("Подтвердить")}},dismissButton={TextButton(onClick={confirmation=null}){Text("Отмена")}})}
}

fun routingKey(app:QuantwayApplication):String{val p=app.preferences.state.value;return "${p.split}:${p.exceptions.joinToString("|")}:${app.routingPolicy?.optString("revision")?:""}"}
private fun family(p:JSONObject)=when{p.protocolLabel().startsWith("Hysteria2")->"Hysteria2";p.optString("engine")=="amneziawg"->"AWG";else->"Xray"}
private fun formatDuration(seconds:Long)="%02d:%02d:%02d".format(seconds/3600,seconds/60%60,seconds%60)
private fun relativeTime(at:Long):String{val seconds=((System.currentTimeMillis()-at)/1000).coerceAtLeast(0);return when{seconds<60->"только что";seconds<3600->"${seconds/60} мин назад";seconds<86400->"${seconds/3600} ч назад";else->"${seconds/86400} дн назад"}}
@Composable internal fun Heading(title:String,subtitle:String=""){Column(Modifier.fillMaxWidth().padding(top=6.dp,bottom=6.dp),verticalArrangement=Arrangement.spacedBy(5.dp)){Text(title,style=MaterialTheme.typography.headlineMedium);if(subtitle.isNotEmpty())Muted(subtitle)}}
@Composable private fun Muted(text:String){Text(text,style=MaterialTheme.typography.bodySmall,color=MaterialTheme.colorScheme.onSurfaceVariant)}
@Composable private fun Tag(text:String){Text(text,modifier=Modifier.background(MaterialTheme.colorScheme.primary.copy(alpha=.1f),RoundedCornerShape(7.dp)).padding(horizontal=8.dp,vertical=4.dp),style=MaterialTheme.typography.labelSmall,color=MaterialTheme.colorScheme.primary)}
@Composable private fun Panel(selected:Boolean=false,modifier:Modifier=Modifier,content:@Composable ColumnScope.()->Unit){Surface(modifier=modifier.fillMaxWidth(),shape=RoundedCornerShape(18.dp),color=if(selected)MaterialTheme.colorScheme.secondaryContainer.copy(alpha=.55f)else MaterialTheme.colorScheme.surface,border=BorderStroke(1.dp,if(selected)MaterialTheme.colorScheme.primary.copy(alpha=.55f)else MaterialTheme.colorScheme.outlineVariant)){Column(Modifier.padding(16.dp),verticalArrangement=Arrangement.spacedBy(8.dp),content=content)}}
@Composable private fun Primary(text:String,modifier:Modifier=Modifier,enabled:Boolean=true,onClick:()->Unit){Button(onClick=onClick,enabled=enabled,modifier=modifier.fillMaxWidth().heightIn(min=49.dp).tvFocus(),shape=RoundedCornerShape(14.dp),contentPadding=PaddingValues(horizontal=16.dp,vertical=12.dp)){Text(text,textAlign=TextAlign.Center,style=MaterialTheme.typography.labelLarge)}}
@Composable private fun Secondary(text:String,modifier:Modifier=Modifier,enabled:Boolean=true,onClick:()->Unit){OutlinedButton(onClick=onClick,enabled=enabled,modifier=modifier.fillMaxWidth().heightIn(min=49.dp).tvFocus(),shape=RoundedCornerShape(14.dp),border=BorderStroke(1.dp,MaterialTheme.colorScheme.outline),contentPadding=PaddingValues(horizontal=16.dp,vertical=12.dp)){Text(text,textAlign=TextAlign.Center,style=MaterialTheme.typography.labelLarge,color=if(enabled)MaterialTheme.colorScheme.onSurface else MaterialTheme.colorScheme.onSurfaceVariant)}}
@Composable internal fun SubscriptionLoading(){
    Column(Modifier.fillMaxWidth().padding(top=72.dp,bottom=32.dp).testTag("subscription-loading"),horizontalAlignment=Alignment.CenterHorizontally,verticalArrangement=Arrangement.spacedBy(16.dp)){
        CircularProgressIndicator(Modifier.size(64.dp),strokeWidth=4.dp)
        Text("Загружаем подписку",style=MaterialTheme.typography.titleLarge,textAlign=TextAlign.Center)
        Text("Получаем и проверяем маршруты. Это может занять некоторое время.",style=MaterialTheme.typography.bodyMedium,textAlign=TextAlign.Center,color=MaterialTheme.colorScheme.onSurfaceVariant)
    }
}
@Composable internal fun ConnectionSummary(profile:JSONObject,online:Boolean,automatic:Boolean,network:String,observation:ProbeObservation?,epoch:String,modifier:Modifier=Modifier,header:String?=null,allowChange:Boolean=true,showSelection:Boolean=true,isSelected:Boolean=false,onClick:()->Unit){
    Surface(onClick=onClick,enabled=allowChange,modifier=modifier.fillMaxWidth().tvFocus().then(if(allowChange)Modifier.testTag("selected-route")else Modifier),shape=RoundedCornerShape(18.dp),color=MaterialTheme.colorScheme.surface,border=BorderStroke(1.dp,MaterialTheme.colorScheme.outlineVariant)){
        Column(Modifier.padding(16.dp),verticalArrangement=Arrangement.spacedBy(12.dp)){
            Row(verticalAlignment=Alignment.CenterVertically,horizontalArrangement=Arrangement.spacedBy(8.dp)){
                Text(header?:if(online)"Текущее подключение" else "Выбранное подключение",Modifier.weight(1f),style=MaterialTheme.typography.labelLarge,color=MaterialTheme.colorScheme.onSurfaceVariant)
                if(allowChange||isSelected)Text(if(allowChange)"Изменить ›" else "Выбран",style=MaterialTheme.typography.labelMedium,color=MaterialTheme.colorScheme.primary)
            }
            Text(profile.optString("label"),style=MaterialTheme.typography.titleLarge)
            HorizontalDivider(color=MaterialTheme.colorScheme.outlineVariant)
            Row(Modifier.fillMaxWidth(),horizontalArrangement=Arrangement.spacedBy(12.dp)){
                Column(Modifier.weight(1f),verticalArrangement=Arrangement.spacedBy(3.dp)){
                    Muted("Протокол")
                    Text(profile.protocolLabel(),style=MaterialTheme.typography.bodyMedium)
                }
                Column(Modifier.weight(1f),verticalArrangement=Arrangement.spacedBy(3.dp)){
                    Muted("Задержка")
                    ObservationLabel(observation,epoch)
                }
            }
            Row(Modifier.fillMaxWidth(),horizontalArrangement=Arrangement.spacedBy(12.dp)){
                Column(Modifier.weight(1f),verticalArrangement=Arrangement.spacedBy(3.dp)){Muted("Сеть");Text(network,style=MaterialTheme.typography.bodyMedium)}
                if(showSelection)Column(Modifier.weight(1f),verticalArrangement=Arrangement.spacedBy(3.dp)){Muted("Выбор маршрута");Text(if(automatic)"Автоматически" else "Вручную",style=MaterialTheme.typography.bodyMedium)}
            }
        }
    }
}
@Composable private fun Power(connected:Boolean,label:String,enabled:Boolean,onClick:()->Unit){
    Button(onClick=onClick,enabled=enabled,modifier=Modifier.fillMaxWidth().heightIn(min=56.dp).tvFocus().semantics{contentDescription="$label VPN"}.testTag("vpn-power"),shape=RoundedCornerShape(50)){
        val ink=if(enabled)MaterialTheme.colorScheme.onPrimary else MaterialTheme.colorScheme.onSurface.copy(alpha=.38f)
        Canvas(Modifier.size(21.dp)){
            val center=Offset(size.width/2,size.height/2);val r=7.5.dp.toPx()
            drawArc(ink,-48f,276f,false,Offset(center.x-r,center.y-r),Size(r*2,r*2),style=Stroke(2.dp.toPx(),cap=StrokeCap.Round))
            drawLine(ink,Offset(center.x,center.y-9.dp.toPx()),Offset(center.x,center.y-1.dp.toPx()),2.dp.toPx(),StrokeCap.Round)
        }
        Spacer(Modifier.width(10.dp))
        Text(label,style=MaterialTheme.typography.labelLarge)
    }
}
@Composable private fun Setting(title:String,subtitle:String,icon:ImageVector,onClick:()->Unit){
    Surface(onClick=onClick,modifier=Modifier.fillMaxWidth().tvFocus(),shape=RoundedCornerShape(14.dp),color=MaterialTheme.colorScheme.surface,border=BorderStroke(1.dp,MaterialTheme.colorScheme.outlineVariant)){
        Row(Modifier.heightIn(min=64.dp).padding(horizontal=14.dp,vertical=10.dp),verticalAlignment=Alignment.CenterVertically,horizontalArrangement=Arrangement.spacedBy(12.dp)){
            Box(Modifier.size(36.dp).background(MaterialTheme.colorScheme.secondaryContainer,RoundedCornerShape(11.dp)),contentAlignment=Alignment.Center){Icon(icon,null,Modifier.size(18.dp),tint=MaterialTheme.colorScheme.primary)}
            Column(Modifier.weight(1f),verticalArrangement=Arrangement.spacedBy(2.dp)){Text(title,style=MaterialTheme.typography.titleSmall);if(subtitle.isNotBlank())Muted(subtitle)}
            Text("›",fontSize=22.sp,color=MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}
@Composable private fun Toggle(title:String,subtitle:String,checked:Boolean,enabled:Boolean=true,onChange:(Boolean)->Unit){
    Surface(Modifier.fillMaxWidth(),shape=RoundedCornerShape(14.dp),color=MaterialTheme.colorScheme.surface,border=BorderStroke(1.dp,MaterialTheme.colorScheme.outlineVariant)){
        Row(Modifier.heightIn(min=64.dp).padding(horizontal=14.dp,vertical=10.dp),verticalAlignment=Alignment.CenterVertically,horizontalArrangement=Arrangement.spacedBy(12.dp)){
            Column(Modifier.weight(1f),verticalArrangement=Arrangement.spacedBy(2.dp)){Text(title,style=MaterialTheme.typography.titleSmall);Muted(subtitle)}
            Switch(checked,onChange,enabled=enabled,modifier=Modifier.tvFocus().semantics{contentDescription=title})
        }
    }
}
@Composable internal fun Detail(label:String,value:String){Column(Modifier.fillMaxWidth(),verticalArrangement=Arrangement.spacedBy(2.dp)){Muted(label);Text(value,style=MaterialTheme.typography.bodyMedium)}}
@Composable private fun Section(text:String){Text(text,Modifier.padding(top=10.dp),style=MaterialTheme.typography.labelSmall,color=MaterialTheme.colorScheme.onSurfaceVariant,letterSpacing=1.2.sp)}
@Composable private fun Empty(title:String,message:String){Panel{Text(title,style=MaterialTheme.typography.titleMedium);Muted(message)}}
@Composable private fun Step(number:String,title:String){Row(verticalAlignment=Alignment.CenterVertically,horizontalArrangement=Arrangement.spacedBy(12.dp)){Box(Modifier.size(27.dp).background(MaterialTheme.colorScheme.surfaceVariant,CircleShape),contentAlignment=Alignment.Center){Text(number,fontSize=12.sp)};Text(title,style=MaterialTheme.typography.bodyMedium)}}
@Composable private fun ObservationLabel(o:ProbeObservation?,epoch:String,textAlign:TextAlign=TextAlign.Start){Text(when{ o==null->"Не проверен";o.epoch!=epoch->"Другая сеть";o.outcome=="ok"->"${o.elapsedMS} мс";o.outcome=="fail"->"Не отвечает";o.outcome=="cancelled"->"Прервано";else->"Проверка недоступна"},style=MaterialTheme.typography.labelSmall,color=if(o?.outcome=="ok"&&o.epoch==epoch)if(MaterialTheme.colorScheme.background.luminance()<.5f)Color(0xff67d7b1)else Color(0xff007b5c) else MaterialTheme.colorScheme.onSurfaceVariant,textAlign=textAlign,maxLines=2,overflow=TextOverflow.Ellipsis)}
@Composable internal fun RouteFilters(showAll:Boolean,protocol:String,protocols:List<String>,onShowAll:(Boolean)->Unit,onProtocol:(String)->Unit){
    var expanded by remember { mutableStateOf(false) }
    Box(Modifier.fillMaxWidth()){
        OutlinedButton(onClick={expanded=true},modifier=Modifier.fillMaxWidth().heightIn(min=48.dp).tvFocus().testTag("routes-filters"),shape=RoundedCornerShape(14.dp),contentPadding=PaddingValues(horizontal=14.dp,vertical=8.dp)){
            Text((if(showAll)"Все маршруты" else "Лучшие маршруты")+" · "+protocol,Modifier.weight(1f),style=MaterialTheme.typography.labelLarge,maxLines=1,overflow=TextOverflow.Ellipsis)
            Icon(Icons.Default.ArrowDropDown,null)
        }
        DropdownMenu(expanded=expanded,onDismissRequest={expanded=false}){
            Text("Показывать",Modifier.padding(horizontal=16.dp,vertical=8.dp),style=MaterialTheme.typography.labelSmall,color=MaterialTheme.colorScheme.onSurfaceVariant)
            listOf(true to "Все маршруты",false to "Лучшие маршруты").forEach{(all,label)->
                DropdownMenuItem(text={Text(label)},onClick={onShowAll(all);expanded=false},leadingIcon={if(showAll==all)Icon(Icons.Default.Check,null)},modifier=Modifier.testTag(if(all)"filter-show-all" else "filter-show-best"))
            }
            HorizontalDivider()
            Text("Протокол",Modifier.padding(horizontal=16.dp,vertical=8.dp),style=MaterialTheme.typography.labelSmall,color=MaterialTheme.colorScheme.onSurfaceVariant)
            protocols.forEach{value->
                DropdownMenuItem(text={Text(value)},onClick={onProtocol(value);expanded=false},leadingIcon={if(protocol==value)Icon(Icons.Default.Check,null)},modifier=Modifier.testTag("protocol-$value"))
            }
        }
    }
}

@Composable internal fun RouteCard(p:JSONObject,selected:Boolean,o:ProbeObservation?,epoch:String,onClick:()->Unit){
    Surface(onClick=onClick,modifier=Modifier.fillMaxWidth().tvFocus().testTag("route-${p.optString("job_id")}"),shape=RoundedCornerShape(14.dp),color=if(selected)MaterialTheme.colorScheme.secondaryContainer.copy(alpha=.55f)else MaterialTheme.colorScheme.surface,border=BorderStroke(1.dp,if(selected)MaterialTheme.colorScheme.primary.copy(alpha=.65f) else MaterialTheme.colorScheme.outlineVariant)){
        Row(Modifier.heightIn(min=64.dp).padding(horizontal=12.dp,vertical=9.dp),verticalAlignment=Alignment.CenterVertically,horizontalArrangement=Arrangement.spacedBy(10.dp)){
            Box(Modifier.size(34.dp).background(MaterialTheme.colorScheme.surfaceContainerHigh,RoundedCornerShape(11.dp)),contentAlignment=Alignment.Center){Icon(Icons.AutoMirrored.Filled.List,null,Modifier.size(18.dp),tint=MaterialTheme.colorScheme.primary)}
            Column(Modifier.weight(1f),verticalArrangement=Arrangement.spacedBy(2.dp)){
                Text(p.optString("label"),style=MaterialTheme.typography.titleSmall,maxLines=2,overflow=TextOverflow.Ellipsis)
                Text(p.protocolLabel(),style=MaterialTheme.typography.bodySmall,color=MaterialTheme.colorScheme.onSurfaceVariant,maxLines=2,overflow=TextOverflow.Ellipsis)
            }
            Column(Modifier.widthIn(max=104.dp),horizontalAlignment=Alignment.End,verticalArrangement=Arrangement.spacedBy(4.dp)){
                ObservationLabel(o,epoch,TextAlign.End)
                if(selected)Icon(Icons.Default.Check,"Выбрано",Modifier.size(16.dp),tint=MaterialTheme.colorScheme.primary)
            }
        }
    }
}

@Composable private fun TvRouteGrid(profiles:List<JSONObject>,selected:String,observations:Map<String,ProbeObservation>,epoch:String,onSelect:(String)->Unit){
    profiles.chunked(2).forEach{row->
        Row(Modifier.fillMaxWidth(),horizontalArrangement=Arrangement.spacedBy(12.dp)){
            row.forEach{profile->Column(Modifier.weight(1f)){val id=profile.optString("job_id");RouteCard(profile,id==selected,observations[id],epoch){onSelect(id)}}}
            if(row.size==1)Spacer(Modifier.weight(1f))
        }
    }
}
@Composable internal fun HelpItem(title:String,text:String){
    var expanded by rememberSaveable{mutableStateOf(false)}
    Surface(onClick={expanded=!expanded},modifier=Modifier.fillMaxWidth().semantics{stateDescription=if(expanded)"Развёрнуто"else "Свёрнуто"},shape=RoundedCornerShape(14.dp),color=MaterialTheme.colorScheme.surface,border=BorderStroke(1.dp,MaterialTheme.colorScheme.outline)){
        Column(Modifier.padding(14.dp),verticalArrangement=Arrangement.spacedBy(8.dp)){
            Row(verticalAlignment=Alignment.CenterVertically,horizontalArrangement=Arrangement.spacedBy(8.dp)){
                Text(title,Modifier.weight(1f),style=MaterialTheme.typography.titleSmall)
                Icon(Icons.Default.ArrowDropDown,null,modifier=Modifier.rotate(if(expanded)180f else 0f),tint=MaterialTheme.colorScheme.primary)
            }
            if(expanded)Muted(text)
        }
    }
}

@Composable internal fun CompactProbeProgress(completed:Int,total:Int){
    val progress=if(total>0)(completed.toFloat()/total).coerceIn(0f,1f)else 0f
    Row(modifier=Modifier.testTag("home-probe-progress"),verticalAlignment=Alignment.CenterVertically,horizontalArrangement=Arrangement.spacedBy(8.dp)){
        if(total>0) CircularProgressIndicator(progress={progress},modifier=Modifier.size(18.dp),strokeWidth=2.dp)
        else CircularProgressIndicator(modifier=Modifier.size(18.dp),strokeWidth=2.dp)
        Text(if(total>0) "Проверяем маршруты · ${(progress*100).toInt()}%" else "Проверяем маршруты…",style=MaterialTheme.typography.bodySmall,color=MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

@Composable internal fun ProbeProgress(completed:Int,total:Int){
    val progress=if(total>0)(completed.toFloat()/total).coerceIn(0f,1f)else 0f
    val percent=(progress*100).toInt()
    Column(Modifier.fillMaxWidth().padding(vertical=24.dp).testTag("probe-progress"),horizontalAlignment=Alignment.CenterHorizontally,verticalArrangement=Arrangement.spacedBy(16.dp)){
        Box(contentAlignment=Alignment.Center){
            if(total>0)CircularProgressIndicator(progress={progress},modifier=Modifier.size(104.dp),strokeWidth=5.dp)
            else CircularProgressIndicator(modifier=Modifier.size(104.dp),strokeWidth=5.dp)
            Text("$percent%",style=MaterialTheme.typography.headlineSmall,modifier=Modifier.testTag("probe-percent"))
        }
        Muted(if(percent==100)"Завершаем проверку…"else "Проверяем доступность маршрутов…")
    }
}

@Composable internal fun HomeRoutes(current:JSONObject,profiles:List<JSONObject>,observations:Map<String,ProbeObservation>,epoch:String,onChoose:(String)->Unit){
    val best=profiles.distinctBy{it.optString("job_id")}.filter{
        it.optString("job_id")!=current.optString("job_id")&&observations[it.optString("job_id")]?.let{o->o.epoch==epoch&&o.outcome=="ok"}==true
    }.sortedBy{observations[it.optString("job_id")]!!.elapsedMS}.take(3)
    if(best.isNotEmpty()){
        Section("ДРУГИЕ МАРШРУТЫ")
        Panel(modifier=Modifier.testTag("home-route-panel")){
            best.forEachIndexed{index,profile->
                if(index>0)HorizontalDivider(color=MaterialTheme.colorScheme.outlineVariant)
                HomeRouteRow(profile,observations[profile.optString("job_id")],epoch){onChoose(profile.getString("job_id"))}
            }
        }
    }
}

@Composable private fun HomeRouteRow(profile:JSONObject,observation:ProbeObservation?,epoch:String,onClick:()->Unit){
    val id=profile.optString("job_id")
    Row(Modifier.fillMaxWidth().tvFocus().clip(RoundedCornerShape(12.dp)).clickable(role=Role.Button,onClick=onClick).heightIn(min=56.dp).padding(horizontal=10.dp,vertical=8.dp).testTag("home-route-choice-$id"),verticalAlignment=Alignment.CenterVertically,horizontalArrangement=Arrangement.spacedBy(10.dp)){
        Column(Modifier.weight(1f),verticalArrangement=Arrangement.spacedBy(3.dp)){
            Text(profile.optString("label"),style=MaterialTheme.typography.titleSmall,maxLines=2,overflow=TextOverflow.Ellipsis)
            Text(profile.protocolLabel(),style=MaterialTheme.typography.bodySmall,color=MaterialTheme.colorScheme.onSurfaceVariant,maxLines=2,overflow=TextOverflow.Ellipsis)
        }
        ObservationLabel(observation,epoch)
    }
}
