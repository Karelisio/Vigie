package com.karelisio.vigie.ui.containers

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.expandVertically
import androidx.compose.animation.fadeIn
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
import androidx.compose.material.icons.automirrored.rounded.Notes
import androidx.compose.material.icons.rounded.HealthAndSafety
import androidx.compose.material.icons.rounded.Notifications
import androidx.compose.material.icons.rounded.NotificationsOff
import androidx.compose.material.icons.rounded.Star
import androidx.compose.material.icons.rounded.StarBorder
import androidx.compose.material.icons.rounded.NewReleases
import androidx.compose.material.icons.rounded.PlayArrow
import androidx.compose.material.icons.rounded.RestartAlt
import androidx.compose.material.icons.rounded.Stop
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialShapes
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.SegmentedButton
import androidx.compose.material3.SegmentedButtonDefaults
import androidx.compose.material3.SingleChoiceSegmentedButtonRow
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.toShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.NavHostController
import com.karelisio.vigie.appContainer
import com.karelisio.vigie.data.Container
import com.karelisio.vigie.data.ContainerDetail
import com.karelisio.vigie.data.CtrPoint
import com.karelisio.vigie.data.Fmt
import com.karelisio.vigie.data.History
import com.karelisio.vigie.data.TimelineEvent
import com.karelisio.vigie.security.LocalActionGuard
import com.karelisio.vigie.ui.components.ActionTone
import com.karelisio.vigie.ui.components.ChartPoint
import com.karelisio.vigie.ui.components.Chip
import com.karelisio.vigie.ui.components.MetricChart
import com.karelisio.vigie.ui.components.MorphActionButton
import com.karelisio.vigie.ui.components.SectionCard
import com.karelisio.vigie.ui.components.StatusDot
import com.karelisio.vigie.ui.components.cascade
import com.karelisio.vigie.ui.components.rememberActionRunner
import com.karelisio.vigie.ui.components.rememberHaptics
import com.karelisio.vigie.ui.components.visualStatus
import com.karelisio.vigie.ui.navigation.ContainerRoute
import com.karelisio.vigie.ui.navigation.LocalSnackbar
import com.karelisio.vigie.ui.navigation.LogsRoute
import com.karelisio.vigie.ui.navigation.sharedBoundsOrSelf
import com.karelisio.vigie.ui.navigation.sharedElementOrSelf
import com.karelisio.vigie.ui.theme.Vigie
import com.karelisio.vigie.ui.theme.numeric
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

class ContainerDetailViewModel : ViewModel() {
    private val repo = appContainer.repository
    val live = repo.live
    private val _detail = MutableStateFlow<ContainerDetail?>(null)
    val detail = _detail.asStateFlow()
    private val _history = MutableStateFlow<History<CtrPoint>?>(null)
    val history = _history.asStateFlow()
    private val _error = MutableStateFlow<String?>(null)
    val error = _error.asStateFlow()
    private val _events = MutableStateFlow<List<TimelineEvent>?>(null)
    val events = _events.asStateFlow()
    var ref: String = ""
        private set

    fun init(ref: String) {
        if (this.ref == ref) return
        this.ref = ref
        reload()
        loadHistory("24h")
    }

    fun reload() {
        viewModelScope.launch {
            runCatching { repo.container(ref) }
                .onSuccess { _detail.value = it; _error.value = null }
                .onFailure { _error.value = it.message }
        }
    }

    fun loadEvents() {
        viewModelScope.launch { runCatching { repo.events(ref) }.onSuccess { _events.value = it } }
    }

    suspend fun setMuted(id: String, muted: Boolean) {
        repo.mute(id, muted)
    }

    suspend fun update(id: String, pullOnly: Boolean) {
        repo.updateContainer(id, pullOnly)
    }

    suspend fun stack(project: String, action: String) {
        repo.stackAction(project, action)
    }

    fun toggleFavorite(name: String, done: (Boolean) -> Unit) {
        viewModelScope.launch {
            val server = appContainer.servers.active ?: return@launch
            done(appContainer.prefs.toggleFavorite(server.id, name))
        }
    }

    fun loadHistory(range: String) {
        viewModelScope.launch { runCatching { repo.containerHistory(ref, range) }.onSuccess { _history.value = it } }
    }

    suspend fun action(id: String, action: String) {
        repo.action(id, action)
        reload()
        loadEvents()
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
fun ContainerDetailScreen(nav: NavHostController, route: ContainerRoute, vm: ContainerDetailViewModel = viewModel()) {
    LaunchedEffect(route.id) { vm.init(route.id) }
    val state by vm.live.collectAsStateWithLifecycle()
    val detail by vm.detail.collectAsStateWithLifecycle()
    val history by vm.history.collectAsStateWithLifecycle()
    val error by vm.error.collectAsStateWithLifecycle()
    val c: Container? = state.containers.firstOrNull { it.id == route.id || it.name == route.id || it.name == route.name }
        ?: detail?.container
    val id = c?.id ?: route.id
    val events by vm.events.collectAsStateWithLifecycle()
    val prefs by appContainer.prefs.flow.collectAsStateWithLifecycle(initialValue = null)
    val activeId by appContainer.servers.activeId.collectAsStateWithLifecycle()
    val favorite = c != null && c.name in prefs?.favorites?.get(activeId).orEmpty()
    val snackbar = LocalSnackbar.current
    val scope = rememberCoroutineScope()
    val haptics = rememberHaptics()
    // Recharge la chronologie à chaque changement d'état (démarrage, arrêt, santé).
    LaunchedEffect(c?.state, c?.startedAt, c?.health) { if (c != null) vm.loadEvents() }

    Column(
        Modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .navigationBarsPadding()
            .padding(bottom = 24.dp),
    ) {
        Header(c, route, onBack = { nav.popBackStack() }) {
            if (c != null) {
                androidx.compose.material3.IconToggleButton(
                    checked = favorite,
                    onCheckedChange = {
                        haptics.toggle(it)
                        vm.toggleFavorite(c.name) { on ->
                            scope.launch {
                                snackbar.currentSnackbarData?.dismiss()
                                snackbar.showSnackbar(if (on) "Épinglé en haut de la liste et dans le widget" else "Désépinglé")
                            }
                        }
                    },
                ) {
                    Icon(if (favorite) Icons.Rounded.Star else Icons.Rounded.StarBorder, if (favorite) "Désépingler" else "Épingler")
                }
                androidx.compose.material3.IconToggleButton(
                    checked = c.muted,
                    onCheckedChange = { mute ->
                        haptics.toggle(!mute)
                        scope.launch {
                            val r = runCatching { vm.setMuted(id, mute) }
                            snackbar.currentSnackbarData?.dismiss()
                            snackbar.showSnackbar(
                                r.fold(
                                    { if (mute) "Plus de notification pour ${c.name}" else "Notifications réactivées pour ${c.name}" },
                                    { "Échec : ${it.message}" },
                                ),
                            )
                        }
                    },
                ) {
                    Icon(
                        if (c.muted) Icons.Rounded.NotificationsOff else Icons.Rounded.Notifications,
                        if (c.muted) "Réactiver les notifications" else "Couper les notifications",
                    )
                }
            }
        }
        if (c == null) {
            Text(
                error ?: "Chargement…",
                Modifier.padding(24.dp),
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            return@Column
        }
        // Accès partagé (lecture seule) : ni actions, ni logs.
        if (!state.readOnly) {
            Spacer(Modifier.height(16.dp))
            Actions(c, onAction = { a -> vm.action(id, a) })
            Spacer(Modifier.height(16.dp))
            Button(
                onClick = { nav.navigate(LogsRoute(id, c.name)) },
                modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp).height(56.dp).cascade(0),
                shapes = ButtonDefaults.shapes(),
            ) {
                Icon(Icons.AutoMirrored.Rounded.Notes, null)
                Spacer(Modifier.width(8.dp))
                Text("Logs en direct")
            }
        }
        if (c.project.isNotEmpty()) {
            Spacer(Modifier.height(8.dp))
            com.karelisio.vigie.ui.logs.StackLogsButton(c.project, Modifier.padding(horizontal = 16.dp).cascade(0)) {
                nav.navigate(com.karelisio.vigie.ui.navigation.StackLogsRoute(c.project))
            }
        }
        Spacer(Modifier.height(12.dp))
        if (c.running) StatsCard(c, Modifier.padding(horizontal = 16.dp).cascade(1))
        Spacer(Modifier.height(12.dp))
        val guard = LocalActionGuard.current
        val now = com.karelisio.vigie.ui.components.rememberNow(5_000)
        val job = state.jobs.firstOrNull { it.concerns(c) && (!it.done || now - it.finishedAt < 10 * 60_000) }
        fun runJob(label: String?, block: suspend () -> Unit) {
            scope.launch {
                if (label != null && !guard(label)) return@launch
                runCatching { block() }.onFailure {
                    haptics.reject()
                    snackbar.showSnackbar("Échec : ${it.message}")
                }
            }
        }
        if (!state.readOnly) UpdateCard(
            c = c,
            enabled = state.updatesEnabled,
            busy = state.runningJob != null,
            job = job,
            onUpdate = { runJob("Mettre à jour ${c.name}") { vm.update(id, false) } },
            onPull = { runJob(null) { vm.update(id, true) } },
            onStack = { runJob("Mettre à jour la stack ${c.project}") { vm.stack(c.project, "update") } },
            modifier = Modifier.padding(horizontal = 16.dp).cascade(2),
            agentVersion = state.agentVersion,
        )
        Spacer(Modifier.height(12.dp))
        Charts(history, vm::loadHistory, Modifier.padding(horizontal = 16.dp).cascade(2))
        Spacer(Modifier.height(12.dp))
        TimelineCard(events, Modifier.padding(horizontal = 16.dp).cascade(3))
        Spacer(Modifier.height(12.dp))
        detail?.health?.takeIf { it.status.isNotEmpty() }?.let { h ->
            SectionCard(Modifier.padding(horizontal = 16.dp).cascade(3), title = "Healthcheck") {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Icon(Icons.Rounded.HealthAndSafety, null, tint = Vigie.status.of(h.status))
                    Spacer(Modifier.width(8.dp))
                    Text(
                        when (h.status) {
                            "healthy" -> "En bonne santé"
                            "unhealthy" -> "En échec (${h.failingStreak} fois de suite)"
                            "starting" -> "Démarrage"
                            else -> h.status
                        },
                        style = MaterialTheme.typography.bodyLarge,
                    )
                }
                h.lastOutput?.takeIf { it.isNotBlank() }?.let {
                    Spacer(Modifier.height(8.dp))
                    Surface(color = MaterialTheme.colorScheme.surfaceContainerHighest, shape = RoundedCornerShape(12.dp)) {
                        Text(it.take(600), Modifier.padding(12.dp), fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodySmall)
                    }
                }
            }
            Spacer(Modifier.height(12.dp))
        }
        SectionCard(Modifier.padding(horizontal = 16.dp).cascade(4), title = "Détails") {
            Info("Image", c.image)
            if (c.updateAvailable) {
                Row(Modifier.padding(vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                    Icon(Icons.Rounded.NewReleases, null, Modifier.size(18.dp), tint = MaterialTheme.colorScheme.tertiary)
                    Spacer(Modifier.width(6.dp))
                    Text(
                        "Nouvelle version de l'image publiée : redéploie la stack dans Dockhand (Pull + Redeploy).",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.tertiary,
                    )
                }
            }
            if (c.muted) Info("Notifications", "Coupées pour ce conteneur")
            if (c.project.isNotEmpty()) Info("Projet compose", c.project + if (c.service.isNotEmpty()) " / ${c.service}" else "")
            Info("Redémarrage auto", when (c.restartPolicy) {
                "always" -> "Toujours"
                "unless-stopped" -> "Sauf arrêt manuel"
                "on-failure" -> "En cas d'échec"
                "no", "" -> "Jamais"
                else -> c.restartPolicy
            })
            Info("Redémarrages", c.restartCount.toString())
            if (c.created > 0) Info("Créé", Fmt.dateTime(c.created))
            if (c.running && c.startedAt > 0) Info("Démarré", Fmt.dateTime(c.startedAt))
            if (!c.running && c.finishedAt > 0) Info("Arrêté", "${Fmt.dateTime(c.finishedAt)} (code ${c.exitCode})")
            detail?.error?.takeIf { it.isNotEmpty() }?.let { Info("Erreur", it) }
            if (c.ports.isNotEmpty()) {
                Spacer(Modifier.height(8.dp))
                Text("Ports", style = MaterialTheme.typography.labelLarge)
                Spacer(Modifier.height(4.dp))
                FlowRow(horizontalArrangement = Arrangement.spacedBy(6.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                    c.ports.forEach { Chip(it, MaterialTheme.colorScheme.secondaryContainer, MaterialTheme.colorScheme.onSecondaryContainer) }
                }
            }
            detail?.networks?.takeIf { it.isNotEmpty() }?.let { nets ->
                Spacer(Modifier.height(8.dp))
                Text("Réseaux", style = MaterialTheme.typography.labelLarge)
                Spacer(Modifier.height(4.dp))
                FlowRow(horizontalArrangement = Arrangement.spacedBy(6.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                    nets.forEach { (n, ip) ->
                        Chip(if (ip.isNotEmpty()) "$n · $ip" else n, MaterialTheme.colorScheme.tertiaryContainer, MaterialTheme.colorScheme.onTertiaryContainer)
                    }
                }
            }
            detail?.mounts?.takeIf { it.isNotEmpty() }?.let { mounts ->
                Spacer(Modifier.height(8.dp))
                Text("Volumes", style = MaterialTheme.typography.labelLarge)
                mounts.forEach { m ->
                    Text(
                        "${m.source.ifEmpty { m.type }} → ${m.destination}${if (!m.rw) " (lecture seule)" else ""}",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 2,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
            }
        }
    }
}

@Composable
internal fun Header(
    c: Container?,
    route: ContainerRoute,
    onBack: () -> Unit,
    actions: @Composable androidx.compose.foundation.layout.RowScope.() -> Unit = {},
) {
    val status = Vigie.status
    val vs = c?.visualStatus() ?: "neutral"
    val color by animateColorAsState(status.of(vs), label = "hdr")
    val spin by animateFloatAsState(if (c?.running == true) 0f else 45f, label = "spin")
    Surface(
        color = MaterialTheme.colorScheme.surfaceContainer,
        shape = RoundedCornerShape(bottomStart = 36.dp, bottomEnd = 36.dp),
        modifier = Modifier.fillMaxWidth().sharedBoundsOrSelf("card-${c?.id ?: route.id}"),
    ) {
        Column(Modifier.padding(bottom = 24.dp)) {
            Row(Modifier.statusBarsPadding().padding(top = 4.dp, start = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                androidx.compose.material3.IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Rounded.ArrowBack, "Retour") }
                Spacer(Modifier.weight(1f))
                actions()
                Spacer(Modifier.width(4.dp))
            }
            Row(Modifier.padding(horizontal = 20.dp), verticalAlignment = Alignment.CenterVertically) {
                Box(contentAlignment = Alignment.Center) {
                    Surface(
                        shape = MaterialShapes.Cookie9Sided.toShape(),
                        color = color.copy(alpha = 0.18f),
                        modifier = Modifier.size(72.dp).graphicsLayer { rotationZ = spin },
                    ) {}
                    StatusDot(vs, Modifier.sharedElementOrSelf("dot-${c?.id ?: route.id}"), size = 18.dp)
                }
                Spacer(Modifier.width(16.dp))
                Column(Modifier.weight(1f)) {
                    Text(
                        c?.name ?: route.name,
                        style = MaterialTheme.typography.headlineSmallEmphasized,
                        maxLines = 2,
                        overflow = TextOverflow.Ellipsis,
                        modifier = Modifier.sharedBoundsOrSelf("name-${c?.id ?: route.id}", text = true),
                    )
                    if (c != null) {
                        Text(Fmt.stateLabel(c), style = MaterialTheme.typography.titleSmall, color = color)
                        if (c.running && c.startedAt > 0) {
                            Text(
                                "En marche depuis ${Fmt.since(c.startedAt)}",
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                    }
                }
            }
        }
    }
}

@Composable
internal fun Actions(c: Container, onAction: suspend (String) -> Unit) {
    val start = rememberActionRunner()
    val stop = rememberActionRunner()
    val restart = rememberActionRunner()
    var confirm by remember { mutableStateOf(false) }
    val snackbar = LocalSnackbar.current
    val scope = rememberCoroutineScope()
    val haptics = rememberHaptics()
    val guard = LocalActionGuard.current

    fun launch(runner: com.karelisio.vigie.ui.components.ActionRunner, action: String) {
        val label = mapOf("start" to "Démarrer", "stop" to "Arrêter", "restart" to "Redémarrer")[action]
        scope.launch {
            if (!guard("$label ${c.name}")) return@launch
            runner.run({ onAction(action) }) { r ->
                r.exceptionOrNull()?.let { scope.launch { snackbar.showSnackbar("Échec : ${it.message}") } }
            }
        }
    }

    Row(
        Modifier.fillMaxWidth().padding(horizontal = 16.dp),
        horizontalArrangement = Arrangement.spacedBy(10.dp, Alignment.CenterHorizontally),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        if (c.running) {
            MorphActionButton("Redémarrer", Icons.Rounded.RestartAlt, restart, { launch(restart, "restart") }, tone = ActionTone.Primary)
            if (!c.self) {
                MorphActionButton("Arrêter", Icons.Rounded.Stop, stop, { haptics.tick(); confirm = true }, tone = ActionTone.Danger)
            }
        } else {
            MorphActionButton("Démarrer", Icons.Rounded.PlayArrow, start, { launch(start, "start") }, tone = ActionTone.Primary)
        }
    }
    AnimatedVisibility(c.self, enter = fadeIn() + expandVertically()) {
        Text(
            "C'est l'agent Vigie : il peut redémarrer mais pas s'arrêter.",
            Modifier.fillMaxWidth().padding(horizontal = 24.dp, vertical = 8.dp),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
    if (confirm) {
        AlertDialog(
            onDismissRequest = { confirm = false },
            icon = { Icon(Icons.Rounded.Stop, null) },
            title = { Text("Arrêter ${c.name} ?") },
            text = { Text("Le conteneur restera arrêté jusqu'à ce que tu le redémarres.") },
            confirmButton = { TextButton(onClick = { confirm = false; launch(stop, "stop") }) { Text("Arrêter") } },
            dismissButton = { TextButton(onClick = { confirm = false }) { Text("Annuler") } },
        )
    }
}

@Composable
internal fun StatsCard(c: Container, modifier: Modifier) {
    SectionCard(modifier, title = "En ce moment") {
        Row(Modifier.fillMaxWidth()) {
            Stat("CPU", Fmt.pct(c.cpu, if (c.cpu < 10) 2 else 1), Modifier.weight(1f))
            Stat("Mémoire", Fmt.bytes(c.memUsed), Modifier.weight(1f))
            Stat("Processus", c.pids.toString(), Modifier.weight(1f))
        }
        Spacer(Modifier.height(12.dp))
        Row(Modifier.fillMaxWidth()) {
            Stat("Réception", Fmt.rate(c.netRx), Modifier.weight(1f))
            Stat("Émission", Fmt.rate(c.netTx), Modifier.weight(1f))
            Stat("Limite RAM", if (c.memLimit > 0) Fmt.bytes(c.memLimit) else "—", Modifier.weight(1f))
        }
    }
}

@Composable
private fun Stat(label: String, value: String, modifier: Modifier) {
    Column(modifier) {
        Text(label, style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
        Text(value, style = MaterialTheme.typography.titleMedium.numeric())
    }
}

@Composable
private fun Charts(history: History<CtrPoint>?, onRange: (String) -> Unit, modifier: Modifier) {
    var range by rememberSaveable { mutableStateOf("24h") }
    val haptics = rememberHaptics()
    val scheme = MaterialTheme.colorScheme
    SectionCard(modifier, title = "Historique") {
        SingleChoiceSegmentedButtonRow(Modifier.fillMaxWidth()) {
            listOf("24h" to "24 h", "7d" to "7 jours").forEachIndexed { i, (key, label) ->
                SegmentedButton(
                    selected = range == key,
                    onClick = { haptics.tick(); range = key; onRange(key) },
                    shape = SegmentedButtonDefaults.itemShape(i, 2),
                ) { Text(label) }
            }
        }
        val pts = history?.points.orEmpty()
        val span = if (range == "7d") 7 * 86_400_000L else 86_400_000L
        Spacer(Modifier.height(12.dp))
        if (pts.size < 2) {
            Text(
                if (history == null) "Chargement…" else "Pas encore assez de données (un point par minute quand le conteneur tourne).",
                style = MaterialTheme.typography.bodyMedium,
                color = scheme.onSurfaceVariant,
            )
        } else {
            Text("CPU", style = MaterialTheme.typography.labelLarge, color = scheme.onSurfaceVariant)
            val maxCpu = (pts.maxOf { it.cpu } * 1.3).coerceAtLeast(1.0)
            MetricChart(pts.map { ChartPoint(it.ts, it.cpu) }, scheme.primary, span, maxY = maxCpu, height = 150.dp) {
                if (it < 10) String.format(java.util.Locale.FRANCE, "%.1f %%", it) else "${it.toInt()} %"
            }
            Spacer(Modifier.height(12.dp))
            Text("Mémoire", style = MaterialTheme.typography.labelLarge, color = scheme.onSurfaceVariant)
            MetricChart(pts.map { ChartPoint(it.ts, it.memBytes.toDouble()) }, scheme.tertiary, span, height = 150.dp) { Fmt.bytes(it) }
        }
    }
}

@Composable
private fun Info(label: String, value: String) {
    Row(Modifier.fillMaxWidth().padding(vertical = 4.dp)) {
        Text(label, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.weight(0.42f))
        Text(value, style = MaterialTheme.typography.bodyMedium, modifier = Modifier.weight(0.58f))
    }
}
