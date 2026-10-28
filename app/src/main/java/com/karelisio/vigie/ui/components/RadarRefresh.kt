package com.karelisio.vigie.ui.components

import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.pulltorefresh.PullToRefreshState
import androidx.compose.material3.pulltorefresh.pullToRefresh
import androidx.compose.material3.pulltorefresh.rememberPullToRefreshState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.rotate
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.flow.distinctUntilChanged
import com.karelisio.vigie.ui.theme.Vigie

/**
 * Tirer pour rafraîchir, avec une onde de radar : les arcs se déploient pendant le geste,
 * puis pulsent en continu pendant le rafraîchissement.
 */
@Composable
fun RadarRefreshBox(
    isRefreshing: Boolean,
    onRefresh: () -> Unit,
    modifier: Modifier = Modifier,
    content: @Composable () -> Unit,
) {
    val state = rememberPullToRefreshState()
    val haptics = rememberHaptics()
    LaunchedEffect(state) {
        snapshotFlow { state.distanceFraction >= 1f }.distinctUntilChanged().collect { if (it) haptics.threshold() }
    }
    val settle by animateFloatAsState(if (isRefreshing) 0.6f else 0f, Springs.bouncy(), label = "settle")
    Box(modifier.pullToRefresh(isRefreshing, state, onRefresh = onRefresh)) {
        Box(Modifier.graphicsLayer { translationY = (maxOf(state.distanceFraction.coerceAtMost(1.4f), settle)) * 56.dp.toPx() }) {
            content()
        }
        RadarRefreshIndicator(state, isRefreshing, Modifier.align(Alignment.TopCenter))
    }
}

@Composable
private fun RadarRefreshIndicator(state: PullToRefreshState, refreshing: Boolean, modifier: Modifier) {
    val color = MaterialTheme.colorScheme.primary
    val reduced = Vigie.reducedMotion
    val t = rememberInfiniteTransition(label = "refresh")
    val wave by t.animateFloat(0f, 1f, infiniteRepeatable(tween(1300, easing = LinearEasing)), label = "wave")
    val sweep by t.animateFloat(0f, 360f, infiniteRepeatable(tween(1100, easing = LinearEasing)), label = "sweep")
    val shown by animateFloatAsState(if (refreshing) 1f else 0f, tween(300), label = "shown")

    Canvas(modifier.fillMaxWidth().height(120.dp)) {
        val f = state.distanceFraction.coerceIn(0f, 1.4f)
        val presence = maxOf(f.coerceAtMost(1f), shown)
        if (presence <= 0.01f) return@Canvas
        val c = Offset(size.width / 2, 24.dp.toPx() + 28.dp.toPx() * minOf(f, 1f).coerceAtLeast(shown * 0.8f))
        val maxR = 46.dp.toPx()
        // Arcs qui se déploient avec le geste.
        val rings = 3
        for (i in 0 until rings) {
            val local = if (refreshing && !reduced) (wave + i / rings.toFloat()) % 1f
            else (f * rings - i).coerceIn(0f, 1f) * (i + 1) / rings
            val r = 10.dp.toPx() + maxR * local
            val alpha = if (refreshing) (1f - local) * 0.7f else 0.25f + 0.5f * (1f - i / rings.toFloat())
            drawArc(
                color.copy(alpha = alpha * presence), 200f, 140f, false,
                Offset(c.x - r, c.y - r), Size(r * 2, r * 2), style = Stroke(2.5.dp.toPx(), cap = StrokeCap.Round),
            )
        }
        // Petit radar central.
        val small = 13.dp.toPx() * presence
        drawCircle(color.copy(alpha = 0.14f * presence), small * 1.6f, c)
        val a = if (refreshing && !reduced) sweep else f * 360f
        rotate(a - 90f, c) {
            drawCircle(
                Brush.sweepGradient(0f to Color.Transparent, 0.7f to Color.Transparent, 1f to color.copy(alpha = 0.8f * presence), center = c),
                small, c,
            )
            drawLine(color.copy(alpha = presence), c, Offset(c.x + small, c.y), 2.dp.toPx(), StrokeCap.Round)
        }
        drawCircle(color.copy(alpha = presence), 2.5.dp.toPx(), c)
    }
}
