package com.karelisio.vigie.ui.components

import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.lerp
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.karelisio.vigie.data.CheckResult
import com.karelisio.vigie.data.UptimeBucket
import com.karelisio.vigie.ui.theme.StatusColors
import com.karelisio.vigie.ui.theme.Vigie

fun StatusColors.forUptime(b: UptimeBucket, empty: Color): Color {
    if (b.total == 0) return empty
    val ratio = b.ok.toFloat() / b.total
    return when {
        ratio >= 0.999f -> ok
        ratio >= 0.95f -> lerp(warn, ok, (ratio - 0.95f) / 0.05f)
        else -> lerp(critical, warn, (ratio / 0.95f).coerceIn(0f, 1f) * 0.4f)
    }
}

/**
 * Barres de disponibilité type « status page ». Elles poussent en cascade à l'apparition ;
 * un appui sélectionne une barre.
 */
@Composable
fun UptimeBars(
    buckets: List<UptimeBucket>,
    modifier: Modifier = Modifier,
    height: Dp = 30.dp,
    onSelect: (Int?) -> Unit = {},
) {
    val status = Vigie.status
    val empty = MaterialTheme.colorScheme.surfaceContainerHighest
    val outline = MaterialTheme.colorScheme.onSurface
    val reduced = Vigie.reducedMotion
    val appear = remember { Animatable(if (reduced) 1f else 0f) }
    var selected by remember { mutableIntStateOf(-1) }
    LaunchedEffect(buckets.size) {
        if (!reduced) {
            appear.snapTo(0f)
            appear.animateTo(1f, tween(900, easing = FastOutSlowInEasing))
        }
    }
    Canvas(
        modifier
            .fillMaxWidth()
            .height(height)
            .pointerInput(buckets.size) {
                detectTapGestures { pos ->
                    if (buckets.isEmpty()) return@detectTapGestures
                    val i = (pos.x / size.width * buckets.size).toInt().coerceIn(0, buckets.lastIndex)
                    selected = if (selected == i) -1 else i
                    onSelect(selected.takeIf { it >= 0 })
                }
            },
    ) {
        val n = buckets.size
        if (n == 0) return@Canvas
        val gap = (size.width / n * 0.22f).coerceIn(1.dp.toPx(), 3.dp.toPx())
        val w = (size.width - gap * (n - 1)) / n
        val radius = CornerRadius(minOf(w / 2, 3.dp.toPx()))
        buckets.forEachIndexed { i, b ->
            val local = ((appear.value * (n + 10) - i) / 10f).coerceIn(0f, 1f)
            if (local <= 0f) return@forEachIndexed
            val h = size.height * (0.25f + 0.75f * local)
            val x = i * (w + gap)
            val color = status.forUptime(b, empty)
            drawRoundRect(
                color = if (selected >= 0 && selected != i) color.copy(alpha = 0.45f) else color,
                topLeft = Offset(x, size.height - h),
                size = Size(w, h),
                cornerRadius = radius,
            )
            if (i == selected) {
                drawRoundRect(outline, Offset(x - 1, 0f), Size(w + 2, size.height), radius, style = Stroke(1.5.dp.toPx()))
            }
        }
    }
}

/** Mini-graphique (latences récentes, métriques temps réel). */
@Composable
fun Sparkline(
    values: List<Float>,
    color: Color,
    modifier: Modifier = Modifier,
    maxValue: Float? = null,
    failures: List<Boolean> = emptyList(),
) {
    val crit = Vigie.status.critical
    Canvas(modifier) {
        if (values.size < 2) return@Canvas
        val max = (maxValue ?: values.max()).coerceAtLeast(1e-3f) * 1.1f
        val step = size.width / (values.size - 1)
        val pts = values.mapIndexed { i, v -> Offset(i * step, size.height - (v / max).coerceIn(0f, 1f) * size.height) }
        val line = Path().apply {
            moveTo(pts[0].x, pts[0].y)
            for (i in 1 until pts.size) {
                val p0 = pts[i - 1]
                val p1 = pts[i]
                val mx = (p0.x + p1.x) / 2
                cubicTo(mx, p0.y, mx, p1.y, p1.x, p1.y)
            }
        }
        val fill = Path().apply {
            addPath(line)
            lineTo(pts.last().x, size.height)
            lineTo(0f, size.height)
            close()
        }
        drawPath(fill, Brush.verticalGradient(listOf(color.copy(alpha = 0.28f), Color.Transparent)))
        drawPath(line, color, style = Stroke(2.dp.toPx(), cap = StrokeCap.Round))
        failures.forEachIndexed { i, failed ->
            if (failed && i < pts.size) drawCircle(crit, 2.5.dp.toPx(), Offset(pts[i].x, size.height - 2.dp.toPx()))
        }
        drawCircle(color, 3.dp.toPx(), pts.last())
    }
}

fun List<CheckResult>.latencies(): List<Float> = map { if (it.ok) it.latencyMs.toFloat() else 0f }
