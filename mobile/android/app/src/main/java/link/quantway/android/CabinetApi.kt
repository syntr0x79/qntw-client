package link.quantway.android

import android.app.Application
import java.net.HttpCookie
import java.net.HttpURLConnection
import java.net.URI
import org.json.JSONObject

class CabinetFailure(val status:Int, message:String):Exception(message)

// Session credentials are independent from device subscription tokens. The
// production origin is fixed; an external subscription can never change it.
class CabinetApi(private val app:Application) {
    private val store=SubscriptionStore(app,"cabinet.enc","quantway-cabinet")
    @Volatile private var saved=runCatching{store.read()}.getOrElse{JSONObject()}
    val signedIn get()=saved.optString("cookie").isNotBlank()
    var deviceId:String
        get()=saved.optString("device_id")
        set(value){synchronized(this){saved.put("device_id",value);store.write(saved)}}
    val phoneName:String
        get()=synchronized(this){saved.optString("phone_name").ifBlank{val name="Quantway · Android "+java.util.UUID.randomUUID().toString().take(8);saved.put("phone_name",name);store.write(saved);name}}
    var deviceLink:String
        get()=saved.optString("device_link")
        set(value){synchronized(this){saved.put("device_link",value);store.write(saved)}}
    private val origin get():String {
        val test=(app as QuantwayApplication).cabinetTestOrigin
        return if(BuildConfig.DEBUG&&test!=null&&URI(test).host=="10.0.2.2")test else "https://qntw.link"
    }
    fun clear(){synchronized(this){saved=JSONObject();store.write(saved)}}
    @Synchronized fun request(path:String,method:String="GET",body:JSONObject?=null):JSONObject {
        require(path.matches(Regex("[a-zA-Z0-9_/?=&.%-]+"))&&!path.contains(".."))
        val url=URI("$origin/api/web/v1/$path").toURL()
        val network=(app as QuantwayApplication).physical.state.value.network
        val c=(network?.openConnection(url)?:url.openConnection()) as HttpURLConnection
        try {
            c.instanceFollowRedirects=false;c.connectTimeout=10_000;c.readTimeout=20_000;c.requestMethod=method
            c.setRequestProperty("Origin",origin);c.setRequestProperty("X-Quantway","1")
            c.setRequestProperty("Content-Type","application/json");c.setRequestProperty("User-Agent","QuantwayAndroid/${BuildConfig.VERSION_NAME}")
            saved.optString("cookie").takeIf{it.isNotBlank()}?.let{c.setRequestProperty("Cookie",it)}
            if(body!=null){c.doOutput=true;c.outputStream.use{it.write(body.toString().toByteArray(Charsets.UTF_8))}}
            val status=c.responseCode
            val bytes=(if(status in 200..299)c.inputStream else c.errorStream)?.use{input->
                val out=java.io.ByteArrayOutputStream();val buffer=ByteArray(8192)
                while(out.size()<=2*1024*1024){val n=input.read(buffer,0,minOf(buffer.size,2*1024*1024+1-out.size()));if(n<0)break;out.write(buffer,0,n)}
                require(out.size()<=2*1024*1024);out.toByteArray()
            }?:byteArrayOf()
            val data=if(bytes.isEmpty())JSONObject()else runCatching{JSONObject(String(bytes,Charsets.UTF_8))}.getOrElse{JSONObject()}
            if(status !in 200..299){if(status==401&&path!="login")clear();throw CabinetFailure(status,data.optJSONObject("error")?.optString("message")?:"Сервис временно недоступен")}
            c.headerFields.filterKeys{it?.equals("Set-Cookie",true)==true}.values.flatten().forEach{header->
                HttpCookie.parse(header).filter{it.name=="qw_session"}.forEach{cookie->
                    if(cookie.maxAge==0L||cookie.value.isBlank())clear()else{saved.put("cookie","qw_session=${cookie.value}");store.write(saved)}
                }
            }
            return data
        } finally {c.disconnect()}
    }
}
