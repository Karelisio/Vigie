package com.karelisio.vigie.ui.containers

import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.height
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.NightsStay
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
import com.karelisio.vigie.data.Container
import com.karelisio.vigie.data.autoUpdates
import com.karelisio.vigie.data.withAutoUpdate
import com.karelisio.vigie.ui.components.rememberHaptics
import com.karelisio.vigie.ui.navigation.LocalSnackbar
import com.karelisio.vigie.ui.settings.ToggleRow
import kotlinx.coroutines.launch

/** Interrupteur « Mise à jour automatique la nuit » de la fiche d'un conteneur (agent 0.4.0 et plus). */
@Composable
internal fun AutoUpdateSwitch(c: Container) {
    val repo = appContainer.repository
    var settings by remember { mutableStateOf<AgentSettings?>(null) }
    var saving by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    val snackbar = LocalSnackbar.current
    val haptics = rememberHaptics()
    LaunchedEffect(Unit) { settings = runCatching { repo.settings() }.getOrNull() }
    // Réglages illisibles (agent injoignable) : l'interrupteur n'est pas proposé.
    val s = settings ?: return
    val on = s.autoUpdates(c)
    Spacer(Modifier.height(8.dp))
    ToggleRow(
        Icons.Rounded.NightsStay,
        "Mise à jour automatique la nuit",
        if (on) "Chaque nuit à ${s.autoUpdateTime}, si une nouvelle image est publiée" else "Installer les nouvelles images pendant la nuit",
        on,
        enabled = !saving,
    ) { want ->
        haptics.toggle(want)
        saving = true
        scope.launch {
            val r = runCatching { repo.saveSettings(s.withAutoUpdate(c, want, repo.live.value.containers)) }
            saving = false
            r.onSuccess { settings = it }.onFailure { haptics.reject() }
            snackbar.currentSnackbarData?.dismiss()
            snackbar.showSnackbar(
                r.fold(
                    { if (want) "${c.name} sera mis à jour chaque nuit à ${it.autoUpdateTime}" else "Plus de mise à jour automatique pour ${c.name}" },
                    { "Échec : ${it.message}" },
                ),
            )
        }
    }
}
