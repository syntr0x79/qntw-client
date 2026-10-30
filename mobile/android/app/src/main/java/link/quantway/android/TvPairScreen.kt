package link.quantway.android

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardCapitalization
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp

@Composable internal fun TvPairReceiver(pair:TvPairViewModel,probe:ProbeViewModel,onDone:()->Unit){
    val state by pair.state.collectAsState()
    DisposableEffect(pair){pair.startTv(probe);onDispose{pair.stopTv()}}
    LaunchedEffect(state.tvDone){if(state.tvDone)onDone()}
    Heading("Подключить с телефона","Передайте подписку на телевизор без ввода длинной ссылки.")
    Surface(Modifier.fillMaxWidth().testTag("tv-pair-card"),shape=RoundedCornerShape(18.dp),color=MaterialTheme.colorScheme.surface){
        Column(Modifier.padding(24.dp),verticalArrangement=Arrangement.spacedBy(12.dp)){
            Text("Код подключения",style=MaterialTheme.typography.titleMedium)
            if(state.code.isNotBlank())Text(state.code.chunked(4).joinToString("  "),modifier=Modifier.testTag("tv-pair-code"),fontSize=42.sp,fontWeight=FontWeight.Bold,letterSpacing=3.sp)
            else if(state.tvMessage=="Создаём код…")CircularProgressIndicator(Modifier.size(28.dp))
            Text(state.tvMessage,style=MaterialTheme.typography.bodyMedium,color=MaterialTheme.colorScheme.onSurfaceVariant)
            Text("На телефоне откройте «Кабинет» → «Подключить телевизор», введите код и выберите подписку. Код действует 5 минут.",style=MaterialTheme.typography.bodyMedium,color=MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
    if(state.tvMessage!="Создаём код…"&&!state.tvDone)SecondaryAction(if(state.code.isBlank())"Получить новый код" else "Обновить код",modifier=Modifier.testTag("tv-pair-retry")){pair.stopTv();pair.startTv(probe)}
}

@Composable internal fun TvPairSender(pair:TvPairViewModel,probe:ProbeViewModel,signedIn:Boolean,onAccount:()->Unit,onSubscriptions:()->Unit){
    val result by pair.state.collectAsState();val state by probe.state.collectAsState()
    LaunchedEffect(pair){pair.resetSender()}
    var code by rememberSaveable{mutableStateOf("")};var sourceId by rememberSaveable{mutableStateOf("")}
    val source=state.sources.firstOrNull{it.id==sourceId.ifBlank{state.sourceId}}?:state.sources.firstOrNull()
    Heading("Подключить телевизор","Введите код с экрана телевизора и выберите подписку.")
    OutlinedTextField(code,{code=it.uppercase().filter{c->c.isLetterOrDigit()}.take(8)},label={Text("Код с телевизора")},singleLine=true,keyboardOptions=KeyboardOptions(capitalization=KeyboardCapitalization.Characters),modifier=Modifier.fillMaxWidth().testTag("pair-code-input"),enabled=!result.sending)
    if(state.sources.isEmpty()){
        Text("Сначала добавьте подписку на этом телефоне.",style=MaterialTheme.typography.bodyMedium)
        SecondaryAction("Добавить подписку",modifier=Modifier.testTag("pair-add-subscription"),onClick=onSubscriptions)
    }else{
        Text("Подписка для телевизора",style=MaterialTheme.typography.titleSmall)
        state.sources.forEach{item->
            val selected=item.id==source?.id
            Surface(onClick={sourceId=item.id},modifier=Modifier.fillMaxWidth().testTag("pair-source-${item.id}"),shape=RoundedCornerShape(14.dp),color=if(selected)MaterialTheme.colorScheme.secondaryContainer else MaterialTheme.colorScheme.surface){
                Row(Modifier.padding(12.dp),horizontalArrangement=Arrangement.spacedBy(8.dp)){RadioButton(selected,onClick={sourceId=item.id});Column{Text(item.name,style=MaterialTheme.typography.titleSmall);Text(if(item.api)"Создадим отдельное устройство в аккаунте" else "Сторонняя подписка",style=MaterialTheme.typography.bodySmall,color=MaterialTheme.colorScheme.onSurfaceVariant)}}
            }
        }
        if(source?.api==false)Text("Провайдер может ограничивать число устройств для этой подписки.",style=MaterialTheme.typography.bodySmall,color=MaterialTheme.colorScheme.onSurfaceVariant)
        if(source?.api==true&&!signedIn){
            Text("Для нашей подписки сначала войдите в кабинет.",style=MaterialTheme.typography.bodySmall)
            SecondaryAction("Войти в кабинет",onClick=onAccount)
        }else Button(onClick={source?.let{pair.send(code,it)}},enabled=!result.sending&&code.length==8&&source!=null,modifier=Modifier.fillMaxWidth().heightIn(min=49.dp).testTag("pair-send")){Text(if(result.sending)"Подключаем…" else "Подключить телевизор")}
    }
    if(result.phoneMessage.isNotBlank())AccountNotice(result.phoneMessage)
    Text("Код одноразовый и действует 5 минут. Не сообщайте его незнакомым людям.",style=MaterialTheme.typography.bodySmall,color=MaterialTheme.colorScheme.onSurfaceVariant)
}
