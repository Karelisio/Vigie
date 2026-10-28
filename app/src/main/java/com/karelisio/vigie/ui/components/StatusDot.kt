package com.karelisio.vigie.ui.components

import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.layout.size
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.karelisio.vigie.data.Container
import com.karelisio.vigie.ui.theme.Vigie

/** Statut visuel d'un conteneur : ok, warning, critical, neutral (arrêté normalement). */
fun Container.visualStatus(): String = when {
    hasProblem -> "critical"
    state == "running" && health == "starting" -> "warning"
    state == "running" -> "ok"
    state == "paused" || state == "restarting" -> "warning"
    else -> "neutral"
}

/**
 * Pastille d'état qui « respire » doucement (alpha + échelle).
 * En défaut, elle respire plus vite et émet un halo.
 */
@Composable
fun StatusDot(status: String, modifier: Modifier = Modifier, size: Dp = 10.dp, breathe: Boolean = true) {
    val colors = Vigie.status
    val color by animateColorAsState(colors.of(status), label = "dot")
    val reduced = Vigie.reducedMotion
    val animate = breathe && !reduced && status != "neutral"
    val critical = status == "critical"
    val period = if (critical) 900 else 2200
    val t = rememberInfiniteTransition(label = "breath")
    val phase by if (animate) t.animateFloat(
        0f, 1f, infiniteRepeatable(tween(period, easing = FastOutSlowInEasing), RepeatMode.Reverse), label = "phase",
    ) else androidx.compose.runtime.remember { androidx.compose.runtime.mutableFloatStateOf(1f) }
    val halo by if (animate && critical) t.animateFloat(
        0f, 1f, infiniteRepeatable(tween(1400, easing = LinearEasing)), label = "halo",
    ) else androidx.compose.runtime.remember { androidx.compose.runtime.mutableFloatStateOf(0f) }

    Canvas(modifier.size(size * 2.2f)) {
        val c = Offset(this.size.width / 2, this.size.height / 2)
        val r = size.toPx() / 2
        if (animate && critical) {
            drawCircle(color.copy(alpha = (1 - halo) * 0.45f), radius = r * (1f + halo * 1.2f), center = c)
        }
        val scale = if (animate) 0.86f + 0.16f * phase else 1f
        val alpha = if (animate) 0.62f + 0.38f * phase else 1f
        if (animate) drawCircle(color.copy(alpha = 0.18f * phase), radius = r * 1.55f, center = c)
        drawCircle(color.copy(alpha = alpha), radius = r * scale, center = c)
    }
}
