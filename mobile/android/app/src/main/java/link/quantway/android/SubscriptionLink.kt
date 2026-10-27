package link.quantway.android

import java.net.URI

// A device link grants configuration/probe access, never account administration.
data class SubscriptionLink(val origin: String, val token: String) {
    companion object {
        fun parse(text: String): SubscriptionLink {
            val uri = URI(text.trim())
            require((uri.scheme == "https" || (BuildConfig.DEBUG && uri.scheme == "http" && uri.host == "10.0.2.2")) && !uri.host.isNullOrBlank() && uri.userInfo == null && uri.fragment == null) { "Нужна HTTPS ссылка на подписку" }
            val match = Regex("^/sub/([A-Za-z0-9_-]{16,256})/?$").matchEntire(uri.path ?: "") ?: error("Нужна ссылка вида https://сервис/sub/…")
            return SubscriptionLink(URI(uri.scheme, null, uri.host, uri.port, null, null, null).toString(), match.groupValues[1])
        }
    }
}
