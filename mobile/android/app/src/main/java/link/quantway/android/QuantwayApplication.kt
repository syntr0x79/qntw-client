package link.quantway.android

import android.app.Application
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import java.net.HttpURLConnection
import java.net.URL

class QuantwayApplication : Application() {
    @Volatile var cabinetTestOrigin:String?=null
    val cabinet by lazy {CabinetApi(this)}
    var subscription:SubscriptionLink?=null
    @Volatile var routingPolicy:org.json.JSONObject?=null
    val preferences by lazy {AppPreferences(this)}
    val subscriptions by lazy {SubscriptionStore(this)}
    val physical by lazy { PhysicalNetwork(this) }
    // Control traffic follows the system default until our own VPN is running.
    // Once our tunnel is active, keep control requests on the underlying network.
    fun openControlConnection(url:URL):HttpURLConnection {
        if(QuantwayVpnService.current!=null){
            val network=physical.state.value.network?:throw java.io.IOException("Physical network unavailable")
            return network.openConnection(url) as HttpURLConnection
        }
        return url.openConnection() as HttpURLConnection
    }
    fun otherVpnActive():Boolean {
        if(QuantwayVpnService.current!=null)return false
        val manager=getSystemService(ConnectivityManager::class.java)
        return manager.getNetworkCapabilities(manager.activeNetwork)?.hasTransport(NetworkCapabilities.TRANSPORT_VPN)==true
    }
    override fun onCreate() { super.onCreate(); go.Seq.setContext(this) }
}
