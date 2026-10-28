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
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.toArgb
import androidx.core.view.WindowCompat
import androidx.compose.ui.graphics.Color
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
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.viewmodel.compose.viewModel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.first
import org.json.JSONObject

class MainActivity:ComponentActivity(){
    override fun onCreate(savedInstanceState:Bundle?){super.onCreate(savedInstanceState);setContent{val app=application as QuantwayApplication;val prefs by app.preferences.state.collectAsState();val dark=when(prefs.theme){"dark"->true;"light"->false;else->isSystemInDarkTheme()};val colors=if(dark)darkColorScheme(primary=Color(0xff3ed8d0),onPrimary=Color(0xff080f0e),background=Color(0xff080f0e),surface=Color(0xff14201e),surfaceVariant=Color(0xff0d1716),onBackground=Color(0xffdde9e7),onSurface=Color(0xffdde9e7),onSurfaceVariant=Color(0xff8a9a97),outline=Color(0xff27332f),secondary=Color(0xff3ed8d0),onSecondary=Color(0xff080f0e),secondaryContainer=Color(0xff183e3a),onSecondaryContainer=Color(0xffdde9e7),surfaceContainerLow=Color(0xff0d1716),surfaceContainer=Color(0xff14201e),surfaceContainerHigh=Color(0xff1b2926),surfaceContainerHighest=Color(0xff24332f))else lightColorScheme(primary=Color(0xff0b5f5a),onPrimary=Color(0xfff5f8f8),background=Color(0xfff5f8f8),surface=Color.White,surfaceVariant=Color(0xffe8f0ef),onBackground=Color(0xff0c1413),onSurface=Color(0xff0c1413),onSurfaceVariant=Color(0xff47544f),outline=Color(0xffd3dedd),secondary=Color(0xff0b5f5a),onSecondary=Color(0xfff5f8f8),secondaryContainer=Color(0xffdcebe8),onSecondaryContainer=Color(0xff0b5f5a),surfaceContainerLow=Color(0xfff0f5f4),surfaceContainer=Color(0xffe8f0ef),surfaceContainerHigh=Color(0xffe3edeb),surfaceContainerHighest=Color(0xffdce8e5));SideEffect{WindowCompat.getInsetsController(window,window.decorView).isAppearanceLightStatusBars=!dark;WindowCompat.getInsetsController(window,window.decorView).isAppearanceLightNavigationBars=!dark;window.decorView.setBackgroundColor(colors.background.toArgb())};MaterialTheme(colorScheme=colors,shapes=Shapes(small=RoundedCornerShape(12.dp),medium=RoundedCornerShape(18.dp),large=RoundedCornerShape(22.dp),extraLarge=RoundedCornerShape(28.dp))){QuantwayApp()}}}
}

private enum class Page{VPN,ROUTES,SUBSCRIPTIONS,SETTINGS,ACCOUNT,IMPORT,PROBE,DETAILS,SPLIT,EXCEPTIONS,DIAGNOSTICS,REPORT,HELP,ABOUT}
private data class Confirmation(val title:String,val message:String,val action:()->Unit)

@OptIn(ExperimentalMaterial3Api::class,ExperimentalLayoutApi::class)
@Composable private fun QuantwayApp(model:ProbeViewModel=viewModel()){
    val cabinet:CabinetViewModel=viewModel()
    val cabinetState by cabinet.state.collectAsState()
    val state by model.state.collectAsState();val prefs by model.preferences.state.collectAsState();val network by model.physical.state.collectAsState()
    val phase by QuantwayVpnService.phase.collectAsState();val active by QuantwayVpnService.activeProfile.collectAsState();val started by QuantwayVpnService.startedAt.collectAsState();val applied by QuantwayVpnService.appliedRules.collectAsState()
    val context=LocalContext.current;val app=context.applicationContext as QuantwayApplication
    var pageName by rememberSaveable{mutableStateOf(Page.VPN.name)};val page=Page.valueOf(pageName)
    var confirmation by remember{mutableStateOf<Confirmation?>(null)}
    var link by rememberSaveable{mutableStateOf("")};var domain by rememberSaveable{mutableStateOf("")};var domainError by remember{mutableStateOf("")}
    var filter by rememberSaveable{mutableStateOf("Все протоколы")};var showAll by rememberSaveable{mutableStateOf(false)};var showUnavailable by rememberSaveable{mutableStateOf(false)}
    var importing by rememberSaveable{mutableStateOf(false)}
    var elapsed by remember{mutableLongStateOf(0)}
    val online=phase=="connected";val serviceRunning=phase!="off";val busy=state.running
    val source=model.activeSource();val inactive=source?.api==true&&model.subscriptionStatus() in listOf("paused","expired","unauthorized")
    val selected=active?:state.profiles.firstOrNull{it.optString("job_id")==state.selectedId}
    fun go(p:Page){pageName=p.name}
    fun openCabinet(){go(Page.ACCOUNT)}
    val consent=rememberLauncherForActivityResult(ActivityResultContracts.StartActivityForResult()){if(it.resultCode==Activity.RESULT_OK)model.connect(context)}
    fun connect(){if(inactive){openCabinet();return};val intent=VpnService.prepare(context);if(intent==null)model.connect(context)else consent.launch(intent)}
    fun choose(id:String?,auto:Boolean=false){
        val change={if(auto)model.selectAutomatic()else id?.let{model.select(it)};go(Page.VPN)}
        if(serviceRunning){confirmation=Confirmation("Сменить маршрут?","Текущее подключение продолжит работать до подтверждения. Затем VPN переподключится."){change();connect()}}else change()
    }
    fun back(){go(when(page){Page.EXCEPTIONS->Page.SPLIT;Page.SPLIT,Page.DIAGNOSTICS,Page.HELP,Page.ABOUT,Page.REPORT->Page.SETTINGS;Page.IMPORT->Page.SUBSCRIPTIONS;Page.SUBSCRIPTIONS->Page.ACCOUNT;else->Page.VPN})}
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
    DisposableEffect(owner,model){val observer=LifecycleEventObserver{_,event->if(event==Lifecycle.Event.ON_RESUME&&model.activeSource()!=null&&!model.state.value.running)model.refresh()};owner.lifecycle.addObserver(observer);onDispose{owner.lifecycle.removeObserver(observer)}}
    val topPage=when(page){Page.ROUTES->Page.ROUTES;Page.ACCOUNT,Page.SUBSCRIPTIONS,Page.IMPORT->Page.ACCOUNT;Page.SETTINGS,Page.SPLIT,Page.EXCEPTIONS,Page.DIAGNOSTICS,Page.REPORT,Page.HELP,Page.ABOUT->Page.SETTINGS;else->Page.VPN}
    val pendingRules=serviceRunning&&applied!=routingKey(app)
    Scaffold(modifier=Modifier.fillMaxSize().systemBarsPadding(),containerColor=MaterialTheme.colorScheme.background,topBar={
        Row(Modifier.fillMaxWidth().padding(horizontal=16.dp).heightIn(min=52.dp),verticalAlignment=Alignment.CenterVertically,horizontalArrangement=Arrangement.SpaceBetween){
            if(page !in listOf(Page.VPN,Page.ROUTES,Page.ACCOUNT,Page.SETTINGS))IconButton(onClick={back()},modifier=Modifier.testTag("back")){Icon(Icons.AutoMirrored.Filled.ArrowBack,"Назад")}else Row{Text("Q",color=MaterialTheme.colorScheme.primary,fontWeight=FontWeight.SemiBold,letterSpacing=2.sp);Text("UANTWAY",fontWeight=FontWeight.SemiBold,letterSpacing=2.sp)}
            Row(verticalAlignment=Alignment.CenterVertically,horizontalArrangement=Arrangement.spacedBy(5.dp)){Icon(if(online)Icons.Default.Lock else Icons.Default.Info,null,Modifier.size(14.dp),tint=MaterialTheme.colorScheme.onSurfaceVariant);Text(if(online)"VPN активен"else "Ваш VPN",style=MaterialTheme.typography.labelSmall,color=MaterialTheme.colorScheme.onSurfaceVariant)}
        }
    },bottomBar={
        NavigationBar(containerColor=MaterialTheme.colorScheme.background,tonalElevation=0.dp,modifier=Modifier.border(BorderStroke(1.dp,MaterialTheme.colorScheme.outline))){listOf(Triple(Page.VPN,"VPN",Icons.Default.Home),Triple(Page.ROUTES,"Маршруты",Icons.AutoMirrored.Filled.List),Triple(Page.ACCOUNT,"Кабинет",Icons.Default.AccountCircle),Triple(Page.SETTINGS,"Настройки",Icons.Default.Settings)).forEach{(id,label,icon)->NavigationBarItem(selected=topPage==id,onClick={go(id)},icon={Icon(icon,null)},label={Text(label,fontSize=11.sp)},modifier=Modifier.testTag("nav-${id.name.lowercase()}"),colors=NavigationBarItemDefaults.colors(selectedIconColor=MaterialTheme.colorScheme.primary,selectedTextColor=MaterialTheme.colorScheme.primary,indicatorColor=MaterialTheme.colorScheme.primary.copy(alpha=.1f)))}}
    }){padding->
        val scroll=rememberScrollState();LaunchedEffect(page){scroll.scrollTo(0)}
        Column(Modifier.fillMaxSize().padding(padding).consumeWindowInsets(padding).imePadding().verticalScroll(scroll).padding(horizontal=16.dp).padding(bottom=20.dp),verticalArrangement=Arrangement.spacedBy(10.dp)){
            when(page){
                Page.VPN->{
                    if(state.profiles.isEmpty()&&!inactive){
                        if(source?.api==true){
                            Spacer(Modifier.height(16.dp));Heading("Пока нет доступных маршрутов","Обновите подписку через минуту. Проверить состояние доступа можно в кабинете.")
                            Primary("Обновить маршруты",enabled=!busy){model.refresh()}
                            Secondary("Открыть кабинет"){go(Page.ACCOUNT)}
                        }else{
                        Spacer(Modifier.height(16.dp));Heading("Добро пожаловать в Quantway","Войдите в свой аккаунт или добавьте подписку другого VPN-провайдера.")
                        Primary(if(cabinetState.authenticated)"Открыть кабинет"else "Войти в Quantway",Modifier.testTag("welcome-login")){go(Page.ACCOUNT)}
                        Secondary("У меня есть ссылка подписки",modifier=Modifier.testTag("add-first")){go(Page.IMPORT)}
                        }
                    }else if(inactive){
                        Spacer(Modifier.height(16.dp));Heading(if(model.subscriptionStatus()=="paused")"Подписка на паузе"else "Продолжим?",if(model.subscriptionStatus()=="paused")"Оставшиеся дни сохраняются. Возобновите доступ в кабинете."else "Продлите подписку, чтобы снова подключиться.")
                        Panel{Text(source?.name?:"Quantway",style=MaterialTheme.typography.labelLarge);model.days()?.let{Text("$it дней",fontSize=35.sp,fontWeight=FontWeight.Medium)};Primary("Открыть кабинет"){openCabinet()}}
                    }else{
                        val title=when(phase){"connected"->"Вы в сети";"waiting"->"Ожидаем сеть";"reconnecting"->"Восстанавливаем связь";"connecting"->"Подключаемся";else->"Всё готово"}
                        Column(Modifier.fillMaxWidth().padding(top=12.dp,bottom=6.dp),horizontalAlignment=Alignment.CenterHorizontally,verticalArrangement=Arrangement.spacedBy(8.dp)){
                            Row(verticalAlignment=Alignment.CenterVertically,horizontalArrangement=Arrangement.spacedBy(7.dp)){Box(Modifier.size(6.dp).background(if(online)MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant,CircleShape));Text(if(online)"VPN подключён"else if(serviceRunning)"VPN восстанавливается"else "VPN выключен",style=MaterialTheme.typography.labelMedium,color=if(online)MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant)}
                            Text(title,fontSize=28.sp,lineHeight=34.sp,textAlign=TextAlign.Center,fontWeight=FontWeight.Medium,letterSpacing=(-.4).sp,modifier=Modifier.testTag("vpn-title"))
                            Text(when{online->"Соединение работает через выбранный узел";phase=="waiting"->"Проверьте Wi-Fi или мобильную сеть";serviceRunning->"Проверяем доступ через выбранный маршрут";state.checkedAt>0->"Рабочий маршрут уже подобран";else->"Выберите маршрут или запустите проверку"},style=MaterialTheme.typography.bodySmall,color=MaterialTheme.colorScheme.onSurfaceVariant)
                            Spacer(Modifier.height(8.dp))
                            Power(online,if(online)"Отключить"else if(serviceRunning)"Отменить"else "Подключить",!busy||serviceRunning){if(serviceRunning)model.disconnect(context)else connect()}
                            Text(if(online)"${formatDuration(elapsed)} · ${network.label}"else network.label,style=MaterialTheme.typography.bodySmall,color=MaterialTheme.colorScheme.onSurfaceVariant)
                        }
                        selected?.let{profile->Panel{Row(Modifier.fillMaxWidth().clickable{go(Page.ROUTES)}.testTag("selected-route"),verticalAlignment=Alignment.CenterVertically,horizontalArrangement=Arrangement.spacedBy(12.dp)){Box(Modifier.size(42.dp).background(MaterialTheme.colorScheme.surfaceVariant,RoundedCornerShape(13.dp)),contentAlignment=Alignment.Center){Text(if(state.automatic)"A"else "→",color=MaterialTheme.colorScheme.primary)};Column(Modifier.weight(1f)){Text(if(state.automatic)"Автоматический выбор"else profile.optString("label"),style=MaterialTheme.typography.titleSmall,maxLines=2,overflow=TextOverflow.Ellipsis);if(state.automatic)Text(profile.optString("label"),style=MaterialTheme.typography.bodySmall,color=MaterialTheme.colorScheme.onSurfaceVariant,maxLines=2,overflow=TextOverflow.Ellipsis)};Text("›",fontSize=23.sp,color=MaterialTheme.colorScheme.onSurfaceVariant)};HorizontalDivider(color=MaterialTheme.colorScheme.outline);FlowRow(Modifier.fillMaxWidth(),horizontalArrangement=Arrangement.spacedBy(12.dp),verticalArrangement=Arrangement.spacedBy(4.dp)){Muted(profile.protocolLabel());ObservationLabel(state.observations[profile.optString("job_id")],network.epoch)}}}
                        if(state.checkedAt>0)Muted("Последняя проверка: ${relativeTime(state.checkedAt)} · ${state.checkedNetwork}")
                        Secondary("Проверить маршруты",enabled=!busy){go(Page.PROBE)}
                        if(online)SecondaryAction("О соединении"){go(Page.DETAILS)}
                        SecondaryAction("Подписка · "+listOfNotNull(source?.name,model.days()?.let{"$it дней"}).joinToString(" · ")){go(Page.SUBSCRIPTIONS)}
                    }
                    if(busy){LinearProgressIndicator(Modifier.fillMaxWidth());Muted(state.message)}
                    if(state.message.startsWith("Не ")||state.message.startsWith("Ссылка "))Text(state.message,color=MaterialTheme.colorScheme.error,style=MaterialTheme.typography.bodySmall)
                }
                Page.ROUTES->{
                    Heading("Маршруты",if(state.checkedAt>0)"Проверены ${relativeTime(state.checkedAt)} · ${state.checkedNetwork}"else "Доступность в этой сети ещё не проверена")
                    if(state.profiles.isEmpty()){Empty("Нет загруженных маршрутов","Добавьте подписку или откройте кабинет, если доступ приостановлен.");Secondary("К подпискам"){go(Page.SUBSCRIPTIONS)}}else{
                        Panel(selected=state.automatic){Row(Modifier.fillMaxWidth().clickable{choose(null,true)}.testTag("automatic-route"),verticalAlignment=Alignment.CenterVertically){Column(Modifier.weight(1f)){Text("Автоматически",style=MaterialTheme.typography.titleSmall);Muted("Подберём рабочий маршрут в этой сети")};if(state.automatic)Icon(Icons.Default.Check,"Выбрано",tint=MaterialTheme.colorScheme.primary)}}
                        Row(Modifier.fillMaxWidth(),horizontalArrangement=Arrangement.spacedBy(8.dp)){FilterChip(selected=!showAll,onClick={showAll=false},label={Text("Лучшие")});FilterChip(selected=showAll,onClick={showAll=true},label={Text("Все")})}
                        val families=listOf("Все протоколы")+state.profiles.map{family(it)}.distinct()
                        FlowRow(horizontalArrangement=Arrangement.spacedBy(7.dp)){families.forEach{f->FilterChip(selected=filter==f,onClick={filter=f},label={Text(f)},modifier=Modifier.testTag("protocol-$f"))}}
                        val all=state.profiles.filter{filter=="Все протоколы"||family(it)==filter};val failed=all.filter{state.observations[it.optString("job_id")]?.let{v->v.epoch==network.epoch&&v.outcome=="fail"}==true}
                        val ordered=(all-failed.toSet()).sortedBy{p->state.observations[p.optString("job_id")]?.takeIf{it.epoch==network.epoch&&it.outcome=="ok"}?.elapsedMS?:Long.MAX_VALUE}
                        val shown=if(showAll)ordered else ordered.take(6)
                        shown.forEach{p->RouteCard(p,!state.automatic&&p.optString("job_id")==state.selectedId,state.observations[p.optString("job_id")],network.epoch){choose(p.getString("job_id"))}}
                        if(failed.isNotEmpty()){SecondaryAction(if(showUnavailable)"Скрыть недоступные"else "Недоступные (${failed.size})"){showUnavailable=!showUnavailable};if(showUnavailable)failed.forEach{p->RouteCard(p,false,state.observations[p.optString("job_id")],network.epoch){choose(p.getString("job_id"))}}}
                        Secondary("Проверить доступность",enabled=!busy){go(Page.PROBE)}
                        Muted("Время ответа относится к проверочному запросу, а не к скорости загрузки. После смены сети замеры считаются устаревшими.")
                    }
                }
                Page.ACCOUNT->{CabinetScreen(cabinet,onSubscriptions={go(Page.SUBSCRIPTIONS)},onLink={url->model.importLink(url);go(if(serviceRunning)Page.SUBSCRIPTIONS else Page.VPN)})}
                Page.SUBSCRIPTIONS->{
                    Heading("Подписки","Ваш VPN и другие подключения — в одном месте.")
                    state.sources.forEach{src->val selectedSource=src.id==state.sourceId;Panel(selected=selectedSource){Row(Modifier.fillMaxWidth(),horizontalArrangement=Arrangement.SpaceBetween){Text(src.name,modifier=Modifier.weight(1f).padding(end=8.dp),style=MaterialTheme.typography.titleMedium,maxLines=2,overflow=TextOverflow.Ellipsis);if(selectedSource)Tag("Выбрана")};src.cache.optJSONObject("subscription")?.let{info->Text("${info.optInt("days_available")} дней",fontSize=32.sp,fontWeight=FontWeight.Medium);Muted(when(info.optString("status")){"active"->"Подписка активна";"paused"->"На паузе · дни сохраняются";else->"Доступ закончился"})};val count=src.cache.optJSONArray("profiles")?.length()?:0;Muted("$count маршрутов · ${relativeTime(src.updatedAt)}");if(src.cache.optInt("skipped")>0)Muted("Неподдерживаемых конфигураций пропущено: ${src.cache.optInt("skipped")}")
                        Row(horizontalArrangement=Arrangement.spacedBy(8.dp)){if(!selectedSource)Button(onClick={val change={model.activate(src.id);go(Page.VPN)};if(serviceRunning)confirmation=Confirmation("Сменить подписку?","Переподключимся через выбранного провайдера. До подтверждения текущий VPN продолжает работать."){change();connect()}else change()},enabled=!busy,modifier=Modifier.weight(1f)){Text("Выбрать")};if(src.api)OutlinedButton(onClick={openCabinet()},modifier=Modifier.weight(1f)){Text("Кабинет")};IconButton(onClick={confirmation=Confirmation("Удалить подписку?","Ссылка будет удалена с этого телефона. Подписка у провайдера сохранится."){if(selectedSource&&serviceRunning)model.disconnect(context);model.removeSource(src.id)}}){Icon(Icons.Default.Delete,"Удалить подписку ${src.name}")}}
                    }}
                    Secondary("Добавить подписку",enabled=!busy,modifier=Modifier.testTag("add-subscription")){go(Page.IMPORT)}
                    if(source!=null)Setting("Обновить маршруты","Загрузить изменения провайдера",Icons.Default.Refresh){model.refresh()}
                    if(busy)LinearProgressIndicator(Modifier.fillMaxWidth());Muted(state.message)
                }
                Page.IMPORT->{
                    Heading("Добавить подписку","Вставьте полную ссылку от VPN-провайдера.")
                    OutlinedTextField(link,{link=it},label={Text("Ссылка подписки")},placeholder={Text("https://…")},singleLine=true,enabled=!busy,keyboardOptions=KeyboardOptions(keyboardType=KeyboardType.Uri),modifier=Modifier.fillMaxWidth().testTag("subscription-link"))
                    Muted("Ссылка содержит данные доступа. Не передавайте её другим людям.")
                    Primary(if(busy)"Загружаем…"else "Добавить",enabled=!busy&&link.isNotBlank(),modifier=Modifier.testTag("import-submit")){importing=true;model.importLink(link)}
                    if(busy)LinearProgressIndicator(Modifier.fillMaxWidth())
                    if(importing||state.message.startsWith("Не ")||state.message.startsWith("Ссылка"))Text(state.message,style=MaterialTheme.typography.bodySmall,color=if(busy)MaterialTheme.colorScheme.onSurfaceVariant else MaterialTheme.colorScheme.error)
                    Muted("Поддерживаются ссылки Quantway и подписки со списками VLESS, Hysteria2 и AmneziaWG. Неподдерживаемые конфигурации не импортируются.")
                }
                Page.PROBE->{
                    val checking=state.activity=="probe"&&busy
                    LaunchedEffect(checking,state.recommendedId){scroll.animateScrollTo(0)}
                    Heading(if(checking)"Ищем рабочий путь"else if(state.recommendedId.isNotEmpty())"Есть рабочие маршруты"else "Проверка маршрутов")
                    if(checking){
                        ProbeProgress(state.completed,state.total)
                        if(state.message.startsWith("Ожидаем"))Muted(state.message)
                    }else if(state.recommendedId.isNotBlank()){
                        val best=state.profiles.firstOrNull{it.optString("job_id")==state.recommendedId}
                        best?.let{
                            Panel(selected=true,modifier=Modifier.testTag("recommended-route")){
                                Tag("Рекомендуем")
                                Text(it.optString("label"),style=MaterialTheme.typography.titleLarge)
                                Muted(it.protocolLabel())
                                ObservationLabel(state.observations[it.optString("job_id")],network.epoch)
                            }
                            Primary(if(online&&active?.optString("job_id")==state.recommendedId)"Оставить текущее подключение"else "Использовать этот маршрут",modifier=Modifier.testTag("use-recommended-route")){
                                if(online&&active?.optString("job_id")==state.recommendedId)go(Page.VPN)else choose(state.recommendedId)
                            }
                        }
                        Muted("Проверено ${state.completed} из ${state.total}")
                    }else{
                        Muted(if(state.checkedAt>0L||state.message.startsWith("Не ")||state.message=="Проверка остановлена")state.message else "Найдём рабочий маршрут в вашей текущей сети.")
                        if(serviceRunning)Muted("VPN останется включён. Маршрут не сменится без подтверждения.")
                    }
                    if(checking)Secondary("Остановить проверку",modifier=Modifier.testTag("cancel-probe")){model.cancel()}else if(state.recommendedId.isNotBlank())Secondary("Проверить снова",enabled=!busy,modifier=Modifier.testTag("start-probe")){model.run(prefs.shareReports)}else Primary("Начать проверку",enabled=!busy&&state.profiles.isNotEmpty(),modifier=Modifier.testTag("start-probe")){model.run(prefs.shareReports)}
                    if(!checking){
                    if(source?.api==true)Toggle("Делиться результатами","Доступность, время ответа и тип сети",prefs.shareReports){model.preferences.update(prefs.copy(shareReports=it))}else Muted("Для внешнего провайдера проверки выполняются только на этом телефоне, без отправки отчётов Quantway.")
                    if(state.checkedAt>0)SecondaryAction("Все результаты"){go(Page.ROUTES)}
                    }
                }
                Page.DETAILS->{Heading("О соединении");Panel{Detail("Состояние",if(online)"Подключено"else "VPN выключен");selected?.let{Detail("Маршрут",it.optString("label"));Detail("Протокол",it.protocolLabel())};Detail("Сеть",network.label);Detail("Режим",if(state.automatic)"Автоматически"else "Вручную");if(online)Detail("Время сеанса",formatDuration(elapsed));Detail("Подписка",source?.name?:"—")};Setting("Изменить маршрут","Страна и способ подключения",Icons.AutoMirrored.Filled.List){go(Page.ROUTES)};Setting("Обход VPN","Правила маршрутизации",Icons.Default.Settings){go(Page.SPLIT)};Secondary("Переподключиться",enabled=state.profiles.isNotEmpty()){connect()}}
                Page.SETTINGS->{Heading("Настройки");Section("ПОДКЛЮЧЕНИЕ");Toggle("Восстанавливать VPN","После смены сети и обрыва связи",prefs.autoRestore){model.preferences.update(prefs.copy(autoRestore=it))};Setting("Обход VPN",if(prefs.split)"Россия и локальные сети — напрямую"else "Весь трафик — через VPN",Icons.Default.Settings){go(Page.SPLIT)};Setting("Диагностика","Проверки, результаты и отчёты",Icons.Default.Info){go(Page.DIAGNOSTICS)};Section("ПРИЛОЖЕНИЕ");Text("Оформление",style=MaterialTheme.typography.titleSmall);FlowRow(horizontalArrangement=Arrangement.spacedBy(7.dp)){listOf("system" to "Авто","dark" to "Тёмное","light" to "Светлое").forEach{(key,label)->FilterChip(selected=prefs.theme==key,onClick={model.preferences.update(prefs.copy(theme=key))},label={Text(label)},modifier=Modifier.testTag("theme-$key"))}};Setting("Помощь","Если подключение не работает",Icons.Default.Info){go(Page.HELP)};Setting("О приложении","Quantway для Android",Icons.Default.Info){go(Page.ABOUT)}}
                Page.SPLIT->{Heading("Обход VPN","Какие ресурсы открывать напрямую.");Toggle("Россия напрямую","Списки из вашей подписки Quantway",prefs.split,enabled=model.routing()!=null){model.preferences.update(prefs.copy(split=it))};if(model.routing()==null)Muted("Списки появятся после загрузки подписки Quantway. Личные исключения можно добавить отдельно.");Panel{Detail("YouTube, Telegram, Discord","VPN");Detail("Локальные сети",if(prefs.split)"Напрямую"else "VPN");Detail("Российские сайты и IP",if(prefs.split)"Напрямую"else "VPN");Detail("Остальные ресурсы","VPN")};Muted("Для ресурсов из списка VPN правило по домену важнее географии IP. Личные исключения имеют самый высокий приоритет.");Setting("Мои исключения","Добавлено: ${prefs.exceptions.size}",Icons.AutoMirrored.Filled.List){go(Page.EXCEPTIONS)};model.routing()?.let{p->Muted("Версия списков: ${p.optString("revision").take(12)}\n${p.optJSONArray("direct_cidrs")?.length()?:0} IP-сетей и ${p.optJSONArray("direct_domains")?.length()?:0} доменных правил");Secondary("Обновить списки",enabled=!busy){model.refresh()}};if(pendingRules)Primary("Применить и переподключиться"){confirmation=Confirmation("Применить правила?","Для применения новых правил VPN кратко переподключится."){connect()}}else if(!serviceRunning)Muted("Изменения применятся при следующем подключении.")}
                Page.EXCEPTIONS->{Heading("Мои исключения","Эти домены открываются напрямую.");OutlinedTextField(domain,{domain=it;domainError=""},label={Text("Домен")},placeholder={Text("example.com")},singleLine=true,modifier=Modifier.fillMaxWidth().testTag("domain-input"));Secondary("Добавить исключение",enabled=domain.isNotBlank()&&prefs.exceptions.size<128){val d=runCatching{java.net.IDN.toASCII(domain.trim().lowercase())}.getOrDefault("");if(d.length>253||!Regex("^(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\\.)+[a-z]{2,63}$").matches(d))domainError="Введите домен без https:// и пути"else{model.preferences.update(prefs.copy(exceptions=(prefs.exceptions+d).distinct().sorted()));domain=""}};if(domainError.isNotEmpty())Text(domainError,color=MaterialTheme.colorScheme.error);prefs.exceptions.forEach{d->Row(Modifier.fillMaxWidth(),verticalAlignment=Alignment.CenterVertically){Text(d,Modifier.weight(1f));IconButton(onClick={model.preferences.update(prefs.copy(exceptions=prefs.exceptions-d))}){Icon(Icons.Default.Close,"Удалить $d")}}};if(prefs.exceptions.isEmpty())Empty("Пока нет исключений","Добавьте домен, который должен видеть IP вашей обычной сети.");if(pendingRules)Secondary("Применить и переподключиться"){confirmation=Confirmation("Применить исключения?","VPN кратко переподключится."){connect()}}}
                Page.DIAGNOSTICS->{Heading("Диагностика","Помогает найти рабочий маршрут.");Panel{Text("Последняя проверка",style=MaterialTheme.typography.titleMedium);Muted(if(state.checkedAt>0)"${relativeTime(state.checkedAt)} · ${state.checkedNetwork}"else "Ещё не выполнялась");Secondary("Проверить маршруты",enabled=!busy){go(Page.PROBE)}};Toggle("Отправлять результаты","Для маршрутов Quantway, без ключей и ссылок",prefs.shareReports){model.preferences.update(prefs.copy(shareReports=it))};if(state.pending>0)Secondary("Отправить отчёты (${state.pending})",enabled=!busy){model.retry()};Setting("Результаты проверки","Доступность и время ответа",Icons.AutoMirrored.Filled.List){go(Page.ROUTES)};Setting("Подготовить отчёт","Просмотрите перед отправкой",Icons.Default.Info){go(Page.REPORT)}}
                Page.REPORT->{Heading("Отчёт диагностики","Вы решаете, что отправить в поддержку.");Panel{Text(model.diagnosticText(),style=MaterialTheme.typography.bodySmall,modifier=Modifier.testTag("diagnostic-report"))};Muted("В отчёте нет ссылок подписки, ключей и адресов серверов.");Primary("Поделиться отчётом"){context.startActivity(Intent.createChooser(Intent(Intent.ACTION_SEND).setType("text/plain").putExtra(Intent.EXTRA_TEXT,model.diagnosticText()),"Отправить отчёт"))}}
                Page.HELP->{Heading("Помощь");HelpItem("VPN включён, но сайт не открывается","Запустите проверку маршрутов. Новый вариант можно выбрать без остановки текущей проверки.");HelpItem("Что происходит при смене сети?","Приложение восстанавливает подключение, если эта функция включена в настройках. Доступность маршрутов зависит от оператора.");HelpItem("Банку нужен мой обычный IP","Включите обход VPN для российских ресурсов или добавьте домен в личные исключения.");HelpItem("Как добавить другого провайдера?","В разделе «Подписки» вставьте полную ссылку. Поддерживаются списки ссылок VLESS, Hysteria2 и AmneziaWG, в том числе Base64.");Secondary("Подготовить отчёт"){go(Page.REPORT)}}
                Page.ABOUT->{Heading("Quantway","Android · версия ${BuildConfig.VERSION_NAME}");Panel{Text("Связь, когда она нужна",style=MaterialTheme.typography.titleLarge);Muted("Рабочие маршруты, восстановление соединения и ваши подписки в одном приложении.")};Muted(engine.Engine.version());Setting("Помощь","Подключение и работа приложения",Icons.Default.Info){go(Page.HELP)}}
            }
        }
    }
    confirmation?.let{c->AlertDialog(onDismissRequest={confirmation=null},title={Text(c.title)},text={Text(c.message)},confirmButton={TextButton(onClick={confirmation=null;c.action()},modifier=Modifier.testTag("confirm-action")){Text("Подтвердить")}},dismissButton={TextButton(onClick={confirmation=null}){Text("Отмена")}})}
}

fun routingKey(app:QuantwayApplication):String{val p=app.preferences.state.value;return "${p.split}:${p.exceptions.joinToString("|")}:${app.routingPolicy?.optString("revision")?:""}"}
private fun family(p:JSONObject)=when{p.protocolLabel().startsWith("Hysteria2")->"Hysteria2";p.optString("engine")=="amneziawg"->"AWG";else->"Xray"}
private fun formatDuration(seconds:Long)="%02d:%02d:%02d".format(seconds/3600,seconds/60%60,seconds%60)
private fun relativeTime(at:Long):String{val seconds=((System.currentTimeMillis()-at)/1000).coerceAtLeast(0);return when{seconds<60->"только что";seconds<3600->"${seconds/60} мин назад";seconds<86400->"${seconds/3600} ч назад";else->"${seconds/86400} дн назад"}}
@Composable internal fun Heading(title:String,subtitle:String=""){Column(Modifier.fillMaxWidth().padding(top=4.dp,bottom=4.dp),verticalArrangement=Arrangement.spacedBy(6.dp)){Text(title,fontSize=26.sp,lineHeight=32.sp,fontWeight=FontWeight.Medium,letterSpacing=(-.4).sp);if(subtitle.isNotEmpty())Muted(subtitle)}}
@Composable private fun Muted(text:String){Text(text,style=MaterialTheme.typography.bodySmall,color=MaterialTheme.colorScheme.onSurfaceVariant)}
@Composable private fun Tag(text:String){Text(text,modifier=Modifier.background(MaterialTheme.colorScheme.primary.copy(alpha=.1f),RoundedCornerShape(7.dp)).padding(horizontal=8.dp,vertical=4.dp),style=MaterialTheme.typography.labelSmall,color=MaterialTheme.colorScheme.primary)}
@Composable private fun Panel(selected:Boolean=false,modifier:Modifier=Modifier,content:@Composable ColumnScope.()->Unit){Surface(modifier=modifier.fillMaxWidth(),shape=RoundedCornerShape(22.dp),color=if(selected)MaterialTheme.colorScheme.primary.copy(alpha=.08f)else MaterialTheme.colorScheme.surface,border=BorderStroke(1.dp,if(selected)MaterialTheme.colorScheme.primary.copy(alpha=.5f)else MaterialTheme.colorScheme.outline)){Column(Modifier.padding(14.dp),verticalArrangement=Arrangement.spacedBy(8.dp),content=content)}}
@Composable private fun Primary(text:String,modifier:Modifier=Modifier,enabled:Boolean=true,onClick:()->Unit){Button(onClick=onClick,enabled=enabled,modifier=modifier.fillMaxWidth().heightIn(min=49.dp),shape=RoundedCornerShape(15.dp)){Text(text,textAlign=TextAlign.Center)}}
@Composable private fun Secondary(text:String,modifier:Modifier=Modifier,enabled:Boolean=true,onClick:()->Unit){OutlinedButton(onClick=onClick,enabled=enabled,modifier=modifier.fillMaxWidth().heightIn(min=49.dp),shape=RoundedCornerShape(15.dp),border=BorderStroke(1.dp,MaterialTheme.colorScheme.outline)){Text(text,textAlign=TextAlign.Center,color=if(enabled)MaterialTheme.colorScheme.onSurface else MaterialTheme.colorScheme.onSurfaceVariant)}}
@Composable private fun Power(connected:Boolean,label:String,enabled:Boolean,onClick:()->Unit){val accent=MaterialTheme.colorScheme.primary;val muted=MaterialTheme.colorScheme.outline;val ink=if(connected)MaterialTheme.colorScheme.onPrimary else MaterialTheme.colorScheme.onSurface;Box(Modifier.fillMaxWidth().height(185.dp),contentAlignment=Alignment.Center){Box(Modifier.size(170.dp).border(1.dp,if(connected)accent.copy(alpha=.4f)else muted,CircleShape),contentAlignment=Alignment.Center){Box(Modifier.size(145.dp).border(1.dp,muted,CircleShape));Column(Modifier.size(120.dp).clip(CircleShape).background(if(connected)accent else MaterialTheme.colorScheme.surface).border(1.dp,if(connected)accent else muted,CircleShape).clickable(enabled=enabled,role=Role.Button,onClick=onClick).semantics{contentDescription="$label VPN"}.testTag("vpn-power"),verticalArrangement=Arrangement.Center,horizontalAlignment=Alignment.CenterHorizontally){Canvas(Modifier.size(36.dp)){val center=Offset(size.width/2,size.height/2);val r=14.dp.toPx();drawArc(ink,-48f,276f,false,Offset(center.x-r,center.y-r),Size(r*2,r*2),style=Stroke(2.5.dp.toPx(),cap=StrokeCap.Round));drawLine(ink,Offset(center.x,center.y-18.dp.toPx()),Offset(center.x,center.y-3.dp.toPx()),2.5.dp.toPx(),StrokeCap.Round)};Spacer(Modifier.height(9.dp));Text(label,fontSize=12.sp,color=ink)}}}}
@Composable private fun Setting(title:String,subtitle:String,icon:ImageVector,onClick:()->Unit){Row(Modifier.fillMaxWidth().clickable(onClick=onClick).heightIn(min=56.dp).padding(vertical=8.dp),verticalAlignment=Alignment.CenterVertically,horizontalArrangement=Arrangement.spacedBy(12.dp)){Icon(icon,null,Modifier.size(20.dp),tint=MaterialTheme.colorScheme.primary);Column(Modifier.weight(1f),verticalArrangement=Arrangement.spacedBy(4.dp)){Text(title,style=MaterialTheme.typography.titleSmall);Muted(subtitle)};Text("›",fontSize=22.sp,color=MaterialTheme.colorScheme.onSurfaceVariant)}}
@Composable private fun Toggle(title:String,subtitle:String,checked:Boolean,enabled:Boolean=true,onChange:(Boolean)->Unit){Row(Modifier.fillMaxWidth().heightIn(min=56.dp).padding(vertical=6.dp),verticalAlignment=Alignment.CenterVertically,horizontalArrangement=Arrangement.spacedBy(12.dp)){Column(Modifier.weight(1f),verticalArrangement=Arrangement.spacedBy(4.dp)){Text(title,style=MaterialTheme.typography.titleSmall);Muted(subtitle)};Switch(checked,onChange,enabled=enabled,modifier=Modifier.semantics{contentDescription=title})}}
@Composable internal fun Detail(label:String,value:String){Column(Modifier.fillMaxWidth(),verticalArrangement=Arrangement.spacedBy(2.dp)){Muted(label);Text(value,style=MaterialTheme.typography.bodyMedium)}}
@Composable private fun Section(text:String){Text(text,Modifier.padding(top=10.dp),style=MaterialTheme.typography.labelSmall,color=MaterialTheme.colorScheme.onSurfaceVariant,letterSpacing=1.2.sp)}
@Composable private fun Empty(title:String,message:String){Panel{Text(title,style=MaterialTheme.typography.titleMedium);Muted(message)}}
@Composable private fun Step(number:String,title:String){Row(verticalAlignment=Alignment.CenterVertically,horizontalArrangement=Arrangement.spacedBy(12.dp)){Box(Modifier.size(27.dp).background(MaterialTheme.colorScheme.surfaceVariant,CircleShape),contentAlignment=Alignment.Center){Text(number,fontSize=12.sp)};Text(title,style=MaterialTheme.typography.bodyMedium)}}
@Composable private fun ObservationLabel(o:ProbeObservation?,epoch:String){Text(when{ o==null->"Не проверен";o.epoch!=epoch->"Другая сеть";o.outcome=="ok"->"${o.elapsedMS} мс";o.outcome=="fail"->"Не отвечает";o.outcome=="cancelled"->"Прервано";else->"Проверка недоступна"},style=MaterialTheme.typography.labelSmall,color=if(o?.outcome=="ok"&&o.epoch==epoch)MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant)}
@Composable internal fun RouteCard(p:JSONObject,selected:Boolean,o:ProbeObservation?,epoch:String,onClick:()->Unit){Surface(onClick=onClick,modifier=Modifier.fillMaxWidth().testTag("route-${p.optString("job_id")}"),shape=RoundedCornerShape(17.dp),color=if(selected)MaterialTheme.colorScheme.primary.copy(alpha=.08f)else MaterialTheme.colorScheme.surface,border=BorderStroke(1.dp,if(selected)MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.outline)){Column(Modifier.padding(14.dp),verticalArrangement=Arrangement.spacedBy(6.dp)){Row(verticalAlignment=Alignment.CenterVertically,horizontalArrangement=Arrangement.spacedBy(8.dp)){Text(p.optString("label"),modifier=Modifier.weight(1f),style=MaterialTheme.typography.titleSmall,maxLines=2,overflow=TextOverflow.Ellipsis);if(selected)Icon(Icons.Default.Check,"Выбрано",Modifier.size(17.dp),tint=MaterialTheme.colorScheme.primary)};Text(listOf(p.protocolLabel(),p.optString("entry_label").ifBlank{p.optString("inbound_id").take(8)}).filter{it.isNotBlank()}.joinToString(" · "),style=MaterialTheme.typography.bodySmall,color=MaterialTheme.colorScheme.onSurfaceVariant,maxLines=2,overflow=TextOverflow.Ellipsis);ObservationLabel(o,epoch)}}}
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
