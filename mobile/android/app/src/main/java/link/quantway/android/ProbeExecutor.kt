package link.quantway.android

import engine.Engine
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.withContext
import org.json.JSONObject

// Android can close a socket before delivering its network-loss callback. Allow
// that callback to settle before classifying a transport failure as a route fault.
class ProbeExecutor(private val physical: PhysicalNetwork) {
    companion object { private val gate = kotlinx.coroutines.sync.Mutex() }
    suspend fun execute(job: JSONObject, controlURL: String, timeoutMS: Long, snapshot: PhysicalNetwork.Snapshot): JSONObject {
        if (job.optBoolean("provisioning")) return JSONObject().put("job_id",job.getString("job_id")).put("outcome","unprobeable").put("reason_code","provisioning_pending").put("elapsed_ms",0)
        gate.lock()
        try {
        val result = withContext(Dispatchers.IO) {
            try { JSONObject(Engine.probe(job.toString(), controlURL, timeoutMS, physical.adapter(snapshot))) }
            catch (_: Exception) { JSONObject().put("job_id", job.getString("job_id")).put("outcome", "unprobeable").put("reason_code", "client_error").put("elapsed_ms", 0) }
        }
        if (result.optString("outcome") == "fail") delay(500)
        try { physical.checkCurrent(snapshot) }
        catch (_: Exception) { result.put("outcome", "cancelled").put("reason_code", "network_changed") }
        return result
        } finally { gate.unlock() }
    }
}
