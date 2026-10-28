package link.quantway.android

import engine.Engine
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.withContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.withPermit
import org.json.JSONObject

// Android can close a socket before delivering its network-loss callback. Allow
// that callback to settle before classifying a transport failure as a route fault.
class ProbeExecutor(private val physical: PhysicalNetwork) {
    companion object { const val PARALLELISM = 10; private val gate = Semaphore(PARALLELISM) }
    suspend fun execute(job: JSONObject, controlURL: String, timeoutMS: Long, snapshot: PhysicalNetwork.Snapshot): JSONObject = gate.withPermit {
        currentCoroutineContext().ensureActive()
        // Provisioning is a snapshot taken when the run was issued. The peer may
        // already be installed by the time we reach AWG; only a real probe can
        // establish connectivity. Give newly allocated peers time to arrive.
        val attempts = if (job.optBoolean("provisioning") && job.optString("engine") == "amneziawg") 3 else 1
        val deadline = android.os.SystemClock.elapsedRealtime() + timeoutMS
        repeat(attempts) { attempt ->
            val remaining = (deadline - android.os.SystemClock.elapsedRealtime()).coerceAtLeast(1L)
            val attemptTimeout = if (attempts > 1) (remaining / (attempts - attempt)).coerceAtLeast(100L) else timeoutMS
            val result = withContext(Dispatchers.IO) {
                try { JSONObject(Engine.probeOnNetwork(job.toString(), controlURL, attemptTimeout, physical.adapter(snapshot), snapshot.epoch)) }
                catch (_: Exception) { JSONObject().put("job_id", job.getString("job_id")).put("outcome", "unprobeable").put("reason_code", "client_error").put("elapsed_ms", 0) }
            }
            if (result.optString("outcome") == "fail") delay(500)
            try { physical.checkCurrent(snapshot) }
            catch (_: Exception) { result.put("outcome", "cancelled").put("reason_code", "network_changed") }
            if (result.optString("outcome") != "fail" || attempt == attempts - 1 || android.os.SystemClock.elapsedRealtime() >= deadline) return@withPermit result
            delay(minOf(500L, (deadline - android.os.SystemClock.elapsedRealtime()).coerceAtLeast(0L)))
        }
        error("Probe did not produce a result")
    }
}
