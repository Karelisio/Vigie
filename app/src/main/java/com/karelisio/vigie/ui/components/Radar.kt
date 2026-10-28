package com.karelisio.vigie.ui.components

import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.withFrameNanos
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.rotate
import androidx.compose.ui.unit.dp
import com.karelisio.vigie.ui.theme.Vigie
import kotlin.math.cos
import kotlin.math.sin

/** Un écho sur le radar : un conteneur ou un check. */
@Immutable
data class RadarBlip(val key: String, val status: String)

private data class BlipPos(val angle: Float, val radius: Float)

private fun position(key: String): BlipPos {
    // Position stable dérivée du nom (le même conteneur reste au même endroit).
    var h = 1125899906842597L
    for (c in key) h = 31 * h + c.code
    val a = ((h ushr 8) % 3600).toFloat() / 10f
    val r = 0.28f + ((h ushr 24) % 1000).toFloat() / 1000f * 0.6f
    return BlipPos(if (a < 0) a + 360 else a, r)
}

/**
 * Radar de l'accueil : balayage continu aux couleurs dynamiques.
 * Tout va bien → vert et lent ; problème → rouge, plus rapide, écho pulsé sur les points en défaut.
 * Hors ligne → le balayage ralentit jusqu'à s'arrêter.
 */
@Composable
fun Radar(
    status: String,
    blips: List<RadarBlip>,
    modifier: Modifier = Modifier,
    active: Boolean = true,
    content: @Composable BoxScope.() -> Unit = {},
) {
    val colors = Vigie.status
    val reduced = Vigie.reducedMotion
    val scheme = MaterialTheme.colorScheme
    val base = when {
        !active -> scheme.outline
        status == "critical" -> colors.critical
        status == "warning" -> colors.warn
        status == "ok" -> colors.ok
        else -> scheme.primary
    }
    val color by animateColorAsState(base, tween(900), label = "radarColor")
    val speedTarget = when {
        !active || reduced -> 0f
        status == "critical" -> 230f
        status == "warning" -> 130f
        else -> 55f
    }
    val speed by animateFloatAsState(speedTarget, tween(1600), label = "radarSpeed")
    var angle by remember { mutableFloatStateOf(300f) }
    LaunchedEffect(Unit) {
        var last = 0L
        while (true) {
            withFrameNanos { t ->
                if (last != 0L && speed > 0.01f) angle = (angle + speed * (t - last) / 1_000_000_000f) % 360f
                last = t
            }
        }
    }
    val pulse = rememberInfiniteTransition(label = "echo")
    val echo by pulse.animateFloat(0f, 1f, infiniteRepeatable(tween(1500, easing = LinearEasing)), label = "echoPhase")
    val positions = remember(blips) { blips.map { it to position(it.key) } }
    val ring = scheme.outlineVariant
    val surface = scheme.surfaceContainerLow

    Box(modifier.aspectRatio(1f), contentAlignment = Alignment.Center) {
        Canvas(Modifier.fillMaxSize()) {
            val r = size.minDimension / 2 - 4.dp.toPx()
            val c = center
            // Fond : disque et halo radial.
            drawCircle(surface, r, c)
            drawCircle(Brush.radialGradient(listOf(color.copy(alpha = 0.16f), color.copy(alpha = 0.03f), Color.Transparent), c, r), r, c)
            // Anneaux et réticule.
            for (i in 1..4) drawCircle(ring.copy(alpha = 0.55f), r * i / 4f, c, style = Stroke(1.dp.toPx()))
            drawLine(ring.copy(alpha = 0.35f), Offset(c.x - r, c.y), Offset(c.x + r, c.y), 1.dp.toPx())
            drawLine(ring.copy(alpha = 0.35f), Offset(c.x, c.y - r), Offset(c.x, c.y + r), 1.dp.toPx())
            drawTicks(c, r, ring)

            // Balayage : dégradé conique qui traîne derrière la ligne.
            rotate(angle, c) {
                drawCircle(
                    Brush.sweepGradient(
                        0f to Color.Transparent,
                        0.70f to Color.Transparent,
                        0.93f to color.copy(alpha = 0.22f),
                        1f to color.copy(alpha = 0.55f),
                        center = c,
                    ),
                    r, c,
                )
                drawLine(
                    Brush.linearGradient(listOf(color.copy(alpha = 0.2f), color), c, Offset(c.x + r, c.y)),
                    c, Offset(c.x + r, c.y), 2.5.dp.toPx(), StrokeCap.Round,
                )
            }

            // Échos.
            for ((blip, pos) in positions) {
                val rad = Math.toRadians(pos.angle.toDouble())
                val p = Offset(c.x + (cos(rad) * pos.radius * r).toFloat(), c.y + (sin(rad) * pos.radius * r).toFloat())
                val behind = ((angle - pos.angle) % 360f + 360f) % 360f
                val lit = if (speed > 1f) (1f - behind / 140f).coerceIn(0f, 1f) else 0.6f
                val bc = when (blip.status) {
                    "critical" -> colors.critical
                    "warning" -> colors.warn
                    "neutral" -> scheme.outline
                    else -> color
                }
                val dot = 3.2.dp.toPx()
                if (blip.status == "critical") {
                    if (!reduced) {
                        for (k in 0..1) {
                            val ph = (echo + k * 0.5f) % 1f
                            drawCircle(bc.copy(alpha = (1 - ph) * 0.55f), dot + ph * r * 0.22f, p, style = Stroke(2.dp.toPx()))
                        }
                    } else {
                        drawCircle(bc.copy(alpha = 0.4f), dot * 3f, p, style = Stroke(2.dp.toPx()))
                    }
                    drawCircle(bc.copy(alpha = 0.28f), dot * 2.4f, p)
                    drawCircle(bc, dot * 1.25f, p)
                } else {
                    val a = 0.18f + 0.82f * lit
                    if (lit > 0.05f) drawCircle(bc.copy(alpha = 0.22f * lit), dot * 2.6f, p)
                    drawCircle(bc.copy(alpha = a), dot * (0.85f + 0.35f * lit), p)
                }
            }
            // Bord.
            drawCircle(color.copy(alpha = 0.45f), r, c, style = Stroke(1.5.dp.toPx()))
            drawCircle(color, 4.dp.toPx(), c)
        }
        content()
    }
}

private fun DrawScope.drawTicks(c: Offset, r: Float, color: Color) {
    for (i in 0 until 72) {
        val long = i % 6 == 0
        val a = Math.toRadians(i * 5.0)
        val len = if (long) 7.dp.toPx() else 3.dp.toPx()
        val o = Offset(c.x + (cos(a) * r).toFloat(), c.y + (sin(a) * r).toFloat())
        val ii = Offset(c.x + (cos(a) * (r - len)).toFloat(), c.y + (sin(a) * (r - len)).toFloat())
        drawLine(color.copy(alpha = if (long) 0.8f else 0.45f), ii, o, 1.dp.toPx())
    }
}

/** Petit radar décoratif (onboarding, test de connexion, rafraîchissement). */
@Composable
fun MiniRadar(modifier: Modifier = Modifier, color: Color = MaterialTheme.colorScheme.primary, speed: Float = 140f, waves: Boolean = true) {
    val reduced = Vigie.reducedMotion
    var angle by remember { mutableFloatStateOf(0f) }
    LaunchedEffect(speed, reduced) {
        if (reduced) return@LaunchedEffect
        var last = 0L
        while (true) withFrameNanos { t ->
            if (last != 0L) angle = (angle + speed * (t - last) / 1_000_000_000f) % 360f
            last = t
        }
    }
    val t = rememberInfiniteTransition(label = "mini")
    val wave by t.animateFloat(0f, 1f, infiniteRepeatable(tween(1800, easing = LinearEasing)), label = "wave")
    val ring = MaterialTheme.colorScheme.outlineVariant
    Canvas(modifier.aspectRatio(1f)) {
        val r = size.minDimension / 2 - 2.dp.toPx()
        val c = center
        if (waves && !reduced) {
            for (k in 0..2) {
                val ph = (wave + k / 3f) % 1f
                drawCircle(color.copy(alpha = (1 - ph) * 0.35f), r * ph, c, style = Stroke(2.dp.toPx()))
            }
        }
        for (i in 1..3) drawCircle(ring.copy(alpha = 0.6f), r * i / 3f, c, style = Stroke(1.dp.toPx()))
        rotate(angle, c) {
            drawCircle(
                Brush.sweepGradient(0f to Color.Transparent, 0.72f to Color.Transparent, 1f to color.copy(alpha = 0.6f), center = c),
                r, c,
            )
            drawLine(color, c, Offset(c.x + r, c.y), 2.dp.toPx(), StrokeCap.Round)
        }
        drawCircle(color, 3.dp.toPx(), c)
    }
}
