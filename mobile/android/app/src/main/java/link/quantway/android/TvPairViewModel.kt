package link.quantway.android

import android.app.Application
import android.os.SystemClock
import android.util.Base64
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import java.security.KeyFactory
import java.security.KeyPairGenerator
import java.security.SecureRandom
import java.security.spec.MGF1ParameterSpec
import java.security.spec.X509EncodedKeySpec
import javax.crypto.Cipher
import javax.crypto.spec.GCMParameterSpec
import javax.crypto.spec.OAEPParameterSpec
import javax.crypto.spec.PSource
import javax.crypto.spec.SecretKeySpec
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.*
import org.json.JSONObject

class TvPairViewModel(application:Application):AndroidViewModel(application) {
    private val app=application as QuantwayApplication
    data class State(val code:String="",val tvMessage:String="",val tvDone:Boolean=false,val sending:Boolean=false,val phoneMessage:String="",val sent:Boolean=false)
    private val mutable=MutableStateFlow(State())
    val state=mutable.asStateFlow()
    private var tvJob:Job?=null
    @Volatile private var receiver=""
    private var pendingCode=""
    private var pendingSender=""
    private var pendingPublic=""
    private var pendingPayload=""
    private var pendingSource=""
    private var pendingDevice=""
    private var pendingCreationAttempted=false
    fun resetSender(){if(!mutable.value.sending)mutable.update{it.copy(phoneMessage="",sent=false)}}

    private fun encode(bytes:ByteArray)=Base64.encodeToString(bytes,Base64.NO_WRAP)
    private fun decode(text:String)=Base64.decode(text,Base64.NO_WRAP)
    private fun keyCode(public:ByteArray):String{
        val alphabet="23456789ABCDEFGHJKLMNPQRSTUVWXYZ"
        val digest=java.security.MessageDigest.getInstance("SHA-256").digest(public)
        var bits=0L;digest.take(5).forEach{bits=(bits shl 8) or (it.toLong() and 255)}
        return (7 downTo 0).map{alphabet[((bits shr (it*5)) and 31).toInt()]}.joinToString("")
    }
    private fun rsa(mode:Int,key:java.security.Key)=Cipher.getInstance("RSA/ECB/OAEPWithSHA-256AndMGF1Padding").apply{
        init(mode,key,OAEPParameterSpec("SHA-256","MGF1",MGF1ParameterSpec.SHA1,PSource.PSpecified.DEFAULT))
    }
    private fun envelope(public:String,link:String):String{
        val bytes=link.toByteArray(Charsets.UTF_8);require(bytes.size in 1..8000)
        val pub=KeyFactory.getInstance("RSA").generatePublic(X509EncodedKeySpec(decode(public)))
        val random=SecureRandom();val secret=ByteArray(32).also{random.nextBytes(it)};val nonce=ByteArray(12).also{random.nextBytes(it)}
        val cipher=Cipher.getInstance("AES/GCM/NoPadding").apply{init(Cipher.ENCRYPT_MODE,SecretKeySpec(secret,"AES"),GCMParameterSpec(128,nonce))}
        return JSONObject().put("key",encode(rsa(Cipher.ENCRYPT_MODE,pub).doFinal(secret))).put("nonce",encode(nonce)).put("ciphertext",encode(cipher.doFinal(bytes))).toString()
    }
    fun startTv(probe:ProbeViewModel){
        if(tvJob?.isActive==true)return
        mutable.value=mutable.value.copy(code="",tvMessage="Создаём код…",tvDone=false)
        tvJob=viewModelScope.launch(Dispatchers.IO){
            try{
                val keys=KeyPairGenerator.getInstance("RSA").apply{initialize(2048)}.generateKeyPair()
                val start=app.cabinet.request("tv-pair/start","POST",JSONObject().put("public_key",encode(keys.public.encoded)))
                ensureActive()
                check(start.getString("code")==keyCode(keys.public.encoded))
                receiver=start.getString("receiver");val expires=SystemClock.elapsedRealtime()+start.getLong("expires_in")*1000
                mutable.update{it.copy(code=start.getString("code"),tvMessage="Откройте Quantway на телефоне и введите этот код.")}
                while(isActive&&SystemClock.elapsedRealtime()<expires){
                    delay(2500)
                    val answer=try{app.cabinet.request("tv-pair/poll","POST",JSONObject().put("receiver",receiver))}catch(e:CabinetFailure){if(e.status==404)break else throw e}catch(_:java.io.IOException){continue}
                    if(answer.isNull("payload"))continue
                    val data=JSONObject(answer.getString("payload"));val secret=rsa(Cipher.DECRYPT_MODE,keys.private).doFinal(decode(data.getString("key")))
                    val cipher=Cipher.getInstance("AES/GCM/NoPadding").apply{init(Cipher.DECRYPT_MODE,SecretKeySpec(secret,"AES"),GCMParameterSpec(128,decode(data.getString("nonce"))))}
                    val link=String(cipher.doFinal(decode(data.getString("ciphertext"))),Charsets.UTF_8)
                    mutable.update{it.copy(tvMessage="Добавляем подписку…")}
                    probe.state.first{!it.running}
                    val startedAt=System.currentTimeMillis()
                    withContext(Dispatchers.Main){probe.importLink(link)}
                    probe.state.first{!it.running}
                    check(probe.state.value.sources.any{it.url==link&&it.updatedAt>=startedAt}){"Не удалось загрузить подписку"}
                    runCatching{app.cabinet.request("tv-pair/finish","POST",JSONObject().put("receiver",receiver))}
                    receiver="";mutable.update{it.copy(tvDone=true,tvMessage="Телевизор подключён")};return@launch
                }
                mutable.update{it.copy(tvMessage="Срок действия кода истёк. Получите новый код.",code="")}
            }catch(e:CancellationException){throw e}catch(_:Exception){mutable.update{it.copy(tvMessage="Не удалось подключить телевизор. Проверьте сеть и попробуйте снова.",code="")}}
        }
    }
    fun stopTv(){
        tvJob?.cancel();tvJob=null
        val old=receiver;receiver=""
        if(old.isNotBlank())viewModelScope.launch(Dispatchers.IO){runCatching{app.cabinet.request("tv-pair/finish","POST",JSONObject().put("receiver",old))}}
    }
    fun send(codeText:String,source:SubscriptionSource){
        val code=codeText.uppercase().filter{!it.isWhitespace()&&it!='-'}
        if(mutable.value.sending)return
        mutable.update{it.copy(sending=true,phoneMessage="Подключаем телевизор…",sent=false)}
        viewModelScope.launch(Dispatchers.IO){try{
            require(code.length==8)
            if(pendingCode!=code){pendingCode=code;pendingSender=Base64.encodeToString(ByteArray(32).also{SecureRandom().nextBytes(it)},Base64.URL_SAFE or Base64.NO_PADDING or Base64.NO_WRAP);pendingSource=""}
            if(pendingSource!=source.id){pendingSource=source.id;pendingPublic="";pendingPayload="";pendingDevice="";pendingCreationAttempted=false}
            if(pendingPayload.isBlank()){
                val lookup=app.cabinet.request("tv-pair/lookup","POST",JSONObject().put("code",code).put("sender",pendingSender))
                if(source.api)check(lookup.optInt("expires_in")>=30){"Код скоро истечёт. Получите новый код на телевизоре."}
                pendingPublic=lookup.getString("public_key")
            }
            check(keyCode(decode(pendingPublic))==code){"Код телевизора не совпал. Начните подключение заново."}
            if(pendingPayload.isBlank()){
                val link=if(source.api){
                    check(app.cabinet.signedIn){"Войдите в кабинет Quantway, чтобы добавить телевизор."}
                    val name="Quantway · Android TV · $code"
                    val devices=app.cabinet.request("account/devices").optJSONArray("devices")
                    val ownsSource=SubscriptionLink.sameDevice(source.url,app.cabinet.deviceLink)||(devices!=null&&(0 until devices.length()).any{index->
                        val device=devices.getJSONObject(index)
                        if(device.optBoolean("blocked"))false else try{SubscriptionLink.sameDevice(source.url,app.cabinet.request("account/devices/${device.getString("id")}/connect").getString("sub_url"))}catch(e:CabinetFailure){if(e.status==404||e.status==409)false else throw e}
                    })
                    check(ownsSource){"Выбранная подписка не связана с этим аккаунтом. Войдите в нужный кабинет."}
                    var id=if(devices==null)""else (0 until devices.length()).map{devices.getJSONObject(it)}.firstOrNull{it.optString("name")==name&&!it.optBoolean("blocked")}?.optString("id").orEmpty()
                    if(id.isBlank()){pendingCreationAttempted=true;id=app.cabinet.request("account/devices","POST",JSONObject().put("name",name).put("platform","tv")).getJSONObject("device").getString("id")}
                    pendingDevice=id
                    app.cabinet.request("account/devices/$id/connect").getString("sub_url")
                }else source.url
                pendingPayload=envelope(pendingPublic,link)
            }
            app.cabinet.request("tv-pair/send","POST",JSONObject().put("code",code).put("sender",pendingSender).put("public_key",pendingPublic).put("payload",pendingPayload))
            if(pendingDevice.isNotBlank())runCatching{app.cabinet.request("account/devices/$pendingDevice","PATCH",JSONObject().put("name","Телевизор"))}
            mutable.update{it.copy(sending=false,sent=true,phoneMessage="Подписка отправлена. Продолжите на телевизоре.")}
        }catch(e:Exception){val message=when(e){is CabinetFailure->e.message?:"Не удалось подключить телевизор";is IllegalStateException->e.message?:"Не удалось подключить телевизор";is java.io.IOException->"Не удалось связаться с сервисом. Проверьте сеть и повторите.";else->"Не удалось подключить телевизор. Проверьте код и повторите."};mutable.update{it.copy(sending=false,phoneMessage=message+if(pendingDevice.isNotBlank()||pendingCreationAttempted)" Телевизор уже мог появиться в списке устройств; при необходимости удалите его в кабинете." else "")}}}
    }
}
