package com.karelisio.vigie.ui.containers

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.Undo
import androidx.compose.material.icons.rounded.History
import androidx.compose.material.icons.rounded.VisibilityOff
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.Stable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.karelisio.vigie.appContainer
import com.karelisio.vigie.data.Container
import com.karelisio.vigie.data.Fmt
import com.karelisio.vigie.data.LiveState
import com.karelisio.vigie.data.RollbackPoint
import com.karelisio.vigie.data.rollbackContainer
import com.karelisio.vigie.data.rollbackPoint
import com.karelisio.vigie.data.summary
import com.karelisio.vigie.data.unignoreRollback
import com.karelisio.vigie.data.versionAtLeast
import com.karelisio.vigie.security.LocalActionGuard
import com.karelisio.vigie.ui.components.rememberHaptics
import com.karelisio.vigie.ui.navigation.LocalSnackbar
import kotlinx.coroutines.launch

/** Version précédente d'un conteneur (agent 0.6.0 et plus) : partagée par la carte Image et la chronologie. */
@Stable
internal class RollbackController(val name: String) {
    var point by mutableStateOf<RollbackPoint?>(null)
        private set

    /** Confirmation demandée (bouton de la carte Image ou de la chronologie). */
    var confirm by mutableStateOf(false)

    /** Retour possible maintenant. */
    val ready get() = point?.let { it.ready && it.reachable } == true

    suspend fun load() {
        runCatching { appContainer.repository.rollbackPoint(name) }.onSuccess { point = it }
    }

    internal fun replace(p: RollbackPoint) {
        point = p
    }
}

/**
 * Point de retour du conteneur, rechargé quand il change (recréé, version ignorée ou non) et à la fin
 * d'une opération qui le concerne. Null en lecture seule, sans mises à jour depuis l'appli, pour
 * l'agent lui-même et pour un agent antérieur à 0.6.
 */
@Composable
internal fun rememberRollback(c: Container?, live: LiveState): RollbackController? {
    val usable = c != null && !c.self && !live.readOnly && live.updatesEnabled && versionAtLeast(live.agentVersion, 0, 6)
    val name = c?.name?.takeIf { usable }
    val controller = remember(name) { name?.let(::RollbackController) }
    val lastJob = c?.let { live.jobs.firstOrNull { j -> j.concerns(it) } }
    LaunchedEffect(controller, c?.id, c?.updateAvailable, c?.updateIgnored, lastJob?.id, lastJob?.done) { controller?.load() }
    return controller
}

/** Ligne « Version précédente » de la carte Image, ou rappel du retour effectué et de la version ignorée. */
@Composable
internal fun RollbackSection(rb: RollbackController, busy: Boolean) {
    val p = rb.point ?: return
    val haptics = rememberHaptics()
    val scope = rememberCoroutineScope()
    val snackbar = LocalSnackbar.current
    var saving by remember { mutableStateOf(false) }
    if (p.ready) {
        Spacer(Modifier.height(12.dp))
        InfoRow(p.summary(Fmt::dateTime), when {
            p.imagePresent -> null
            p.downloadable -> "Image supprimée du disque : elle sera re-téléchargée."
            else -> "Image supprimée du disque et sans registre d'où la récupérer : retour impossible."
        })
        Spacer(Modifier.height(8.dp))
        OutlinedButton(
            onClick = { haptics.tick(); rb.confirm = true },
            enabled = !busy && p.reachable,
            shapes = ButtonDefaults.shapes(),
            modifier = Modifier.fillMaxWidth(),
        ) {
            Icon(Icons.AutoMirrored.Rounded.Undo, null)
            Spacer(Modifier.width(8.dp))
            Text(if (p.vulnfix) "Revenir à l'image d'origine" else "Revenir à cette version")
        }
    } else if (p.rolledBack) {
        Spacer(Modifier.height(12.dp))
        InfoRow(
            if (p.vulnfix) "Revenu à l'image d'origine le ${Fmt.dateTime(p.rolledBackAt)}"
            else "Revenu en version ${p.oldLabel} le ${Fmt.dateTime(p.rolledBackAt)}",
            null,
        )
    }
    AnimatedVisibility(p.ignored) {
        Column {
            Spacer(Modifier.height(8.dp))
            Row(verticalAlignment = Alignment.CenterVertically) {
                Icon(Icons.Rounded.VisibilityOff, null, Modifier.size(20.dp), tint = MaterialTheme.colorScheme.onSurfaceVariant)
                Spacer(Modifier.width(10.dp))
                Column(Modifier.weight(1f)) {
                    Text(if (p.vulnfix) "Correction écartée" else "Version ${p.newLabel} ignorée", style = MaterialTheme.typography.bodyMedium)
                    Text(
                        if (p.vulnfix) "La correction automatique de nuit ne la refait pas. Le bouton Corriger reste possible."
                        else "Les mises à jour automatiques ne la réinstallent pas, une version plus récente si.",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                TextButton(
                    onClick = {
                        haptics.tick()
                        saving = true
                        scope.launch {
                            val r = runCatching { appContainer.repository.unignoreRollback(rb.name) }
                            saving = false
                            r.onSuccess { rb.replace(it) }.onFailure { haptics.reject() }
                            snackbar.currentSnackbarData?.dismiss()
                            snackbar.showSnackbar(
                                r.fold(
                                    {
                                        if (p.vulnfix) "La correction automatique de ${rb.name} est de nouveau possible"
                                        else "La version ${p.newLabel} de ${rb.name} n'est plus ignorée"
                                    },
                                    { "Échec : ${it.message}" },
                                ),
                            )
                        }
                    },
                    enabled = !saving,
                ) { Text(if (p.vulnfix) "Autoriser" else "Ne plus ignorer") }
            }
        }
    }
}

@Composable
private fun InfoRow(text: String, note: String?) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        Icon(Icons.Rounded.History, null, Modifier.size(20.dp), tint = MaterialTheme.colorScheme.onSurfaceVariant)
        Spacer(Modifier.width(10.dp))
        Column {
            Text(text, style = MaterialTheme.typography.bodyMedium)
            if (note != null) {
                Text(note, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        }
    }
}

/** Ce que le retour va faire, expliqué avant confirmation. */
internal fun rollbackExplanation(name: String, p: RollbackPoint): String = if (p.vulnfix) {
    buildString {
        append("$name sera recréé avec son image d'origine (${p.oldLabel}), sans les paquets mis à niveau par Vigie : ")
        append("mêmes réglages, réseaux et volumes, quelques secondes de coupure. Les failles corrigées réapparaîtront ")
        append("et la correction automatique de nuit ne la refera pas ; le bouton Corriger reste possible.")
        if (!p.imagePresent) append(" L'image d'origine a été supprimée du disque : elle sera re-téléchargée.")
    }
} else buildString {
    append("$name sera recréé avec sa version ${p.oldLabel}")
    if (p.updatedAt > 0) append(", celle d'avant la mise à jour du ${Fmt.dateTime(p.updatedAt)}")
    append(" : mêmes réglages, réseaux et volumes, quelques secondes de coupure. ")
    append("La version ${p.newLabel} sera ignorée par les mises à jour automatiques jusqu'à la suivante.")
    if (!p.imagePresent) append(" L'ancienne image a été supprimée du disque : elle sera re-téléchargée.")
    append("\n\nSi la mise à jour a converti ses données, l'ancienne version peut ne pas les relire.")
}

/** Confirmation du retour à la version précédente, puis lancement (empreinte si le verrou l'exige). */
@Composable
internal fun RollbackDialog(rb: RollbackController) {
    val p = rb.point
    if (!rb.confirm || p == null) return
    val guard = LocalActionGuard.current
    val snackbar = LocalSnackbar.current
    val scope = rememberCoroutineScope()
    val haptics = rememberHaptics()
    AlertDialog(
        onDismissRequest = { rb.confirm = false },
        icon = { Icon(Icons.AutoMirrored.Rounded.Undo, null) },
        title = { Text(if (p.vulnfix) "Revenir à l'image d'origine de ${rb.name} ?" else "Revenir à la version ${p.oldLabel} de ${rb.name} ?") },
        text = { Text(rollbackExplanation(rb.name, p)) },
        confirmButton = {
            TextButton(onClick = {
                rb.confirm = false
                scope.launch {
                    if (!guard(if (p.vulnfix) "Revenir à l'image d'origine de ${rb.name}" else "Revenir à la version précédente de ${rb.name}")) return@launch
                    runCatching { appContainer.repository.rollbackContainer(rb.name) }.onFailure {
                        haptics.reject()
                        snackbar.showSnackbar("Échec : ${it.message}")
                    }
                }
            }) { Text("Revenir") }
        },
        dismissButton = { TextButton(onClick = { rb.confirm = false }) { Text("Annuler") } },
    )
}
