package link.quantway.android

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import org.json.JSONObject

class CabinetViewModel(app:Application):AndroidViewModel(app) {
    private val api=(app as QuantwayApplication).cabinet
    data class State(val authenticated:Boolean=false,val busy:Boolean=false,val message:String="",val identity:JSONObject=JSONObject(),val me:JSONObject=JSONObject(),val devices:JSONObject=JSONObject(),val data:JSONObject=JSONObject(),val page:String="home",val botURL:String="",val phoneId:String="")
    private val mutable=MutableStateFlow(State(authenticated=api.signedIn))
    val state=mutable.asStateFlow()
    init{if(api.signedIn)refresh()else info()}
    private fun task(block:()->Unit){if(mutable.value.busy)return;mutable.value=mutable.value.copy(busy=true,message="");viewModelScope.launch(Dispatchers.IO){try{block()}catch(e:Exception){mutable.value=mutable.value.copy(authenticated=api.signedIn,message=if(e is CabinetFailure)e.message?:"Не удалось выполнить запрос"else "Не удалось связаться с сервисом. Проверьте сеть.")}finally{mutable.value=mutable.value.copy(busy=false)}}}
    private fun info(){task{mutable.value=mutable.value.copy(botURL=api.request("login-info").optString("botUrl").takeIf{it!="null"}?:"")}}
    fun login(code:String){task{api.clear();val identity=api.request("login","POST",JSONObject().put("code",code));check(api.signedIn);mutable.value=mutable.value.copy(authenticated=true,identity=identity,page="home");loadHome()}}
    private fun cachedDeviceLinks():List<String> = runCatching {
        val saved=(getApplication<Application>() as QuantwayApplication).subscriptions.read()
        val sources=saved.optJSONArray("sources")?:return@runCatching emptyList<String>()
        (0 until sources.length()).map{sources.getJSONObject(it)}.sortedByDescending{it.optString("id")==saved.optString("active")}.filter{it.optBoolean("api")}.map{it.optString("url")}
    }.getOrDefault(emptyList())
    private fun linkKey(url:String):String? = runCatching{SubscriptionLink.parse(url).let{it.origin.lowercase(java.util.Locale.ROOT)+"/"+it.token}}.getOrNull()
    // Resolve only against devices returned by the authenticated account. Merely
    // opening the cabinet must never create a slot or invent a local device row.
    private fun resolvePhone(devices:JSONObject):String {
        val rows=devices.optJSONArray("devices")?:return ""
        val owned=(0 until rows.length()).map{rows.getJSONObject(it)}
        if(api.deviceId.isNotBlank()&&owned.any{it.optString("id")==api.deviceId})return api.deviceId
        if(api.deviceId.isNotBlank()){api.deviceId="";api.deviceLink=""}
        val wanted=cachedDeviceLinks().mapNotNull{linkKey(it)}.distinct()
        if(wanted.isEmpty())return ""
        val links=mutableMapOf<String,Pair<String,String>>()
        for(device in owned){
            if(device.optBoolean("blocked"))continue
            val link=try{api.request("account/devices/${device.getString("id")}/connect").getString("sub_url")}catch(e:CabinetFailure){if(e.status==401)throw e else continue}catch(_:java.io.IOException){break}
            linkKey(link)?.takeIf{it in wanted}?.let{links[it]=device.getString("id") to link}
        }
        val found=wanted.firstNotNullOfOrNull{links[it]}?:return ""
        api.deviceId=found.first;api.deviceLink=found.second
        return found.first
    }
    private fun loadHome(){
        val identity=api.request("me");val me=api.request("account/me")
        val devices=if(identity.optString("kind")!="owner")api.request("account/devices")else JSONObject()
        val phoneId=resolvePhone(devices)
        mutable.value=mutable.value.copy(authenticated=true,identity=identity,me=me,devices=devices,phoneId=phoneId,data=if(mutable.value.page=="devices")devices else mutable.value.data)
    }
    fun refresh(){task{loadHome()}}
    fun open(page:String,path:String?=null){
        if(mutable.value.busy)return
        mutable.value=mutable.value.copy(page=page,message="",data=JSONObject())
        if(path!=null)task{
            val data=api.request("account/$path")
            if(path=="devices")mutable.value=mutable.value.copy(devices=data,phoneId=resolvePhone(data))
            if(data.optJSONObject("invoice")?.optString("state")=="paid")loadHome()
            mutable.value=mutable.value.copy(data=data)
        }
    }
    fun action(path:String,method:String="POST",body:JSONObject=JSONObject(),reload:String?=null,done:(()->Unit)?=null){task{val result=api.request("account/$path",method,body);loadHome();mutable.value=mutable.value.copy(message=result.optString("message"),data=if(reload=="devices")mutable.value.devices else if(reload!=null)api.request("account/$reload")else mutable.value.data);done?.let{viewModelScope.launch(Dispatchers.Main){it()}}}}
    fun useDevice(id:String,onLink:(String)->Unit){task{val data=api.request("account/devices/$id/connect");val link=data.getString("sub_url");require(SubscriptionLink.parse(link).origin=="https://qntw.link"||(BuildConfig.DEBUG&&SubscriptionLink.parse(link).origin.startsWith("http://10.0.2.2:")));api.deviceId=id;api.deviceLink=link;mutable.value=mutable.value.copy(phoneId=id);viewModelScope.launch(Dispatchers.Main){onLink(link)}}}
    fun addPhone(onLink:(String)->Unit){task{
        var id=api.deviceId
        val devices=api.request("account/devices").optJSONArray("devices")
        if(id.isBlank()&&devices!=null){
            val cached=((getApplication<Application>() as QuantwayApplication).subscriptions.read().optJSONArray("sources"))
            val links=if(cached==null)emptySet()else(0 until cached.length()).mapNotNull{linkKey(cached.getJSONObject(it).optString("url"))}.toSet()
            for(index in 0 until devices.length()){
                val candidate=devices.getJSONObject(index)
                if(candidate.optBoolean("blocked"))continue
                val found=api.request("account/devices/${candidate.getString("id")}/connect").getString("sub_url")
                if(linkKey(found) in links){id=candidate.getString("id");api.deviceId=id;break}
            }
        }
        if(id.isBlank()&&devices!=null){for(index in 0 until devices.length()){val candidate=devices.getJSONObject(index);if(candidate.optString("name")==api.phoneName){id=candidate.getString("id");api.deviceId=id;break}}}
        if(id.isBlank()||devices==null||(0 until devices.length()).none{devices.getJSONObject(it).optString("id")==id}){
            val created=api.request("account/devices","POST",JSONObject().put("name",api.phoneName).put("platform","android"));id=created.getJSONObject("device").getString("id");api.deviceId=id
        }
        val link=api.request("account/devices/$id/connect").getString("sub_url")
        api.deviceLink=link;loadHome();viewModelScope.launch(Dispatchers.Main){onLink(link)}
    }}
    fun invoice(plan:String,method:String,devices:Int?){task{val body=JSONObject().put("plan",plan).put("method",method);devices?.let{body.put("devices",it)};val result=api.request("account/invoices","POST",body);mutable.value=mutable.value.copy(page="invoice",data=result)}}
    fun logout(){task{try{api.request("logout","POST",JSONObject())}finally{api.clear();mutable.value=State(busy=true,message="Вы вышли из аккаунта",botURL=mutable.value.botURL)}}}
}
