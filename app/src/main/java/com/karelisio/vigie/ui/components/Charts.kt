package com.karelisio.vigie.ui.components

import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.State
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.drawWithContent
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Paint
import androidx.compose.ui.graphics.drawscope.clipRect
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.karelisio.vigie.ui.theme.Vigie
import com.patrykandpatrick.vico.compose.cartesian.CartesianChartHost
import com.patrykandpatrick.vico.compose.cartesian.Zoom
import com.patrykandpatrick.vico.compose.cartesian.axis.HorizontalAxis
import com.patrykandpatrick.vico.compose.cartesian.axis.VerticalAxis
import com.patrykandpatrick.vico.compose.cartesian.axis.rememberAxisGuidelineComponent
import com.patrykandpatrick.vico.compose.cartesian.axis.rememberAxisLabelComponent
import com.patrykandpatrick.vico.compose.cartesian.data.CartesianChartModelProducer
import com.patrykandpatrick.vico.compose.cartesian.data.CartesianLayerRangeProvider
import com.patrykandpatrick.vico.compose.cartesian.data.CartesianValueFormatter
import com.patrykandpatrick.vico.compose.cartesian.data.LineCartesianLayerModel
import com.patrykandpatrick.vico.compose.cartesian.data.lineModel
import com.patrykandpatrick.vico.compose.cartesian.layer.LineCartesianLayer
import com.patrykandpatrick.vico.compose.cartesian.layer.rememberLine
import com.patrykandpatrick.vico.compose.cartesian.layer.rememberLineCartesianLayer
import com.patrykandpatrick.vico.compose.cartesian.marker.DefaultCartesianMarker
import com.patrykandpatrick.vico.compose.cartesian.marker.rememberDefaultCartesianMarker
import com.patrykandpatrick.vico.compose.cartesian.rememberCartesianChart
import com.patrykandpatrick.vico.compose.cartesian.rememberVicoScrollState
import com.patrykandpatrick.vico.compose.cartesian.rememberVicoZoomState
import com.patrykandpatrick.vico.compose.common.Fill
import com.patrykandpatrick.vico.compose.common.Insets
import com.patrykandpatrick.vico.compose.common.component.Component
import com.patrykandpatrick.vico.compose.common.component.rememberShapeComponent
import com.patrykandpatrick.vico.compose.common.component.rememberTextComponent
import com.patrykandpatrick.vico.compose.common.data.ExtraStore
import com.patrykandpatrick.vico.compose.common.DrawingContext
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

data class ChartPoint(val ts: Long, val value: Double)

/** Point courant qui pulse (lit un état animé pendant le dessin : seul le dessin est invalidé). */
private class PulseComponent(private val color: Color, private val phase: State<Float>, private val reduced: Boolean) : Component {
    private val paint = Paint()
    override fun draw(context: DrawingContext, left: Float, top: Float, right: Float, bottom: Float) {
        val c = Offset((left + right) / 2, (top + bottom) / 2)
        val r = (right - left) / 2
        val p = if (reduced) 0f else phase.value
        paint.color = color.copy(alpha = (1f - p) * 0.45f)
        context.canvas.drawCircle(c, r * (1f + p * 1.6f), paint)
        paint.color = color
        context.canvas.drawCircle(c, r * 0.62f, paint)
        paint.color = Color.White.copy(alpha = 0.9f)
        context.canvas.drawCircle(c, r * 0.26f, paint)
    }
}

private class LastPointProvider(private val point: LineCartesianLayer.Point) : LineCartesianLayer.PointProvider {
    @Volatile var lastX: Double = Double.NaN
    override fun getPoint(entry: LineCartesianLayerModel.Entry, extraStore: ExtraStore): LineCartesianLayer.Point? =
        if (entry.x == lastX) point else null

    override fun getLargestPoint(extraStore: ExtraStore): LineCartesianLayer.Point = point
}

/**
 * Graphique de métrique (Vico) : tracé progressif à l'ouverture, transition fluide entre périodes,
 * point courant qui pulse.
 */
@Composable
fun MetricChart(
    points: List<ChartPoint>,
    color: Color,
    spanMs: Long,
    modifier: Modifier = Modifier,
    maxY: Double? = null,
    height: Dp = 180.dp,
    format: (Double) -> String,
) {
    val reduced = Vigie.reducedMotion
    val producer = remember { CartesianChartModelProducer() }
    val pulse = rememberInfiniteTransition(label = "pulse")
    val phase = pulse.animateFloat(0f, 1f, infiniteRepeatable(tween(1400, easing = LinearEasing), RepeatMode.Restart), label = "p")
    val pointProvider = remember(color, reduced) {
        LastPointProvider(LineCartesianLayer.Point(PulseComponent(color, phase, reduced), 12.dp))
    }
    val reveal = remember { Animatable(if (reduced) 1f else 0f) }

    LaunchedEffect(points) {
        if (points.size < 2) return@LaunchedEffect
        val xs = points.map { it.ts / 60_000.0 }
        pointProvider.lastX = xs.last()
        producer.runTransaction { lineModel { series(xs, points.map { it.value }) } }
        if (reveal.value < 1f) reveal.animateTo(1f, tween(1100, easing = FastOutSlowInEasing))
    }

    val scheme = MaterialTheme.colorScheme
    val labelStyle = MaterialTheme.typography.labelSmall.copy(color = scheme.onSurfaceVariant)
    val timeFmt = remember(spanMs) {
        SimpleDateFormat(if (spanMs > 2 * 86_400_000L) "EEE HH'h'" else "HH:mm", Locale.FRANCE)
    }
    val markerTime = remember { SimpleDateFormat("EEE d MMM, HH:mm", Locale.FRANCE) }

    val line = LineCartesianLayer.rememberLine(
        fill = remember(color) { LineCartesianLayer.LineFill.single(Fill(color)) },
        areaFill = remember(color) {
            LineCartesianLayer.AreaFill.single(Fill(Brush.verticalGradient(listOf(color.copy(alpha = 0.38f), color.copy(alpha = 0.02f)))))
        },
        pointProvider = pointProvider,
        interpolator = remember { LineCartesianLayer.Interpolator.cubic(0.45f) },
    )
    val layer = rememberLineCartesianLayer(
        lineProvider = LineCartesianLayer.LineProvider.series(line),
        rangeProvider = remember(maxY) { CartesianLayerRangeProvider.fixed(minY = 0.0, maxY = maxY) },
    )
    val marker = rememberDefaultCartesianMarker(
        label = rememberTextComponent(
            style = MaterialTheme.typography.labelMedium.copy(color = scheme.inverseOnSurface),
            padding = Insets(8.dp, 4.dp),
            background = rememberShapeComponent(Fill(scheme.inverseSurface), RoundedCornerShape(8.dp)),
        ),
        valueFormatter = remember(format) {
            DefaultCartesianMarker.ValueFormatter { _, targets ->
                val t = targets.firstOrNull() ?: return@ValueFormatter ""
                val y = (t as? com.patrykandpatrick.vico.compose.cartesian.marker.LineCartesianLayerMarkerTarget)
                    ?.points?.firstOrNull()?.entry?.y ?: 0.0
                "${format(y)} · ${markerTime.format(Date((t.x * 60_000).toLong()))}"
            }
        },
        guideline = rememberAxisGuidelineComponent(fill = Fill(color.copy(alpha = 0.5f))),
    )
    val chart = rememberCartesianChart(
        layer,
        startAxis = VerticalAxis.rememberStart(
            line = null,
            tick = null,
            label = rememberAxisLabelComponent(style = labelStyle),
            guideline = rememberAxisGuidelineComponent(fill = Fill(scheme.outlineVariant.copy(alpha = 0.6f))),
            valueFormatter = remember(format) { CartesianValueFormatter { _, v, _ -> format(v) } },
            itemPlacer = remember { VerticalAxis.ItemPlacer.count({ 4 }) },
        ),
        bottomAxis = HorizontalAxis.rememberBottom(
            line = null,
            tick = null,
            guideline = null,
            label = rememberAxisLabelComponent(style = labelStyle),
            valueFormatter = remember(timeFmt) { CartesianValueFormatter { _, x, _ -> timeFmt.format(Date((x * 60_000).toLong())) } },
            itemPlacer = remember(points.size) {
                HorizontalAxis.ItemPlacer.aligned(spacing = { maxOf(1, points.size / 4) }, addExtremeLabelPadding = true)
            },
        ),
        marker = marker,
    )
    CartesianChartHost(
        chart = chart,
        modelProducer = producer,
        modifier = modifier
            .fillMaxWidth()
            .height(height)
            .drawWithContent {
                // Tracé progressif de gauche à droite.
                clipRect(right = size.width * reveal.value) { this@drawWithContent.drawContent() }
            },
        scrollState = rememberVicoScrollState(scrollEnabled = false),
        zoomState = rememberVicoZoomState(zoomEnabled = false, initialZoom = remember { Zoom.Content }),
        animationSpec = if (reduced) null else tween(650, easing = FastOutSlowInEasing),
    )
}

