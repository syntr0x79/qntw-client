package link.quantway.android

import android.app.UiModeManager
import android.content.Context
import android.content.res.Configuration
import android.content.pm.PackageManager
import androidx.compose.foundation.border
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.ime
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.focus.FocusDirection
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.type
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.unit.dp

internal val LocalTvMode = staticCompositionLocalOf { false }

internal fun isTelevision(context:Context):Boolean = context.getSystemService(UiModeManager::class.java)?.currentModeType==Configuration.UI_MODE_TYPE_TELEVISION || context.packageManager.hasSystemFeature(PackageManager.FEATURE_LEANBACK)

@Composable internal fun isTelevision():Boolean {
    val context=LocalContext.current
    return remember(context){isTelevision(context)}
}

@Composable internal fun Modifier.tvInputNavigation():Modifier {
    if(!LocalTvMode.current)return this
    val keyboardVisible=WindowInsets.ime.getBottom(LocalDensity.current)>0
    val focus=LocalFocusManager.current
    return this.onPreviewKeyEvent{event->
        if(keyboardVisible || event.type!=KeyEventType.KeyDown)false else {
            val direction=when(event.key){Key.DirectionLeft->FocusDirection.Left;Key.DirectionUp->FocusDirection.Up;Key.DirectionDown->FocusDirection.Down;else->null}
            direction?.let{focus.moveFocus(it)}?:false
        }
    }
}

@Composable internal fun Modifier.tvFocus():Modifier {
    if(!LocalTvMode.current)return this
    var focused by remember { mutableStateOf(false) }
    val color=if(focused)MaterialTheme.colorScheme.primary else androidx.compose.ui.graphics.Color.Transparent
    return this.onFocusChanged{focused=it.hasFocus}.border(3.dp,color,RoundedCornerShape(14.dp))
}
