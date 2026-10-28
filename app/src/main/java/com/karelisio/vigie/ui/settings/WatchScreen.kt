package com.karelisio.vigie.ui.settings

import android.app.Application
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.OpenInNew
import androidx.compose.material.icons.automirrored.rounded.Send
import androidx.compose.material.icons.rounded.LinkOff
import androidx.compose.material.icons.rounded.Refresh
import androidx.compose.material.icons.rounded.Visibility
import androidx.compose.material.icons.rounded.Watch
import androidx.compose.material.icons.rounded.WatchOff
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LoadingIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.unit.dp
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.NavHostController
import com.karelisio.vigie.BuildConfig
import com.karelisio.vigie.appContainer
import com.karelisio.vigie.data.versionAtLeast
import com.karelisio.vigie.security.LocalActionGuard
import com.karelisio.vigie.ui.components.ScreenHeader
import com.karelisio.vigie.ui.components.SectionCard
import com.karelisio.vigie.ui.components.cascade
import com.karelisio.vigie.ui.components.rememberHaptics
import com.karelisio.vigie.ui.navigation.LocalSnackbar
import com.karelisio.vigie.ui.theme.Vigie
import com.karelisio.vigie.wearos.WatchStatus
import com.karelisio.vigie.wearos.WearLink
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

class WatchViewModel(app: Application) : AndroidViewModel(app) {
    private val c = appContainer
    private val link = WearLink(app)

    private val _status = MutableStateFlow<WatchStatus>(WatchStatus.Checking)
    val status = _status.asStateFlow()

    /** Version de l'agent du serveur actif (accès dédié : agent 0.5 et plus). */
    private val _agentVersion = MutableStateFlow(c.repository.live.value.agentVersion)
    val agentVersion = _agentVersion.asStateFlow()

    private val _busy = MutableStateFlow(false)
    val busy = _busy.asStateFlow()

    val server get() = c.servers.active

    init {
        if (_agentVersion.value.isEmpty()) {
            viewModelScope.launch { runCatching { c.repository.info() }.onSuccess { _agentVersion.value = it.version } }
        }
    }

    fun check() {
        viewModelScope.launch {
            if (_status.value !is WatchStatus.Ready) _status.value = WatchStatus.Checking
            _status.value = link.status()
        }
    }

    fun dedicatedInUse(): Boolean = server?.let { link.hasDedicatedAccess(it) } == true

    /** Envoie l'accès du serveur actif ; renvoie le nom de la montre. */
    suspend fun send(dedicated: Boolean): Result<String> = run {
        val server = server ?: return Result.failure(IllegalStateException("aucun serveur configuré"))
        _busy.value = true
        try {
            runCatching {
                val ready = link.status().also { _status.value = it } as? WatchStatus.Ready
                    ?: error("la montre n'est pas prête")
                link.send(c.repository, server, ready.nodeIds, dedicated)
                ready.watch
            }
        } finally {
            _busy.value = false
        }
    }

    suspend fun forget(): Result<Unit> = run {
        val server = server ?: return Result.failure(IllegalStateException("aucun serveur configuré"))
        _busy.value = true
        try {
            runCatching {
                val ready = link.status().also { _status.value = it } as? WatchStatus.Ready ?: error("la montre n'est pas connectée")
                link.forget(c.repository, server, ready.nodeIds)
            }
        } finally {
            _busy.value = false
        }
    }
}

/** Montre Wear OS : état de la montre, envoi de l'accès au serveur et installation pas à pas. */
@Composable
fun WatchScreen(nav: NavHostController, vm: WatchViewModel = viewModel()) {
    val status by vm.status.collectAsStateWithLifecycle()
    val version by vm.agentVersion.collectAsStateWithLifecycle()
    val busy by vm.busy.collectAsStateWithLifecycle()
    val server = vm.server
    val snackbar = LocalSnackbar.current
    val guard = LocalActionGuard.current
    val haptics = rememberHaptics()
    val scope = rememberCoroutineScope()
    val uri = LocalUriHandler.current

    // Accès dédié possible : agent 0.5 ou plus, et serveur actif avec le token principal.
    val dedicatedPossible = server != null && !server.readOnly && versionAtLeast(version, 0, 5)
    var dedicated by rememberSaveable { mutableStateOf(true) }
    var linked by remember { mutableStateOf(vm.dedicatedInUse()) }

    // La montre peut se connecter ou s'installer pendant que l'écran est ouvert.
    LifecycleResumeEffect(Unit) {
        vm.check()
        onPauseOrDispose { }
    }

    LazyColumn(
        Modifier.fillMaxSize().navigationBarsPadding(),
        contentPadding = PaddingValues(start = 16.dp, end = 16.dp, bottom = 32.dp),
        verticalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        item { ScreenHeader("Montre", nav) }
        item {
            Text(
                "L'état de ton serveur au poignet : une tuile, une complication pour le cadran et une petite appli. " +
                    "La montre interroge l'agent elle-même (Wi-Fi, LTE ou via le téléphone).",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(horizontal = 4.dp),
            )
        }
        item { StatusCard(status, Modifier.cascade(0)) { vm.check() } }
        item {
            val subtitle = when {
                server?.readOnly == true -> "Ton accès à ce serveur est déjà en lecture seule : il est envoyé tel quel"
                server != null && !versionAtLeast(version, 0, 5) -> "Nécessite l'agent 0.5 ou plus récent : le token du serveur sera envoyé"
                else -> "Recommandé : la montre reçoit son propre accès « Montre », révocable dans Accès partagés, plutôt que ton token principal"
            }
            ToggleRow(Icons.Rounded.Visibility, "Accès dédié en lecture seule", subtitle, dedicated && dedicatedPossible, enabled = dedicatedPossible) {
                haptics.toggle(it)
                dedicated = it
            }
        }
        item {
            Button(
                onClick = {
                    haptics.tick()
                    scope.launch {
                        if (!guard("Envoyer l'accès à la montre")) return@launch
                        vm.send(dedicated && dedicatedPossible).fold(
                            { watch ->
                                haptics.confirm()
                                linked = vm.dedicatedInUse()
                                snackbar.showSnackbar("✓ Accès envoyé à $watch")
                            },
                            { e ->
                                haptics.reject()
                                snackbar.showSnackbar("Échec : ${e.message}")
                            },
                        )
                    }
                },
                enabled = status is WatchStatus.Ready && server != null && !busy,
                shapes = ButtonDefaults.shapes(),
                modifier = Modifier.fillMaxWidth().height(56.dp).cascade(1),
            ) {
                if (busy) {
                    LoadingIndicator(Modifier.size(24.dp))
                } else {
                    Icon(Icons.AutoMirrored.Rounded.Send, null)
                }
                Spacer(Modifier.width(8.dp))
                Text("Envoyer à la montre")
            }
        }
        if (server != null) {
            item {
                Text(
                    "Serveur envoyé : ${server.name}, le serveur actif. Pour un autre serveur, rends-le actif dans les réglages, puis renvoie.",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(horizontal = 4.dp),
                )
            }
        }
        if (status is WatchStatus.Ready) {
            item {
                TextButton(
                    onClick = {
                        scope.launch {
                            if (!guard("Retirer l'accès de la montre")) return@launch
                            val wasLinked = linked
                            vm.forget().fold(
                                {
                                    linked = false
                                    snackbar.showSnackbar(if (wasLinked) "Accès retiré de la montre et révoqué" else "Accès retiré de la montre")
                                },
                                { e -> snackbar.showSnackbar("Échec : ${e.message}") },
                            )
                        }
                    },
                    enabled = !busy,
                ) {
                    Icon(Icons.Rounded.LinkOff, null)
                    Spacer(Modifier.width(8.dp))
                    Text(if (linked) "Retirer l'accès et le révoquer" else "Retirer l'accès de la montre")
                }
            }
        }
        item { InstallSteps(Modifier.cascade(2)) { uri.openUri("https://github.com/${BuildConfig.GITHUB_REPO}/releases/latest") } }
    }
}

@Composable
private fun StatusCard(status: WatchStatus, modifier: Modifier = Modifier, onCheck: () -> Unit) {
    val (title, text) = when (status) {
        WatchStatus.Checking -> "Recherche de ta montre…" to "Connexion à l'appli Wear OS du téléphone."
        WatchStatus.Unavailable -> "Aucune montre Wear OS" to
            "Ce téléphone n'a pas d'appli compagnon Wear OS (Wear OS, Pixel Watch, Galaxy Wearable…). Associe d'abord ta montre."
        WatchStatus.NoWatch -> "Aucune montre connectée" to "Vérifie que ta montre est allumée, associée et à portée (Bluetooth ou Wi-Fi)."
        is WatchStatus.NotInstalled -> "${status.watch} est connectée" to
            "L'appli Vigie n'est pas encore installée dessus : suis les étapes ci-dessous."
        is WatchStatus.Ready -> "Vigie est installée sur ${status.watch}" to "Prête à recevoir l'accès à ton serveur."
    }
    val ready = status is WatchStatus.Ready
    SectionCard(modifier) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Icon(
                if (status is WatchStatus.Unavailable || status is WatchStatus.NoWatch) Icons.Rounded.WatchOff else Icons.Rounded.Watch,
                null,
                tint = if (ready) Vigie.status.ok else MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.width(16.dp))
            Column(Modifier.weight(1f)) {
                Text(title, style = MaterialTheme.typography.titleMedium)
                Text(text, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            if (status is WatchStatus.Checking) {
                LoadingIndicator(Modifier.size(40.dp))
            } else {
                IconButton(onClick = onCheck) { Icon(Icons.Rounded.Refresh, "Vérifier à nouveau") }
            }
        }
    }
}

private val STEPS = listOf(
    "Sur la montre : Paramètres → Système → À propos → Versions, touche 7 fois « Numéro de build » pour activer les options pour les développeurs.",
    "Options pour les développeurs : active « Débogage ADB » puis « Déboguer via Wi-Fi » (montre et ordinateur sur le même Wi-Fi).",
    "Touche « Associer un nouvel appareil » : sur l'ordinateur, lance adb pair IP:port avec le code affiché, puis adb connect IP:port (adresse indiquée sous « Déboguer via Wi-Fi »).",
    "Télécharge vigie-wear-X.Y.Z.apk dans la dernière version sur GitHub (même version que cette appli), puis lance adb install vigie-wear-X.Y.Z.apk.",
    "Reviens ici et touche « Envoyer à la montre ». Ajoute ensuite la tuile Vigie (balaye le cadran, « + ») ou la complication sur ton cadran.",
)

@Composable
private fun InstallSteps(modifier: Modifier = Modifier, onDownload: () -> Unit) {
    SectionCard(modifier, title = "Installer Vigie sur la montre") {
        STEPS.forEachIndexed { i, step ->
            Row(Modifier.padding(vertical = 4.dp)) {
                Text("${i + 1}.", style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.primary, modifier = Modifier.width(24.dp))
                Text(step, style = MaterialTheme.typography.bodyMedium)
            }
        }
        Spacer(Modifier.height(8.dp))
        OutlinedButton(onClick = onDownload, shapes = ButtonDefaults.shapes()) {
            Icon(Icons.AutoMirrored.Rounded.OpenInNew, null)
            Spacer(Modifier.width(8.dp))
            Text("Versions sur GitHub")
        }
        Text(
            "Version de cette appli : ${BuildConfig.VERSION_NAME}. Les deux applis doivent venir de la même release (même signature).",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.padding(top = 8.dp),
        )
    }
}
