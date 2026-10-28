package com.karelisio.vigie.ui.home

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Construction
import androidx.compose.material.icons.rounded.NotificationsActive
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialShapes
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.toShape
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.karelisio.vigie.data.Fmt
import com.karelisio.vigie.ui.components.rememberHaptics
import com.karelisio.vigie.ui.components.rememberNow
import com.karelisio.vigie.ui.theme.Vigie

/** Durées proposées pour le mode maintenance, en minutes. */
private val Durations = listOf(30 to "30 min", 60 to "1 h", 120 to "2 h", 240 to "4 h")

private fun remaining(ms: Long): String {
    val min = (ms + 59_999) / 60_000
    return if (min < 60) "$min min" else "${min / 60} h ${(min % 60).toString().padStart(2, '0')}"
}

/** Bandeau affiché tant que les notifications sont coupées. */
@Composable
fun MaintenanceBanner(until: Long, onStop: () -> Unit, modifier: Modifier = Modifier) {
    val now = rememberNow(15_000)
    val status = Vigie.status
    Surface(color = status.warnContainer, contentColor = status.onWarnContainer, shape = RoundedCornerShape(24.dp), modifier = modifier.fillMaxWidth()) {
        Row(Modifier.padding(start = 16.dp, end = 8.dp, top = 12.dp, bottom = 12.dp), verticalAlignment = Alignment.CenterVertically) {
            Icon(Icons.Rounded.Construction, null)
            Spacer(Modifier.width(12.dp))
            Column(Modifier.weight(1f)) {
                Text("Maintenance en cours", style = MaterialTheme.typography.titleSmall)
                Text(
                    "Notifications coupées jusqu'à ${Fmt.time(until)} (encore ${remaining(until - now)})",
                    style = MaterialTheme.typography.bodySmall,
                )
            }
            TextButton(onClick = onStop) { Text("Arrêter") }
        }
    }
}

/** Choix de la durée du mode maintenance. */
@OptIn(ExperimentalMaterial3Api::class, ExperimentalLayoutApi::class)
@Composable
fun MaintenanceSheet(activeUntil: Long, onPick: (Int) -> Unit, onDismiss: () -> Unit) {
    val haptics = rememberHaptics()
    val active = activeUntil > System.currentTimeMillis()
    ModalBottomSheet(onDismissRequest = onDismiss) {
        Column(
            Modifier.fillMaxWidth().padding(horizontal = 24.dp).navigationBarsPadding().padding(bottom = 16.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Box(Modifier.size(72.dp), contentAlignment = Alignment.Center) {
                Surface(color = MaterialTheme.colorScheme.tertiaryContainer, shape = MaterialShapes.Sunny.toShape(), modifier = Modifier.size(72.dp)) {}
                Icon(Icons.Rounded.Construction, null, tint = MaterialTheme.colorScheme.onTertiaryContainer, modifier = Modifier.size(32.dp))
            }
            Spacer(Modifier.height(16.dp))
            Text("Mode maintenance", style = MaterialTheme.typography.titleLarge)
            Spacer(Modifier.height(6.dp))
            Text(
                "Aucune notification pendant la durée choisie : idéal pendant que tu travailles sur le serveur. " +
                    "Un bilan des alertes arrive à la fin.",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.height(20.dp))
            FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp, Alignment.CenterHorizontally), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Durations.forEach { (minutes, label) ->
                    FilledTonalButton(onClick = { haptics.confirm(); onPick(minutes) }, shapes = ButtonDefaults.shapes()) { Text(label) }
                }
            }
            if (active) {
                Spacer(Modifier.height(12.dp))
                Button(onClick = { haptics.confirm(); onPick(0) }, shapes = ButtonDefaults.shapes(), modifier = Modifier.fillMaxWidth()) {
                    Icon(Icons.Rounded.NotificationsActive, null)
                    Spacer(Modifier.width(8.dp))
                    Text("Réactiver les notifications")
                }
            }
        }
    }
}
