package link.quantway.android

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.AtomicFile
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

// Credentials and cached profiles never enter ordinary preferences or backups.
class SubscriptionStore(context:Context) {
    private val file=AtomicFile(File(context.noBackupFilesDir,"subscriptions.enc"))
    private fun key():SecretKey {
        val store=KeyStore.getInstance("AndroidKeyStore").apply{load(null)}
        (store.getKey("quantway-subscriptions",null) as? SecretKey)?.let{return it}
        return KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES,"AndroidKeyStore").apply{init(KeyGenParameterSpec.Builder("quantway-subscriptions",KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT).setBlockModes(KeyProperties.BLOCK_MODE_GCM).setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE).build())}.generateKey()
    }
    @Synchronized fun read():JSONObject {
        if(!file.baseFile.exists())return JSONObject().put("sources",JSONArray())
        val data=file.openRead().use{it.readBytes()}
        require(data.size in 29..(8*1024*1024))
        val cipher=Cipher.getInstance("AES/GCM/NoPadding").apply{init(Cipher.DECRYPT_MODE,key(),GCMParameterSpec(128,data.copyOfRange(0,12)))}
        return JSONObject(String(cipher.doFinal(data.copyOfRange(12,data.size)),Charsets.UTF_8))
    }
    @Synchronized fun write(value:JSONObject) {
        val cipher=Cipher.getInstance("AES/GCM/NoPadding").apply{init(Cipher.ENCRYPT_MODE,key())}
        val bytes=cipher.iv+cipher.doFinal(value.toString().toByteArray(Charsets.UTF_8))
        val stream=file.startWrite();try{stream.write(bytes);file.finishWrite(stream)}catch(e:Exception){file.failWrite(stream);throw e}
    }
}

data class SubscriptionSource(val id:String,val url:String,val name:String,val api:Boolean,val cache:JSONObject,val updatedAt:Long) {
    fun json()=JSONObject().put("id",id).put("url",url).put("name",name).put("api",api).put("cache",cache).put("updated_at",updatedAt)
    companion object {fun from(v:JSONObject)=SubscriptionSource(v.getString("id"),v.getString("url"),v.getString("name"),v.getBoolean("api"),v.getJSONObject("cache"),v.optLong("updated_at"))}
}

data class ProbeObservation(val jobId:String,val label:String,val outcome:String,val elapsedMS:Long,val epoch:String,val network:String,val at:Long)
fun JSONObject.protocolLabel():String = when(optString("protocol").lowercase()) {
    "hysteria","hy2","hysteria2"->"Hysteria2"
    "awg","amneziawg"->"AmneziaWG"
    "vless"->"VLESS"+when{optString("security")=="reality"->" · Reality";optString("transport")=="xhttp"->" · XHTTP";optString("transport").isNotBlank()->" · "+optString("transport").uppercase();else->""}
    else->if(optString("engine")=="amneziawg")"AmneziaWG" else "Xray"
}
