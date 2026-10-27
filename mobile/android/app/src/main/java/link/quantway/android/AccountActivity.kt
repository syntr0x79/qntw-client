package link.quantway.android

import android.os.Bundle
import android.content.Intent
import android.net.Uri
import android.webkit.*
import androidx.activity.ComponentActivity

// Reuse the existing authenticated cabinet for balance, pause/resume and device
// management. Subscription links never become account administration credentials.
class AccountActivity : ComponentActivity() {
    private var view:WebView?=null
    override fun onCreate(savedInstanceState:Bundle?) {
        super.onCreate(savedInstanceState)
        val uri=Uri.parse(intent.getStringExtra("url")?:return finish())
        if(uri.scheme!="https"||uri.host.isNullOrBlank()){finish();return}
        val web=WebView(this);view=web
        web.settings.javaScriptEnabled=true;web.settings.domStorageEnabled=true
        web.settings.allowFileAccess=false;web.settings.allowContentAccess=false
        web.settings.mixedContentMode=WebSettings.MIXED_CONTENT_NEVER_ALLOW
        CookieManager.getInstance().setAcceptThirdPartyCookies(web,false)
        web.webViewClient=object:WebViewClient(){
            override fun shouldOverrideUrlLoading(view:WebView,request:WebResourceRequest):Boolean{
                val target=request.url
                if(target.scheme=="https"&&target.host==uri.host&&target.port==uri.port)return false
                if(request.isForMainFrame&&(target.scheme=="https"||target.scheme=="tg"))startActivity(Intent(Intent.ACTION_VIEW,target))
                return true
            }
        }
        setContentView(web);web.loadUrl(uri.toString())
    }
    override fun onDestroy(){view?.destroy();super.onDestroy()}
}
