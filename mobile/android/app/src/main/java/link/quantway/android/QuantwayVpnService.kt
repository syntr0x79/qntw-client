package link.quantway.android

import android.app.*
import android.content.Intent
import android.net.VpnService
import android.os.ParcelFileDescriptor
import engine.Engine
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.sync.withLock
import org.json.JSONObject

class QuantwayVpnService : VpnService() {
    companion object {
        @Volatile var current: QuantwayVpnService? = null
        val state = MutableStateFlow("Отключён")
        val activeEngine = MutableStateFlow("")
        val phase=MutableStateFlow("off")
        val activeProfile=MutableStateFlow<JSONObject?>(null)
        val startedAt=MutableStateFlow(0L)
        val appliedRules=MutableStateFlow("")
        @Volatile var automaticMode=true
        @Volatile var selected: JSONObject? = null
        @Volatile var controlURL: String = "https://example.com/"
        @Volatile var profiles: List<JSONObject> = emptyList()
        private val lifecycleGate = kotlinx.coroutines.sync.Mutex()
        const val STOP = "link.quantway.STOP"
    }
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private var tun: ParcelFileDescriptor? = null
    private val stopping=java.util.concurrent.atomic.AtomicBoolean(false)
    private var reconnect: Job? = null
    private val physical get() = (application as QuantwayApplication).physical
    override fun onCreate() {
        super.onCreate(); current = this
        getSystemService(NotificationManager::class.java).createNotificationChannel(NotificationChannel("vpn", "VPN соединение", NotificationManager.IMPORTANCE_LOW))
    }
    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == STOP) { shutdown(); return START_NOT_STICKY }
        val notification = Notification.Builder(this, "vpn").setSmallIcon(R.drawable.ic_launcher).setContentTitle("Quantway VPN").setContentText("Соединение активно или восстанавливается").setOngoing(true)
            .addAction(Notification.Action.Builder(null, "Отключить", PendingIntent.getService(this, 1, Intent(this, QuantwayVpnService::class.java).setAction(STOP), PendingIntent.FLAG_IMMUTABLE)).build()).build()
        startForeground(1, notification)
        reconnect?.cancel()
        reconnect = scope.launch {
            var lastEpoch:String?=null
            physical.state.collectLatest { network ->
                if(lastEpoch!=null&&lastEpoch!=network.epoch&&!(application as QuantwayApplication).preferences.state.value.autoRestore){shutdown();return@collectLatest}
                lastEpoch=network.epoch
                var failedProfile:String?=null
                while (isActive) {
                    val connected = lifecycleGate.withLock {
                        Engine.stopVPN(); activeEngine.value = "";phase.value="connecting";activeProfile.value=null;startedAt.value=0
                        if (network.network == null) { phase.value="waiting";state.value = "Ожидание сети"; return@withLock false }
                        try {
                            state.value = "Подбираем подключение…"
                            val preferred = selected ?: return@withLock false
                            var profile: JSONObject? = null
                            for (candidate in (listOf(preferred) + if(automaticMode)profiles else emptyList()).distinctBy { it.getString("job_id") }.sortedBy{it.getString("job_id")==failedProfile}.take(6)) {
                                val result = ProbeExecutor(physical).execute(candidate, controlURL, 5_000, network)
                                if (result.getString("outcome") == "ok") { profile = candidate; break }
                            }
                            ensureActive()
                            val chosen = profile ?: error("Нет рабочего подключения")
                            selected = chosen
                            val kind = chosen.getString("engine")
                            val conf = chosen.optString("awg_conf")
                            val address = if (kind == "amneziawg") Regex("(?im)^Address\\s*=\\s*([^/\\s]+)").find(conf)?.groupValues?.get(1) ?: error("Нет адреса AWG") else "172.19.0.1"
                            val dns = if (kind == "amneziawg") Regex("(?im)^DNS\\s*=\\s*([^,\\s]+)").find(conf)?.groupValues?.get(1) ?: "1.1.1.1" else "1.1.1.1"
                            val oldTun = tun
                            tun = Builder().setSession("Quantway").setMtu(1280).addAddress(address, 32).addRoute("0.0.0.0", 0).addRoute("::", 0).addDnsServer(dns).setBlocking(false).setUnderlyingNetworks(arrayOf(network.network)).establish() ?: error("Не удалось создать VPN")
                            oldTun?.close()
                            setUnderlyingNetworks(arrayOf(network.network))
                            val app=application as QuantwayApplication
                            val policy=app.routingPolicy?.let{JSONObject(it.toString())}?:JSONObject()
                            policy.put("enabled",app.preferences.state.value.split&&app.routingPolicy!=null).put("exceptions",org.json.JSONArray(app.preferences.state.value.exceptions))
                            Engine.configureRouting(policy.toString())
                            Engine.startVPN(tun!!.fd.toLong(), chosen.toString(), physical.adapter(network))
                            appliedRules.value=routingKey(app)
                            activeEngine.value = kind;activeProfile.value=chosen;startedAt.value=android.os.SystemClock.elapsedRealtime();phase.value="connected"
                            state.value = "Подключён · ${chosen.getString("label")} · ${network.label}"
                            true
                        } catch (cancelled: CancellationException) { throw cancelled }
                        catch (_: Exception) {phase.value="reconnecting"; state.value = "Восстанавливаем соединение…"; false }
                    }
                    if (network.network == null) awaitCancellation()
                    if(connected) {
                        while(isActive) {
                            delay(15_000)
                            try {
                                val manager=getSystemService(android.net.ConnectivityManager::class.java)
                                check(manager.getNetworkCapabilities(manager.activeNetwork)?.hasTransport(android.net.NetworkCapabilities.TRANSPORT_VPN)==true)
                                check(Engine.checkVPN(controlURL))
                            }catch(_:Exception){ensureActive();phase.value="reconnecting";state.value="Восстанавливаем соединение…";failedProfile=selected?.getString("job_id");break}
                        }
                    }
                    delay(3_000)
                }
            }
        }
        return START_NOT_STICKY
    }
    private fun shutdown() {
        if(!stopping.compareAndSet(false,true))return
        reconnect?.cancel();scope.cancel()
        CoroutineScope(Dispatchers.IO).launch {
            lifecycleGate.withLock {Engine.stopVPN();tun?.close();tun=null}
            withContext(Dispatchers.Main) {
                activeEngine.value="";phase.value="off";activeProfile.value=null;startedAt.value=0;state.value="Отключён";if(current===this@QuantwayVpnService)current=null
                stopForeground(STOP_FOREGROUND_REMOVE);stopSelf()
            }
        }
    }
    override fun onRevoke() {shutdown()}
    override fun onDestroy() {shutdown();super.onDestroy()}
}
