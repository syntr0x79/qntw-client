package link.quantway.android

import android.content.Intent
import android.net.Uri
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.ui.Alignment
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import org.json.JSONArray
import org.json.JSONObject

private fun JSONArray?.objects():List<JSONObject> = if(this==null)emptyList()else(0 until length()).map{getJSONObject(it)}
@Composable private fun CButton(text:String,enabled:Boolean=true,tag:String="",onClick:()->Unit){Button(onClick,enabled=enabled,modifier=Modifier.fillMaxWidth().testTag(tag)){Text(text,textAlign=TextAlign.Center)}}
@Composable private fun CPanel(content:@Composable ColumnScope.()->Unit){Card(Modifier.fillMaxWidth()){Column(Modifier.padding(14.dp),verticalArrangement=Arrangement.spacedBy(8.dp),content=content)}}
@Composable private fun CMuted(text:String){Text(text,style=MaterialTheme.typography.bodySmall,color=MaterialTheme.colorScheme.onSurfaceVariant)}

@OptIn(ExperimentalLayoutApi::class)
@Composable fun CabinetScreen(model:CabinetViewModel,onSubscriptions:()->Unit,onLink:(String)->Unit){
    val state by model.state.collectAsState();val context=LocalContext.current
    var code by remember{mutableStateOf("")};var promo by remember{mutableStateOf("")};var message by remember{mutableStateOf("")}
    var selectedPlan by remember{mutableStateOf("")};var method by remember{mutableStateOf("")};var extras by remember{mutableIntStateOf(0)}
    var confirm by remember{mutableStateOf<Pair<String,()->Unit>?>(null)}
    var renameId by remember{mutableStateOf("")};var renameText by remember{mutableStateOf("")}
    val home=state.page=="home"
    BackHandler(!home){model.open("home")}
    LaunchedEffect(state.authenticated){if(state.authenticated)code=""}
    fun external(url:String){val uri=runCatching{Uri.parse(url)}.getOrNull()?:return;if(uri.scheme=="https")context.startActivity(Intent(Intent.ACTION_VIEW,uri))}
    if(!home)TextButton(onClick={model.open("home")}){Text("← Кабинет")}
    Text(if(!state.authenticated)"Вход в Quantway"else when(state.page){"devices"->"Мои устройства";"plans"->"Продление подписки";"invoices"->"Мои счета";"invoice"->"Оплата";"support","ticket"->"Поддержка";"history"->"История подписки";"referral"->"Пригласить друга";else->"Мой кабинет"},style=MaterialTheme.typography.headlineMedium)
    if(state.busy)LinearProgressIndicator(Modifier.fillMaxWidth())
    if(state.message.isNotBlank())AccountNotice(state.message)
    if(!state.authenticated){
        CMuted("Введите тот же код, которым входите в кабинет Quantway. Он есть в сообщении бота или был выдан при регистрации.")
        OutlinedTextField(code,{code=it},label={Text("Код для входа")},singleLine=true,enabled=!state.busy,visualTransformation=PasswordVisualTransformation(),keyboardOptions=KeyboardOptions(keyboardType=KeyboardType.Password),modifier=Modifier.fillMaxWidth().testTag("account-code"))
        CButton("Войти",!state.busy&&code.isNotBlank(),"account-login"){model.login(code.trim())}
        if(state.botURL.isNotBlank())TextButton(onClick={external(state.botURL)}){Text("Получить код в Telegram")}
        CMuted("Код и сессия кабинета не передаются сторонним VPN-провайдерам.")
    }else when(state.page){
        "home"->{
            val sub=state.me.optJSONObject("subscription")?:JSONObject();val features=state.me.optJSONObject("features")?:JSONObject();val retail=state.identity.optString("kind")=="retail";val owner=state.identity.optString("kind")=="owner"
            CPanel{if(state.identity.optString("orgName")!="null"&&state.identity.optString("orgName").isNotBlank())Text(state.identity.optString("orgName"));Text(sub.optString("headline","Подписка Quantway"),style=MaterialTheme.typography.titleMedium);if(!sub.isNull("days_left"))Text("${sub.optInt("days_left")} дней",fontSize=34.sp);CMuted(sub.optString("until_text"));if(sub.has("devices"))CMuted("Устройств по подписке: ${sub.optInt("devices")}")}
            if(!owner){CButton("Подключить этот телефон",!state.busy&&sub.optBoolean("link_works"),"connect-account-phone"){model.addPhone(onLink)};AccountLink("Мои устройства",!state.busy,"account-devices"){model.open("devices","devices")}}
            if(retail){CButton("Продлить подписку",!state.busy,"account-plans"){model.open("plans","plans")};OutlinedButton(onClick={confirm=(if(sub.optString("status")=="paused")"Возобновить подписку?"else "Поставить подписку на паузу?") to {model.action("subscription/"+if(sub.optString("status")=="paused")"resume"else "pause")}},enabled=!state.busy,modifier=Modifier.fillMaxWidth().testTag("account-pause")){Text(if(sub.optString("status")=="paused")"Возобновить"else "Приостановить")};AccountLink("История дней"){model.open("history","history")}}
            AccountLink("Счета и оплаты"){model.open("invoices","invoices")}
            if(features.optBoolean("support"))AccountLink("Поддержка",tag="account-support"){model.open("support","support")}
            if(features.optBoolean("referral"))AccountLink("Пригласить друга"){model.open("referral","referral")}
            TextButton(onClick={model.refresh()},enabled=!state.busy){Text("Обновить кабинет")}
            TextButton(onClick={confirm="Выйти из аккаунта? VPN-подписки останутся на этом телефоне." to {model.logout()}},enabled=!state.busy,modifier=Modifier.testTag("account-logout")){Text("Выйти")}
        }
        "devices"->{
            val devices=state.devices
            CMuted("Используется ${devices.optInt("in_use")} из ${devices.optInt("paid")} мест")
            if(!state.busy&&state.phoneId.isBlank()){
                CMuted("Не определён профиль этого телефона. Можно выбрать существующий через «⋮» или добавить телефон.")
                CButton("Добавить этот телефон",devices.optBoolean("link_works"),"bind-account-phone"){model.addPhone(onLink)}
            }
            devices.optJSONArray("devices").objects().sortedByDescending{it.optString("id")==state.phoneId}.forEach{d->
                val id=d.getString("id");val self=id==state.phoneId
                key(id){CompactDeviceRow(d,self,state.busy,onUse={
                    if(self)model.useDevice(id,onLink)else confirm="Использовать эту ссылку на телефоне? Если она уже используется другим устройством, возможен конфликт подключений." to {model.useDevice(id,onLink)}
                },onRename={renameId=id;renameText=d.optString("name")},onBlock={
                    confirm=(if(d.optBoolean("blocked"))"Разблокировать устройство?"else "Заблокировать устройство?") to {model.action("devices/$id/"+if(d.optBoolean("blocked"))"unblock"else "block",reload="devices")}
                },onDelete={confirm="Удалить устройство? Его VPN-ссылка перестанет работать." to {model.action("devices/$id","DELETE",reload="devices")}})}
            }
            TextButton(onClick={model.open("devices","devices")},enabled=!state.busy,modifier=Modifier.testTag("refresh-account-devices")){Text("Обновить список")}
        }
        "plans"->{
            val plans=state.data.optJSONArray("plans").objects();val methods=state.data.optJSONArray("methods").objects();val choice=state.data.optJSONObject("devices")
            LaunchedEffect(state.data){if(plans.none{it.optString("id")==selectedPlan})selectedPlan=plans.firstOrNull()?.optString("id")?:"";if(methods.none{it.optString("id")==method})method=methods.firstOrNull()?.optString("id")?:"";extras=choice?.optInt("current")?:0}
            plans.forEach{p->CPanel{Text(p.optString("title"),style=MaterialTheme.typography.titleMedium);Text(p.optString("price"));CMuted("${p.optInt("days")} дней · ${p.optInt("devices")} устройств");if(p.optString("feature").isNotBlank())CMuted(p.optString("feature"));FilterChip(selectedPlan==p.optString("id"),{selectedPlan=p.optString("id")},label={Text(if(selectedPlan==p.optString("id"))"Выбран"else "Выбрать")})}}
            if(choice!=null){CMuted("Дополнительные устройства · ${choice.optString("monthly")}");Row{OutlinedButton({if(extras>0)extras--},enabled=!state.busy&&extras>0){Text("−")};Text("  $extras  ",Modifier.padding(12.dp));OutlinedButton({extras++},enabled=!state.busy&&extras<choice.optInt("max")){Text("+")}}}
            val selected=plans.firstOrNull{it.optString("id")==selectedPlan};val total=selected?.optJSONArray("totals").objects().firstOrNull{it.optInt("devices")==extras};val price=total?.optString("promo_price")?.ifBlank{total.optString("price")}?:selected?.optString("promo_price")?.ifBlank{selected.optString("price")}
            if(!price.isNullOrBlank())Text("К оплате: $price",style=MaterialTheme.typography.titleLarge)
            FlowRow(horizontalArrangement=Arrangement.spacedBy(8.dp)){methods.forEach{m->FilterChip(method==m.optString("id"),{method=m.optString("id")},label={Text(m.optString("label"))})}}
            if(state.me.optJSONObject("features")?.optBoolean("promo")==true){state.data.optJSONObject("promo")?.let{held->CMuted("Промокод: ${held.optString("code")}");TextButton(onClick={model.action("promo","DELETE",reload="plans")},enabled=!state.busy){Text("Убрать промокод")}};OutlinedTextField(promo,{promo=it},label={Text("Промокод")},singleLine=true,modifier=Modifier.fillMaxWidth());CButton("Применить промокод",!state.busy&&promo.isNotBlank()){model.action("promo",body=JSONObject().put("code",promo.trim()),reload="plans")}}
            CButton("Создать счёт",!state.busy&&selectedPlan.isNotBlank()&&method.isNotBlank(),"account-create-invoice"){model.invoice(selectedPlan,method,if(choice!=null)extras else null)}
        }
        "invoices"->{state.data.optJSONArray("invoices").objects().forEach{i->CPanel{Text(i.optString("title"));Text("${i.optString("amount_rub")} · ${i.optString("status")}");CMuted(i.optString("date"));CButton("Открыть счёт",!state.busy){model.open("invoice","invoices/${i.getString("id")}")}}};if(state.data.optBoolean("more"))CButton("Следующие счета",!state.busy){model.open("invoices","invoices?before=${Uri.encode(state.data.optString("next_before"))}")}}
        "invoice"->{state.data.optJSONObject("invoice")?.let{i->CPanel{Text(i.optString("title"));Text(i.optString("status"),style=MaterialTheme.typography.titleLarge);Text(if(i.optBoolean("is_fiat"))i.optString("amount_rub")else i.optString("amount_btc")+" BTC");CMuted(i.optString("deadline"));if(i.optString("state")=="awaiting"){if(i.optString("pay_url").isNotBlank()){CButton("Перейти к оплате",!state.busy){external(i.optString("pay_url"))};CMuted("Откроется защищённая страница платёжного провайдера.")}else{SelectionContainer{Text(i.optString("address"))};CMuted("Укажите точную сумму счёта. После оплаты нажмите «Проверить оплату».")}};CButton("Проверить оплату",!state.busy){model.open("invoice","invoices/${i.getString("id")}")}}}}
        "history"->{state.data.optJSONArray("moves").objects().forEach{r->CPanel{Text("${r.optInt("delta")} дней · ${r.optString("what")}");CMuted(r.optString("when"))}};if(state.data.optBoolean("more"))CButton("Следующие записи",!state.busy){model.open("history","history?before=${Uri.encode(state.data.optString("next_before"))}")}}
        "referral"->{CPanel{Text("Приглашено: ${state.data.optInt("invited")}");Text("Начислено дней: ${state.data.optInt("days_earned")}");CMuted(state.data.optString("share_text"));CButton("Поделиться приглашением"){context.startActivity(Intent.createChooser(Intent(Intent.ACTION_SEND).setType("text/plain").putExtra(Intent.EXTRA_TEXT,state.data.optString("share_text")+"\n"+state.data.optString("link")),"Пригласить друга"))}}}
        "support","ticket"->{
            val conversation=if(state.page=="support")state.data.optJSONObject("current")else state.data
            val messages=conversation?.optJSONArray("messages").objects()
            messages.forEach{m->CPanel{CMuted(m.optString("author")+" · "+m.optString("at"));Text(m.optString("body"));if(m.optString("note").isNotBlank())CMuted(m.optString("note"))}}
            if(state.page=="support"){OutlinedTextField(message,{message=it},label={Text("Сообщение поддержке")},minLines=3,maxLines=6,enabled=!state.busy,modifier=Modifier.fillMaxWidth().testTag("account-support-message"));CButton("Отправить",!state.busy&&message.isNotBlank()&&message.length<=state.data.optInt("max_runes",4000),"account-support-send"){model.action("support/messages",body=JSONObject().put("body",message),reload="support",done={message=""})};state.data.optJSONArray("history").objects().forEach{t->TextButton(onClick={model.open("ticket","support/tickets/${t.getLong("id")}")}){Text(t.optString("subject")+" · "+t.optString("closed"))}}}
            TextButton(onClick={model.open(state.page,if(state.page=="support")"support"else "support/tickets/${state.data.optJSONObject("ticket")?.optLong("id")}")},enabled=!state.busy){Text("Обновить переписку")}
        }
    }
    HorizontalDivider()
    OutlinedButton(onClick=onSubscriptions,modifier=Modifier.fillMaxWidth().testTag("manage-subscriptions")){Text("Мои VPN-подписки")}
    if(renameId.isNotBlank())AlertDialog(onDismissRequest={renameId=""},title={Text("Название устройства")},text={OutlinedTextField(renameText,{renameText=it},singleLine=true,modifier=Modifier.fillMaxWidth())},confirmButton={TextButton(onClick={model.action("devices/$renameId","PATCH",JSONObject().put("name",renameText.trim()),reload="devices");renameId=""},enabled=renameText.isNotBlank()&&!state.busy){Text("Сохранить")}},dismissButton={TextButton(onClick={renameId=""}){Text("Отмена")}})
    confirm?.let{(text,action)->AlertDialog(onDismissRequest={confirm=null},title={Text("Подтвердите действие")},text={Text(text)},confirmButton={TextButton(onClick={confirm=null;action()}){Text("Подтвердить")}},dismissButton={TextButton(onClick={confirm=null}){Text("Отмена")}})}
}


@Composable internal fun CompactDeviceRow(device:JSONObject,self:Boolean,busy:Boolean,onUse:()->Unit,onRename:()->Unit,onBlock:()->Unit,onDelete:()->Unit){
    val id=device.getString("id");val name=device.optString("name").ifBlank{"Устройство"}
    var menu by remember{mutableStateOf(false)}
    Surface(modifier=Modifier.fillMaxWidth().testTag("device-card-$id"),shape=RoundedCornerShape(16.dp),color=MaterialTheme.colorScheme.surface,border=BorderStroke(1.dp,if(self)MaterialTheme.colorScheme.primary.copy(alpha=.55f)else MaterialTheme.colorScheme.outline)){
        Row(Modifier.padding(horizontal=14.dp,vertical=8.dp),verticalAlignment=Alignment.CenterVertically,horizontalArrangement=Arrangement.spacedBy(10.dp)){
            Column(Modifier.weight(1f),verticalArrangement=Arrangement.spacedBy(3.dp)){
                Column(verticalArrangement=Arrangement.spacedBy(2.dp)){
                    Text(name,modifier=Modifier.fillMaxWidth(),style=MaterialTheme.typography.titleSmall,maxLines=1,overflow=TextOverflow.Ellipsis)
                    if(self)Text("Этот телефон",modifier=Modifier.testTag("current-account-device"),style=MaterialTheme.typography.labelSmall,color=MaterialTheme.colorScheme.primary,maxLines=1)
                }
                Text(if(device.optBoolean("blocked"))"Заблокировано"else if(device.optBoolean("online"))"В сети"else device.optString("last_seen").takeIf{it.isNotBlank()}?.let{"Был: $it"}?:"Ещё не подключалось",style=MaterialTheme.typography.bodySmall,color=MaterialTheme.colorScheme.onSurfaceVariant,maxLines=1,overflow=TextOverflow.Ellipsis)
            }
            Box{
                IconButton(onClick={menu=true},enabled=!busy,modifier=Modifier.testTag("device-menu-$id")){Icon(Icons.Default.MoreVert,"Действия с устройством $name")}
                DropdownMenu(expanded=menu,onDismissRequest={menu=false}){
                    if(!device.optBoolean("blocked"))DropdownMenuItem(text={Text(if(self)"Обновить подключение"else "Использовать на этом телефоне")},onClick={menu=false;onUse()},modifier=Modifier.testTag("use-device-$id"))
                    DropdownMenuItem(text={Text("Переименовать")},onClick={menu=false;onRename()},modifier=Modifier.testTag("rename-device-$id"))
                    DropdownMenuItem(text={Text(if(device.optBoolean("blocked"))"Разблокировать"else "Заблокировать")},onClick={menu=false;onBlock()},modifier=Modifier.testTag("block-device-$id"))
                    DropdownMenuItem(text={Text("Удалить устройство",color=MaterialTheme.colorScheme.error)},onClick={menu=false;onDelete()},modifier=Modifier.testTag("delete-device-$id"))
                }
            }
        }
    }
}

@Composable private fun AccountLink(text:String,enabled:Boolean=true,tag:String="",onClick:()->Unit){Surface(onClick=onClick,enabled=enabled,modifier=Modifier.fillMaxWidth().testTag(tag),shape=RoundedCornerShape(12.dp),color=MaterialTheme.colorScheme.surface){Row(Modifier.heightIn(min=48.dp).padding(horizontal=14.dp,vertical=10.dp),verticalAlignment=Alignment.CenterVertically,horizontalArrangement=Arrangement.spacedBy(12.dp)){Text(text,Modifier.weight(1f),style=MaterialTheme.typography.titleSmall);Text("›",color=MaterialTheme.colorScheme.onSurfaceVariant)}}}

@Composable internal fun AccountNotice(message:String){Surface(Modifier.fillMaxWidth().testTag("account-message"),shape=RoundedCornerShape(12.dp),color=MaterialTheme.colorScheme.surfaceVariant){Text(message.trim().replace(Regex("\\n[ \\t\\r]*\\n+"),"\n"),modifier=Modifier.padding(12.dp),style=MaterialTheme.typography.bodySmall)}}
