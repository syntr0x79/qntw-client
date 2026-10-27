package link.quantway.android

import java.net.HttpURLConnection
import java.net.URI
import org.json.JSONObject

class ApiFailure(val status: Int, val retryAfter: Long = 5) : Exception("Сервер ответил $status")

class ProbeApi(base: String, private val token: String, private val network: (() -> android.net.Network?)? = null) {
    private val origin: String
    val scope = java.security.MessageDigest.getInstance("SHA-256").digest(token.toByteArray(Charsets.UTF_8)).joinToString("") { "%02x".format(it) }
    init {
        val uri = URI(base.trim())
        require((uri.scheme == "https" || (BuildConfig.DEBUG && uri.scheme == "http" && uri.host == "10.0.2.2")) && !uri.host.isNullOrBlank() && uri.userInfo == null && uri.query == null && uri.fragment == null && (uri.path.isNullOrEmpty() || uri.path == "/")) { "Укажите HTTPS адрес сервиса без пути" }
        require(token.isNotBlank() && !token.contains('\n') && !token.contains('\r')) { "Нужен токен устройства" }
        origin = base.trim().trimEnd('/')
    }
    fun post(path: String, body: JSONObject): JSONObject = request("POST", "/api/mobile/v1/probe/$path", body)
    fun profiles(): JSONObject = request("GET", "/api/mobile/v1/profiles", null)
    private fun request(method: String, path: String, body: JSONObject?): JSONObject {
        val url = URI("$origin$path").toURL()
        val physical = network?.invoke() ?: (QuantwayVpnService.current?.application as? QuantwayApplication)?.physical?.state?.value?.network
        val connection = (physical?.openConnection(url) ?: url.openConnection()) as HttpURLConnection
        try {
            connection.requestMethod = method
            connection.instanceFollowRedirects = false
            connection.connectTimeout = 10_000
            connection.readTimeout = 20_000
            connection.doOutput = body != null
            connection.setRequestProperty("Content-Type", "application/json")
            connection.setRequestProperty("Authorization", "Bearer $token")
            connection.setRequestProperty("User-Agent", "QuantwayAndroid/${BuildConfig.VERSION_NAME}")
            if (body != null) connection.outputStream.use { it.write(body.toString().toByteArray(Charsets.UTF_8)) }
            if (connection.responseCode !in 200..299) throw ApiFailure(connection.responseCode, connection.getHeaderField("Retry-After")?.toLongOrNull()?.coerceIn(1, 60) ?: 5)
            val data = connection.inputStream.use { input ->
                val output = java.io.ByteArrayOutputStream()
                val buffer = ByteArray(8192)
                while (output.size() <= 1024 * 1024) {
                    val count = input.read(buffer, 0, minOf(buffer.size, 1024 * 1024 + 1 - output.size()))
                    if (count < 0) break
                    output.write(buffer, 0, count)
                }
                output.toByteArray()
            }
            require(data.size <= 1024 * 1024) { "Ответ сервера слишком большой" }
            return JSONObject(String(data, Charsets.UTF_8))
        } finally { connection.disconnect() }
    }
}
