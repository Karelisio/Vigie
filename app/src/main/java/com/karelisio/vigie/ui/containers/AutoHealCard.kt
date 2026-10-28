package com.karelisio.vigie.ui.containers

import androidx.compose.animation.animateContentSize
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.height
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Healing
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.karelisio.vigie.appContainer
import com.karelisio.vigie.data.AgentSettings
import com.karelisio.vigie.data.AutoHealRepair
import com.karelisio.vigie.data.Container
import com.karelisio.vigie.data.autoHealHistory
import com.karelisio.vigie.data.autoHeals
import com.karelisio.vigie.data.versionAtLeast
import com.karelisio.vigie.data.withAutoHeal
import com.karelisio.vigie.ui.components.SectionCard
import com.karelisio.vigie.ui.components.rememberHaptics
import com.karelisio.vigie.ui.navigation.LocalSnackbar
import com.karelisio.vigie.ui.settings.RepairRow
import com.karelisio.vigie.ui.settings.ToggleRow
import kotlinx.coroutines.launch

/**
 * Réparation automatique dans la fiche d'un conteneur (agent 0.5.0 et plus) : interrupteur et
 * dernières réparations. Jamais pour l'agent lui-même.
 */
@Composable
internal fun AutoHealCard(c: Container, agentVersion: String, modifier: Modifier = Modifier) {
    if (c.self || !versionAtLeast(agentVersion, 0, 5)) return
    val repo = appContainer.repository
    var settings by remember { mutableStateOf<AgentSettings?>(null) }
    var repairs by remember { mutableStateOf<List<AutoHealRepair>>(emptyList()) }
    var saving by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    val snackbar = LocalSnackbar.current
    val haptics = rememberHaptics()
    LaunchedEffect(Unit) { settings = runCatching { repo.settings() }.getOrNull() }
    // Rechargées à chaque changement d'état : une réparation vient peut-être d'avoir lieu.
    LaunchedEffect(c.state, c.startedAt, c.health) {
        runCatching { repo.autoHealHistory() }.onSuccess { list -> repairs = list.filter { it.container == c.name } }
    }
    // Réglages illisibles (agent injoignable) : la carte n'est pas proposée.
    val s = settings ?: return
    val on = s.autoHeals(c)
    SectionCard(modifier.animateContentSize(), title = "Réparation automatique") {
        ToggleRow(
            Icons.Rounded.Healing,
            "Réparer ce conteneur",
            if (on) {
                "Redémarré après ${s.autoHealDelay} min de healthcheck en échec, relancé s'il plante (${s.autoHealMax} fois par heure au plus)"
            } else {
                "Le redémarrer s'il reste en mauvaise santé ou s'arrête anormalement"
            },
            on,
            enabled = !saving,
        ) { want ->
            haptics.toggle(want)
            saving = true
            scope.launch {
                val r = runCatching { repo.saveSettings(s.withAutoHeal(c, want, repo.live.value.containers)) }
                saving = false
                r.onSuccess { settings = it }.onFailure { haptics.reject() }
                snackbar.currentSnackbarData?.dismiss()
                snackbar.showSnackbar(
                    r.fold(
                        { if (want) "${c.name} sera réparé automatiquement" else "Plus de réparation automatique pour ${c.name}" },
                        { "Échec : ${it.message}" },
                    ),
                )
            }
        }
        if (repairs.isNotEmpty()) {
            Spacer(Modifier.height(12.dp))
            Text("Dernières réparations", style = MaterialTheme.typography.labelLarge)
            repairs.take(3).forEach { RepairRow(it, showName = false) }
        }
    }
    Spacer(Modifier.height(12.dp))
}
