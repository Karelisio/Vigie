package com.karelisio.vigie.ui.home

import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.CheckCircle
import androidx.compose.material.icons.rounded.HourglassEmpty
import androidx.compose.material.icons.rounded.Warning
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.drawWithContent
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.unit.dp
import com.karelisio.vigie.data.OracleMetric
import com.karelisio.vigie.data.OracleReport
import com.karelisio.vigie.ui.components.Chip
import com.karelisio.vigie.ui.components.Springs
import com.karelisio.vigie.ui.components.SectionCard
import com.karelisio.vigie.ui.theme.Vigie
import com.karelisio.vigie.ui.theme.numeric
import kotlin.math.roundToInt

/** Risque de récupération d'une instance Oracle Always Free (CPU, réseau et mémoire sous 20 % sur 7 jours). */
@Composable
fun OracleCard(report: OracleReport, modifier: Modifier = Modifier) {
    val status = Vigie.status
    val (label, tone, icon) = when (report.risk) {
        "low" -> Triple("Risque faible", "ok", Icons.Rounded.CheckCircle)
        "medium" -> Triple("À surveiller", "warning", Icons.Rounded.Warning)
        "high" -> Triple("Risque de récupération", "critical", Icons.Rounded.Warning)
        else -> Triple("Analyse en cours", "neutral", Icons.Rounded.HourglassEmpty)
    }
    SectionCard(
        modifier,
        title = "Oracle Always Free",
        action = { Chip(label, status.containerOf(tone), status.of(tone), icon = icon) },
    ) {
        if (report.summary.isNotEmpty()) {
            Text(report.summary, style = MaterialTheme.typography.bodyMedium)
            Spacer(Modifier.height(12.dp))
        }
        MetricRow("CPU", report.cpu)
        MetricRow("Réseau", report.net, note = if (report.netMbps > 0) "de ${report.netMbps.roundToInt()} Mb/s" else null)
        MetricRow("Mémoire", report.mem, note = if (!report.mem.applies) "non comptée hors ARM" else null)
        Spacer(Modifier.height(6.dp))
        Text(
            "95e centile sur ${if (report.days >= 1) "${report.days.roundToInt()} j" else "moins d'un jour"} · " +
                "Oracle peut récupérer l'instance si tous les critères restent sous 20 % pendant 7 jours.",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

@Composable
private fun MetricRow(label: String, m: OracleMetric, note: String? = null) {
    val status = Vigie.status
    val color = when {
        !m.applies -> MaterialTheme.colorScheme.outline
        m.below -> status.warn
        else -> status.ok
    }
    val progress by animateFloatAsState((m.value / 100).toFloat().coerceIn(0f, 1f), Springs.gentle(), label = "oracle-$label")
    val marker = MaterialTheme.colorScheme.onSurface
    Column(Modifier.padding(vertical = 4.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(label, style = MaterialTheme.typography.labelLarge, modifier = Modifier.weight(1f))
            if (note != null) {
                Text(note, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                Spacer(Modifier.width(8.dp))
            }
            Text(
                if (m.applies) "${"%.1f".format(m.value)} %" else "—",
                style = MaterialTheme.typography.labelLarge.numeric(),
                color = color,
            )
        }
        Spacer(Modifier.height(4.dp))
        // Trait vertical : seuil d'inactivité d'Oracle (20 %).
        LinearProgressIndicator(
            progress = { if (m.applies) progress else 0f },
            color = color,
            modifier = Modifier
                .fillMaxWidth()
                .height(8.dp)
                .drawWithContent {
                    drawContent()
                    val x = size.width * (m.threshold / 100).toFloat()
                    drawLine(marker, Offset(x, -2f), Offset(x, size.height + 2f), strokeWidth = Stroke.HairlineWidth + 2f)
                },
        )
    }
}
