package com.karelisio.vigie.ui.logs

import android.os.Build
import androidx.activity.compose.BackHandler
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.tween
import androidx.compose.animation.expandVertically
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.scaleIn
import androidx.compose.animation.scaleOut
import androidx.compose.animation.shrinkVertically
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
import androidx.compose.material.icons.rounded.Close
import androidx.compose.material.icons.rounded.ContentCopy
import androidx.compose.material.icons.rounded.DeleteSweep
import androidx.compose.material.icons.rounded.KeyboardDoubleArrowDown
import androidx.compose.material.icons.rounded.NotificationAdd
import androidx.compose.material.icons.rounded.Pause
import androidx.compose.material.icons.rounded.PlayArrow
import androidx.compose.material.icons.rounded.Search
import androidx.compose.material.icons.rounded.Share
import androidx.compose.material3.ExtendedFloatingActionButton
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextField
import androidx.compose.material3.TextFieldDefaults
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.NavHostController
import com.karelisio.vigie.appContainer
import com.karelisio.vigie.data.Container
import com.karelisio.vigie.data.Fmt
import com.karelisio.vigie.data.LogEvent
import com.karelisio.vigie.data.updateLogRules
import com.karelisio.vigie.ui.components.StatusDot
import com.karelisio.vigie.ui.components.rememberHaptics
import com.karelisio.vigie.ui.navigation.LocalSnackbar
import com.karelisio.vigie.ui.navigation.LogsRoute
import com.karelisio.vigie.ui.theme.StatusColors
import com.karelisio.vigie.ui.theme.Vigie
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

enum class Level(val tag: String) { Error("ERR"), Warn("WARN"), Info("INFO"), Debug("DBG"), None("") }

/** Ligne affichée ; [source] : conteneur d'origine dans les logs d'une stack (vide pour un seul conteneur). */
@Immutable
data class UiLine(val id: Long, val ts: Long, val stream: String, val text: String, val level: Level, val source: String = "")

private val errorRe = Regex("""(?i)\b(error|err|fatal|crit(ical)?|panic|exception|emerg|alert)\b|level=(error|fatal|panic)|\[(e|error)]""")
private val warnRe = Regex("""(?i)\b(warn(ing)?)\b|level=warn|\[w(arn)?]""")
private val infoRe = Regex("""(?i)\binfo\b|level=info|\[i(nfo)?]""")
private val debugRe = Regex("""(?i)\b(debug|trace|dbg|verbose)\b|level=(debug|trace)""")

fun levelOf(text: String): Level {
    val head = text.take(160)
    return when {
        errorRe.containsMatchIn(head) -> Level.Error
        warnRe.containsMatchIn(head) -> Level.Warn
        infoRe.containsMatchIn(head) -> Level.Info
        debugRe.containsMatchIn(head) -> Level.Debug
        else -> Level.None
    }
}

enum class StreamStatus { Connecting, Live, Ended, Failed, Paused }

/**
 * Suit les logs en direct d'un conteneur, avec reconnexion progressive après une coupure
 * ([onReconnect] avant chaque nouvel essai, [retryTail] lignes d'historique redemandées).
 * Se termine quand le conteneur s'arrête.
 */
internal suspend fun followLogStream(
    id: String,
    tail: Int,
    retryTail: Int,
    onReconnect: () -> Unit,
    onEvent: (LogEvent) -> Unit,
) {
    var attempts = 0
    var t = tail
    while (true) {
        var ended = false
        try {
            appContainer.repository.logStream(id, t).collect { e ->
                if (e == LogEvent.Connected) attempts = 0
                if (e is LogEvent.Ended) ended = true
                onEvent(e)
            }
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            onEvent(LogEvent.Failed(e.message ?: "Connexion perdue"))
        }
        if (ended) return
        attempts++
        delay(minOf(2_000L * attempts, 15_000L))
        t = retryTail
        onReconnect()
    }
}

class LogsViewModel : ViewModel() {
    private val _lines = MutableStateFlow<List<UiLine>>(emptyList())
    val lines = _lines.asStateFlow()
    private val _status = MutableStateFlow(StreamStatus.Connecting)
    val status = _status.asStateFlow()
    private val _message = MutableStateFlow<String?>(null)
    val message = _message.asStateFlow()
    /** Identifiant de la dernière ligne du chargement initial : les suivantes sont animées. */
    var initialMax = -1L
        private set
    private var nextId = 0L
    private var job: Job? = null
    private var ref = ""
    private var paused = false
    private val pending = ArrayList<UiLine>()

    fun start(id: String) {
        if (ref == id && job?.isActive == true) return
        ref = id
        connect(tail = 500)
    }

    private fun connect(tail: Int) {
        job?.cancel()
        _status.value = StreamStatus.Connecting
        job = viewModelScope.launch {
            // Reprise sans recharger l'historique.
            followLogStream(ref, tail, retryTail = 0, onReconnect = { _status.value = StreamStatus.Connecting }) { e ->
                when (e) {
                    LogEvent.Connected -> _status.value = if (paused) StreamStatus.Paused else StreamStatus.Live
                    is LogEvent.Lines -> append(e.lines.map { UiLine(nextId++, it.ts, it.stream, it.line, levelOf(it.line)) })
                    is LogEvent.Ended -> {
                        _status.value = StreamStatus.Ended
                        _message.value = e.reason
                    }
                    is LogEvent.Failed -> {
                        _status.value = StreamStatus.Failed
                        _message.value = e.message
                    }
                }
            }
        }
    }

    private fun append(new: List<UiLine>) {
        if (new.isEmpty()) return
        if (initialMax < 0) initialMax = new.last().id
        if (paused) {
            pending += new
            return
        }
        _lines.update { (it + new).takeLast(MAX) }
    }

    fun togglePause() {
        paused = !paused
        if (!paused) {
            _lines.update { (it + pending).takeLast(MAX) }
            pending.clear()
            if (_status.value == StreamStatus.Paused) _status.value = StreamStatus.Live
        } else if (_status.value == StreamStatus.Live) {
            _status.value = StreamStatus.Paused
        }
    }

    fun clear() {
        _lines.value = emptyList()
        initialMax = nextId
    }

    fun retry() = connect(tail = 200)

    private companion object {
        const val MAX = 5000
    }
}

private fun StatusColors.levelColor(l: Level, scheme: androidx.compose.material3.ColorScheme): Color = when (l) {
    Level.Error -> critical
    Level.Warn -> warn
    Level.Info -> scheme.primary
    Level.Debug -> scheme.outline
    Level.None -> scheme.onSurfaceVariant
}

@Composable
fun LogsScreen(nav: NavHostController, route: LogsRoute, vm: LogsViewModel = viewModel()) {
    LaunchedEffect(route.id) { vm.start(route.id) }
    val lines by vm.lines.collectAsStateWithLifecycle()
    val status by vm.status.collectAsStateWithLifecycle()
    val message by vm.message.collectAsStateWithLifecycle()
    var levelFilter by rememberSaveable { mutableStateOf<Level?>(null) }
    val scope = rememberCoroutineScope()
    val haptics = rememberHaptics()
    val snackbar = LocalSnackbar.current
    val logAlerts = logAlertsSupported()
    // Dialogue d'alerte : conteneurs proposés et texte recherché (null : dialogue fermé).
    var alertDraft by remember { mutableStateOf<Pair<List<Container>, String>?>(null) }

    val filtered = remember(lines, levelFilter) {
        lines.filter { l -> levelFilter == null || l.level == levelFilter || (levelFilter == Level.Warn && l.level == Level.Error) }
    }

    LogViewer(
        title = route.name,
        status = status,
        statusText = when (status) {
            StreamStatus.Live -> "En direct · ${lines.size} lignes"
            StreamStatus.Connecting -> "Connexion…"
            StreamStatus.Paused -> "En pause"
            StreamStatus.Ended -> "Flux terminé" + (message?.let { " ($it)" } ?: "")
            StreamStatus.Failed -> message ?: "Connexion perdue"
        },
        lines = filtered,
        initialMax = vm.initialMax,
        onBack = { nav.popBackStack() },
        onTogglePause = vm::togglePause,
        onRetry = vm::retry,
        excerptHeader = { picked ->
            val project = appContainer.repository.live.value.containers.find { it.id == route.id || it.name == route.name }?.project.orEmpty()
            excerptHeader(project, listOf(route.name), picked.first().ts)
        },
        filters = {
            item {
                FilterChip(levelFilter == null, onClick = { levelFilter = null }, label = { Text("Tout") })
            }
            item {
                FilterChip(levelFilter == Level.Error, onClick = { levelFilter = Level.Error }, label = { Text("Erreurs · ${lines.count { it.level == Level.Error }}") })
            }
            item {
                FilterChip(levelFilter == Level.Warn, onClick = { levelFilter = Level.Warn }, label = { Text("Avertissements et +") })
            }
        },
        actions = { query ->
            IconButton(onClick = { haptics.tick(); vm.clear() }) { Icon(Icons.Rounded.DeleteSweep, "Effacer") }
            if (logAlerts) {
                IconButton(onClick = { haptics.tick(); alertDraft = appContainer.repository.live.value.containers to query }) {
                    Icon(Icons.Rounded.NotificationAdd, "Créer une alerte sur ces logs")
                }
            }
        },
    )

    alertDraft?.let { (containers, pattern) ->
        LogRuleDialog(
            containers = containers.map { it.name },
            initialContainer = containers.find { it.id == route.id }?.name ?: route.name,
            initialPattern = pattern,
            onDismiss = { alertDraft = null },
        ) { rule ->
            appContainer.repository.updateLogRules { it + rule }
            alertDraft = null
            scope.launch { snackbar.showSnackbar("Alerte ajoutée pour ${rule.container}") }
        }
    }
}

/**
 * Corps commun des écrans de logs : barre d'outils, recherche, filtres ([filters]), liste qui suit
 * les nouvelles lignes, et sélection de lignes (appui long, puis appuis) à copier ou partager.
 * [excerptHeader] donne l'en-tête de l'extrait à partir des lignes choisies.
 */
@Composable
internal fun LogViewer(
    title: String,
    status: StreamStatus,
    statusText: String,
    lines: List<UiLine>,
    initialMax: Long,
    onBack: () -> Unit,
    onTogglePause: () -> Unit,
    onRetry: () -> Unit,
    excerptHeader: (List<UiLine>) -> String,
    filters: LazyListScope.() -> Unit,
    actions: @Composable RowScope.(query: String) -> Unit = {},
    leading: (@Composable (UiLine) -> Unit)? = null,
) {
    var searching by rememberSaveable { mutableStateOf(false) }
    var query by rememberSaveable { mutableStateOf("") }
    var selected by remember { mutableStateOf(emptySet<Long>()) }
    val selecting = selected.isNotEmpty()
    var follow by remember { mutableStateOf(true) }
    val listState = rememberLazyListState()
    val scope = rememberCoroutineScope()
    val haptics = rememberHaptics()
    val context = LocalContext.current
    val snackbar = LocalSnackbar.current
    val scheme = MaterialTheme.colorScheme

    val shown = remember(lines, query) { if (query.isBlank()) lines else lines.filter { it.text.contains(query, ignoreCase = true) } }

    BackHandler(enabled = selecting) { selected = emptySet() }

    fun picked(): List<UiLine> = lines.filter { it.id in selected }

    fun toggle(line: UiLine) {
        haptics.tick()
        selected = if (line.id in selected) selected - line.id else selected + line.id
    }

    // Défilement automatique tant que l'utilisateur est en bas.
    LaunchedEffect(shown.size, follow) {
        if (follow && shown.isNotEmpty()) {
            val last = shown.lastIndex
            if (last - listState.firstVisibleItemIndex < 40) listState.animateScrollToItem(last) else listState.scrollToItem(last)
        }
    }
    LaunchedEffect(listState) {
        snapshotFlow { listState.isScrollInProgress to listState.canScrollForward }.collect { (scrolling, canForward) ->
            if (scrolling && canForward) follow = false
            if (!canForward && !follow && !scrolling) follow = true
        }
    }
    val lastIndex by rememberUpdatedState(shown.lastIndex)
    val newBelow by remember {
        derivedStateOf { if (follow) 0 else (lastIndex - (listState.layoutInfo.visibleItemsInfo.lastOrNull()?.index ?: 0)).coerceAtLeast(0) }
    }

    Scaffold(
        topBar = {
            Column {
                if (selecting) {
                    TopAppBar(
                        navigationIcon = { IconButton(onClick = { selected = emptySet() }) { Icon(Icons.Rounded.Close, "Annuler la sélection") } },
                        title = { Text(if (selected.size == 1) "1 ligne sélectionnée" else "${selected.size} lignes sélectionnées") },
                        actions = {
                            IconButton(onClick = {
                                val p = picked()
                                if (p.isEmpty()) return@IconButton
                                haptics.confirm()
                                copyExcerpt(context, excerptText(excerptHeader(p), p))
                                selected = emptySet()
                                // Android 13 et plus confirment eux-mêmes la copie.
                                if (Build.VERSION.SDK_INT < 33) {
                                    scope.launch { snackbar.showSnackbar(if (p.size == 1) "Ligne copiée" else "${p.size} lignes copiées") }
                                }
                            }) { Icon(Icons.Rounded.ContentCopy, "Copier") }
                            IconButton(onClick = {
                                val p = picked()
                                if (p.isEmpty()) return@IconButton
                                haptics.tick()
                                val header = excerptHeader(p)
                                shareExcerpt(context, header, excerptText(header, p))
                                selected = emptySet()
                            }) { Icon(Icons.Rounded.Share, "Partager") }
                        },
                        colors = TopAppBarDefaults.topAppBarColors(
                            containerColor = scheme.secondaryContainer,
                            titleContentColor = scheme.onSecondaryContainer,
                            navigationIconContentColor = scheme.onSecondaryContainer,
                            actionIconContentColor = scheme.onSecondaryContainer,
                        ),
                    )
                } else {
                    TopAppBar(
                        navigationIcon = { IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Rounded.ArrowBack, "Retour") } },
                        title = {
                            Column {
                                Text(title, maxLines = 1, style = MaterialTheme.typography.titleLarge)
                                Row(verticalAlignment = Alignment.CenterVertically) {
                                    StatusDot(
                                        when (status) {
                                            StreamStatus.Live -> "ok"
                                            StreamStatus.Connecting, StreamStatus.Paused -> "warning"
                                            StreamStatus.Failed -> "critical"
                                            StreamStatus.Ended -> "neutral"
                                        },
                                        size = 7.dp,
                                    )
                                    Text(
                                        statusText,
                                        style = MaterialTheme.typography.labelMedium,
                                        color = scheme.onSurfaceVariant,
                                        maxLines = 1,
                                    )
                                }
                            }
                        },
                        actions = {
                            IconButton(onClick = { haptics.tick(); searching = !searching; if (!searching) query = "" }) {
                                Icon(if (searching) Icons.Rounded.Close else Icons.Rounded.Search, "Rechercher")
                            }
                            IconButton(onClick = { haptics.toggle(status == StreamStatus.Paused); onTogglePause() }) {
                                Icon(if (status == StreamStatus.Paused) Icons.Rounded.PlayArrow else Icons.Rounded.Pause, "Pause")
                            }
                            actions(query)
                        },
                    )
                }
                AnimatedVisibility(searching, enter = expandVertically() + fadeIn(), exit = shrinkVertically() + fadeOut()) {
                    TextField(
                        value = query,
                        onValueChange = { query = it },
                        placeholder = { Text("Rechercher dans les logs") },
                        leadingIcon = { Icon(Icons.Rounded.Search, null) },
                        singleLine = true,
                        shape = RoundedCornerShape(50),
                        colors = TextFieldDefaults.colors(
                            focusedIndicatorColor = Color.Transparent,
                            unfocusedIndicatorColor = Color.Transparent,
                        ),
                        supportingText = if (query.isNotBlank()) ({ Text("${shown.size} résultat(s)") }) else null,
                        modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 4.dp),
                    )
                }
                LazyRow(
                    contentPadding = PaddingValues(horizontal = 16.dp, vertical = 4.dp),
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                    content = filters,
                )
            }
        },
        floatingActionButton = {
            AnimatedVisibility(!follow && !selecting, enter = scaleIn() + fadeIn(), exit = scaleOut() + fadeOut()) {
                ExtendedFloatingActionButton(
                    onClick = {
                        haptics.tick()
                        follow = true
                        scope.launch { if (shown.isNotEmpty()) listState.animateScrollToItem(shown.lastIndex) }
                    },
                    icon = { Icon(Icons.Rounded.KeyboardDoubleArrowDown, null) },
                    text = { Text(if (newBelow > 0) "$newBelow lignes plus bas" else "Suivre") },
                )
            }
        },
    ) { padding ->
        Box(Modifier.fillMaxSize().padding(padding)) {
            if (shown.isEmpty()) {
                Text(
                    when {
                        status == StreamStatus.Connecting -> "Connexion au flux…"
                        query.isNotBlank() -> "Aucune ligne ne correspond."
                        else -> "Aucune ligne pour l'instant."
                    },
                    Modifier.align(Alignment.Center),
                    color = scheme.onSurfaceVariant,
                )
            }
            LazyColumn(
                state = listState,
                modifier = Modifier.fillMaxSize(),
                contentPadding = PaddingValues(start = 8.dp, end = 8.dp, bottom = 96.dp),
            ) {
                items(shown, key = { it.id }) { line ->
                    LogRow(
                        line = line,
                        query = query,
                        animate = line.id > initialMax,
                        selected = line.id in selected,
                        modifier = Modifier.animateItem(fadeInSpec = tween(260), placementSpec = null, fadeOutSpec = null),
                        onClick = { if (selecting) toggle(line) },
                        onLongClick = {
                            if (selecting) {
                                toggle(line)
                            } else {
                                // La liste ne bouge plus pendant la sélection.
                                haptics.longPress()
                                follow = false
                                selected = setOf(line.id)
                            }
                        },
                        leading = if (leading != null) ({ leading(line) }) else null,
                    )
                }
            }
            if (status == StreamStatus.Failed) {
                Surface(
                    onClick = onRetry,
                    color = scheme.errorContainer,
                    shape = RoundedCornerShape(16.dp),
                    modifier = Modifier.align(Alignment.BottomCenter).navigationBarsPadding().padding(16.dp),
                ) {
                    Text("Connexion perdue · toucher pour réessayer", Modifier.padding(16.dp), color = scheme.onErrorContainer)
                }
            }
        }
    }
}

@Composable
internal fun LogRow(
    line: UiLine,
    query: String,
    animate: Boolean,
    selected: Boolean,
    modifier: Modifier,
    onClick: () -> Unit,
    onLongClick: () -> Unit,
    leading: (@Composable () -> Unit)? = null,
) {
    val colors = Vigie.status
    val scheme = MaterialTheme.colorScheme
    val reduced = Vigie.reducedMotion
    val enter = remember { Animatable(if (animate && !reduced) 0f else 1f) }
    LaunchedEffect(Unit) { if (enter.value < 1f) enter.animateTo(1f, tween(320)) }
    val lc = colors.levelColor(line.level, scheme)
    val bg by animateColorAsState(
        when {
            selected -> scheme.secondaryContainer
            line.level == Level.Error -> colors.critical.copy(alpha = 0.08f)
            line.level == Level.Warn -> colors.warn.copy(alpha = 0.06f)
            else -> Color.Transparent
        },
        label = "logBg",
    )
    Row(
        modifier
            .fillMaxWidth()
            .graphicsLayer {
                alpha = enter.value
                translationY = (1f - enter.value) * 14.dp.toPx()
            }
            .semantics { this.selected = selected }
            .combinedClickable(onClick = onClick, onLongClick = onLongClick)
            .padding(vertical = 1.dp),
    ) {
        Surface(color = bg, shape = RoundedCornerShape(6.dp), modifier = Modifier.fillMaxWidth()) {
            Row(Modifier.padding(horizontal = 6.dp, vertical = 2.dp)) {
                if (leading != null) {
                    leading()
                    Spacer(Modifier.width(6.dp))
                }
                Text(
                    Fmt.timeSeconds(line.ts),
                    fontFamily = FontFamily.Monospace,
                    fontSize = 11.sp,
                    color = scheme.outline,
                    modifier = Modifier.padding(top = 1.dp),
                )
                Spacer(Modifier.width(6.dp))
                if (line.level != Level.None) {
                    Text(
                        line.level.tag,
                        fontFamily = FontFamily.Monospace,
                        fontSize = 11.sp,
                        fontWeight = FontWeight.Bold,
                        color = lc,
                        modifier = Modifier.width(34.dp).padding(top = 1.dp),
                    )
                } else {
                    Spacer(Modifier.width(34.dp))
                }
                Text(
                    highlight(line.text, query, scheme.tertiaryContainer, scheme.onTertiaryContainer),
                    fontFamily = FontFamily.Monospace,
                    fontSize = 12.sp,
                    lineHeight = 16.sp,
                    color = if (line.stream == "stderr" && line.level == Level.None) colors.warn else scheme.onSurface,
                )
            }
        }
    }
}

private fun highlight(text: String, query: String, bg: Color, fg: Color): AnnotatedString = buildAnnotatedString {
    if (query.isBlank()) {
        append(text)
        return@buildAnnotatedString
    }
    var i = 0
    while (i < text.length) {
        val j = text.indexOf(query, i, ignoreCase = true)
        if (j < 0) {
            append(text.substring(i))
            break
        }
        append(text.substring(i, j))
        withStyle(SpanStyle(background = bg, color = fg, fontWeight = FontWeight.Bold)) { append(text.substring(j, j + query.length)) }
        i = j + query.length
    }
}
