package link.quantway.android

import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.*
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp

internal val QuantwayDark = darkColorScheme(
    primary=Color(0xffdbeaf0), onPrimary=Color(0xff10202a),
    secondary=Color(0xffa7c7d7), onSecondary=Color(0xff17323f),
    background=Color(0xff0b1319), onBackground=Color(0xffeef3f5),
    surface=Color(0xff15212a), onSurface=Color(0xffeef3f5),
    surfaceVariant=Color(0xff1e2c35), onSurfaceVariant=Color(0xffaabac4),
    surfaceContainerLow=Color(0xff111b22), surfaceContainer=Color(0xff15212a),
    surfaceContainerHigh=Color(0xff22323d), surfaceContainerHighest=Color(0xff2c3d48),
    outline=Color(0xff40535f), outlineVariant=Color(0xff293b46),
    secondaryContainer=Color(0xff263f4c), onSecondaryContainer=Color(0xffd2eaf4)
)
internal val QuantwayLight = lightColorScheme(
    primary=Color(0xff182b38), onPrimary=Color.White,
    secondary=Color(0xff506776), onSecondary=Color.White,
    background=Color(0xfff5f3ee), onBackground=Color(0xff17232d),
    surface=Color.White, onSurface=Color(0xff17232d),
    surfaceVariant=Color(0xffe7e8e7), onSurfaceVariant=Color(0xff687782),
    surfaceContainerLow=Color(0xffeeeee9), surfaceContainer=Color.White,
    surfaceContainerHigh=Color(0xffe3e9ea), surfaceContainerHighest=Color(0xffd6e2e5),
    outline=Color(0xffb8c6cc), outlineVariant=Color(0xffd8dfe0),
    secondaryContainer=Color(0xffd8e8ed), onSecondaryContainer=Color(0xff183744)
)
private val QuantwayTypography=Typography(
    headlineMedium=TextStyle(fontFamily=FontFamily.SansSerif,fontWeight=FontWeight.SemiBold,fontSize=27.sp,lineHeight=34.sp,letterSpacing=(-.5).sp),
    headlineSmall=TextStyle(fontFamily=FontFamily.SansSerif,fontWeight=FontWeight.SemiBold,fontSize=23.sp,lineHeight=30.sp),
    titleLarge=TextStyle(fontFamily=FontFamily.SansSerif,fontWeight=FontWeight.SemiBold,fontSize=20.sp,lineHeight=27.sp),
    titleMedium=TextStyle(fontFamily=FontFamily.SansSerif,fontWeight=FontWeight.SemiBold,fontSize=16.sp,lineHeight=23.sp),
    titleSmall=TextStyle(fontFamily=FontFamily.SansSerif,fontWeight=FontWeight.Medium,fontSize=15.sp,lineHeight=21.sp),
    bodyMedium=TextStyle(fontFamily=FontFamily.SansSerif,fontSize=14.sp,lineHeight=21.sp),
    bodySmall=TextStyle(fontFamily=FontFamily.SansSerif,fontSize=13.sp,lineHeight=19.sp),
    labelLarge=TextStyle(fontFamily=FontFamily.SansSerif,fontWeight=FontWeight.SemiBold,fontSize=14.sp,lineHeight=20.sp),
    labelMedium=TextStyle(fontFamily=FontFamily.SansSerif,fontWeight=FontWeight.Medium,fontSize=12.sp,lineHeight=17.sp),
    labelSmall=TextStyle(fontFamily=FontFamily.SansSerif,fontWeight=FontWeight.Medium,fontSize=11.sp,lineHeight=16.sp)
)

@Composable internal fun QuantwayTheme(dark:Boolean,content:@Composable ()->Unit){
    MaterialTheme(colorScheme=if(dark)QuantwayDark else QuantwayLight,typography=QuantwayTypography,
        shapes=Shapes(small=RoundedCornerShape(10.dp),medium=RoundedCornerShape(14.dp),large=RoundedCornerShape(18.dp),extraLarge=RoundedCornerShape(24.dp)),content=content)
}
