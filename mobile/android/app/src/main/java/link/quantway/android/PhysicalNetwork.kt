package link.quantway.android

import android.content.Context
import android.net.*
import android.os.ParcelFileDescriptor
import engine.Network as EngineNetwork
import java.io.IOException
import java.util.UUID
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow

// Monitor physical networks explicitly, including while our VPN is the default.
class PhysicalNetwork(context: Context) : AutoCloseable {
    private var closed = false
    private val manager = context.getSystemService(ConnectivityManager::class.java)
    data class Snapshot(val network: Network?, val epoch: String, val label: String)
    private val mutable = MutableStateFlow(Snapshot(null, UUID.randomUUID().toString(), "Нет сети"))
    val state = mutable.asStateFlow()
    private val known=mutableMapOf<Network,NetworkCapabilities>()
    private val callback = object : ConnectivityManager.NetworkCallback() {
        override fun onAvailable(network: Network) { manager.getNetworkCapabilities(network)?.let{record(network,it)} }
        override fun onLost(network: Network) = record(network,null)
        override fun onCapabilitiesChanged(network: Network, caps: NetworkCapabilities) = record(network,caps)
    }
    init {
        manager.registerNetworkCallback(NetworkRequest.Builder().addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET).addCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN).build(), callback)
        manager.allNetworks.forEach { network->manager.getNetworkCapabilities(network)?.let{record(network,it)} }
    }
    @Synchronized private fun record(network:Network,caps:NetworkCapabilities?) {
        if (closed) return
        if(caps==null)known.remove(network) else known[network]=caps
        val selected = known.keys.filter {
            val caps = known[it]
            caps?.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN) == true && caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_VALIDATED)
        }.sortedByDescending { if (known[it]?.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) == true) 1 else 0 }.firstOrNull()
        val chosenCaps = selected?.let { known[it] }
        val label = when {
            selected == null -> "Нет подтверждённого доступа в интернет"
            chosenCaps!!.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) -> "Wi-Fi"
            chosenCaps.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR) -> "Мобильная сеть"
            else -> "Сеть устройства"
        }
        if (mutable.value.network != selected || mutable.value.label != label) {
            mutable.value = Snapshot(selected, UUID.randomUUID().toString(), label)
            android.util.Log.i("QuantwayNetwork", "Physical network: $selected $label")
            engine.Engine.cancel()
        }
    }
    fun adapter(snapshot: Snapshot): EngineNetwork = object : EngineNetwork {
        override fun prepareSocket(fd: Long) {
            checkCurrent(snapshot)
            val vpn = QuantwayVpnService.current
            if (vpn != null && !vpn.protect(fd.toInt())) throw IOException("Cannot protect VPN socket")
            ParcelFileDescriptor.fromFd(fd.toInt()).use { snapshot.network!!.bindSocket(it.fileDescriptor) }
        }
        override fun resolve(host: String): String {
            checkCurrent(snapshot)
            return snapshot.network!!.getAllByName(host).firstOrNull()?.hostAddress ?: throw IOException("DNS unavailable")
        }
    }
    fun checkCurrent(snapshot: Snapshot) {
        if (snapshot.network == null || mutable.value.epoch != snapshot.epoch) throw IOException("Network changed")
        val caps = manager.getNetworkCapabilities(snapshot.network)
        if (caps?.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN) != true || !caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_VALIDATED)) throw IOException("Physical network unavailable")
    }
    @Synchronized override fun close() { closed = true; manager.unregisterNetworkCallback(callback) }
}
