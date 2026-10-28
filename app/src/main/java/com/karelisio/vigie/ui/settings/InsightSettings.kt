package com.karelisio.vigie.ui.settings

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Summarize
import androidx.compose.material3.FilterChip
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import com.karelisio.vigie.data.AgentSettings
import com.karelisio.vigie.ui.components.SectionCard
import com.karelisio.vigie.ui.components.rememberHaptics
import java.text.DecimalFormat
import java.text.DecimalFormatSymbols
import java.util.Locale

/** Quota saisi en To (virgule ou point) ; null si invalide. */
private fun parseQuota(text: String): Double? =
    text.trim().replace(',', '.').toDoubleOrNull()?.takeIf { it in 0.0..1000.0 }

private fun formatQuota(tb: Double): String = DecimalFormat("0.###", DecimalFormatSymbols(Locale.FRANCE)).format(tb)

/** Quota de trafic sortant mensuel (0 : pas de suivi) et seuil de la première alerte. */
@OptIn(ExperimentalLayoutApi::class)
@Composable
internal fun TrafficQuotaCard(s: AgentSettings, modifier: Modifier, onChange: (AgentSettings) -> Unit) {
    val haptics = rememberHaptics()
    var text by remember { mutableStateOf(formatQuota(s.netQuotaTb)) }
    // Valeur changée ailleurs (rechargement) : on met le champ à jour sans gêner la saisie en cours.
    LaunchedEffect(s.netQuotaTb) { if (parseQuota(text) != s.netQuotaTb) text = formatQuota(s.netQuotaTb) }
    val invalid = parseQuota(text) == null
    SectionCard(modifier, title = "Quota de trafic") {
        Text(
            "Oracle Always Free inclut 10 To de trafic sortant par mois. Vigie compte le trafic du serveur et te prévient avant le dépassement.",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(12.dp))
        OutlinedTextField(
            value = text,
            onValueChange = { v ->
                text = v
                parseQuota(v)?.let { if (it != s.netQuotaTb) onChange(s.copy(netQuotaTb = it)) }
            },
            label = { Text("Quota sortant par mois") },
            suffix = { Text("To") },
            singleLine = true,
            isError = invalid,
            supportingText = { Text(if (invalid) "Entre 0 et 1000 To" else "0 : pas de suivi du quota") },
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Decimal),
            modifier = Modifier.fillMaxWidth(),
        )
        AnimatedVisibility(s.netQuotaTb > 0) {
            Column {
                Spacer(Modifier.height(8.dp))
                Text("Prévenir à", style = MaterialTheme.typography.labelLarge)
                Spacer(Modifier.height(4.dp))
                FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    listOf(50, 60, 70, 80, 90, 100).forEach { p ->
                        FilterChip(
                            selected = s.netQuotaAlert == p,
                            onClick = { haptics.tick(); onChange(s.copy(netQuotaAlert = p)) },
                            label = { Text("$p %") },
                        )
                    }
                }
                Text(
                    "Une seconde alerte part quand le quota est atteint. Une seule notification par mois et par niveau.",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
    }
}

/** Rapport de la semaine envoyé en notification le dimanche soir. */
@Composable
internal fun WeeklyReportCard(s: AgentSettings, modifier: Modifier, onChange: (AgentSettings) -> Unit) {
    val haptics = rememberHaptics()
    SectionCard(modifier, title = "Rapport de la semaine") {
        ToggleRow(
            Icons.Rounded.Summarize, "Rapport hebdomadaire (dimanche 19 h)",
            "Disponibilité, incidents, plantages, mises à jour et trafic, en une notification",
            s.weeklyReport,
        ) { on -> haptics.toggle(on); onChange(s.copy(weeklyReport = on)) }
    }
}
