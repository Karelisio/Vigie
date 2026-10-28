package com.karelisio.vigie.ui.settings

import android.os.Build
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.asPaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBars
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.selection.selectableGroup
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowForward
import androidx.compose.material.icons.automirrored.rounded.ListAlt
import androidx.compose.material.icons.rounded.Add
import androidx.compose.material.icons.rounded.CheckCircle
import androidx.compose.material.icons.rounded.Dns
import androidx.compose.material.icons.rounded.Edit
import androidx.compose.material.icons.rounded.Fingerprint
import androidx.compose.material.icons.rounded.SystemUpdateAlt
import androidx.compose.material.icons.rounded.Tune
import androidx.compose.material.icons.rounded.Info
import androidx.compose.material.icons.rounded.NotificationsActive
import androidx.compose.material.icons.rounded.Palette
import androidx.compose.material.icons.rounded.Security
import androidx.compose.material.icons.rounded.SendToMobile
import androidx.compose.material.icons.rounded.SystemUpdate
import androidx.compose.material.icons.rounded.WarningAmber
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Icon
import androidx.compose.material3.RadioButton
import androidx.compose.material3.TextButton
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.semantics.Role
import androidx.fragment.app.FragmentActivity
import com.karelisio.vigie.data.LockMode
import com.karelisio.vigie.security.authenticate
import com.karelisio.vigie.security.canAuthenticate
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.NavHostController
import com.karelisio.vigie.appContainer
import com.karelisio.vigie.data.ServerInfo
import com.karelisio.vigie.data.UserPrefs
import com.karelisio.vigie.ui.components.rememberHaptics
import com.karelisio.vigie.ui.navigation.AboutRoute
import com.karelisio.vigie.ui.navigation.AlertsRoute
import com.karelisio.vigie.ui.navigation.AuditRoute
import com.karelisio.vigie.ui.navigation.LocalSnackbar
import com.karelisio.vigie.ui.navigation.PermissionsRoute
import com.karelisio.vigie.ui.navigation.ServerEditRoute
import com.karelisio.vigie.ui.theme.Vigie
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

class SettingsViewModel : ViewModel() {
    private val c = appContainer
    val servers = c.servers.servers
    val activeId = c.servers.activeId
    val prefs = c.prefs.flow
    private val _info = MutableStateFlow<ServerInfo?>(null)
    val info = _info.asStateFlow()

    init {
        viewModelScope.launch { runCatching { c.repository.info() }.onSuccess { _info.value = it } }
    }

    fun setActive(id: String) {
        c.servers.setActive(id)
        _info.value = null
        viewModelScope.launch { runCatching { c.repository.info() }.onSuccess { _info.value = it } }
    }

    suspend fun setNotifications(on: Boolean) {
        c.prefs.setNotifications(on)
        if (on) c.push.syncToken()
    }

    suspend fun setDynamic(on: Boolean) = c.prefs.setDynamicColor(on)

    suspend fun testPush(): String {
        val token = c.push.token() ?: return "Firebase n'est pas configuré dans cette version de l'appli."
        c.servers.active?.let { c.repository.registerDevice(it, token) }
        return runCatching { c.repository.testPush(token); "Notification envoyée" }.getOrElse { it.message ?: "Échec" }
    }

    suspend fun updateAgent(): String? = runCatching { c.repository.updateAgent(); null }.getOrElse { "Échec : ${it.message}" }

    suspend fun checkUpdate(): String = runCatching {
        if (c.updater.check(force = true) == null) "Vigie est à jour (${c.updater.currentVersion})" else ""
    }.getOrElse { "Vérification impossible : ${it.message}" }
}

@Composable
fun SettingsScreen(nav: NavHostController, vm: SettingsViewModel = viewModel()) {
    val servers by vm.servers.collectAsStateWithLifecycle()
    val active by vm.activeId.collectAsStateWithLifecycle()
    val prefs by vm.prefs.collectAsStateWithLifecycle(UserPrefs())
    val info by vm.info.collectAsStateWithLifecycle()
    val scope = rememberCoroutineScope()
    val snackbar = LocalSnackbar.current
    val haptics = rememberHaptics()
    val bottom = WindowInsets.navigationBars.asPaddingValues().calculateBottomPadding() + 96.dp
    val context = LocalContext.current
    var lockDialog by remember { mutableStateOf(false) }
    var agentDialog by remember { mutableStateOf(false) }
    val guard = com.karelisio.vigie.security.LocalActionGuard.current
    val live by appContainer.repository.live.collectAsStateWithLifecycle()

    if (agentDialog) {
        AlertDialog(
            onDismissRequest = { agentDialog = false },
            icon = { Icon(Icons.Rounded.SystemUpdateAlt, null) },
            title = { Text("Mettre à jour l'agent ?") },
            text = {
                Text(
                    "Vigie télécharge la dernière image de l'agent puis se remplace (environ 10 secondes de coupure). " +
                        "Si la nouvelle version ne démarre pas, l'ancienne est rétablie automatiquement.",
                )
            },
            confirmButton = {
                TextButton(onClick = {
                    agentDialog = false
                    scope.launch {
                        if (!guard("Mettre à jour l'agent")) return@launch
                        vm.updateAgent()?.let { snackbar.showSnackbar(it) }
                    }
                }) { Text("Mettre à jour") }
            },
            dismissButton = { TextButton(onClick = { agentDialog = false }) { Text("Annuler") } },
        )
    }

    if (lockDialog) {
        LockDialog(prefs.lockMode, onDismiss = { lockDialog = false }) { mode ->
            lockDialog = false
            val activity = context as? FragmentActivity ?: return@LockDialog
            scope.launch {
                // Toute modification du verrou demande l'empreinte (y compris pour le désactiver).
                if (activity.authenticate("Modifier le verrouillage")) {
                    appContainer.prefs.setLockMode(mode)
                    appContainer.lock.unlock()
                    haptics.confirm()
                }
            }
        }
    }

    LazyColumn(
        Modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = 16.dp, end = 16.dp, bottom = bottom),
        verticalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        item {
            Text(
                "Réglages",
                style = MaterialTheme.typography.headlineLargeEmphasized,
                modifier = Modifier.statusBarsPadding().padding(start = 4.dp, top = 16.dp, bottom = 8.dp),
            )
        }
        item { Section("Serveurs") }
        items(servers, key = { it.id }) { s ->
            val selected = s.id == active
            Surface(
                onClick = { haptics.tick(); vm.setActive(s.id) },
                shape = RoundedCornerShape(24.dp),
                color = if (selected) MaterialTheme.colorScheme.secondaryContainer else MaterialTheme.colorScheme.surfaceContainer,
                modifier = Modifier.fillMaxWidth().animateItem(),
            ) {
                Row(Modifier.padding(16.dp), verticalAlignment = Alignment.CenterVertically) {
                    Icon(if (selected) Icons.Rounded.CheckCircle else Icons.Rounded.Dns, null, tint = if (selected) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant)
                    Spacer(Modifier.width(16.dp))
                    Column(Modifier.weight(1f)) {
                        Text(s.name, style = MaterialTheme.typography.titleMedium)
                        Text(s.host, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                    IconButton(onClick = { nav.navigate(ServerEditRoute(id = s.id)) }) { Icon(Icons.Rounded.Edit, "Modifier") }
                }
            }
        }
        item { Row_(Icons.Rounded.Add, "Ajouter un serveur", "Par QR code ou saisie manuelle") { nav.navigate(ServerEditRoute()) } }

        item { Section("Notifications") }
        item {
            ToggleRow(Icons.Rounded.NotificationsActive, "Alertes push", pushSubtitle(info), prefs.notifications) { on ->
                haptics.toggle(on)
                scope.launch { vm.setNotifications(on) }
            }
        }
        item {
            Row_(Icons.Rounded.SendToMobile, "Envoyer une notification de test", "Vérifie toute la chaîne agent → Firebase → téléphone") {
                scope.launch { snackbar.showSnackbar(vm.testPush()) }
            }
        }
        item { Section("Apparence") }
        item {
            ToggleRow(
                Icons.Rounded.Palette, "Couleurs dynamiques",
                if (Build.VERSION.SDK_INT >= 31) "Couleurs tirées de ton fond d'écran" else "Nécessite Android 12",
                prefs.dynamicColor && Build.VERSION.SDK_INT >= 31,
                enabled = Build.VERSION.SDK_INT >= 31,
            ) { on -> haptics.toggle(on); scope.launch { vm.setDynamic(on) } }
        }
        item { Section("Sécurité et activité") }
        item {
            Row_(Icons.Rounded.Tune, "Alertes et seuils", "Seuils, heures calmes, certificats, sourdine") {
                nav.navigate(com.karelisio.vigie.ui.navigation.AlertSettingsRoute)
            }
        }
        item {
            val canLock = remember { canAuthenticate(context) }
            Row_(
                Icons.Rounded.Fingerprint, "Verrouillage",
                if (!canLock) "Configure une empreinte ou un code sur le téléphone pour l'activer" else when (prefs.lockMode) {
                    LockMode.Off -> "Désactivé"
                    LockMode.Open -> "Empreinte à l'ouverture de l'appli"
                    LockMode.Actions -> "Empreinte avant démarrer, arrêter ou redémarrer"
                },
            ) { if (canLock) lockDialog = true }
        }
        item { Row_(Icons.Rounded.Security, "Autorisations", "Notifications, caméra, batterie, installation") { nav.navigate(PermissionsRoute) } }
        item { Row_(Icons.Rounded.WarningAmber, "Alertes récentes", "Incidents des 30 derniers jours") { nav.navigate(AlertsRoute) } }
        item { Row_(Icons.AutoMirrored.Rounded.ListAlt, "Journal des actions", "Qui a démarré, arrêté ou redémarré quoi") { nav.navigate(AuditRoute) } }
        item { Section("Application") }
        item {
            Row_(Icons.Rounded.SystemUpdate, "Rechercher une mise à jour", "Version ${appContainer.updater.currentVersion}") {
                scope.launch { vm.checkUpdate().takeIf { it.isNotEmpty() }?.let { snackbar.showSnackbar(it) } }
            }
        }
        item {
            val i = info
            val failed = i?.lastSelfUpdate?.takeIf { !it.ok && System.currentTimeMillis() - it.ts < 7 * 86_400_000L }
            Row_(
                Icons.Rounded.SystemUpdateAlt, "Mettre à jour l'agent",
                when {
                    i == null -> "Télécharge la dernière image et redémarre l'agent"
                    !i.updatesEnabled -> "Agent ${i.version} : fais une première fois Pull + Redeploy dans Dockhand"
                    live.runningJob != null -> "Une opération est en cours…"
                    failed != null -> "Dernière tentative annulée : ${failed.error}"
                    else -> "Agent ${i.version} · télécharge la dernière image et redémarre"
                },
            ) { if (i?.updatesEnabled == true && live.runningJob == null) agentDialog = true }
        }
        item { Row_(Icons.Rounded.Info, "À propos", info?.let { "Agent ${it.version} · ${it.host.hostname}" } ?: "Vigie") { nav.navigate(AboutRoute) } }
    }
}

private fun pushSubtitle(info: ServerInfo?): String = when {
    info == null -> "Prévenu dès qu'un problème survient"
    info.pushEnabled -> "Actives sur l'agent"
    else -> "L'agent n'a pas encore de compte de service Firebase"
}

@Composable
private fun Section(title: String) {
    Text(
        title,
        style = MaterialTheme.typography.titleSmall,
        color = MaterialTheme.colorScheme.primary,
        modifier = Modifier.padding(start = 8.dp, top = 16.dp, bottom = 4.dp),
    )
}

@Composable
private fun Row_(icon: ImageVector, title: String, subtitle: String, onClick: () -> Unit) {
    Surface(onClick = onClick, shape = RoundedCornerShape(24.dp), color = MaterialTheme.colorScheme.surfaceContainer, modifier = Modifier.fillMaxWidth()) {
        Row(Modifier.padding(16.dp), verticalAlignment = Alignment.CenterVertically) {
            Icon(icon, null, tint = MaterialTheme.colorScheme.onSurfaceVariant)
            Spacer(Modifier.width(16.dp))
            Column(Modifier.weight(1f)) {
                Text(title, style = MaterialTheme.typography.titleMedium)
                Text(subtitle, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            Icon(Icons.AutoMirrored.Rounded.ArrowForward, null, Modifier.size(20.dp), tint = MaterialTheme.colorScheme.outline)
        }
    }
}

@Composable
internal fun ToggleRow(icon: ImageVector, title: String, subtitle: String, checked: Boolean, enabled: Boolean = true, onChange: (Boolean) -> Unit) {
    Surface(
        onClick = { if (enabled) onChange(!checked) },
        shape = RoundedCornerShape(24.dp),
        color = MaterialTheme.colorScheme.surfaceContainer,
        modifier = Modifier.fillMaxWidth(),
    ) {
        Row(Modifier.padding(16.dp), verticalAlignment = Alignment.CenterVertically) {
            Icon(icon, null, tint = if (checked) Vigie.status.ok else MaterialTheme.colorScheme.onSurfaceVariant)
            Spacer(Modifier.width(16.dp))
            Column(Modifier.weight(1f)) {
                Text(title, style = MaterialTheme.typography.titleMedium)
                Text(subtitle, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            Switch(checked = checked, onCheckedChange = onChange, enabled = enabled)
        }
    }
}

@Composable
private fun LockDialog(current: LockMode, onDismiss: () -> Unit, onPick: (LockMode) -> Unit) {
    var choice by remember { mutableStateOf(current) }
    AlertDialog(
        onDismissRequest = onDismiss,
        icon = { Icon(Icons.Rounded.Fingerprint, null) },
        title = { Text("Verrouillage") },
        text = {
            Column(Modifier.selectableGroup()) {
                listOf(
                    LockMode.Off to "Désactivé",
                    LockMode.Open to "À chaque ouverture (après 30 s en arrière-plan)",
                    LockMode.Actions to "Seulement pour démarrer, arrêter ou redémarrer",
                ).forEach { (mode, label) ->
                    Row(
                        Modifier
                            .fillMaxWidth()
                            .selectable(choice == mode, role = Role.RadioButton) { choice = mode }
                            .padding(vertical = 10.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        RadioButton(selected = choice == mode, onClick = null)
                        Spacer(Modifier.width(12.dp))
                        Text(label, style = MaterialTheme.typography.bodyLarge)
                    }
                }
            }
        },
        confirmButton = { TextButton(onClick = { onPick(choice) }, enabled = choice != current) { Text("Valider") } },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Annuler") } },
    )
}
