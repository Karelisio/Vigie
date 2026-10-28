package com.karelisio.vigie.ui.security

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.asPaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBars
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Cloud
import androidx.compose.material.icons.rounded.Lan
import androidx.compose.material.icons.rounded.NetworkCheck
import androidx.compose.material.icons.rounded.NotificationsActive
import androidx.compose.material.icons.rounded.Public
import androidx.compose.material.icons.rounded.Refresh
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LoadingIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
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
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.NavHostController
import com.karelisio.vigie.appContainer
import com.karelisio.vigie.data.AgentSettings
import com.karelisio.vigie.data.ApiException
import com.karelisio.vigie.data.Fmt
import com.karelisio.vigie.data.OpenPort
import com.karelisio.vigie.data.PortsReport
import com.karelisio.vigie.data.ReachReport
import com.karelisio.vigie.data.hostOf
import com.karelisio.vigie.data.ports
import com.karelisio.vigie.data.probePorts
import com.karelisio.vigie.data.setPortAllowed
import com.karelisio.vigie.data.versionAtLeast
import com.karelisio.vigie.ui.components.Chip
import com.karelisio.vigie.ui.components.EmptyState
import com.karelisio.vigie.ui.components.ScreenHeader
import com.karelisio.vigie.ui.components.SectionCard
import com.karelisio.vigie.ui.components.SectionTitle
import com.karelisio.vigie.ui.components.cascade
import com.karelisio.vigie.ui.components.rememberHaptics
import com.karelisio.vigie.ui.navigation.LocalSnackbar
import com.karelisio.vigie.ui.settings.ToggleRow
import com.karelisio.vigie.ui.theme.Vigie
import com.karelisio.vigie.ui.theme.numeric
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

class PortsViewModel : ViewModel() {
    private val repo = appContainer.repository
    private val _report = MutableStateFlow<PortsReport?>(null)
    val report = _report.asStateFlow()
    private val _settings = MutableStateFlow<AgentSettings?>(null)
    val settings = _settings.asStateFlow()
    private val _error = MutableStateFlow<String?>(null)
    val error = _error.asStateFlow()
    private val _unsupported = MutableStateFlow(false)
    val unsupported = _unsupported.asStateFlow()
    var refreshing by mutableStateOf(false)
        private set
    var probing by mutableStateOf(false)
        private set
    var reach by mutableStateOf<ReachReport?>(null)
        private set
    /** Ports dont l'autorisation est en cours d'enregistrement. */
    var saving by mutableStateOf(emptySet<String>())
        private set

    init {
        load(false)
    }

    fun load(refresh: Boolean) {
        viewModelScope.launch {
            refreshing = true
            _error.value = null
            runCatching { repo.ports(refresh) }
                .onSuccess { _report.value = it }
                .onFailure { if ((it as? ApiException)?.code == 404) _unsupported.value = true else _error.value = it.message }
            runCatching { repo.settings() }.onSuccess { _settings.value = it }
            refreshing = false
        }
    }

    /** Autorise ou non un port ; la liste est mise à jour tout de suite, l'agent en tient compte à sa prochaine analyse. */
    suspend fun setAllowed(p: OpenPort, allowed: Boolean): Result<Unit> {
        saving = saving + p.key
        return runCatching {
            _settings.value = repo.setPortAllowed(p.key, allowed)
            _report.value = _report.value?.let { r ->
                r.copy(ports = r.ports.map { if (it.key == p.key) it.copy(allowed = allowed, alerted = it.alerted && !allowed) else it })
            }
        }.also { saving = saving - p.key }
    }

    suspend fun setAlert(on: Boolean): Result<Unit> = runCatching {
        val s = _settings.value ?: repo.settings()
        _settings.value = repo.saveSettings(s.copy(portAlert = on))
    }

    /** Teste depuis le téléphone chaque port TCP public. */
    fun probe(serverUrl: String) {
        val list = _report.value?.public?.filter { it.isTcp }?.map { it.port }.orEmpty()
        if (list.isEmpty() || probing) return
        viewModelScope.launch {
            probing = true
            reach = probePorts(hostOf(serverUrl), list)
            probing = false
        }
    }
}

/** Ports ouverts sur le serveur : ce qui est joignable depuis Internet, et ce qui contourne Pangolin. */
@OptIn(ExperimentalLayoutApi::class)
@Composable
fun PortsScreen(nav: NavHostController, vm: PortsViewModel = viewModel()) {
    val report by vm.report.collectAsStateWithLifecycle()
    val settings by vm.settings.collectAsStateWithLifecycle()
    val error by vm.error.collectAsStateWithLifecycle()
    val unsupportedApi by vm.unsupported.collectAsStateWithLifecycle()
    val live by appContainer.repository.live.collectAsStateWithLifecycle()
    val unsupported = unsupportedApi || (live.agentVersion.isNotEmpty() && !versionAtLeast(live.agentVersion, 0, 6))
    val serverUrl = appContainer.repository.activeServer?.url.orEmpty()
    val scope = rememberCoroutineScope()
    val snackbar = LocalSnackbar.current
    val haptics = rememberHaptics()
    val bottom = WindowInsets.navigationBars.asPaddingValues().calculateBottomPadding() + 32.dp

    LazyColumn(
        Modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = 16.dp, end = 16.dp, bottom = bottom),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        item {
            ScreenHeader("Ports ouverts", nav) {
                if (!unsupported) {
                    IconButton(onClick = { haptics.tick(); vm.load(true) }, enabled = !vm.refreshing) {
                        if (vm.refreshing) LoadingIndicator(Modifier.size(24.dp)) else Icon(Icons.Rounded.Refresh, "Analyser maintenant")
                    }
                }
            }
        }
        val r = report
        when {
            unsupported -> item {
                EmptyState(Icons.Rounded.Lan, "Indisponible sur cet agent", "Mets l'agent à jour (0.6.0 ou plus) pour voir les ports ouverts.")
            }
            r == null && error != null -> item {
                EmptyState(Icons.Rounded.Cloud, "Ports indisponibles", error.orEmpty()) {
                    OutlinedButton(onClick = { vm.load(false) }, shapes = ButtonDefaults.shapes()) { Text("Réessayer") }
                }
            }
            r == null -> item {
                Box(Modifier.fillMaxWidth().padding(48.dp), contentAlignment = Alignment.Center) { LoadingIndicator() }
            }
            else -> {
                item { PortsSummary(r, Modifier.cascade(0)) }
                item {
                    ReachCard(r, vm.reach, vm.probing, Modifier.cascade(1)) {
                        haptics.tick()
                        vm.probe(serverUrl)
                    }
                }
                val public = r.public
                item { SectionTitle("Sur Internet", Modifier.padding(start = 8.dp)) }
                if (public.isEmpty()) {
                    item {
                        Text(
                            "Aucun port ouvert sur toutes les interfaces.",
                            style = MaterialTheme.typography.bodyMedium,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.padding(horizontal = 8.dp),
                        )
                    }
                }
                itemsIndexed(public, key = { _, p -> "p" + p.key }) { i, p ->
                    PortRow(
                        p, vm.reach, saving = p.key in vm.saving,
                        modifier = Modifier.animateItem().cascade(i + 2),
                    ) { allowed ->
                        haptics.toggle(allowed)
                        scope.launch {
                            vm.setAllowed(p, allowed).onFailure {
                                haptics.reject()
                                snackbar.showSnackbar("Échec : ${it.message}")
                            }
                        }
                    }
                }
                val local = r.local
                if (local.isNotEmpty()) {
                    item {
                        Column(Modifier.padding(horizontal = 8.dp)) {
                            SectionTitle("Sur le serveur seulement")
                            Text(
                                "Boucle locale ou réseaux Docker : pas joignables depuis Internet.",
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                    }
                    itemsIndexed(local, key = { _, p -> "l" + p.key }) { i, p ->
                        PortRow(p, null, saving = false, modifier = Modifier.animateItem().cascade(i + public.size + 2), onAllow = null)
                    }
                }
                item {
                    val s = settings
                    SectionCard(Modifier.cascade(public.size + local.size + 3), title = "Alerte") {
                        ToggleRow(
                            Icons.Rounded.NotificationsActive, "Nouveau port sur Internet",
                            "Prévenu quand un port s'ouvre hors Pangolin sans être autorisé",
                            s?.portAlert ?: true, enabled = s != null,
                        ) { on ->
                            haptics.toggle(on)
                            scope.launch {
                                vm.setAlert(on).onFailure {
                                    haptics.reject()
                                    snackbar.showSnackbar("Échec : ${it.message}")
                                }
                            }
                        }
                    }
                }
                item { ExplainCard(Modifier.cascade(public.size + local.size + 4)) }
            }
        }
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun PortsSummary(r: PortsReport, modifier: Modifier) {
    val status = Vigie.status
    val public = r.public
    val bypass = public.count { it.bypassesPangolin && !it.allowed }
    val review = public.count { it.needsReview }
    SectionCard(modifier) {
        Row(verticalAlignment = Alignment.Bottom) {
            Text("${public.size}", style = MaterialTheme.typography.displaySmall.numeric())
            Spacer(Modifier.width(8.dp))
            Text(
                if (public.size > 1) "ports ouverts sur Internet" else "port ouvert sur Internet",
                style = MaterialTheme.typography.titleMedium,
                modifier = Modifier.padding(bottom = 6.dp),
            )
        }
        Spacer(Modifier.height(8.dp))
        FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            when {
                review == 0 -> Chip("Tout est attendu", status.okContainer, status.onOkContainer)
                else -> Chip(if (review > 1) "$review à vérifier" else "1 à vérifier", status.warnContainer, status.onWarnContainer)
            }
            if (bypass > 0) {
                Chip(
                    if (bypass > 1) "$bypass contournent Pangolin" else "1 contourne Pangolin",
                    status.criticalContainer, status.onCriticalContainer,
                )
            }
            if (r.pangolin.isNotEmpty()) {
                Chip("Pangolin : ${r.pangolin.joinToString(", ")}", MaterialTheme.colorScheme.secondaryContainer, MaterialTheme.colorScheme.onSecondaryContainer)
            }
        }
        Spacer(Modifier.height(8.dp))
        val note = buildList {
            if (r.hostIp.isNotEmpty()) add("IP de l'hôte : ${r.hostIp}")
            if (r.ts > 0) add("analysé ${Fmt.ago(r.ts)}")
        }.joinToString(" · ")
        if (note.isNotEmpty()) {
            Text(note, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        if (r.error.isNotEmpty()) {
            Spacer(Modifier.height(4.dp))
            Text(r.error, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
        }
    }
}

@Composable
private fun ReachCard(r: PortsReport, reach: ReachReport?, probing: Boolean, modifier: Modifier, onProbe: () -> Unit) {
    val tcp = r.public.count { it.isTcp }
    val udp = r.public.count { !it.isTcp }
    SectionCard(modifier, title = "Tester depuis Internet") {
        Text(
            "Ton téléphone essaie de se connecter à chaque port TCP public du serveur (3 s par port). " +
                "Passe en données mobiles si ton Wi-Fi bloque certains ports.",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(12.dp))
        FilledTonalButton(
            onClick = onProbe,
            enabled = !probing && tcp > 0,
            shapes = ButtonDefaults.shapes(),
            modifier = Modifier.fillMaxWidth().height(52.dp),
        ) {
            if (probing) LoadingIndicator(Modifier.size(24.dp)) else Icon(Icons.Rounded.NetworkCheck, null)
            Spacer(Modifier.width(8.dp))
            Text(if (probing) "Test en cours…" else "Tester $tcp port(s) TCP")
        }
        AnimatedVisibility(reach != null) {
            reach?.let { rr ->
                Column(Modifier.padding(top = 12.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    if (rr.error != null) {
                        Text(rr.error, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.error)
                    } else {
                        val open = rr.results.count { it.value }
                        Text(
                            "$open joignable(s), ${rr.results.size - open} fermé(s) ou filtré(s)",
                            style = MaterialTheme.typography.titleSmall,
                        )
                        Text(
                            "Adresse testée : ${rr.ip} (${rr.host}). Si ce n'est pas l'IP publique de ton VPS " +
                                "(proxy Cloudflare…), le résultat ne vaut pas pour lui.",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                }
            }
        }
        if (udp > 0) {
            Text(
                "UDP ($udp port(s)) : non testable ainsi, un port UDP ne répond pas à une simple connexion.",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(top = 8.dp),
            )
        }
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun PortRow(p: OpenPort, reach: ReachReport?, saving: Boolean, modifier: Modifier, onAllow: ((Boolean) -> Unit)?) {
    val status = Vigie.status
    val scheme = MaterialTheme.colorScheme
    Surface(shape = RoundedCornerShape(24.dp), color = scheme.surfaceContainer, modifier = modifier.fillMaxWidth()) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Icon(
                    if (p.isPublic) Icons.Rounded.Public else Icons.Rounded.Lan, null,
                    tint = if (p.needsReview) status.warn else scheme.onSurfaceVariant,
                )
                Spacer(Modifier.width(12.dp))
                Column(Modifier.weight(1f)) {
                    Text(p.key, style = MaterialTheme.typography.titleMedium.numeric())
                    Text(ownerLine(p), style = MaterialTheme.typography.bodySmall, color = scheme.onSurfaceVariant)
                }
                val reachable = reach?.results?.get(p.port)
                if (p.isTcp && reachable != null) {
                    when {
                        !reachable -> Chip("Fermé ou filtré", scheme.surfaceContainerHighest, scheme.onSurfaceVariant)
                        p.needsReview -> Chip("Joignable", status.criticalContainer, status.onCriticalContainer)
                        else -> Chip("Joignable", status.okContainer, status.onOkContainer)
                    }
                }
            }
            Text(
                p.addresses.ifEmpty { listOf(p.address) }.joinToString("  ") + if (p.iface.isNotEmpty()) "  ·  ${p.iface}" else "",
                style = MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace),
                color = scheme.onSurfaceVariant,
            )
            val badges = p.pangolin || p.allowed || p.bypassesPangolin || p.alerted || p.hostNetwork
            if (badges) {
                FlowRow(horizontalArrangement = Arrangement.spacedBy(6.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                    if (p.pangolin) Chip("Pangolin", status.okContainer, status.onOkContainer)
                    if (p.allowed) Chip("Autorisé", scheme.secondaryContainer, scheme.onSecondaryContainer)
                    if (p.bypassesPangolin) Chip("Contourne Pangolin", status.warnContainer, status.onWarnContainer)
                    if (p.alerted) Chip("Signalé", status.criticalContainer, status.onCriticalContainer)
                    if (p.hostNetwork) Chip("Réseau hôte", scheme.surfaceContainerHighest, scheme.onSurfaceVariant)
                }
            }
            if (onAllow != null && p.isPublic && !p.pangolin) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Column(Modifier.weight(1f)) {
                        Text("Autorisé", style = MaterialTheme.typography.labelLarge)
                        Text(
                            if (p.allowed) "Voulu : pas d'alerte pour ce port" else "Marque-le s'il doit rester ouvert",
                            style = MaterialTheme.typography.bodySmall,
                            color = scheme.onSurfaceVariant,
                        )
                    }
                    if (saving) LoadingIndicator(Modifier.size(24.dp).padding(end = 8.dp))
                    Switch(checked = p.allowed, onCheckedChange = onAllow, enabled = !saving)
                }
            }
        }
    }
}

/** « comet · port 8000 du conteneur · depuis 3 j ». */
private fun ownerLine(p: OpenPort): String = buildList {
    add(p.owner)
    if (p.container.isNotEmpty() && p.targetPort > 0 && p.targetPort != p.port && !p.hostNetwork) add("port ${p.targetPort} du conteneur")
    if (p.since > 0) add("depuis ${Fmt.since(p.since)}")
}.joinToString(" · ")

@Composable
private fun ExplainCard(modifier: Modifier) {
    SectionCard(modifier, title = "Bon à savoir") {
        val style = MaterialTheme.typography.bodySmall
        val color = MaterialTheme.colorScheme.onSurfaceVariant
        Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Text(
                "« Sur Internet » veut dire que le service écoute sur toutes les interfaces. Chez Oracle, la liste de " +
                    "sécurité du réseau (pare-feu du cloud) filtre en amont : seuls les ports que tu y as ouverts passent. " +
                    "Le test depuis Internet le vérifie.",
                style = style, color = color,
            )
            Text(
                "Docker contourne UFW : un port publié avec « ports: » est ouvert par Docker avant le pare-feu de l'hôte. " +
                    "Pour qu'un service ne passe que par Pangolin, remplace « ports: » par « expose: » (Pangolin le joint par " +
                    "le réseau Docker), ou publie-le sur 127.0.0.1 (« 127.0.0.1:8080:8080 »).",
                style = style, color = color,
            )
            Text(
                "Pangolin (gerbil, traefik) a besoin de 80, 443 et 51820/udp : ces ports sont attendus.",
                style = style, color = color,
            )
        }
    }
}

/** Sous-titre de la ligne « Ports ouverts » de l'écran Sécurité. */
@Composable
internal fun portsSubtitle(default: String): String {
    var text by remember { mutableStateOf(default) }
    LaunchedEffect(Unit) {
        runCatching { appContainer.repository.ports() }.onSuccess { r -> text = portsSummaryText(r) }
    }
    return text
}

internal fun portsSummaryText(r: PortsReport): String {
    val public = r.public
    if (public.isEmpty()) return "Aucun port ouvert sur Internet"
    val parts = mutableListOf(if (public.size > 1) "${public.size} ports sur Internet" else "1 port sur Internet")
    val bypass = public.count { it.bypassesPangolin && !it.allowed }
    val review = public.count { it.needsReview }
    when {
        bypass > 0 -> parts += if (bypass > 1) "$bypass contournent Pangolin" else "1 contourne Pangolin"
        review > 0 -> parts += if (review > 1) "$review à vérifier" else "1 à vérifier"
    }
    return parts.joinToString(", ")
}
