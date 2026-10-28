package com.karelisio.vigie.ui.theme

import android.content.Context
import android.os.Build
import android.provider.Settings
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.ColorScheme
import androidx.compose.material3.MaterialExpressiveTheme
import androidx.compose.material3.MotionScheme
import androidx.compose.material3.Typography
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.dynamicDarkColorScheme
import androidx.compose.material3.dynamicLightColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.Immutable
import androidx.compose.runtime.ReadOnlyComposable
import androidx.compose.runtime.remember
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.lerp
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.sp

// Palette de repli bleu-vert (sans couleurs dynamiques, Android < 12).
private val LightFallback = lightColorScheme(
    primary = Color(0xFF006A6A), onPrimary = Color.White,
    primaryContainer = Color(0xFF9CF1F0), onPrimaryContainer = Color(0xFF004F50),
    secondary = Color(0xFF4A6363), onSecondary = Color.White,
    secondaryContainer = Color(0xFFCCE8E7), onSecondaryContainer = Color(0xFF324B4B),
    tertiary = Color(0xFF4B607C), onTertiary = Color.White,
    tertiaryContainer = Color(0xFFD3E4FF), onTertiaryContainer = Color(0xFF334863),
    error = Color(0xFFBA1A1A), onError = Color.White,
    errorContainer = Color(0xFFFFDAD6), onErrorContainer = Color(0xFF93000A),
    background = Color(0xFFF4FBFA), onBackground = Color(0xFF161D1D),
    surface = Color(0xFFF4FBFA), onSurface = Color(0xFF161D1D),
    surfaceVariant = Color(0xFFDAE5E4), onSurfaceVariant = Color(0xFF3F4948),
    outline = Color(0xFF6F7979), outlineVariant = Color(0xFFBEC9C8),
    inverseSurface = Color(0xFF2B3231), inverseOnSurface = Color(0xFFECF2F1), inversePrimary = Color(0xFF80D5D4),
    surfaceDim = Color(0xFFD5DBDA), surfaceBright = Color(0xFFF4FBFA),
    surfaceContainerLowest = Color.White, surfaceContainerLow = Color(0xFFEFF5F4),
    surfaceContainer = Color(0xFFE9EFEE), surfaceContainerHigh = Color(0xFFE3E9E8),
    surfaceContainerHighest = Color(0xFFDDE4E3),
)

private val DarkFallback = darkColorScheme(
    primary = Color(0xFF80D5D4), onPrimary = Color(0xFF003737),
    primaryContainer = Color(0xFF004F4F), onPrimaryContainer = Color(0xFF9CF1F0),
    secondary = Color(0xFFB0CCCB), onSecondary = Color(0xFF1B3434),
    secondaryContainer = Color(0xFF324B4B), onSecondaryContainer = Color(0xFFCCE8E7),
    tertiary = Color(0xFFB3C8E8), onTertiary = Color(0xFF1C314B),
    tertiaryContainer = Color(0xFF334863), onTertiaryContainer = Color(0xFFD3E4FF),
    error = Color(0xFFFFB4AB), onError = Color(0xFF690005),
    errorContainer = Color(0xFF93000A), onErrorContainer = Color(0xFFFFDAD6),
    background = Color(0xFF0E1514), onBackground = Color(0xFFDDE4E3),
    surface = Color(0xFF0E1514), onSurface = Color(0xFFDDE4E3),
    surfaceVariant = Color(0xFF3F4948), onSurfaceVariant = Color(0xFFBEC9C8),
    outline = Color(0xFF889392), outlineVariant = Color(0xFF3F4948),
    inverseSurface = Color(0xFFDDE4E3), inverseOnSurface = Color(0xFF2B3231), inversePrimary = Color(0xFF006A6A),
    surfaceDim = Color(0xFF0E1514), surfaceBright = Color(0xFF343A3A),
    surfaceContainerLowest = Color(0xFF090F0F), surfaceContainerLow = Color(0xFF161D1D),
    surfaceContainer = Color(0xFF1A2121), surfaceContainerHigh = Color(0xFF252B2B),
    surfaceContainerHighest = Color(0xFF2F3636),
)

/** Couleurs sémantiques d'état, légèrement harmonisées avec la couleur principale. */
@Immutable
data class StatusColors(
    val ok: Color,
    val okContainer: Color,
    val onOkContainer: Color,
    val warn: Color,
    val warnContainer: Color,
    val onWarnContainer: Color,
    val critical: Color,
    val criticalContainer: Color,
    val onCriticalContainer: Color,
    val neutral: Color,
) {
    fun of(status: String): Color = when (status) {
        "ok", "up", "running", "healthy" -> ok
        "warning", "starting", "pending" -> warn
        "critical", "down" -> critical
        else -> neutral
    }

    fun containerOf(status: String): Color = when (status) {
        "ok", "up" -> okContainer
        "warning", "pending" -> warnContainer
        "critical", "down" -> criticalContainer
        else -> neutral.copy(alpha = 0.16f)
    }

    fun onContainerOf(status: String): Color = when (status) {
        "ok", "up" -> onOkContainer
        "warning", "pending" -> onWarnContainer
        "critical", "down" -> onCriticalContainer
        else -> neutral
    }

    /** Glisse du vert à l'orange puis au rouge à l'approche du seuil. */
    fun forLevel(value: Double, threshold: Double): Color {
        val warnAt = threshold - 25
        val critAt = threshold - 8
        return when {
            value <= warnAt -> ok
            value <= critAt -> lerp(ok, warn, ((value - warnAt) / (critAt - warnAt)).toFloat())
            value <= threshold -> lerp(warn, critical, ((value - critAt) / (threshold - critAt)).toFloat())
            else -> critical
        }
    }

    companion object {
        fun create(scheme: ColorScheme, dark: Boolean): StatusColors {
            fun h(c: Color) = lerp(c, scheme.primary, 0.12f)
            return if (dark) StatusColors(
                ok = h(Color(0xFF6EDB8F)), okContainer = h(Color(0xFF0F5228)), onOkContainer = Color(0xFFB8F5C8),
                warn = h(Color(0xFFFFB86B)), warnContainer = h(Color(0xFF653E00)), onWarnContainer = Color(0xFFFFDDB8),
                critical = h(Color(0xFFFF8A80)), criticalContainer = h(Color(0xFF8C1D18)), onCriticalContainer = Color(0xFFFFDAD6),
                neutral = scheme.outline,
            ) else StatusColors(
                ok = h(Color(0xFF1B873F)), okContainer = h(Color(0xFFC8F5D2)), onOkContainer = Color(0xFF00391A),
                warn = h(Color(0xFFB25E00)), warnContainer = h(Color(0xFFFFDDB8)), onWarnContainer = Color(0xFF2D1600),
                critical = h(Color(0xFFC62828)), criticalContainer = h(Color(0xFFFFDAD6)), onCriticalContainer = Color(0xFF410002),
                neutral = scheme.outline,
            )
        }
    }
}

val LocalStatusColors = staticCompositionLocalOf { StatusColors.create(LightFallback, false) }

/** Vrai quand l'utilisateur a désactivé les animations du système. */
val LocalReducedMotion = staticCompositionLocalOf { false }

object Vigie {
    val status: StatusColors
        @Composable @ReadOnlyComposable get() = LocalStatusColors.current
    val reducedMotion: Boolean
        @Composable @ReadOnlyComposable get() = LocalReducedMotion.current
}

fun Context.animationsDisabled(): Boolean =
    Settings.Global.getFloat(contentResolver, Settings.Global.ANIMATOR_DURATION_SCALE, 1f) == 0f

private val Tnum = "tnum"

private fun Typography.expressive(): Typography = copy(
    displayLarge = displayLarge.copy(fontWeight = FontWeight.Medium, letterSpacing = (-0.5).sp),
    displayMedium = displayMedium.copy(fontWeight = FontWeight.Medium),
    headlineLarge = headlineLarge.copy(fontWeight = FontWeight.SemiBold),
    headlineMedium = headlineMedium.copy(fontWeight = FontWeight.SemiBold),
    titleLarge = titleLarge.copy(fontWeight = FontWeight.SemiBold),
    labelLarge = labelLarge.copy(fontWeight = FontWeight.SemiBold),
)

/** Style pour les nombres (chiffres tabulaires : pas de saut horizontal quand ils défilent). */
fun TextStyle.numeric(): TextStyle = copy(fontFeatureSettings = Tnum)

@Composable
fun VigieTheme(dynamicColor: Boolean = true, content: @Composable () -> Unit) {
    val dark = isSystemInDarkTheme()
    val context = LocalContext.current
    val scheme = when {
        dynamicColor && Build.VERSION.SDK_INT >= Build.VERSION_CODES.S ->
            if (dark) dynamicDarkColorScheme(context) else dynamicLightColorScheme(context)
        dark -> DarkFallback
        else -> LightFallback
    }
    val status = remember(scheme, dark) { StatusColors.create(scheme, dark) }
    val reduced = remember { context.animationsDisabled() }
    val typography = remember { Typography().expressive() }
    MaterialExpressiveTheme(colorScheme = scheme, motionScheme = MotionScheme.expressive(), typography = typography) {
        CompositionLocalProvider(
            LocalStatusColors provides status,
            LocalReducedMotion provides reduced,
            content = content,
        )
    }
}
