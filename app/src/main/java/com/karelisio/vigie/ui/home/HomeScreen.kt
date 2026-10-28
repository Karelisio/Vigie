package com.karelisio.vigie.ui.home

import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.animateContentSize
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInVertically
import androidx.compose.animation.slideOutVertically
import androidx.compose.animation.togetherWith
import androidx.compose.foundation.clickable
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
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowForward
import androidx.compose.material.icons.rounded.ArrowDownward
import androidx.compose.material.icons.rounded.ArrowUpward
import androidx.compose.material.icons.rounded.Dns
import androidx.compose.material.icons.rounded.ErrorOutline
import androidx.compose.material.icons.rounded.ExpandMore
import androidx.compose.material.icons.rounded.Language
import androidx.compose.material.icons.rounded.Memory
import androidx.compose.material.icons.rounded.Speed
import androidx.compose.material.icons.rounded.Thermostat
import androidx.compose.material.icons.rounded.Timer
import androidx.compose.material.icons.rounded.WarningAmber
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.SegmentedButton
import androidx.compose.material3.SegmentedButtonDefaults
import androidx.compose.material3.SingleChoiceSegmentedButtonRow
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.NavHostController
import com.karelisio.vigie.appContainer
import com.karelisio.vigie.data.Connection
import com.karelisio.vigie.data.Fmt
import com.karelisio.vigie.data.History
import com.karelisio.vigie.data.Issue
import com.karelisio.vigie.data.LiveState
import com.karelisio.vigie.data.SysPoint
import com.karelisio.vigie.ui.components.ChartPoint
import com.karelisio.vigie.ui.components.CircularGauge
import com.karelisio.vigie.ui.components.ConnectionBanner
import com.karelisio.vigie.ui.components.MetricChart
import com.karelisio.vigie.ui.components.Radar
import com.karelisio.vigie.ui.components.RadarBlip
import com.karelisio.vigie.ui.components.RadarRefreshBox
import com.karelisio.vigie.ui.components.SectionCard
import com.karelisio.vigie.ui.components.Sparkline
import com.karelisio.vigie.ui.components.StatusDot
import com.karelisio.vigie.ui.components.cascade
import com.karelisio.vigie.ui.components.rememberHaptics
import com.karelisio.vigie.ui.components.shakeOnAppear
import com.karelisio.vigie.ui.components.visualStatus
import com.karelisio.vigie.ui.navigation.ChecksRoute
import com.karelisio.vigie.ui.navigation.ContainerRoute
import com.karelisio.vigie.ui.navigation.ContainersRoute
import com.karelisio.vigie.ui.navigation.ServerEditRoute
import com.karelisio.vigie.ui.navigation.navigateTab
import com.karelisio.vigie.ui.theme.Vigie
import com.karelisio.vigie.ui.theme.numeric
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

class HomeViewModel : ViewModel() {
    private val repo = appContainer.repository
    val live = repo.live
    val servers = appContainer.servers.servers
    val activeId = appContainer.servers.activeId

    private val _history = MutableStateFlow<History<SysPoint>?>(null)
    val history = _history.asStateFlow()
    private val _refreshing = MutableStateFlow(false)
    val refreshing = _refreshing.asStateFlow()
    var range = "24h"
        private set

    init {
        loadHistory("24h")
    }

    fun loadHistory(r: String) {
        range = r
        viewModelScope.launch { runCatching { repo.systemHistory(r) }.onSuccess { _history.value = it } }
    }

    fun refresh() {
        viewModelScope.launch {
            _refreshing.value = true
            repo.reconnect()
            runCatching { repo.systemHistory(range) }.onSuccess { _history.value = it }
            delay(900)
            _refreshing.value = false
        }
    }

    fun switchServer(id: String) {
        appContainer.servers.setActive(id)
        _history.value = null
        loadHistory(range)
    }
}

@Composable
fun HomeScreen(nav: NavHostController, vm: HomeViewModel = viewModel()) {
    val state by vm.live.collectAsStateWithLifecycle()
    val history by vm.history.collectAsStateWithLifecycle()
    val refreshing by vm.refreshing.collectAsStateWithLifecycle()
    val servers by vm.servers.collectAsStateWithLifecycle()
    val activeId by vm.activeId.collectAsStateWithLifecycle()
    val bottom = WindowInsets.navigationBars.asPaddingValues().calculateBottomPadding() + 96.dp

    RadarRefreshBox(isRefreshing = refreshing, onRefresh = vm::refresh, modifier = Modifier.fillMaxSize()) {
        LazyColumn(
            Modifier.fillMaxSize(),
            contentPadding = PaddingValues(bottom = bottom),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            item(key = "header") {
                Header(
                    name = servers.firstOrNull { it.id == activeId }?.name ?: "Vigie",
                    state = state,
                    servers = servers.map { it.id to it.name },
                    onSwitch = vm::switchServer,
                    onAdd = { nav.navigate(ServerEditRoute()) },
                )
            }
            item(key = "banner") { ConnectionBanner(state) }
            item(key = "radar") { RadarSection(state) }
            val issues = state.overview?.issues.orEmpty()
            items(issues, key = { "issue-${it.kind}-${it.target}" }) { issue ->
                IssueCard(
                    issue,
                    Modifier
                        .padding(horizontal = 16.dp)
                        .animateItem()
                        .shakeOnAppear(issue.severity == "critical", issue.target),
                ) {
                    when (issue.kind) {
                        "container" -> nav.navigate(ContainerRoute(issue.id ?: issue.target, issue.target))
                        "check" -> nav.navigateTab(ChecksRoute)
                        else -> Unit
                    }
                }
            }
            item(key = "gauges") { GaugesCard(state, Modifier.padding(horizontal = 16.dp).cascade(0)) }
            item(key = "network") { NetworkCard(state, Modifier.padding(horizontal = 16.dp).cascade(1)) }
            item(key = "facts") { FactsRow(state, Modifier.padding(horizontal = 16.dp).cascade(2)) }
            item(key = "history") {
                HistoryCard(history, vm.range, state, onRange = vm::loadHistory, modifier = Modifier.padding(horizontal = 16.dp).cascade(3))
            }
            item(key = "containers") {
                ContainersSummary(state, Modifier.padding(horizontal = 16.dp).cascade(4)) { nav.navigateTab(ContainersRoute) }
            }
        }
    }
}

@Composable
private fun Header(
    name: String,
    state: LiveState,
    servers: List<Pair<String, String>>,
    onSwitch: (String) -> Unit,
    onAdd: () -> Unit,
) {
    var menu by remember { mutableStateOf(false) }
    val haptics = rememberHaptics()
    Row(
        Modifier
            .fillMaxWidth()
            .statusBarsPadding()
            .padding(start = 20.dp, end = 16.dp, top = 12.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(Modifier.weight(1f)) {
            Row(
                Modifier
                    .clickable { haptics.tick(); menu = true }
                    .padding(vertical = 4.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Text(name, style = MaterialTheme.typography.headlineMediumEmphasized, maxLines = 1)
                Icon(Icons.Rounded.ExpandMore, "Changer de serveur", tint = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            DropdownMenu(menu, onDismissRequest = { menu = false }) {
                servers.forEach { (id, n) ->
                    DropdownMenuItem(text = { Text(n) }, leadingIcon = { Icon(Icons.Rounded.Dns, null) }, onClick = { menu = false; onSwitch(id) })
                }
                DropdownMenuItem(text = { Text("Ajouter un serveur…") }, onClick = { menu = false; onAdd() })
            }
        }
        LiveChip(state)
    }
}

@Composable
private fun LiveChip(state: LiveState) {
    val status = Vigie.status
    val (label, dot) = when (state.connection) {
        Connection.Live -> "En direct" to "ok"
        Connection.Connecting, Connection.Idle -> "Connexion" to "warning"
        Connection.Unauthorized -> "Token refusé" to "critical"
        Connection.Offline -> "Hors ligne" to "neutral"
    }
    Surface(shape = RoundedCornerShape(50), color = MaterialTheme.colorScheme.surfaceContainerHigh) {
        Row(Modifier.padding(start = 4.dp, end = 12.dp, top = 4.dp, bottom = 4.dp), verticalAlignment = Alignment.CenterVertically) {
            StatusDot(dot, size = 8.dp)
            AnimatedContent(label, label = "live") { Text(it, style = MaterialTheme.typography.labelLarge, color = if (dot == "critical") status.critical else Color.Unspecified) }
        }
    }
}

@Composable
private fun RadarSection(state: LiveState) {
    val ov = state.overview
    val status = when {
        ov == null -> "unknown"
        else -> ov.status
    }
    val blips = remember(state.containers, state.checks) {
        state.containers.filterNot { it.ignored }.take(40).map { RadarBlip(it.name, it.visualStatus()) } +
            state.checks.filter { it.enabled }.map { RadarBlip("check:" + it.name, if (it.state == "down") "critical" else "ok") }
    }
    val colors = Vigie.status
    val headline = when {
        ov == null && state.connection == Connection.Unauthorized -> "Token refusé"
        ov == null -> "Connexion…"
        status == "ok" -> "Tout va bien"
        status == "warning" -> "À surveiller"
        else -> ov.issues.count { it.severity == "critical" }.let { if (it <= 1) "1 problème" else "$it problèmes" }
    }
    Column(Modifier.fillMaxWidth().padding(top = 4.dp), horizontalAlignment = Alignment.CenterHorizontally) {
        Radar(
            status = status,
            blips = blips,
            active = state.connection == Connection.Live,
            modifier = Modifier.fillMaxWidth(0.78f),
        )
        Spacer(Modifier.height(16.dp))
        AnimatedContent(
            targetState = headline,
            transitionSpec = {
                (slideInVertically { it / 2 } + fadeIn()) togetherWith (slideOutVertically { -it / 2 } + fadeOut())
            },
            label = "headline",
        ) { h ->
            Text(
                h,
                style = MaterialTheme.typography.displaySmallEmphasized,
                color = when (status) {
                    "critical" -> colors.critical
                    "warning" -> colors.warn
                    "ok" -> colors.ok
                    else -> MaterialTheme.colorScheme.onSurface
                },
                textAlign = TextAlign.Center,
            )
        }
        if (ov != null) {
            val parts = buildList {
                add("${ov.containers.running}/${ov.containers.total} conteneurs actifs")
                if (ov.checks.total > 0) add("${ov.checks.up}/${ov.checks.total} services OK")
            }
            Text(
                parts.joinToString(" · "),
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

@Composable
private fun IssueCard(issue: Issue, modifier: Modifier, onClick: () -> Unit) {
    val colors = Vigie.status
    val crit = issue.severity == "critical"
    Surface(
        onClick = onClick,
        shape = RoundedCornerShape(24.dp),
        color = if (crit) colors.criticalContainer else colors.warnContainer,
        contentColor = if (crit) colors.onCriticalContainer else colors.onWarnContainer,
        modifier = modifier.fillMaxWidth(),
    ) {
        Row(Modifier.padding(16.dp), verticalAlignment = Alignment.CenterVertically) {
            Icon(if (crit) Icons.Rounded.ErrorOutline else Icons.Rounded.WarningAmber, null)
            Spacer(Modifier.width(12.dp))
            Column(Modifier.weight(1f)) {
                Text(issue.target, style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.Bold)
                Text(issue.message, style = MaterialTheme.typography.bodyMedium)
            }
            if (issue.kind == "container" || issue.kind == "check") Icon(Icons.AutoMirrored.Rounded.ArrowForward, null)
        }
    }
}

@Composable
private fun GaugesCard(state: LiveState, modifier: Modifier) {
    val s = state.system
    val t = state.thresholds
    SectionCard(modifier) {
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            CircularGauge(
                value = s?.cpu ?: 0.0, label = "CPU", threshold = t.cpu,
                caption = s?.numCpu?.takeIf { it > 0 }?.let { "$it cœurs" }, modifier = Modifier.weight(1f),
            )
            CircularGauge(
                value = s?.mem ?: 0.0, label = "RAM", threshold = t.ram,
                caption = s?.let { "${Fmt.bytes(it.memUsed)} / ${Fmt.bytes(it.memTotal)}" }, modifier = Modifier.weight(1f),
            )
            CircularGauge(
                value = s?.disk ?: 0.0, label = "Disque", threshold = t.disk,
                caption = s?.let { "${Fmt.bytes(it.diskUsed)} / ${Fmt.bytes(it.diskTotal)}" }, modifier = Modifier.weight(1f),
            )
        }
    }
}

@Composable
private fun NetworkCard(state: LiveState, modifier: Modifier) {
    val s = state.system
    val scheme = MaterialTheme.colorScheme
    val rx = state.recent.map { it.netRx.toFloat() }
    val tx = state.recent.map { it.netTx.toFloat() }
    val max = (rx + tx).maxOrNull() ?: 1f
    SectionCard(modifier, title = "Réseau") {
        Row(Modifier.fillMaxWidth()) {
            Rate(Icons.Rounded.ArrowDownward, "Réception", s?.netRx ?: 0.0, scheme.primary, Modifier.weight(1f))
            Rate(Icons.Rounded.ArrowUpward, "Émission", s?.netTx ?: 0.0, scheme.tertiary, Modifier.weight(1f))
        }
        Spacer(Modifier.height(12.dp))
        Box(Modifier.fillMaxWidth().height(64.dp)) {
            Sparkline(rx, scheme.primary, Modifier.fillMaxSize(), maxValue = max)
            Sparkline(tx, scheme.tertiary, Modifier.fillMaxSize(), maxValue = max)
        }
        if (s != null) {
            Spacer(Modifier.height(8.dp))
            Text(
                "Depuis le démarrage : ↓ ${Fmt.bytes(s.netRxTotal)} · ↑ ${Fmt.bytes(s.netTxTotal)}",
                style = MaterialTheme.typography.bodySmall,
                color = scheme.onSurfaceVariant,
            )
        }
    }
}

@Composable
private fun Rate(icon: ImageVector, label: String, value: Double, color: Color, modifier: Modifier) {
    Row(modifier, verticalAlignment = Alignment.CenterVertically) {
        Surface(shape = RoundedCornerShape(14.dp), color = color.copy(alpha = 0.16f), contentColor = color, modifier = Modifier.size(40.dp)) {
            Box(contentAlignment = Alignment.Center) { Icon(icon, null) }
        }
        Spacer(Modifier.width(10.dp))
        Column {
            Text(label, style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Text(Fmt.rate(value), style = MaterialTheme.typography.titleMedium.numeric())
        }
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun FactsRow(state: LiveState, modifier: Modifier) {
    val s = state.system ?: return
    FlowRow(modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Fact(Icons.Rounded.Speed, "Charge", String.format(java.util.Locale.FRANCE, "%.2f · %.2f · %.2f", s.load1, s.load5, s.load15))
        Fact(Icons.Rounded.Timer, "En ligne depuis", Fmt.duration(s.uptime))
        s.temp?.let { Fact(Icons.Rounded.Thermostat, "Température", String.format(java.util.Locale.FRANCE, "%.0f °C", it)) }
        if (s.swapTotal > 0) Fact(Icons.Rounded.Memory, "Swap", "${Fmt.bytes(s.swapUsed)} / ${Fmt.bytes(s.swapTotal)}")
        if (s.cpuSteal >= 1) Fact(Icons.Rounded.Language, "Steal CPU", Fmt.pct(s.cpuSteal, 1))
    }
}

@Composable
private fun Fact(icon: ImageVector, label: String, value: String) {
    Surface(shape = RoundedCornerShape(20.dp), color = MaterialTheme.colorScheme.surfaceContainer) {
        Row(Modifier.padding(horizontal = 14.dp, vertical = 10.dp), verticalAlignment = Alignment.CenterVertically) {
            Icon(icon, null, Modifier.size(20.dp), tint = MaterialTheme.colorScheme.primary)
            Spacer(Modifier.width(8.dp))
            Column {
                Text(label, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                Text(value, style = MaterialTheme.typography.labelLarge.numeric())
            }
        }
    }
}

private enum class Metric(val label: String) { Cpu("CPU"), Ram("RAM"), Disk("Disque"), Net("Réseau") }

@Composable
private fun HistoryCard(
    history: History<SysPoint>?,
    range: String,
    state: LiveState,
    onRange: (String) -> Unit,
    modifier: Modifier,
) {
    var metric by rememberSaveable { mutableStateOf(Metric.Cpu) }
    var selectedRange by rememberSaveable { mutableStateOf(range) }
    val haptics = rememberHaptics()
    val scheme = MaterialTheme.colorScheme
    val status = Vigie.status
    SectionCard(modifier.animateContentSize(), title = "Historique") {
        SingleChoiceSegmentedButtonRow(Modifier.fillMaxWidth()) {
            listOf("24h" to "24 h", "7d" to "7 jours").forEachIndexed { i, (key, label) ->
                SegmentedButton(
                    selected = selectedRange == key,
                    onClick = { haptics.tick(); selectedRange = key; onRange(key) },
                    shape = SegmentedButtonDefaults.itemShape(i, 2),
                ) { Text(label) }
            }
        }
        Spacer(Modifier.height(8.dp))
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Metric.entries.forEach { m ->
                FilterChip(selected = metric == m, onClick = { haptics.tick(); metric = m }, label = { Text(m.label) })
            }
        }
        Spacer(Modifier.height(8.dp))
        val pts = history?.points.orEmpty()
        val (series, color, max) = when (metric) {
            Metric.Cpu -> Triple(pts.map { ChartPoint(it.ts, it.cpu) }, status.forLevel(pts.maxOfOrNull { it.cpu } ?: 0.0, state.thresholds.cpu), 100.0)
            Metric.Ram -> Triple(pts.map { ChartPoint(it.ts, it.mem) }, scheme.primary, 100.0)
            Metric.Disk -> Triple(pts.map { ChartPoint(it.ts, it.disk) }, scheme.tertiary, 100.0)
            Metric.Net -> Triple(pts.map { ChartPoint(it.ts, it.rx + it.tx) }, scheme.secondary, null)
        }
        if (series.size < 2) {
            Box(Modifier.fillMaxWidth().height(180.dp), contentAlignment = Alignment.Center) {
                Text(
                    if (history == null) "Chargement…" else "L'historique se remplit (un point par minute).",
                    style = MaterialTheme.typography.bodyMedium,
                    color = scheme.onSurfaceVariant,
                )
            }
        } else {
            MetricChart(
                points = series,
                color = color,
                spanMs = if (selectedRange == "7d") 7 * 86_400_000L else 86_400_000L,
                maxY = max,
                format = if (metric == Metric.Net) { v -> Fmt.rate(v) } else { v -> "${v.toInt()} %" },
            )
        }
    }
}

@Composable
private fun ContainersSummary(state: LiveState, modifier: Modifier, onOpen: () -> Unit) {
    val counts = state.overview?.containers ?: return
    val status = Vigie.status
    Surface(onClick = onOpen, shape = RoundedCornerShape(28.dp), color = MaterialTheme.colorScheme.surfaceContainer, modifier = modifier.fillMaxWidth()) {
        Row(Modifier.padding(20.dp), verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f)) {
                Text("Conteneurs", style = MaterialTheme.typography.titleMedium)
                Spacer(Modifier.height(4.dp))
                Row(horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                    Count(counts.running, "actifs", status.ok)
                    Count(counts.stopped, "arrêtés", MaterialTheme.colorScheme.outline)
                    if (counts.problems > 0) Count(counts.problems, "en défaut", status.critical)
                }
            }
            Icon(Icons.AutoMirrored.Rounded.ArrowForward, null)
        }
    }
}

@Composable
private fun Count(n: Int, label: String, color: Color) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        Text("$n", style = MaterialTheme.typography.titleLarge.numeric(), color = color)
        Spacer(Modifier.width(4.dp))
        Text(label, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

