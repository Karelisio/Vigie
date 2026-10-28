package com.karelisio.vigie.ui.home

import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.Animatable
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.TrendingUp
import androidx.compose.material.icons.rounded.ArrowDownward
import androidx.compose.material.icons.rounded.Event
import androidx.compose.material3.Icon
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.LinearWavyProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawWithContent
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.karelisio.vigie.data.Fmt
import com.karelisio.vigie.data.TrafficMonth
import com.karelisio.vigie.data.TrafficReport
import com.karelisio.vigie.ui.components.Chip
import com.karelisio.vigie.ui.components.Springs
import com.karelisio.vigie.ui.components.SectionCard
import com.karelisio.vigie.ui.theme.Vigie
import com.karelisio.vigie.ui.theme.numeric
import kotlinx.coroutines.delay
import java.time.YearMonth
import java.time.format.TextStyle
import java.util.Locale
import kotlin.math.ceil

/** Trafic du mois : sortant face au quota, projection à la fin du mois, entrant et six derniers mois. */
@Composable
fun TrafficCard(report: TrafficReport, modifier: Modifier = Modifier) {
    val status = Vigie.status
    val scheme = MaterialTheme.colorScheme
    val quota = report.quota > 0
    val color by animateColorAsState(if (quota) status.forLevel(report.pct, 100.0) else scheme.tertiary, label = "traffic")
    val days = ceil(report.daysLeft).toInt()
    SectionCard(
        modifier,
        title = "Trafic du mois",
        action = {
            Chip(
                if (days <= 1) "Dernier jour" else "$days j restants",
                scheme.secondaryContainer, scheme.onSecondaryContainer, icon = Icons.Rounded.Event,
            )
        },
    ) {
        Row(verticalAlignment = Alignment.Bottom) {
            Text(Fmt.volume(report.tx), style = MaterialTheme.typography.headlineMediumEmphasized.numeric(), color = color)
            Spacer(Modifier.width(8.dp))
            Text(
                if (quota) "sortants sur ${Fmt.volume(report.quota)}" else "sortants",
                style = MaterialTheme.typography.bodyMedium,
                color = scheme.onSurfaceVariant,
                modifier = Modifier.weight(1f).padding(bottom = 4.dp),
            )
            if (quota) Text(Fmt.pct(report.pct), style = MaterialTheme.typography.titleLarge.numeric(), color = color)
        }
        if (quota) {
            Spacer(Modifier.height(10.dp))
            QuotaBar(report, color)
        }
        Spacer(Modifier.height(12.dp))
        val over = quota && report.projectedTx > report.quota
        Line(Icons.AutoMirrored.Rounded.TrendingUp, if (over) status.warn else scheme.onSurfaceVariant) {
            Text(
                "Au rythme actuel : ${Fmt.volume(report.projectedTx)} à la fin du mois" + if (over) ", au-delà du quota" else "",
                style = MaterialTheme.typography.bodyMedium,
                color = if (over) status.warn else scheme.onSurface,
            )
        }
        Spacer(Modifier.height(4.dp))
        Line(Icons.Rounded.ArrowDownward, scheme.primary) {
            Text("Entrant : ${Fmt.volume(report.rx)}", style = MaterialTheme.typography.bodySmall, color = scheme.onSurfaceVariant)
        }
        Spacer(Modifier.height(16.dp))
        MonthBars(lastMonths(report), report.quota, color)
    }
}

@Composable
private fun Line(icon: ImageVector, tint: Color, content: @Composable () -> Unit) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        Icon(icon, null, Modifier.size(18.dp), tint = tint)
        Spacer(Modifier.width(8.dp))
        content()
    }
}

/** Part du quota consommée (indicateur ondulé qui se remplit en ressort) ; trait : niveau prévu en fin de mois. */
@Composable
private fun QuotaBar(report: TrafficReport, color: Color) {
    val reduced = Vigie.reducedMotion
    val target = (report.pct / 100).toFloat().coerceIn(0f, 1f)
    val progress = remember { Animatable(if (reduced) target else 0f) }
    LaunchedEffect(target) { if (reduced) progress.snapTo(target) else progress.animateTo(target, Springs.gentle()) }
    val projected = (report.projectedTx.toDouble() / report.quota).toFloat()
    val marker = MaterialTheme.colorScheme.onSurfaceVariant
    val bar = Modifier
        .fillMaxWidth()
        .drawWithContent {
            drawContent()
            if (projected in 0.02f..1f) {
                val x = size.width * projected
                drawLine(marker, Offset(x, -3.dp.toPx()), Offset(x, size.height + 3.dp.toPx()), strokeWidth = 2.dp.toPx())
            }
        }
    if (reduced) LinearProgressIndicator(progress = { progress.value }, modifier = bar.height(8.dp), color = color)
    else LinearWavyProgressIndicator(progress = { progress.value }, modifier = bar, color = color)
}

/** Les derniers mois, le mois en cours en dernier ; à zéro quand rien n'a été mesuré. */
internal fun lastMonths(report: TrafficReport, count: Int = 6): List<TrafficMonth> {
    val current = runCatching { YearMonth.parse(report.month) }.getOrNull() ?: return report.history.takeLast(count)
    val known = report.history.associateBy { it.month }
    return (count - 1 downTo 0).map { i ->
        val month = current.minusMonths(i.toLong()).toString()
        known[month] ?: TrafficMonth(month)
    }
}

private fun monthLabel(month: String): String =
    runCatching { YearMonth.parse(month).month.getDisplayName(TextStyle.SHORT, Locale.FRANCE) }.getOrDefault(month)

/** Mini-historique : une barre par mois (sortant), qui pousse en ressort ; rouge au-delà du quota. */
@Composable
private fun MonthBars(months: List<TrafficMonth>, quota: Long, current: Color) {
    val status = Vigie.status
    val scheme = MaterialTheme.colorScheme
    val reduced = Vigie.reducedMotion
    val max = months.maxOfOrNull { it.tx }?.takeIf { it > 0 } ?: 1L
    val summary = months.joinToString(", ") { "${monthLabel(it.month)} ${Fmt.volume(it.tx)}" }
    Row(
        Modifier.fillMaxWidth().semantics(mergeDescendants = true) { contentDescription = "Sortant par mois : $summary" },
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        months.forEachIndexed { i, m ->
            val target = m.tx.toFloat() / max
            val grow = remember(m.month) { Animatable(if (reduced) target else 0f) }
            LaunchedEffect(target) {
                if (reduced) grow.snapTo(target)
                else {
                    delay(i * 45L)
                    grow.animateTo(target, Springs.bouncy())
                }
            }
            val last = i == months.lastIndex
            val color = when {
                quota > 0 && m.tx > quota -> status.critical
                last -> current
                else -> scheme.secondary.copy(alpha = 0.45f)
            }
            Column(Modifier.weight(1f), horizontalAlignment = Alignment.CenterHorizontally) {
                Box(Modifier.fillMaxWidth().height(56.dp), contentAlignment = Alignment.BottomCenter) {
                    Box(
                        Modifier
                            .fillMaxWidth(0.62f)
                            .fillMaxHeight(grow.value.coerceIn(0.04f, 1f))
                            .clip(RoundedCornerShape(6.dp))
                            .background(if (m.tx > 0) color else scheme.surfaceContainerHighest),
                    )
                }
                Spacer(Modifier.height(4.dp))
                Text(
                    monthLabel(m.month),
                    style = MaterialTheme.typography.labelSmall,
                    color = if (last) scheme.onSurface else scheme.onSurfaceVariant,
                    textAlign = TextAlign.Center,
                    maxLines = 1,
                )
            }
        }
    }
}
