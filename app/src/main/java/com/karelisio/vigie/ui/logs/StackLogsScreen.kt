package com.karelisio.vigie.ui.logs

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
import androidx.compose.material.icons.rounded.Layers
import androidx.compose.material.icons.rounded.LayersClear
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.lerp
import androidx.compose.ui.graphics.luminance
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.NavHostController
import com.karelisio.vigie.appContainer
import com.karelisio.vigie.data.LogEvent
import com.karelisio.vigie.data.LogLine
import com.karelisio.vigie.ui.components.EmptyState
import com.karelisio.vigie.ui.components.rememberHaptics
import com.karelisio.vigie.ui.navigation.StackLogsRoute
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.filter
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

/** Conteneur d'une stack dont on suit les logs, et l'état de sa connexion. */
data class LogSource(val name: String, val status: StreamStatus = StreamStatus.Connecting)

/** Insère [batch] dans [current], toutes deux dans l'ordre chronologique, et garde les [max] lignes les plus récentes. */
internal fun mergeByTime(current: List<UiLine>, batch: List<UiLine>, max: Int): List<UiLine> {
    if (batch.isEmpty()) return current
    val sorted = batch.sortedBy { it.ts }
    if (current.isEmpty() || sorted.first().ts >= current.last().ts) return (current + sorted).takeLast(max)
    val out = ArrayList<UiLine>(current.size + sorted.size)
    var i = 0
    var j = 0
    while (i < current.size && j < sorted.size) out += if (sorted[j].ts < current[i].ts) sorted[j++] else current[i++]
    while (i < current.size) out += current[i++]
    while (j < sorted.size) out += sorted[j++]
    return out.takeLast(max)
}

/** État global d'une stack : en direct dès qu'un conteneur l'est. */
internal fun stackStatus(sources: List<LogSource>, paused: Boolean): StreamStatus = when {
    paused -> StreamStatus.Paused
    sources.any { it.status == StreamStatus.Live } -> StreamStatus.Live
    sources.isEmpty() || sources.any { it.status == StreamStatus.Connecting } -> StreamStatus.Connecting
    sources.all { it.status == StreamStatus.Ended } -> StreamStatus.Ended
    else -> StreamStatus.Failed
}

/** Logs en direct de tous les conteneurs d'un projet compose : une connexion par conteneur, lignes fusionnées par horodatage. */
class StackLogsViewModel : ViewModel() {
    private val _lines = MutableStateFlow<List<UiLine>>(emptyList())
    val lines = _lines.asStateFlow()
    /** Conteneurs dans l'ordre d'apparition (il fixe leur couleur). */
    private val _sources = MutableStateFlow<List<LogSource>>(emptyList())
    val sources = _sources.asStateFlow()
    private val _paused = MutableStateFlow(false)
    val paused = _paused.asStateFlow()
    /** Vrai une fois la liste des conteneurs reçue. */
    private val _synced = MutableStateFlow(false)
    val synced = _synced.asStateFlow()
    /** Identifiant de la dernière ligne des chargements initiaux : les suivantes sont animées. */
    var initialMax = -1L
        private set
    private var nextId = 0L
    private var project: String? = null
    private val streams = mutableMapOf<String, Stream>()
    /** Horodatage de la dernière ligne reçue, par conteneur. */
    private val lastTs = mutableMapOf<String, Long>()
    private val pending = ArrayList<UiLine>()

    private data class Member(val id: String, val name: String, val running: Boolean, val startedAt: Long)
    private class Stream(val member: Member, val job: Job)

    fun start(project: String) {
        if (this.project != null) return
        this.project = project
        viewModelScope.launch {
            appContainer.repository.live
                .filter { it.loaded }
                .map { s -> s.containers.filter { it.project == project }.sortedBy { it.name }.map { Member(it.id, it.name, it.running, it.startedAt) } }
                .distinctUntilChanged()
                .collect(::sync)
        }
    }

    private fun sync(members: List<Member>) {
        _sources.update { cur -> cur + members.filter { m -> cur.none { it.name == m.name } }.map { LogSource(it.name) } }
        _synced.value = true
        members.forEach { m ->
            val s = streams[m.name]
            // Nouveau conteneur, recréé (nouvel identifiant), ou redémarré après la fin de son flux.
            if (s == null || s.member.id != m.id || (!s.job.isActive && m.running && m.startedAt != s.member.startedAt)) {
                s?.job?.cancel()
                streams[m.name] = Stream(m, follow(m))
            }
        }
    }

    private fun follow(m: Member): Job = viewModelScope.launch {
        setStatus(m.name, StreamStatus.Connecting)
        // Après une reconnexion, l'historique redemandé recouvre des lignes déjà affichées : on les saute.
        var skipUpTo = Long.MIN_VALUE
        followLogStream(m.id, TAIL, retryTail = TAIL, onReconnect = { setStatus(m.name, StreamStatus.Connecting) }) { e ->
            when (e) {
                LogEvent.Connected -> {
                    skipUpTo = lastTs[m.name] ?: Long.MIN_VALUE
                    setStatus(m.name, StreamStatus.Live)
                }
                is LogEvent.Lines -> {
                    val fresh = e.lines.dropWhile { it.ts <= skipUpTo }
                    if (fresh.isNotEmpty()) {
                        skipUpTo = Long.MIN_VALUE
                        append(m.name, fresh)
                    }
                }
                is LogEvent.Ended -> setStatus(m.name, StreamStatus.Ended)
                is LogEvent.Failed -> setStatus(m.name, StreamStatus.Failed)
            }
        }
    }

    private fun setStatus(name: String, status: StreamStatus) =
        _sources.update { list -> list.map { if (it.name == name) it.copy(status = status) else it } }

    private fun append(name: String, batch: List<LogLine>) {
        val first = name !in lastTs
        lastTs[name] = maxOf(lastTs[name] ?: Long.MIN_VALUE, batch.maxOf { it.ts })
        val ui = batch.map { UiLine(nextId++, it.ts, it.stream, it.line, levelOf(it.line), name) }
        if (first) initialMax = nextId - 1
        if (_paused.value) {
            pending += ui
            if (pending.size > MAX) pending.subList(0, pending.size - MAX).clear()
        } else {
            _lines.update { mergeByTime(it, ui, MAX) }
        }
    }

    fun togglePause() {
        _paused.value = !_paused.value
        if (!_paused.value) {
            _lines.update { mergeByTime(it, pending, MAX) }
            pending.clear()
        }
    }

    /** Relance tout de suite les connexions perdues. */
    fun retry() {
        val failed = _sources.value.filter { it.status == StreamStatus.Failed }.map { it.name }
        failed.mapNotNull { streams[it] }.forEach { s ->
            s.job.cancel()
            streams[s.member.name] = Stream(s.member, follow(s.member))
        }
    }

    private companion object {
        const val TAIL = 300
        const val MAX = 5000
    }
}

/** Teintes des conteneurs, bien distinctes, attribuées dans l'ordre d'apparition. */
private val SourceHues = floatArrayOf(210f, 320f, 170f, 270f, 45f, 130f, 240f, 15f)

/** Couleur stable de chaque conteneur, harmonisée avec la couleur principale du thème. */
@Composable
private fun rememberSourceColors(names: List<String>): Map<String, Color> {
    val scheme = MaterialTheme.colorScheme
    val dark = scheme.surface.luminance() < 0.5f
    return remember(names, scheme) {
        names.withIndex().associate { (i, name) ->
            val base = Color.hsl(SourceHues[i % SourceHues.size], 0.6f, if (dark) 0.72f else 0.4f)
            name to lerp(base, scheme.primary, 0.15f)
        }
    }
}

/** Puce colorée au nom du conteneur, en tête de chaque ligne. */
@Composable
private fun SourcePill(name: String, color: Color) {
    Surface(
        color = color.copy(alpha = 0.16f),
        contentColor = color,
        shape = RoundedCornerShape(50),
        modifier = Modifier.padding(top = 1.dp).widthIn(max = 96.dp),
    ) {
        Text(
            name,
            fontSize = 10.sp,
            lineHeight = 14.sp,
            fontWeight = FontWeight.Medium,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.padding(horizontal = 6.dp),
        )
    }
}

@Composable
fun StackLogsScreen(nav: NavHostController, route: StackLogsRoute, vm: StackLogsViewModel = viewModel()) {
    LaunchedEffect(route.project) { vm.start(route.project) }
    val lines by vm.lines.collectAsStateWithLifecycle()
    val sources by vm.sources.collectAsStateWithLifecycle()
    val paused by vm.paused.collectAsStateWithLifecycle()
    val synced by vm.synced.collectAsStateWithLifecycle()
    // Conteneurs affichés (vide : tous).
    var only by remember { mutableStateOf(emptySet<String>()) }
    val haptics = rememberHaptics()
    val colors = rememberSourceColors(sources.map { it.name })

    if (synced && sources.isEmpty()) {
        Scaffold(
            topBar = {
                TopAppBar(
                    navigationIcon = { IconButton(onClick = { nav.popBackStack() }) { Icon(Icons.AutoMirrored.Rounded.ArrowBack, "Retour") } },
                    title = { Text(route.project) },
                )
            },
        ) { padding ->
            EmptyState(
                Icons.Rounded.LayersClear,
                "Aucun conteneur",
                "La stack « ${route.project} » n'a aucun conteneur sur ce serveur.",
                Modifier.padding(padding),
            )
        }
        return
    }

    val shown = remember(lines, only) { if (only.isEmpty()) lines else lines.filter { it.source in only } }
    val status = stackStatus(sources, paused)
    LogViewer(
        title = route.project,
        status = status,
        statusText = when (status) {
            StreamStatus.Live -> "En direct · ${sources.count { it.status == StreamStatus.Live }}/${sources.size} conteneurs · ${lines.size} lignes"
            StreamStatus.Connecting -> "Connexion…"
            StreamStatus.Paused -> "En pause"
            StreamStatus.Ended -> "Tous les conteneurs sont arrêtés"
            StreamStatus.Failed -> "Connexion perdue"
        },
        lines = shown,
        initialMax = vm.initialMax,
        onBack = { nav.popBackStack() },
        onTogglePause = vm::togglePause,
        onRetry = vm::retry,
        excerptHeader = { picked -> excerptHeader(route.project, picked.map { it.source }.distinct(), picked.first().ts) },
        filters = {
            item {
                FilterChip(only.isEmpty(), onClick = { haptics.tick(); only = emptySet() }, label = { Text("Tout") })
            }
            items(sources, key = { it.name }) { s ->
                FilterChip(
                    selected = s.name in only,
                    onClick = { haptics.tick(); only = if (s.name in only) only - s.name else only + s.name },
                    label = {
                        Text(
                            when (s.status) {
                                StreamStatus.Ended -> "${s.name} · arrêté"
                                StreamStatus.Failed -> "${s.name} · coupé"
                                else -> s.name
                            },
                        )
                    },
                    leadingIcon = { Box(Modifier.size(10.dp).background(colors[s.name] ?: MaterialTheme.colorScheme.outline, CircleShape)) },
                )
            }
        },
        leading = { line -> SourcePill(line.source, colors[line.source] ?: MaterialTheme.colorScheme.outline) },
    )
}

/** Accès aux logs de la stack depuis la fiche d'un de ses conteneurs. */
@Composable
fun StackLogsButton(project: String, modifier: Modifier = Modifier, onClick: () -> Unit) {
    val haptics = rememberHaptics()
    FilledTonalButton(
        onClick = { haptics.tick(); onClick() },
        shapes = ButtonDefaults.shapes(),
        modifier = modifier.fillMaxWidth().height(48.dp),
    ) {
        Icon(Icons.Rounded.Layers, null)
        Spacer(Modifier.width(8.dp))
        Text("Logs de la stack $project", maxLines = 1, overflow = TextOverflow.Ellipsis)
    }
}
