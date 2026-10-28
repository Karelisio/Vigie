package com.karelisio.vigie.ui.containers

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.Notes
import androidx.compose.material.icons.rounded.CloudDownload
import androidx.compose.material.icons.rounded.Layers
import androidx.compose.material.icons.rounded.LayersClear
import androidx.compose.material.icons.rounded.MoreVert
import androidx.compose.material.icons.rounded.PlayArrow
import androidx.compose.material.icons.rounded.RestartAlt
import androidx.compose.material.icons.rounded.Stop
import androidx.compose.material.icons.rounded.SystemUpdateAlt
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.unit.dp
import com.karelisio.vigie.data.Container
import com.karelisio.vigie.ui.components.rememberHaptics

/** Libellés des actions de stack (menu, confirmation, verrou). */
internal val StackActions = mapOf(
    "update" to "Mettre à jour",
    "pull" to "Télécharger les images",
    "restart" to "Redémarrer",
    "start" to "Démarrer",
    "stop" to "Arrêter",
)

/** En-tête d'une stack compose (vue groupée) avec son menu d'actions. */
@Composable
internal fun StackHeader(
    project: String,
    members: List<Container>,
    updatesEnabled: Boolean,
    actions: Boolean,
    busy: Boolean,
    onAction: (String) -> Unit,
    onLogs: () -> Unit,
    modifier: Modifier = Modifier,
) {
    var menu by remember { mutableStateOf(false) }
    val haptics = rememberHaptics()
    val running = members.count { it.running }
    val updates = members.count { it.updateAvailable }
    val scheme = MaterialTheme.colorScheme
    Surface(color = scheme.secondaryContainer, shape = RoundedCornerShape(20.dp), modifier = modifier.fillMaxWidth()) {
        Row(Modifier.padding(start = 16.dp, end = 4.dp, top = 10.dp, bottom = 10.dp), verticalAlignment = Alignment.CenterVertically) {
            Icon(if (project.isEmpty()) Icons.Rounded.LayersClear else Icons.Rounded.Layers, null, tint = scheme.onSecondaryContainer)
            Spacer(Modifier.width(12.dp))
            Column(Modifier.weight(1f)) {
                Text(project.ifEmpty { "Hors stack" }, style = MaterialTheme.typography.titleMedium, color = scheme.onSecondaryContainer)
                Text(
                    "$running/${members.size} actifs" + when (updates) {
                        0 -> ""
                        1 -> " · 1 mise à jour"
                        else -> " · $updates mises à jour"
                    },
                    style = MaterialTheme.typography.bodySmall,
                    color = scheme.onSecondaryContainer.copy(alpha = 0.8f),
                )
            }
            if (project.isNotEmpty()) {
                Box {
                    IconButton(onClick = { haptics.tick(); menu = true }) {
                        Icon(Icons.Rounded.MoreVert, "Actions de la stack", tint = scheme.onSecondaryContainer)
                    }
                    DropdownMenu(menu, onDismissRequest = { menu = false }) {
                        DropdownMenuItem(
                            text = { Text("Logs de la stack") },
                            leadingIcon = { Icon(Icons.AutoMirrored.Rounded.Notes, null) },
                            onClick = { menu = false; onLogs() },
                        )
                        if (actions) {
                            HorizontalDivider()
                            @Composable
                            fun Item(action: String, icon: ImageVector) {
                                DropdownMenuItem(
                                    text = { Text(StackActions.getValue(action)) },
                                    leadingIcon = { Icon(icon, null) },
                                    onClick = { menu = false; onAction(action) },
                                    enabled = !busy,
                                )
                            }
                            if (updatesEnabled) {
                                Item("update", Icons.Rounded.SystemUpdateAlt)
                                Item("pull", Icons.Rounded.CloudDownload)
                                HorizontalDivider()
                            }
                            Item("restart", Icons.Rounded.RestartAlt)
                            if (running < members.size) Item("start", Icons.Rounded.PlayArrow)
                            if (running > 0) Item("stop", Icons.Rounded.Stop)
                        }
                    }
                }
            }
        }
    }
}
