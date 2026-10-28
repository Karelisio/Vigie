package com.karelisio.vigie.ui.containers

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.CloudDownload
import androidx.compose.material.icons.rounded.Layers
import androidx.compose.material.icons.rounded.NewReleases
import androidx.compose.material.icons.rounded.SystemUpdateAlt
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import com.karelisio.vigie.data.AgentJob
import com.karelisio.vigie.data.Container
import com.karelisio.vigie.data.versionAtLeast
import com.karelisio.vigie.ui.components.Chip
import com.karelisio.vigie.ui.components.JobProgress
import com.karelisio.vigie.ui.components.SectionCard
import com.karelisio.vigie.ui.components.rememberHaptics

/** Ce qu'une mise à jour va faire, expliqué avant confirmation. */
internal fun updateExplanation(c: Container): String =
    if (c.self) {
        "Vigie télécharge sa dernière image puis se remplace (environ 10 secondes de coupure). " +
            "Si la nouvelle version ne démarre pas, l'ancienne est rétablie automatiquement."
    } else {
        "Vigie télécharge la dernière image de ${c.name} puis recrée le conteneur à l'identique " +
            "(nom, réseaux, volumes, variables) : quelques secondes de coupure. " +
            "S'il ne démarre pas, l'ancienne version est rétablie automatiquement."
    }

/**
 * Image du conteneur et actions de mise à jour.
 * [job] est l'opération en cours ou récente qui le concerne. Nouveautés et mise à jour de nuit
 * demandent un agent 0.4.0 ou plus ([agentVersion]).
 */
@Composable
internal fun UpdateCard(
    c: Container,
    enabled: Boolean,
    busy: Boolean,
    job: AgentJob?,
    onUpdate: () -> Unit,
    onPull: () -> Unit,
    onStack: () -> Unit,
    modifier: Modifier = Modifier,
    agentVersion: String = "",
) {
    var confirm by remember { mutableStateOf<String?>(null) }
    var changelog by remember { mutableStateOf(false) }
    val recent = versionAtLeast(agentVersion, 0, 4)
    val haptics = rememberHaptics()
    SectionCard(
        modifier,
        title = "Image",
        action = {
            if (c.updateAvailable) {
                Chip(
                    "Nouvelle version",
                    MaterialTheme.colorScheme.tertiaryContainer,
                    MaterialTheme.colorScheme.onTertiaryContainer,
                    icon = Icons.Rounded.NewReleases,
                )
            }
        },
    ) {
        Text(c.imageRef.ifEmpty { c.image }, style = MaterialTheme.typography.bodyMedium, fontFamily = FontFamily.Monospace)
        AnimatedVisibility(job != null) {
            job?.let { JobProgress(it, Modifier.padding(top = 12.dp)) }
        }
        Spacer(Modifier.height(12.dp))
        if (recent) {
            ChangelogButton(highlight = c.updateAvailable) { haptics.tick(); changelog = true }
            Spacer(Modifier.height(8.dp))
        }
        if (!enabled) {
            Text(
                "Mises à jour depuis l'appli indisponibles : mets l'agent à jour (version 0.3.0 ou plus) " +
                    "ou vérifie que VIGIE_ALLOW_UPDATES n'est pas à « false ».",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            return@SectionCard
        }
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
            val label = if (c.self) "Mettre à jour l'agent" else "Mettre à jour"
            if (c.updateAvailable) {
                Button(onClick = { haptics.tick(); confirm = "update" }, enabled = !busy, shapes = ButtonDefaults.shapes(), modifier = Modifier.weight(1f)) {
                    Icon(Icons.Rounded.SystemUpdateAlt, null)
                    Spacer(Modifier.width(8.dp))
                    Text(label)
                }
            } else {
                FilledTonalButton(onClick = { haptics.tick(); confirm = "update" }, enabled = !busy, shapes = ButtonDefaults.shapes(), modifier = Modifier.weight(1f)) {
                    Icon(Icons.Rounded.SystemUpdateAlt, null)
                    Spacer(Modifier.width(8.dp))
                    Text(label)
                }
            }
            OutlinedButton(onClick = { haptics.tick(); onPull() }, enabled = !busy, shapes = ButtonDefaults.shapes()) {
                Icon(Icons.Rounded.CloudDownload, "Télécharger l'image sans redémarrer")
            }
        }
        if (recent) AutoUpdateSwitch(c)
        if (c.project.isNotEmpty()) {
            TextButton(onClick = { haptics.tick(); confirm = "stack" }, enabled = !busy) {
                Icon(Icons.Rounded.Layers, null)
                Spacer(Modifier.width(8.dp))
                Text("Mettre à jour toute la stack « ${c.project} »")
            }
        }
    }

    if (changelog) {
        ChangelogSheet(
            c, canUpdate = enabled, busy = busy,
            onUpdate = { changelog = false; confirm = "update" },
            onDismiss = { changelog = false },
        )
    }

    confirm?.let { what ->
        AlertDialog(
            onDismissRequest = { confirm = null },
            icon = { Icon(if (what == "stack") Icons.Rounded.Layers else Icons.Rounded.SystemUpdateAlt, null) },
            title = { Text(if (what == "stack") "Mettre à jour la stack « ${c.project} » ?" else "Mettre à jour ${c.name} ?") },
            text = {
                Text(
                    if (what == "stack") {
                        "Chaque conteneur de la stack est mis à jour l'un après l'autre s'il existe une nouvelle image. " +
                            "En cas d'échec, l'ancienne version du conteneur concerné est rétablie."
                    } else updateExplanation(c),
                )
            },
            confirmButton = {
                TextButton(onClick = {
                    confirm = null
                    if (what == "stack") onStack() else onUpdate()
                }) { Text("Mettre à jour") }
            },
            dismissButton = { TextButton(onClick = { confirm = null }) { Text("Annuler") } },
        )
    }
}
