package com.karelisio.vigie.ui.components

import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.SpringSpec
import androidx.compose.animation.core.Spring
import androidx.compose.animation.core.keyframes
import androidx.compose.animation.core.spring
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.composed
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.hapticfeedback.HapticFeedback
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.platform.LocalHapticFeedback
import com.karelisio.vigie.ui.theme.LocalReducedMotion

/** Ressorts communs (rebond doux, dans l'esprit M3 Expressive). */
object Springs {
    fun <T> bouncy(): SpringSpec<T> = spring(dampingRatio = 0.62f, stiffness = Spring.StiffnessMediumLow)
    fun <T> gentle(): SpringSpec<T> = spring(dampingRatio = Spring.DampingRatioNoBouncy, stiffness = Spring.StiffnessLow)
    fun <T> snappy(): SpringSpec<T> = spring(dampingRatio = 0.75f, stiffness = Spring.StiffnessMedium)
}

/** Retours haptiques subtils, synchronisés avec les animations. */
class Haptics(private val h: HapticFeedback) {
    fun tick() = h.performHapticFeedback(HapticFeedbackType.SegmentTick)
    fun frequentTick() = h.performHapticFeedback(HapticFeedbackType.SegmentFrequentTick)
    fun confirm() = h.performHapticFeedback(HapticFeedbackType.Confirm)
    fun reject() = h.performHapticFeedback(HapticFeedbackType.Reject)
    fun threshold() = h.performHapticFeedback(HapticFeedbackType.GestureThresholdActivate)
    fun toggle(on: Boolean) = h.performHapticFeedback(if (on) HapticFeedbackType.ToggleOn else HapticFeedbackType.ToggleOff)
    fun longPress() = h.performHapticFeedback(HapticFeedbackType.LongPress)
}

@Composable
fun rememberHaptics(): Haptics {
    val h = LocalHapticFeedback.current
    return remember(h) { Haptics(h) }
}

/** Léger tremblement horizontal à la première apparition (conteneur en erreur). */
fun Modifier.shakeOnAppear(enabled: Boolean, key: Any? = Unit): Modifier = composed {
    val reduced = LocalReducedMotion.current
    val offset = remember { Animatable(0f) }
    // Premier affichage d'un élément revenu à l'écran par défilement : pas de tremblement.
    val fresh = screenJustOpened()
    val skipFirst = remember { !fresh }
    val first = remember { booleanArrayOf(true) }
    LaunchedEffect(enabled, key) {
        val skip = first[0] && skipFirst
        first[0] = false
        if (enabled && !reduced && !skip) {
            offset.animateTo(0f, keyframes {
                durationMillis = 520
                (-9f) at 60
                9f at 140
                (-7f) at 220
                6f at 300
                (-3f) at 380
                2f at 450
                0f at 520
            })
        }
    }
    graphicsLayer { translationX = offset.value * density }
}

/** Tremblement déclenché à la demande (erreur d'action). */
class ShakeController {
    internal val offset = Animatable(0f)
    suspend fun shake() {
        offset.snapTo(0f)
        offset.animateTo(0f, keyframes {
            durationMillis = 420
            (-10f) at 50
            10f at 120
            (-7f) at 200
            5f at 280
            (-2f) at 350
            0f at 420
        })
    }
}

fun Modifier.shake(controller: ShakeController): Modifier =
    graphicsLayer { translationX = controller.offset.value * density }
