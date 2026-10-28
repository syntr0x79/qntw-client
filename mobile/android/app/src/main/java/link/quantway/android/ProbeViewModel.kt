package link.quantway.android

import android.app.Application
import android.content.Context
import android.content.Intent
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import engine.Engine
import java.io.File
import java.io.IOException
import java.net.HttpURLConnection
import java.net.URI
import java.security.MessageDigest
import java.time.Instant
import java.util.UUID
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.*
import org.json.JSONArray
import org.json.JSONObject

class ProbeViewModel(app:Application):AndroidViewModel(app) {
    private val application=app as QuantwayApplication
    val physical=application.physical
    val preferences=application.preferences
    private val directory=File(app.noBackupFilesDir,"probe-outbox").apply{mkdirs()}
    data class State(val running:Boolean=false,val activity:String="",val message:String="Добавьте ссылку на подписку",val rows:List<String> = emptyList(),val pending:Int=0,val profiles:List<JSONObject> = emptyList(),val selectedId:String="",val automatic:Boolean=true,val sources:List<SubscriptionSource> = emptyList(),val sourceId:String="",val observations:Map<String,ProbeObservation> = emptyMap(),val recommendedId:String="",val completed:Int=0,val total:Int=0,val checkedAt:Long=0,val checkedNetwork:String="",val checkedEpoch:String="")
    private val mutable=MutableStateFlow(State(profiles=QuantwayVpnService.profiles,selectedId=QuantwayVpnService.selected?.optString("job_id")?:"",pending=directory.listFiles{f->f.extension=="json"}?.size?:0))
    val state=mutable.asStateFlow()
    private var subscription:SubscriptionLink?=application.subscription
    private var work:Job?=null
    private val probeGeneration=java.util.concurrent.atomic.AtomicLong()
    private var reportWork:Job?=null
    @Volatile private var cancelled=false
    init {try{val saved=application.subscriptions.read();val sources=saved.optJSONArray("sources")?:JSONArray();val list=(0 until sources.length()).map{SubscriptionSource.from(sources.getJSONObject(it))};val active=list.firstOrNull{it.id==saved.optString("active")}?:list.firstOrNull();if(active!=null){mutable.value=mutable.value.copy(sources=list);activate(active.id,false);mutable.value=mutable.value.copy(selectedId=saved.optString("selected",mutable.value.selectedId),automatic=saved.optBoolean("automatic",true));QuantwayVpnService.automaticMode=mutable.value.automatic}}catch(_:Exception){mutable.value=mutable.value.copy(message="Добавьте подписку повторно: сохранённые данные недоступны")}}
    fun activeSource()=mutable.value.sources.firstOrNull{it.id==mutable.value.sourceId}
    fun accountURL():String?=activeSource()?.takeIf{it.api}?.let{SubscriptionLink.parse(it.url).origin+"/account"}?:subscription?.origin?.plus("/account")
    fun days():Int?=activeSource()?.cache?.optJSONObject("subscription")?.takeIf{it.has("days_available")}?.getInt("days_available")
    fun subscriptionStatus():String=activeSource()?.cache?.optJSONObject("subscription")?.optString("status")?:"unknown"
    fun routing():JSONObject?=activeSource()?.cache?.optJSONObject("routing")?:mutable.value.sources.firstNotNullOfOrNull{it.cache.optJSONObject("routing")}
    private fun persist(){val s=mutable.value;application.subscriptions.write(JSONObject().put("sources",JSONArray(s.sources.map{it.json()})).put("active",s.sourceId).put("selected",s.selectedId).put("automatic",s.automatic))}
    fun select(id:String){if(mutable.value.profiles.none{it.optString("job_id")==id})return;mutable.value=mutable.value.copy(selectedId=id,automatic=false);QuantwayVpnService.automaticMode=false;persist()}
    fun selectAutomatic(){val s=mutable.value;val best=s.profiles.filter{s.observations[it.optString("job_id")]?.let{v->v.epoch==physical.state.value.epoch&&v.outcome=="ok"}==true}.minByOrNull{s.observations[it.optString("job_id")]!!.elapsedMS};mutable.value=s.copy(automatic=true,selectedId=best?.getString("job_id")?:s.selectedId);QuantwayVpnService.automaticMode=true;persist()}
    fun activate(id:String,save:Boolean=true){val source=mutable.value.sources.firstOrNull{it.id==id}?:return;val a=source.cache.optJSONArray("profiles")?:JSONArray();val profiles=(0 until a.length()).map{a.getJSONObject(it)};subscription=if(source.api)SubscriptionLink.parse(source.url)else null;application.subscription=subscription;application.routingPolicy=source.cache.optJSONObject("routing")?:mutable.value.sources.firstNotNullOfOrNull{it.cache.optJSONObject("routing")};QuantwayVpnService.profiles=profiles;source.cache.optString("control_url").takeIf{it.startsWith("https://")}?.let{QuantwayVpnService.controlURL=it};mutable.value=mutable.value.copy(sourceId=id,profiles=profiles,selectedId=profiles.firstOrNull()?.optString("job_id")?:"",observations=emptyMap(),rows=emptyList(),recommendedId="",checkedAt=0,message="Подписка выбрана");if(save)persist()}
    fun removeSource(id:String){if(mutable.value.running)return;val next=mutable.value.sources.filterNot{it.id==id};mutable.value=mutable.value.copy(sources=next);if(id==mutable.value.sourceId){if(next.isNotEmpty())activate(next.first().id,false)else{subscription=null;application.subscription=null;QuantwayVpnService.profiles=emptyList();mutable.value=mutable.value.copy(sourceId="",profiles=emptyList(),selectedId="",observations=emptyMap(),rows=emptyList())}};persist()}
    fun importLink(text:String){if(mutable.value.running)return;load(text,true)}
    fun refresh(){val source=activeSource()?:return;if(mutable.value.running)return;load(source.url,false)}
    private fun load(text:String,activateNew:Boolean){
        mutable.value=mutable.value.copy(running=true,activity="import",message="Загружаем подписку…")
        viewModelScope.launch(Dispatchers.IO){try{
            val uri=URI(text.trim());require(uri.host!=null&&uri.userInfo==null&&uri.fragment==null&&(uri.scheme=="https"||(BuildConfig.DEBUG&&uri.scheme=="http"&&uri.host=="10.0.2.2")))
            val link=runCatching{SubscriptionLink.parse(text)}.getOrNull()
            var api=link!=null
            val response=if(link!=null){try{ProbeApi(link.origin,link.token){physical.state.value.network}.profiles()}catch(e:ApiFailure){if(e.status!=404)throw e;api=false;JSONObject(Engine.importSubscription(fetch(text.trim())))}}else{api=false;JSONObject(Engine.importSubscription(fetch(text.trim())))}
            require(response.getInt("version")==1)
            val a=response.getJSONArray("profiles");require(a.length()>0||response.optJSONObject("subscription")!=null)
            val id=MessageDigest.getInstance("SHA-256").digest(text.trim().toByteArray()).take(16).joinToString(""){"%02x".format(it)}
            val old=mutable.value.sources.firstOrNull{it.id==id}
            val source=SubscriptionSource(id,text.trim(),old?.name?:if(api)"Quantway" else uri.host,api,response,System.currentTimeMillis())
            require(old!=null||mutable.value.sources.size<10)
            val previousId=mutable.value.selectedId;val previousAuto=mutable.value.automatic
            mutable.value=mutable.value.copy(sources=mutable.value.sources.filterNot{it.id==id}+source)
            if((activateNew&&QuantwayVpnService.current==null)||mutable.value.sourceId==id||mutable.value.sourceId.isBlank()){activate(id,false);if(mutable.value.profiles.any{it.optString("job_id")==previousId})mutable.value=mutable.value.copy(selectedId=previousId,automatic=previousAuto)}
            if(api&&mutable.value.sourceId==id&&response.optJSONObject("subscription")?.optString("status") in listOf("paused","expired")&&QuantwayVpnService.current!=null)disconnect(application)
            persist();mutable.value=mutable.value.copy(message=if(activateNew)"Подписка добавлена"else "Подписка обновлена")
        }catch(_:CancellationException){throw CancellationException()}catch(e:Exception){mutable.value=mutable.value.copy(message=message(e))}finally{mutable.value=mutable.value.copy(running=false,activity="")}}
    }
    private fun fetch(link:String):String{
        val url=URI(link).toURL();val network=physical.state.value.network?:throw IOException()
        val c=network.openConnection(url) as HttpURLConnection
        try{c.instanceFollowRedirects=false;c.connectTimeout=10_000;c.readTimeout=20_000;c.setRequestProperty("User-Agent","QuantwayAndroid/${BuildConfig.VERSION_NAME}");require(c.responseCode in 200..299);val b=c.inputStream.use{it.readBytesLimited(1 shl 20)};return String(b,Charsets.UTF_8)}finally{c.disconnect()}
    }
    private fun java.io.InputStream.readBytesLimited(max:Int):ByteArray{val out=java.io.ByteArrayOutputStream();val buffer=ByteArray(8192);while(out.size()<=max){val n=read(buffer,0,minOf(buffer.size,max+1-out.size()));if(n<0)break;out.write(buffer,0,n)};require(out.size()<=max);return out.toByteArray()}
    fun connect(context:Context){
        val source=activeSource();val s=mutable.value;val profile=s.profiles.firstOrNull{it.optString("job_id")==s.selectedId}?:s.profiles.firstOrNull()?:return
        if(source?.api==true&&subscriptionStatus() in listOf("paused","expired","unauthorized")){mutable.value=s.copy(message="Подписка неактивна — откройте кабинет");return}
        QuantwayVpnService.selected=profile;QuantwayVpnService.automaticMode=s.automatic
        context.startForegroundService(Intent(context,QuantwayVpnService::class.java))
    }
    fun disconnect(context:Context){context.startService(Intent(context,QuantwayVpnService::class.java).setAction(QuantwayVpnService.STOP))}
    fun cancel(){probeGeneration.incrementAndGet();cancelled=true;Engine.cancel();work?.cancel();mutable.value=mutable.value.copy(running=false,activity="",message="Проверка остановлена")}
    fun run(upload:Boolean){
        if(mutable.value.running||mutable.value.profiles.isEmpty())return
        val sourceId=mutable.value.sourceId;val link=subscription;val isExternal=activeSource()?.api==false
        val generation=probeGeneration.incrementAndGet();val previous=work
        cancelled=false;mutable.value=mutable.value.copy(running=true,activity="probe",rows=emptyList(),recommendedId="",completed=0,total=0,message="Подбираем рабочую маску…")
        work=viewModelScope.launch(Dispatchers.IO){previous?.join();ensureActive();val api=link?.let{ProbeApi(it.origin,it.token){physical.state.value.network}}
            try{while(isActive&&!cancelled){
                mutable.value=mutable.value.copy(message="Ожидаем устойчивую сеть…")
                val snapshot=physical.state.first{it.network!=null};delay(750);if(physical.state.value.epoch!=snapshot.epoch)continue
                val run=if(isExternal){JSONObject().put("version",1).put("network_epoch",snapshot.epoch).put("control_url",activeSource()?.cache?.optString("control_url")?:"https://example.com/").put("jobs",JSONArray(mutable.value.profiles))}else try{requireNotNull(api).post("runs",JSONObject().put("network_epoch",snapshot.epoch).put("network_type",if(snapshot.label=="Wi-Fi")"wifi" else if(snapshot.label=="Мобильная сеть")"cellular"else "other").put("engines",JSONArray(listOf("xray","amneziawg"))).put("active_engine",QuantwayVpnService.activeEngine.value))}catch(e:ApiFailure){if(e.status==429){mutable.value=mutable.value.copy(message="Ожидаем разрешения на повтор…");delay(e.retryAfter*1000);continue}else throw e}catch(e:Exception){delay(750);if(physical.state.value.epoch!=snapshot.epoch)continue else throw e}
                require(run.getInt("version")==1&&run.getString("network_epoch")==snapshot.epoch)
                val jobs=run.getJSONArray("jobs");mutable.value=mutable.value.copy(total=jobs.length(),completed=0)
                val results=JSONArray();val rows=mutableListOf<String>();var changed=false
                // Ten workers pull from the whole catalog, so a slow route does
                // not stall a batch of fast routes. Only the collector mutates UI
                // state and JSON arrays; native calls always use one snapshot.
                coroutineScope {
                    val next=java.util.concurrent.atomic.AtomicInteger(0)
                    val completed=kotlinx.coroutines.channels.Channel<Pair<JSONObject,JSONObject>>(ProbeExecutor.PARALLELISM)
                    val workers=List(minOf(ProbeExecutor.PARALLELISM,jobs.length())) {
                        launch {
                            while(isActive){
                                val index=next.getAndIncrement()
                                if(index>=jobs.length())break
                                val job=jobs.getJSONObject(index)
                                val result=if(physical.state.value.epoch!=snapshot.epoch)JSONObject().put("job_id",job.getString("job_id")).put("outcome","cancelled").put("reason_code","network_changed").put("elapsed_ms",0)else ProbeExecutor(physical).execute(job,run.getString("control_url"),8_000L,snapshot)
                                ensureActive()
                                completed.send(job to result)
                            }
                        }
                    }
                    launch { workers.joinAll();completed.close() }
                    for((job,result) in completed){
                        ensureActive()
                        if(physical.state.value.epoch!=snapshot.epoch){changed=true;result.put("outcome","cancelled").put("reason_code","network_changed")}
                        results.put(result);val outcome=result.getString("outcome");val id=job.getString("job_id")
                        rows+="${job.probeLabel()} · "+when(outcome){"ok"->"Работает";"fail"->"Не отвечает";"cancelled"->"Повторим в новой сети";else->"Проверка недоступна"}
                        mutable.value=mutable.value.copy(message="Проверка ${results.length()} из ${jobs.length()}",rows=rows.toList(),completed=results.length(),observations=mutable.value.observations+(id to ProbeObservation(id,job.probeLabel(),outcome,result.optLong("elapsed_ms"),snapshot.epoch,snapshot.label,System.currentTimeMillis())))
                    }
                }
                if(physical.state.value.epoch!=snapshot.epoch)changed=true
                ensureActive()
                if(upload&&!isExternal&&jobs.length()>0&&api!=null&&link!=null){val report=JSONObject().put("report_id",UUID.randomUUID().toString()).put("run_id",run.getString("run_id")).put("network_epoch",snapshot.epoch).put("app_version",BuildConfig.VERSION_NAME).put("engine_version",Engine.version()).put("results",results);val envelope=JSONObject().put("origin",link.origin).put("scope",api.scope).put("expires_at",run.getString("expires_at")).put("report",report);val temp=File(directory,"${report.getString("report_id")}.tmp");temp.writeText(envelope.toString());check(temp.renameTo(File(directory,"${report.getString("report_id")}.json")));sendReports(api,link.origin)}
                ensureActive()
                if(changed){mutable.value=mutable.value.copy(rows=emptyList(),observations=emptyMap());continue}
                val successful=(0 until results.length()).map{results.getJSONObject(it)}.filter{it.getString("outcome")=="ok"}.minByOrNull{it.getLong("elapsed_ms")}
                val best=successful?.getString("job_id")?:""
                if(best.isNotBlank()&&QuantwayVpnService.current==null&&sourceId==mutable.value.sourceId){mutable.value=mutable.value.copy(selectedId=best);persist()}
                mutable.value=mutable.value.copy(recommendedId=best,checkedAt=System.currentTimeMillis(),checkedNetwork=snapshot.label,checkedEpoch=snapshot.epoch,message=if(best.isNotBlank())"Рабочее подключение выбрано"else "Не найдено рабочих маршрутов")
                break
            }}catch(_:CancellationException){throw CancellationException()}catch(e:Exception){mutable.value=mutable.value.copy(message=message(e))}finally{if(probeGeneration.get()==generation)mutable.update{it.copy(running=false,activity="",pending=directory.listFiles{f->f.extension=="json"}?.size?:0)}}
        }
    }
    fun retry(){val link=subscription?:return;sendReports(ProbeApi(link.origin,link.token){physical.state.value.network},link.origin)}
    private fun sendReports(api:ProbeApi,origin:String){
        synchronized(directory){
            if(reportWork?.isActive==true)return
            reportWork=viewModelScope.launch(Dispatchers.IO){
                try{flush(api,origin)}catch(_:Exception){}finally{mutable.update{it.copy(pending=directory.listFiles{f->f.extension=="json"}?.size?:0)}}
            }
        }
    }
    private fun flush(api:ProbeApi,origin:String){directory.listFiles{f->f.extension=="json"}?.forEach{file->val e=JSONObject(file.readText());if(!Instant.parse(e.getString("expires_at")).isAfter(Instant.now())){file.delete();return@forEach};if(e.getString("origin")!=origin||e.optString("scope")!=api.scope)return@forEach;if(api.post("reports",e.getJSONObject("report")).optBoolean("accepted"))file.delete()}}
    fun diagnosticText():String {val s=mutable.value;return buildString{appendLine("Quantway Android ${BuildConfig.VERSION_NAME}");appendLine(Engine.version());appendLine("Сеть: ${physical.state.value.label}");appendLine("VPN: ${QuantwayVpnService.phase.value}");s.observations.values.forEach{appendLine("${it.outcome}: ${it.elapsedMS} ms · ${it.network}")}}}
    private fun message(e:Exception):String=when(e){is ApiFailure->when(e.status){401->"Ссылка недействительна";402->"Подписка неактивна — откройте кабинет";404->"Сервис не поддерживает приложение";else->"Сервис временно недоступен"};else->"Не удалось загрузить или проверить данные. Проверьте ссылку и сеть."}
    override fun onCleared(){cancel();super.onCleared()}
}
