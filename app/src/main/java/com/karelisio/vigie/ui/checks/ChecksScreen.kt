package com.karelisio.vigie.ui.checks

import android.net.Uri
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.animateContentSize
import androidx.compose.animation.expandVertically
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.shrinkVertically
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
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
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Add
import androidx.compose.material.icons.rounded.Delete
import androidx.compose.material.icons.rounded.Edit
import androidx.compose.material.icons.rounded.MonitorHeart
import androidx.compose.material.icons.rounded.NetworkCheck
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.ExtendedFloatingActionButton
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.SegmentedButton
import androidx.compose.material3.SegmentedButtonDefaults
import androidx.compose.material3.SingleChoiceSegmentedButtonRow
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.NavHostController
import com.karelisio.vigie.appContainer
import com.karelisio.vigie.data.CheckHistory
import com.karelisio.vigie.data.CheckInput
import com.karelisio.vigie.data.CheckStatus
import com.karelisio.vigie.data.Fmt
import com.karelisio.vigie.ui.components.ActionTone
import com.karelisio.vigie.ui.components.AnimatedCounter
import com.karelisio.vigie.ui.components.ConnectionBanner
import com.karelisio.vigie.ui.components.EmptyState
import com.karelisio.vigie.ui.components.MorphActionButton
import com.karelisio.vigie.ui.components.RadarRefreshBox
import com.karelisio.vigie.ui.components.Sparkline
import com.karelisio.vigie.ui.components.StatusDot
import com.karelisio.vigie.ui.components.UptimeBars
import com.karelisio.vigie.ui.components.cascade
import com.karelisio.vigie.ui.components.latencies
import com.karelisio.vigie.ui.components.rememberActionRunner
import com.karelisio.vigie.ui.components.rememberHaptics
import com.karelisio.vigie.ui.components.shakeOnAppear
import com.karelisio.vigie.ui.navigation.LocalSnackbar
import com.karelisio.vigie.ui.theme.Vigie
import com.karelisio.vigie.ui.theme.numeric
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

data class RangeSpec(val key: String, val label: String, val buckets: Int, val from: String)

val Ranges = listOf(
    RangeSpec("24h", "24 h", 48, "il y a 24 h"),
    RangeSpec("7d", "7 jours", 56, "il y a 7 jours"),
    RangeSpec("90d", "90 jours", 90, "il y a 90 jours"),
)

class ChecksViewModel : ViewModel() {
    private val repo = appContainer.repository
    val live = repo.live
    private val _history = MutableStateFlow<Map<Long, CheckHistory>>(emptyMap())
    val history = _history.asStateFlow()
    private val _refreshing = MutableStateFlow(false)
    val refreshing = _refreshing.asStateFlow()
    var range = Ranges[0]
        private set

    init {
        viewModelScope.launch {
            runCatching { repo.checks() }
            loadHistory()
        }
    }

    fun setRange(r: RangeSpec) {
        range = r
        _history.value = emptyMap()
        loadHistory()
    }

    fun loadHistory() {
        val r = range
        viewModelScope.launch {
            live.value.checks.forEach { c ->
                launch {
                    runCatching { repo.checkHistory(c.id, r.key, r.buckets) }.onSuccess { h ->
                        if (range == r) _history.update { it + (c.id to h) }
                    }
                }
            }
        }
    }

    fun refresh() {
        viewModelScope.launch {
            _refreshing.value = true
            runCatching { repo.checks() }
            loadHistory()
            delay(800)
            _refreshing.value = false
        }
    }

    suspend fun run(id: Long) {
        val s = repo.runCheck(id)
        if (s.last?.ok != true) error(s.last?.error ?: "Échec")
        loadHistory()
    }

    suspend fun save(id: Long?, input: CheckInput) {
        if (id == null) repo.createCheck(input) else repo.updateCheck(id, input)
        loadHistory()
    }

    fun delete(id: Long, done: (Result<Unit>) -> Unit) {
        viewModelScope.launch { done(runCatching { repo.deleteCheck(id) }) }
    }
}

@Composable
fun ChecksScreen(nav: NavHostController, vm: ChecksViewModel = viewModel()) {
    val state by vm.live.collectAsStateWithLifecycle()
    val history by vm.history.collectAsStateWithLifecycle()
    val refreshing by vm.refreshing.collectAsStateWithLifecycle()
    var editing by remember { mutableStateOf<CheckStatus?>(null) }
    var creating by rememberSaveable { mutableStateOf(false) }
    var rangeIndex by rememberSaveable { mutableIntStateOf(0) }
    var expanded by rememberSaveable { mutableStateOf<Long?>(null) }
    var confirmDelete by remember { mutableStateOf<CheckStatus?>(null) }
    val haptics = rememberHaptics()
    val snackbar = LocalSnackbar.current
    val scope = rememberCoroutineScope()
    val checks = state.checks
    LaunchedEffect(checks.map { it.id }) { if (checks.any { it.id !in history }) vm.loadHistory() }

    val bottom = WindowInsets.navigationBars.asPaddingValues().calculateBottomPadding() + 96.dp
    Box(Modifier.fillMaxSize()) {
        RadarRefreshBox(refreshing, vm::refresh, Modifier.fillMaxSize()) {
            LazyColumn(
                Modifier.fillMaxSize(),
                contentPadding = PaddingValues(bottom = bottom + 72.dp),
                verticalArrangement = Arrangement.spacedBy(10.dp),
            ) {
                item(key = "title") {
                    Column(Modifier.statusBarsPadding().padding(start = 20.dp, end = 20.dp, top = 16.dp)) {
                        Text("Services", style = MaterialTheme.typography.headlineLargeEmphasized)
                        val up = checks.count { it.state == "up" }
                        Text(
                            if (checks.isEmpty()) "Vérifie que tes addons répondent vraiment"
                            else "$up en ligne sur ${checks.count { it.enabled }} surveillés",
                            style = MaterialTheme.typography.bodyMedium,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                }
                item(key = "banner") { ConnectionBanner(state) }
                if (checks.isNotEmpty()) {
                    item(key = "range") {
                        SingleChoiceSegmentedButtonRow(Modifier.fillMaxWidth().padding(horizontal = 16.dp)) {
                            Ranges.forEachIndexed { i, r ->
                                SegmentedButton(
                                    selected = rangeIndex == i,
                                    onClick = { haptics.tick(); rangeIndex = i; vm.setRange(r) },
                                    shape = SegmentedButtonDefaults.itemShape(i, Ranges.size),
                                ) { Text(r.label) }
                            }
                        }
                    }
                } else if (state.loaded) {
                    item(key = "empty") {
                        EmptyState(
                            Icons.Rounded.MonitorHeart,
                            "Aucun service surveillé",
                            "Ajoute l'URL du manifest de tes addons Stremio (AIOStreams, Comet, AIOMetadata…) : Vigie vérifiera chaque minute qu'ils répondent.",
                        )
                    }
                }
                items(checks, key = { it.id }) { c ->
                    CheckCard(
                        c = c,
                        history = history[c.id],
                        range = Ranges[rangeIndex],
                        expanded = expanded == c.id,
                        onToggle = { haptics.tick(); expanded = if (expanded == c.id) null else c.id },
                        onRun = { vm.run(c.id) },
                        onEdit = { editing = c },
                        onDelete = { confirmDelete = c },
                        modifier = Modifier
                            .padding(horizontal = 16.dp)
                            .animateItem()
                            .cascade(checks.indexOf(c), "checks")
                            .shakeOnAppear(c.state == "down", c.state),
                    )
                }
            }
        }
        ExtendedFloatingActionButton(
            onClick = { haptics.tick(); creating = true },
            icon = { Icon(Icons.Rounded.Add, null) },
            text = { Text("Ajouter") },
            modifier = Modifier.align(Alignment.BottomEnd).padding(end = 16.dp, bottom = bottom),
        )
    }

    if (creating || editing != null) {
        val server = appContainer.servers.active
        CheckEditSheet(
            initial = editing,
            baseDomain = server?.host?.substringAfter('.'),
            onDismiss = { creating = false; editing = null },
            onSave = { input ->
                vm.save(editing?.id, input)
                creating = false
                editing = null
                snackbar.showSnackbar("« ${input.name} » enregistré")
            },
        )
    }
    confirmDelete?.let { c ->
        AlertDialog(
            onDismissRequest = { confirmDelete = null },
            icon = { Icon(Icons.Rounded.Delete, null) },
            title = { Text("Supprimer « ${c.name} » ?") },
            text = { Text("Son historique de disponibilité sera aussi supprimé.") },
            confirmButton = {
                TextButton(onClick = {
                    confirmDelete = null
                    vm.delete(c.id) { r -> scope.launch { snackbar.showSnackbar(if (r.isSuccess) "Supprimé" else "Échec : ${r.exceptionOrNull()?.message}") } }
                }) { Text("Supprimer") }
            },
            dismissButton = { TextButton(onClick = { confirmDelete = null }) { Text("Annuler") } },
        )
    }
}

@Composable
internal fun CheckCard(
    c: CheckStatus,
    history: CheckHistory?,
    range: RangeSpec,
    expanded: Boolean,
    onToggle: () -> Unit,
    onRun: suspend () -> Unit,
    onEdit: () -> Unit,
    onDelete: () -> Unit,
    modifier: Modifier,
) {
    val colors = Vigie.status
    val scheme = MaterialTheme.colorScheme
    val dot = when (c.state) {
        "up" -> "ok"
        "down" -> "critical"
        "pending" -> "warning"
        else -> "neutral"
    }
    var selected by remember(range) { mutableStateOf<Int?>(null) }
    val runner = rememberActionRunner()
    Surface(
        onClick = onToggle,
        shape = RoundedCornerShape(28.dp),
        color = if (c.state == "down") colors.criticalContainer.copy(alpha = 0.5f) else scheme.surfaceContainer,
        modifier = modifier.fillMaxWidth(),
    ) {
        Column(Modifier.padding(18.dp).animateContentSize()) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                StatusDot(dot, size = 11.dp)
                Spacer(Modifier.width(8.dp))
                Column(Modifier.weight(1f)) {
                    Text(c.name, style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    Text(
                        Uri.parse(c.url).host ?: c.url,
                        style = MaterialTheme.typography.bodySmall,
                        color = scheme.onSurfaceVariant,
                        maxLines = 1,
                    )
                }
                Column(horizontalAlignment = Alignment.End) {
                    val last = c.last
                    if (last != null && last.ok) {
                        AnimatedCounter(last.latencyMs.toInt(), MaterialTheme.typography.titleMedium, suffix = " ms")
                    } else {
                        Text(if (c.state == "down") "Hors ligne" else "—", style = MaterialTheme.typography.titleSmall, color = colors.of(dot))
                    }
                    val up = history?.uptime ?: c.uptime24h
                    Text(Fmt.uptimePct(up), style = MaterialTheme.typography.labelMedium.numeric(), color = scheme.onSurfaceVariant)
                }
            }
            Spacer(Modifier.height(14.dp))
            if (history != null) {
                UptimeBars(history.buckets, onSelect = { selected = it })
                Row(Modifier.fillMaxWidth().padding(top = 4.dp)) {
                    val sel = selected?.let { history.buckets.getOrNull(it) }
                    Text(
                        if (sel != null) {
                            val date = if (range.key == "90d") Fmt.day(sel.ts) else Fmt.dateTime(sel.ts)
                            if (sel.total == 0) "$date · pas de données"
                            else "$date · ${Fmt.uptimePct(100.0 * sel.ok / sel.total)}" + (sel.latency?.let { " · ${it.toInt()} ms" } ?: "")
                        } else range.from,
                        style = MaterialTheme.typography.labelSmall,
                        color = scheme.onSurfaceVariant,
                        modifier = Modifier.weight(1f),
                    )
                    if (selected == null) Text("maintenant", style = MaterialTheme.typography.labelSmall, color = scheme.onSurfaceVariant)
                }
            } else {
                Box(Modifier.fillMaxWidth().height(30.dp))
            }
            AnimatedVisibility(expanded, enter = expandVertically() + fadeIn(), exit = shrinkVertically() + fadeOut()) {
                Column(Modifier.padding(top = 14.dp)) {
                    if (c.recent.size >= 2) {
                        Text("Latence (${c.recent.size} derniers essais)", style = MaterialTheme.typography.labelMedium, color = scheme.onSurfaceVariant)
                        Spacer(Modifier.height(6.dp))
                        Sparkline(
                            c.recent.latencies(),
                            scheme.primary,
                            Modifier.fillMaxWidth().height(56.dp),
                            failures = c.recent.map { !it.ok },
                        )
                        Spacer(Modifier.height(10.dp))
                    }
                    c.last?.error?.takeIf { !c.last.ok }?.let {
                        Text("Dernière erreur : $it", style = MaterialTheme.typography.bodyMedium, color = colors.critical)
                        Spacer(Modifier.height(6.dp))
                    }
                    Text(c.url, style = MaterialTheme.typography.bodySmall, color = scheme.onSurfaceVariant)
                    Text(
                        "Code attendu ${c.expectedStatus}" + (if (c.keyword.isNotEmpty()) " · contient « ${c.keyword} »" else "") +
                            " · toutes les ${if (c.intervalSec >= 60) "${c.intervalSec / 60} min" else "${c.intervalSec} s"}" +
                            (if (c.latency24h > 0) " · moyenne 24 h : ${c.latency24h.toInt()} ms" else ""),
                        style = MaterialTheme.typography.bodySmall,
                        color = scheme.onSurfaceVariant,
                    )
                    Spacer(Modifier.height(12.dp))
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                        MorphActionButton("Tester", Icons.Rounded.NetworkCheck, runner, { runner.run({ onRun() }) }, tone = ActionTone.Primary)
                        OutlinedButton(onClick = onEdit, modifier = Modifier.height(56.dp)) {
                            Icon(Icons.Rounded.Edit, null)
                        }
                        OutlinedButton(onClick = onDelete, modifier = Modifier.height(56.dp)) {
                            Icon(Icons.Rounded.Delete, null)
                        }
                    }
                }
            }
        }
    }
}
