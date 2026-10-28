package com.karelisio.vigie.ui.home

import androidx.compose.animation.animateContentSize
import androidx.compose.animation.core.Animatable
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.SegmentedButton
import androidx.compose.material3.SegmentedButtonDefaults
import androidx.compose.material3.SingleChoiceSegmentedButtonRow
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.karelisio.vigie.data.Consumer
import com.karelisio.vigie.data.Container
import com.karelisio.vigie.data.Fmt
import com.karelisio.vigie.ui.components.SectionCard
import com.karelisio.vigie.ui.components.Springs
import com.karelisio.vigie.ui.components.StatusDot
import com.karelisio.vigie.ui.components.rememberHaptics
import com.karelisio.vigie.ui.components.visualStatus
import com.karelisio.vigie.ui.theme.Vigie
import com.karelisio.vigie.ui.theme.numeric
import kotlinx.coroutines.delay

private enum class TopMetric(val label: String) { Cpu("CPU"), Mem("Mémoire"), Net("Réseau") }

private fun Consumer.value(m: TopMetric): Double = when (m) {
    TopMetric.Cpu -> cpuAvg
    TopMetric.Mem -> memAvg.toDouble()
    TopMetric.Net -> (rxBytes + txBytes).toDouble()
}

/** Les cinq conteneurs les plus gourmands (CPU, mémoire ou réseau) sur 24 h ou 7 jours ; un appui ouvre la fiche. */
@Composable
fun TopCard(
    consumers: List<Consumer>,
    containers: List<Container>,
    range: String,
    onRange: (String) -> Unit,
    onOpen: (String) -> Unit,
    modifier: Modifier = Modifier,
) {
    var metric by rememberSaveable { mutableStateOf(TopMetric.Cpu) }
    val haptics = rememberHaptics()
    val scheme = MaterialTheme.colorScheme
    val color = when (metric) {
        TopMetric.Cpu -> scheme.primary
        TopMetric.Mem -> scheme.tertiary
        TopMetric.Net -> scheme.secondary
    }
    val top = remember(consumers, metric) { consumers.sortedByDescending { it.value(metric) }.take(5) }
    val max = top.maxOfOrNull { it.value(metric) }?.takeIf { it > 0 } ?: 1.0
    SectionCard(
        modifier.animateContentSize(),
        title = "Top consommateurs",
        action = {
            SingleChoiceSegmentedButtonRow {
                listOf("24h" to "24 h", "7d" to "7 j").forEachIndexed { i, (key, label) ->
                    SegmentedButton(
                        selected = range == key,
                        onClick = { if (range != key) { haptics.tick(); onRange(key) } },
                        shape = SegmentedButtonDefaults.itemShape(i, 2),
                        icon = {},
                    ) { Text(label) }
                }
            }
        },
    ) {
        SingleChoiceSegmentedButtonRow(Modifier.fillMaxWidth()) {
            TopMetric.entries.forEachIndexed { i, m ->
                SegmentedButton(
                    selected = metric == m,
                    onClick = { haptics.tick(); metric = m },
                    shape = SegmentedButtonDefaults.itemShape(i, TopMetric.entries.size),
                ) { Text(m.label, maxLines = 1) }
            }
        }
        Spacer(Modifier.height(8.dp))
        if (top.isEmpty()) {
            Text(
                "Pas encore d'historique sur cette période : une mesure par minute.",
                style = MaterialTheme.typography.bodyMedium,
                color = scheme.onSurfaceVariant,
                modifier = Modifier.padding(vertical = 8.dp),
            )
        }
        top.forEachIndexed { i, c ->
            key(c.name) {
                ConsumerRow(
                    c, metric, (c.value(metric) / max).toFloat(), i, color,
                    status = containers.firstOrNull { it.name == c.name }?.visualStatus() ?: "neutral",
                ) { haptics.tick(); onOpen(c.name) }
            }
        }
    }
}

@Composable
private fun ConsumerRow(c: Consumer, metric: TopMetric, fraction: Float, index: Int, color: Color, status: String, onClick: () -> Unit) {
    val scheme = MaterialTheme.colorScheme
    val reduced = Vigie.reducedMotion
    // Barre qui s'allonge en ressort, en cascade, à chaque changement de critère ou de période.
    val width = remember { Animatable(if (reduced) fraction else 0f) }
    LaunchedEffect(fraction) {
        if (reduced) width.snapTo(fraction)
        else {
            delay(index * 50L)
            width.animateTo(fraction, Springs.bouncy())
        }
    }
    val (value, detail) = when (metric) {
        TopMetric.Cpu -> Fmt.pct(c.cpuAvg, 1) to "pic ${Fmt.pct(c.cpuMax)}"
        TopMetric.Mem -> Fmt.bytes(c.memAvg) to "en moyenne"
        TopMetric.Net -> Fmt.bytes(c.rxBytes + c.txBytes) to "↓ ${Fmt.bytes(c.rxBytes)} · ↑ ${Fmt.bytes(c.txBytes)}"
    }
    Surface(onClick = onClick, shape = RoundedCornerShape(16.dp), color = scheme.surfaceContainer, modifier = Modifier.fillMaxWidth()) {
        Column(Modifier.padding(horizontal = 4.dp, vertical = 8.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                StatusDot(status, size = 8.dp, breathe = false)
                Spacer(Modifier.width(6.dp))
                Text(
                    c.name,
                    style = MaterialTheme.typography.titleSmall,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f),
                )
                Text(detail, style = MaterialTheme.typography.labelSmall, color = scheme.onSurfaceVariant, maxLines = 1)
                Spacer(Modifier.width(8.dp))
                Text(value, style = MaterialTheme.typography.labelLarge.numeric())
            }
            Spacer(Modifier.height(6.dp))
            Box(
                Modifier
                    .fillMaxWidth()
                    .height(6.dp)
                    .clip(RoundedCornerShape(50))
                    .background(scheme.surfaceContainerHighest),
            ) {
                Box(
                    Modifier
                        .fillMaxWidth(width.value.coerceIn(0f, 1f))
                        .height(6.dp)
                        .clip(RoundedCornerShape(50))
                        .background(color),
                )
            }
        }
    }
}
