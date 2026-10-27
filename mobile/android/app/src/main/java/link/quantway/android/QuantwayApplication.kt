package link.quantway.android

import android.app.Application

class QuantwayApplication : Application() {
    var subscription:SubscriptionLink?=null
    @Volatile var routingPolicy:org.json.JSONObject?=null
    val preferences by lazy {AppPreferences(this)}
    val subscriptions by lazy {SubscriptionStore(this)}
    val physical by lazy { PhysicalNetwork(this) }
    override fun onCreate() { super.onCreate(); go.Seq.setContext(this) }
}
