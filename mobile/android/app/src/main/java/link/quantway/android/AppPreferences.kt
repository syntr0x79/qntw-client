package link.quantway.android

import android.content.Context
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow

class AppPreferences(context:Context) {
    private val prefs=context.getSharedPreferences("preferences",Context.MODE_PRIVATE)
    data class Values(val theme:String="dark",val shareReports:Boolean=false,val autoRestore:Boolean=true,val split:Boolean=true,val exceptions:List<String> = emptyList())
    private val mutable=MutableStateFlow(Values(prefs.getString("theme","dark")?:"dark",prefs.getBoolean("reports",false),prefs.getBoolean("restore",true),prefs.getBoolean("split",true),prefs.getStringSet("exceptions",emptySet())!!.sorted()))
    val state=mutable.asStateFlow()
    fun update(values:Values){prefs.edit().putString("theme",values.theme).putBoolean("reports",values.shareReports).putBoolean("restore",values.autoRestore).putBoolean("split",values.split).putStringSet("exceptions",values.exceptions.toSet()).apply();mutable.value=values}
}
