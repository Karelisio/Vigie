package com.karelisio.vigie.ui.storage

import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.PathEffect
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.clipRect
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import com.karelisio.vigie.data.DiskPoint
import com.karelisio.vigie.data.StorageDisk
import com.karelisio.vigie.data.StorageFmt
import com.karelisio.vigie.ui.theme.Vigie
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

/** La projection s'arrête à 30 jours : au-delà, le graphique écraserait l'historique. */
private const val MAX_PROJECTION_DAYS = 30.0

/**
 * Fin de la projection affichée : au rythme actuel, jusqu'au disque plein (30 jours au plus).
 * Null sans croissance nette.
 */
internal fun forecastEnd(d: StorageDisk): DiskPoint? {
    val last = d.points.lastOrNull() ?: return null
    val left = d.daysLeft ?: return null
    val rate = d.trendPerDay ?: return null
    if (d.status != StorageDisk.STATUS_OK || rate <= 0) return null
    val days = left.coerceIn(0.0, MAX_PROJECTION_DAYS)
    val used = if (left <= MAX_PROJECTION_DAYS) d.total else (last.used + rate * days).toLong().coerceAtMost(d.total)
    return DiskPoint(last.ts + (days * 86_400_000).toLong(), used)
}

/**
 * Tendance du disque : occupation des 7 derniers jours, projection en pointillés au rythme actuel
 * et ligne du disque plein en haut.
 */
@Composable
internal fun ForecastChart(d: StorageDisk, color: Color, modifier: Modifier = Modifier) {
    val scheme = MaterialTheme.colorScheme
    val pts = d.points
    if (pts.size < 2 || d.total <= 0) {
        Text(
            "Le graphique apparaît après quelques heures de mesures.",
            style = MaterialTheme.typography.bodySmall,
            color = scheme.onSurfaceVariant,
            modifier = modifier,
        )
        return
    }
    val end = forecastEnd(d)
    val reduced = Vigie.reducedMotion
    val reveal = remember { Animatable(if (reduced) 1f else 0f) }
    LaunchedEffect(Unit) { if (!reduced) reveal.animateTo(1f, tween(1100, easing = FastOutSlowInEasing)) }
    val full = Vigie.status.critical
    val grid = scheme.outlineVariant
    val dateFmt = remember { SimpleDateFormat("d MMM", Locale.FRANCE) }
    val first = pts.first()
    val last = pts.last()
    val summary = buildString {
        append("Occupation du disque depuis le ${dateFmt.format(Date(first.ts))} : ")
        append("${StorageFmt.size(first.used)} puis ${StorageFmt.size(last.used)}.")
        if (end != null) append(" Projection : ${StorageFmt.size(end.used)} le ${dateFmt.format(Date(end.ts))}.")
    }
    Column(modifier) {
        Canvas(Modifier.fillMaxWidth().height(140.dp).semantics { contentDescription = summary }) {
            val x0 = first.ts.toDouble()
            val x1 = maxOf((end?.ts ?: last.ts).toDouble(), x0 + 1)
            val total = d.total.toDouble()
            val top = 6.dp.toPx()
            val h = size.height - top
            fun x(ts: Long) = ((ts - x0) / (x1 - x0) * size.width).toFloat()
            fun y(v: Long) = top + (h - (v / total).coerceIn(0.0, 1.0) * h).toFloat()
            val dash = PathEffect.dashPathEffect(floatArrayOf(6.dp.toPx(), 5.dp.toPx()))
            // Disque plein, et repère à mi-hauteur.
            drawLine(full.copy(alpha = 0.7f), Offset(0f, top), Offset(size.width, top), strokeWidth = 1.5.dp.toPx(), pathEffect = dash)
            drawLine(grid.copy(alpha = 0.6f), Offset(0f, y(d.total / 2)), Offset(size.width, y(d.total / 2)), strokeWidth = 1.dp.toPx())
            clipRect(right = size.width * reveal.value) {
                val line = Path().apply {
                    moveTo(x(pts[0].ts), y(pts[0].used))
                    for (p in pts.drop(1)) lineTo(x(p.ts), y(p.used))
                }
                val area = Path().apply {
                    addPath(line)
                    lineTo(x(last.ts), size.height)
                    lineTo(x(first.ts), size.height)
                    close()
                }
                drawPath(area, Brush.verticalGradient(listOf(color.copy(alpha = 0.32f), color.copy(alpha = 0.02f))))
                drawPath(line, color, style = Stroke(2.dp.toPx(), cap = StrokeCap.Round))
                if (end != null) {
                    val a = Offset(x(last.ts), y(last.used))
                    val b = Offset(x(end.ts), y(end.used))
                    drawLine(color.copy(alpha = 0.85f), a, b, strokeWidth = 2.dp.toPx(), pathEffect = dash, cap = StrokeCap.Round)
                    drawCircle(if (end.used >= d.total) full else color, 4.dp.toPx(), b)
                }
                drawCircle(color, 4.dp.toPx(), Offset(x(last.ts), y(last.used)))
            }
        }
        Row(Modifier.fillMaxWidth().padding(top = 4.dp)) {
            val label = MaterialTheme.typography.labelSmall
            Text(dateFmt.format(Date(first.ts)), style = label, color = scheme.onSurfaceVariant)
            Spacer(Modifier.weight(1f))
            Text(
                when {
                    end == null -> "aujourd'hui"
                    end.used >= d.total -> "plein vers le ${dateFmt.format(Date(end.ts))}"
                    else -> "${dateFmt.format(Date(end.ts))} : ${StorageFmt.size(end.used)}"
                },
                style = label,
                color = if (end != null && end.used >= d.total) full else scheme.onSurfaceVariant,
            )
        }
    }
}
