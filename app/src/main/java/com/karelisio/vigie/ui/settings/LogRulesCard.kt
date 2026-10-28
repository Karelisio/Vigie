package com.karelisio.vigie.ui.settings

import androidx.compose.animation.animateContentSize
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ManageSearch
import androidx.compose.material.icons.rounded.Add
import androidx.compose.material.icons.rounded.Delete
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialShapes
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.SnackbarDuration
import androidx.compose.material3.SnackbarResult
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.toShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.karelisio.vigie.appContainer
import com.karelisio.vigie.data.Container
import com.karelisio.vigie.data.Fmt
import com.karelisio.vigie.data.LogRule
import com.karelisio.vigie.data.LogWatchStatus
import com.karelisio.vigie.data.logWatch
import com.karelisio.vigie.ui.components.Chip
import com.karelisio.vigie.ui.components.SectionCard
import com.karelisio.vigie.ui.components.Springs
import com.karelisio.vigie.ui.components.StatusDot
import com.karelisio.vigie.ui.components.rememberHaptics
import com.karelisio.vigie.ui.components.rememberNow
import com.karelisio.vigie.ui.logs.LogRuleDialog
import com.karelisio.vigie.ui.navigation.LocalSnackbar
import com.karelisio.vigie.ui.theme.Vigie
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/**
 * Alertes sur les logs : liste, dernière détection, ajout et suppression.
 * [change] enregistre directement sur l'agent (hors brouillon de l'écran) et lève une erreur en cas de refus.
 */
@Composable
internal fun LogRulesCard(
    rules: List<LogRule>,
    change: suspend ((List<LogRule>) -> List<LogRule>) -> Unit,
    modifier: Modifier = Modifier,
) {
    val repo = appContainer.repository
    val live by repo.live.collectAsStateWithLifecycle()
    val snackbar = LocalSnackbar.current
    val haptics = rememberHaptics()
    val scope = rememberCoroutineScope()
    val now = rememberNow(30_000)
    var adding by remember { mutableStateOf(false) }
    var statuses by remember { mutableStateOf<Map<String, LogWatchStatus>>(emptyMap()) }
    LaunchedEffect(rules) {
        while (true) {
            runCatching { repo.logWatch() }.onSuccess { list -> statuses = list.associateBy { it.ruleId } }
            delay(30_000)
        }
    }

    LogRulesContent(
        rules, statuses, live.containers, now, modifier,
        onAdd = { haptics.tick(); adding = true },
        onDelete = { rule ->
            scope.launch {
                runCatching { change { list -> list.filterNot { it.id == rule.id } } }
                    .onSuccess {
                        haptics.confirm()
                        val r = snackbar.showSnackbar("Alerte supprimée", actionLabel = "Annuler", duration = SnackbarDuration.Short)
                        if (r == SnackbarResult.ActionPerformed) runCatching { change { it + rule } }
                    }
                    .onFailure {
                        haptics.reject()
                        snackbar.showSnackbar("Échec : ${it.message}")
                    }
            }
        },
    )

    if (adding) {
        LogRuleDialog(
            containers = live.containers.map { it.name },
            initialContainer = null,
            initialPattern = "",
            onDismiss = { adding = false },
        ) { rule ->
            change { it + rule }
            adding = false
            scope.launch { snackbar.showSnackbar("Alerte ajoutée pour ${rule.container}") }
        }
    }
}

@Composable
internal fun LogRulesContent(
    rules: List<LogRule>,
    statuses: Map<String, LogWatchStatus>,
    containers: List<Container>,
    now: Long,
    modifier: Modifier,
    onAdd: () -> Unit,
    onDelete: (LogRule) -> Unit,
) {
    SectionCard(
        modifier,
        title = "Alertes sur les logs",
        action = {
            FilledTonalButton(onClick = onAdd, shapes = ButtonDefaults.shapes()) {
                Icon(Icons.Rounded.Add, null, Modifier.size(18.dp))
                Spacer(Modifier.width(6.dp))
                Text("Ajouter")
            }
        },
    ) {
        Text(
            "Une notification quand une nouvelle ligne de log d'un conteneur contient le texte choisi.",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(12.dp))
        val reduced = Vigie.reducedMotion
        Column(
            if (reduced) Modifier else Modifier.animateContentSize(Springs.gentle()),
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            if (rules.isEmpty()) {
                Text(
                    "Aucune. Ajoutes-en une ici ou depuis les logs d'un conteneur.",
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            rules.forEach { rule ->
                LogRuleRow(rule, statuses[rule.id], containers.find { it.name == rule.container }, now) { onDelete(rule) }
            }
        }
    }
}

@Composable
private fun LogRuleRow(rule: LogRule, status: LogWatchStatus?, container: Container?, now: Long, onDelete: () -> Unit) {
    val colors = Vigie.status
    val scheme = MaterialTheme.colorScheme
    val recent = (status?.matches24h ?: 0) > 0
    Surface(shape = RoundedCornerShape(20.dp), color = scheme.surfaceContainerHigh, modifier = Modifier.fillMaxWidth()) {
        Row(Modifier.padding(start = 12.dp, top = 12.dp, bottom = 12.dp, end = 4.dp), verticalAlignment = Alignment.CenterVertically) {
            Surface(
                shape = MaterialShapes.Cookie6Sided.toShape(),
                color = if (recent) colors.warnContainer else scheme.secondaryContainer,
                contentColor = if (recent) colors.onWarnContainer else scheme.onSecondaryContainer,
                modifier = Modifier.size(40.dp),
            ) {
                Icon(Icons.AutoMirrored.Rounded.ManageSearch, null, Modifier.padding(9.dp))
            }
            Spacer(Modifier.width(12.dp))
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    StatusDot(if (status?.active == true) "ok" else "neutral", size = 7.dp)
                    Text(
                        rule.container,
                        style = MaterialTheme.typography.titleSmall,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                        modifier = Modifier.weight(1f, fill = false),
                    )
                    if (rule.regex) {
                        Spacer(Modifier.width(8.dp))
                        Chip("regex", scheme.tertiaryContainer, scheme.onTertiaryContainer)
                    }
                }
                Text(rule.pattern, fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodyMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
                detection(status, container, now)?.let {
                    Text(it, style = MaterialTheme.typography.labelSmall, color = if (recent) colors.warn else scheme.onSurfaceVariant)
                }
                status?.lastLine?.takeIf { it.isNotEmpty() }?.let {
                    Text(it, fontFamily = FontFamily.Monospace, fontSize = 11.sp, lineHeight = 14.sp, color = scheme.onSurfaceVariant, maxLines = 2, overflow = TextOverflow.Ellipsis)
                }
            }
            IconButton(onClick = onDelete) { Icon(Icons.Rounded.Delete, "Supprimer l'alerte") }
        }
    }
}

/** « Détectée il y a 3 min · 12 fois en 24 h », et la raison d'un suivi en pause. */
private fun detection(status: LogWatchStatus?, container: Container?, now: Long): String? {
    if (status == null) return null
    val found = if (status.lastMatch > 0) {
        "Détectée ${Fmt.ago(status.lastMatch, now)}" + if (status.matches24h > 0) " · ${status.matches24h} fois en 24 h" else ""
    } else "Jamais détectée"
    val paused = when {
        status.active -> null
        container == null -> "conteneur introuvable"
        !container.running -> "en pause, conteneur arrêté"
        else -> null
    }
    return listOfNotNull(found, paused).joinToString(" · ")
}
