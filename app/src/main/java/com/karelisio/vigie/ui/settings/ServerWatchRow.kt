package com.karelisio.vigie.ui.settings

import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.NetworkCheck
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.karelisio.vigie.appContainer
import com.karelisio.vigie.data.Fmt
import com.karelisio.vigie.ui.components.rememberHaptics
import com.karelisio.vigie.ui.components.rememberNow
import com.karelisio.vigie.watch.watchMoment

/** Interrupteur « Surveiller le serveur depuis le téléphone », avec les pannes en cours. */
@Composable
internal fun ServerWatchRow(notificationsOn: Boolean) {
    val watch = appContainer.watch
    val enabled by watch.enabled.collectAsStateWithLifecycle()
    val states by watch.states.collectAsStateWithLifecycle()
    val servers by appContainer.servers.servers.collectAsStateWithLifecycle()
    val haptics = rememberHaptics()
    val now = rememberNow(60_000)
    val outages = servers.mapNotNull { s -> states[s.id]?.takeIf { it.alerted }?.let { s.name to it } }
    val lastCheck = states.values.maxOfOrNull { it.checkedAt }?.takeIf { it > 0 }
    val subtitle = when {
        !notificationsOn -> "Coupée avec les alertes push"
        enabled && outages.isNotEmpty() -> outages.joinToString("\n") { (name, s) ->
            "$name ne répond plus depuis ${watchMoment(s.since, now)} · ${s.cause}"
        }
        else -> "Toutes les 15 min, ton téléphone vérifie que chaque serveur répond et te prévient s'il tombe : " +
            "l'agent ne peut alors plus rien envoyer" +
            if (enabled && lastCheck != null) ".\nDernière vérification ${Fmt.ago(lastCheck, now)}" else ""
    }
    ToggleRow(
        Icons.Rounded.NetworkCheck, "Surveiller le serveur depuis le téléphone", subtitle,
        checked = enabled && notificationsOn,
        enabled = notificationsOn,
    ) { on ->
        haptics.toggle(on)
        watch.setEnabled(on)
    }
}
