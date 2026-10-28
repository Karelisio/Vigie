package com.karelisio.vigie.ui.containers

import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.combinedClickable
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
import androidx.compose.foundation.layout.navigationBars
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Clear
import androidx.compose.material.icons.rounded.NewReleases
import androidx.compose.material.icons.rounded.Star
import androidx.compose.material.icons.rounded.Update
import androidx.compose.material.icons.rounded.Inventory2
import androidx.compose.material.icons.rounded.PlayArrow
import androidx.compose.material.icons.rounded.RestartAlt
import androidx.compose.material.icons.rounded.Search
import androidx.compose.material.icons.rounded.Stop
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LoadingIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.PlainTooltip
import androidx.compose.material3.TooltipAnchorPosition
import androidx.compose.material3.TooltipBox
import androidx.compose.material3.TooltipDefaults
import androidx.compose.material3.rememberTooltipState
import androidx.compose.material3.Surface
import androidx.compose.material3.SwipeToDismissBox
import androidx.compose.material3.SwipeToDismissBoxValue
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TextField
import androidx.compose.material3.TextFieldDefaults
import androidx.compose.material3.rememberSwipeToDismissBoxState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.NavHostController
import com.karelisio.vigie.appContainer
import com.karelisio.vigie.data.Container
import com.karelisio.vigie.data.Fmt
import com.karelisio.vigie.ui.components.ConnectionBanner
import com.karelisio.vigie.ui.components.EmptyState
import com.karelisio.vigie.ui.components.RadarRefreshBox
import com.karelisio.vigie.ui.components.StatusDot
import com.karelisio.vigie.ui.components.cascade
import com.karelisio.vigie.ui.components.rememberHaptics
import com.karelisio.vigie.ui.components.shakeOnAppear
import com.karelisio.vigie.ui.components.visualStatus
import com.karelisio.vigie.ui.navigation.ContainerRoute
import com.karelisio.vigie.ui.navigation.LocalSnackbar
import com.karelisio.vigie.ui.navigation.sharedBoundsOrSelf
import com.karelisio.vigie.ui.navigation.sharedElementOrSelf
import com.karelisio.vigie.security.LocalActionGuard
import com.karelisio.vigie.ui.components.SectionTitle
import com.karelisio.vigie.ui.theme.Vigie
import com.karelisio.vigie.ui.theme.numeric
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

enum class ContainerFilter(val label: String) { All("Tous"), Running("Actifs"), Stopped("Arrêtés"), Problems("En défaut"), Updates("Mises à jour") }

class ContainersViewModel : ViewModel() {
    private val repo = appContainer.repository
    val live = repo.live
    private val _busy = MutableStateFlow(emptySet<String>())
    val busy = _busy.asStateFlow()
    private val _refreshing = MutableStateFlow(false)
    val refreshing = _refreshing.asStateFlow()
    private val _checkingImages = MutableStateFlow(false)
    val checkingImages = _checkingImages.asStateFlow()

    /** Interroge les registres pour toutes les images ; renvoie le nombre de mises à jour disponibles. */
    fun checkImages(done: (Result<Int>) -> Unit) {
        viewModelScope.launch {
            _checkingImages.value = true
            val r = runCatching { repo.checkImages().values.count { it.updateAvailable } }
            _checkingImages.value = false
            done(r)
        }
    }

    fun toggleFavorite(name: String, done: (Boolean) -> Unit) {
        viewModelScope.launch {
            val id = appContainer.servers.active?.id ?: return@launch
            done(appContainer.prefs.toggleFavorite(id, name))
        }
    }

    fun action(c: Container, action: String, done: (Result<Unit>) -> Unit) {
        viewModelScope.launch {
            _busy.update { it + c.id }
            val r = runCatching { repo.action(c.id, action); Unit }
            _busy.update { it - c.id }
            done(r)
        }
    }

    fun refresh() {
        viewModelScope.launch {
            _refreshing.value = true
            repo.reconnect()
            delay(900)
            _refreshing.value = false
        }
    }
}

/** Ordre : en défaut d'abord, puis en marche, puis arrêtés ; alphabétique ensuite. */
private fun rank(c: Container) = when {
    c.hasProblem -> 0
    c.running -> 1
    else -> 2
}

@Composable
fun ContainersScreen(nav: NavHostController, vm: ContainersViewModel = viewModel()) {
    val state by vm.live.collectAsStateWithLifecycle()
    val busy by vm.busy.collectAsStateWithLifecycle()
    val refreshing by vm.refreshing.collectAsStateWithLifecycle()
    val checkingImages by vm.checkingImages.collectAsStateWithLifecycle()
    val prefs by appContainer.prefs.flow.collectAsStateWithLifecycle(initialValue = null)
    val activeId by appContainer.servers.activeId.collectAsStateWithLifecycle()
    val favorites = prefs?.favorites?.get(activeId).orEmpty()
    val guard = LocalActionGuard.current
    var query by rememberSaveable { mutableStateOf("") }
    var filter by rememberSaveable { mutableStateOf(ContainerFilter.All) }
    var confirmStop by remember { mutableStateOf<Container?>(null) }
    val snackbar = LocalSnackbar.current
    val scope = rememberCoroutineScope()
    val haptics = rememberHaptics()

    val all = state.containers
    val list = remember(all, query, filter) {
        all.asSequence()
            .filter { c ->
                query.isBlank() || c.name.contains(query, true) || c.image.contains(query, true) || c.project.contains(query, true)
            }
            .filter { c ->
                when (filter) {
                    ContainerFilter.All -> true
                    ContainerFilter.Running -> c.running
                    ContainerFilter.Stopped -> !c.running
                    ContainerFilter.Problems -> c.hasProblem
                    ContainerFilter.Updates -> c.updateAvailable
                }
            }
            .sortedWith(compareBy<Container> { rank(it) }.thenBy { it.name.lowercase() })
            .toList()
    }
    // Les conteneurs épinglés restent en tête de liste.
    val pinned = list.filter { it.name in favorites }
    val others = list.filter { it.name !in favorites }

    fun run(c: Container, action: String) {
        val verb = mapOf("start" to "démarré", "stop" to "arrêté", "restart" to "redémarré")[action]
        val label = mapOf("start" to "Démarrer", "stop" to "Arrêter", "restart" to "Redémarrer")[action]
        scope.launch {
            if (!guard("$label ${c.name}")) return@launch
            vm.action(c, action) { r ->
                scope.launch {
                    if (r.isSuccess) {
                        haptics.confirm()
                        snackbar.showSnackbar("${c.name} $verb")
                    } else {
                        haptics.reject()
                        snackbar.showSnackbar("Échec : ${r.exceptionOrNull()?.message}")
                    }
                }
            }
        }
    }

    fun toggleFavorite(c: Container) {
        haptics.longPress()
        vm.toggleFavorite(c.name) { pinnedNow ->
            scope.launch {
                snackbar.currentSnackbarData?.dismiss()
                snackbar.showSnackbar(if (pinnedNow) "${c.name} épinglé en haut de la liste" else "${c.name} désépinglé")
            }
        }
    }

    fun checkImages() {
        haptics.tick()
        vm.checkImages { r ->
            scope.launch {
                snackbar.showSnackbar(
                    r.fold(
                        { n -> if (n == 0) "Toutes les images sont à jour" else if (n == 1) "1 mise à jour d'image disponible" else "$n mises à jour d'images disponibles" },
                        { "Vérification impossible : ${it.message}" },
                    ),
                )
                if ((r.getOrNull() ?: 0) > 0) filter = ContainerFilter.Updates
            }
        }
    }

    @Composable
    fun androidx.compose.foundation.lazy.LazyItemScope.Row_(index: Int, c: Container) {
        SwipeableContainer(
            c = c,
            busy = c.id in busy,
            favorite = c.name in favorites,
            modifier = Modifier
                .padding(horizontal = 16.dp)
                .animateItem()
                .cascade(index, key = "list"),
            onOpen = { nav.navigate(ContainerRoute(c.id, c.name)) },
            onLongPress = { toggleFavorite(c) },
            onRestart = { run(c, "restart") },
            onToggle = { if (c.running) confirmStop = c else run(c, "start") },
        )
    }

    val bottom = WindowInsets.navigationBars.asPaddingValues().calculateBottomPadding() + 96.dp
    RadarRefreshBox(refreshing, vm::refresh, Modifier.fillMaxSize()) {
        LazyColumn(
            Modifier.fillMaxSize(),
            contentPadding = PaddingValues(bottom = bottom),
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            item(key = "title") {
                Row(
                    Modifier.statusBarsPadding().padding(start = 20.dp, end = 12.dp, top = 16.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Column(Modifier.weight(1f)) {
                        Text("Conteneurs", style = MaterialTheme.typography.headlineLargeEmphasized)
                        Text(
                            "${all.count { it.running }} actifs sur ${all.size}",
                            style = MaterialTheme.typography.bodyMedium,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                    TooltipBox(
                        positionProvider = TooltipDefaults.rememberTooltipPositionProvider(TooltipAnchorPosition.Below),
                        tooltip = { PlainTooltip { Text("Vérifier les mises à jour d'images") } },
                        state = rememberTooltipState(),
                    ) {
                        IconButton(onClick = ::checkImages, enabled = !checkingImages) {
                            if (checkingImages) LoadingIndicator(Modifier.size(28.dp))
                            else Icon(Icons.Rounded.Update, "Vérifier les mises à jour d'images")
                        }
                    }
                }
            }
            item(key = "banner") { ConnectionBanner(state) }
            item(key = "search") {
                TextField(
                    value = query,
                    onValueChange = { query = it },
                    placeholder = { Text("Rechercher un conteneur, une image…") },
                    leadingIcon = { Icon(Icons.Rounded.Search, null) },
                    trailingIcon = {
                        if (query.isNotEmpty()) IconButton(onClick = { query = "" }) { Icon(Icons.Rounded.Clear, "Effacer") }
                    },
                    singleLine = true,
                    shape = RoundedCornerShape(50),
                    colors = TextFieldDefaults.colors(
                        focusedIndicatorColor = Color.Transparent,
                        unfocusedIndicatorColor = Color.Transparent,
                        focusedContainerColor = MaterialTheme.colorScheme.surfaceContainerHigh,
                        unfocusedContainerColor = MaterialTheme.colorScheme.surfaceContainerHigh,
                    ),
                    modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp),
                )
            }
            item(key = "filters") {
                LazyRow(contentPadding = PaddingValues(horizontal = 16.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    val updates = all.count { it.updateAvailable }
                    val shown = ContainerFilter.entries.filter { it != ContainerFilter.Updates || updates > 0 || filter == it }
                    items(shown, key = { it.name }) { f ->
                        val n = when (f) {
                            ContainerFilter.All -> all.size
                            ContainerFilter.Running -> all.count { it.running }
                            ContainerFilter.Stopped -> all.count { !it.running }
                            ContainerFilter.Problems -> all.count { it.hasProblem }
                            ContainerFilter.Updates -> updates
                        }
                        FilterChip(
                            selected = filter == f,
                            onClick = { haptics.tick(); filter = f },
                            label = { Text("${f.label} · $n") },
                        )
                    }
                }
            }
            if (list.isEmpty() && state.loaded) {
                item(key = "empty") {
                    EmptyState(
                        Icons.Rounded.Inventory2,
                        if (all.isEmpty()) "Aucun conteneur" else "Aucun résultat",
                        if (all.isEmpty()) "L'agent ne voit encore aucun conteneur." else "Essaie un autre filtre ou une autre recherche.",
                    )
                }
            }
            val sections = pinned.isNotEmpty() && others.isNotEmpty()
            if (sections) item(key = "h-pinned") { SectionTitle("Épinglés", Modifier.animateItem().padding(horizontal = 20.dp)) }
            items(pinned, key = { it.id }) { c -> Row_(pinned.indexOf(c), c) }
            if (sections) item(key = "h-others") { SectionTitle("Autres", Modifier.animateItem().padding(horizontal = 20.dp)) }
            items(others, key = { it.id }) { c -> Row_(pinned.size + others.indexOf(c), c) }
        }
    }

    confirmStop?.let { c ->
        AlertDialog(
            onDismissRequest = { confirmStop = null },
            icon = { Icon(Icons.Rounded.Stop, null) },
            title = { Text("Arrêter ${c.name} ?") },
            text = { Text("Le conteneur restera arrêté jusqu'à ce que tu le redémarres. Vigie ne le signalera pas comme une panne.") },
            confirmButton = { TextButton(onClick = { confirmStop = null; run(c, "stop") }) { Text("Arrêter") } },
            dismissButton = { TextButton(onClick = { confirmStop = null }) { Text("Annuler") } },
        )
    }
}

@Composable
private fun SwipeableContainer(
    c: Container,
    busy: Boolean,
    favorite: Boolean,
    modifier: Modifier,
    onOpen: () -> Unit,
    onLongPress: () -> Unit,
    onRestart: () -> Unit,
    onToggle: () -> Unit,
) {
    val state = rememberSwipeToDismissBoxState()
    val scope = rememberCoroutineScope()
    val haptics = rememberHaptics()
    val status = Vigie.status
    LaunchedEffect(state) {
        snapshotFlow { state.targetValue }.distinctUntilChanged().collect {
            if (it != SwipeToDismissBoxValue.Settled) haptics.threshold()
        }
    }
    SwipeToDismissBox(
        state = state,
        modifier = modifier.clip(RoundedCornerShape(24.dp)),
        gesturesEnabled = !busy && !c.self,
        onDismiss = { value ->
            when (value) {
                SwipeToDismissBoxValue.StartToEnd -> onRestart()
                SwipeToDismissBoxValue.EndToStart -> onToggle()
                else -> Unit
            }
            scope.launch { state.reset() }
        },
        backgroundContent = {
            val dir = state.dismissDirection
            val reached = state.targetValue != SwipeToDismissBoxValue.Settled
            val scheme = MaterialTheme.colorScheme
            val (bg, fg) = when (dir) {
                SwipeToDismissBoxValue.StartToEnd -> scheme.primaryContainer to scheme.onPrimaryContainer
                SwipeToDismissBoxValue.EndToStart -> if (c.running) scheme.errorContainer to scheme.onErrorContainer
                else status.okContainer to status.onOkContainer
                else -> Color.Transparent to Color.Transparent
            }
            val color by animateColorAsState(if (reached) bg else bg.copy(alpha = 0.55f), label = "swipeBg")
            val scale by animateFloatAsState(if (reached) 1.25f else 0.85f, label = "swipeIcon")
            Row(
                Modifier.fillMaxSize().background(color).padding(horizontal = 24.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = if (dir == SwipeToDismissBoxValue.EndToStart) Arrangement.End else Arrangement.Start,
            ) {
                val (icon, label) = when (dir) {
                    SwipeToDismissBoxValue.StartToEnd -> Icons.Rounded.RestartAlt to "Redémarrer"
                    SwipeToDismissBoxValue.EndToStart -> if (c.running) Icons.Rounded.Stop to "Arrêter" else Icons.Rounded.PlayArrow to "Démarrer"
                    else -> null to ""
                }
                if (icon != null) {
                    if (dir == SwipeToDismissBoxValue.EndToStart) Text(label, color = fg, style = MaterialTheme.typography.labelLarge)
                    Icon(icon, null, tint = fg, modifier = Modifier.padding(horizontal = 8.dp).graphicsLayer { scaleX = scale; scaleY = scale })
                    if (dir == SwipeToDismissBoxValue.StartToEnd) Text(label, color = fg, style = MaterialTheme.typography.labelLarge)
                }
            }
        },
    ) {
        ContainerCard(c, busy, onOpen, onLongPress, favorite)
    }
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
fun ContainerCard(c: Container, busy: Boolean, onOpen: () -> Unit, onLongPress: (() -> Unit)? = null, favorite: Boolean = false) {
    val status = Vigie.status
    val vs = c.visualStatus()
    val container by animateColorAsState(
        if (c.hasProblem) status.criticalContainer.copy(alpha = 0.55f) else MaterialTheme.colorScheme.surfaceContainer,
        label = "cardColor",
    )
    val shape = RoundedCornerShape(24.dp)
    Surface(
        color = container,
        shape = shape,
        modifier = Modifier
            .fillMaxWidth()
            .sharedBoundsOrSelf("card-${c.id}")
            .shakeOnAppear(c.hasProblem, c.problem)
            .clip(shape)
            .combinedClickable(onClick = onOpen, onLongClick = onLongPress, onLongClickLabel = if (favorite) "Désépingler" else "Épingler"),
    ) {
        Row(Modifier.padding(horizontal = 12.dp, vertical = 14.dp), verticalAlignment = Alignment.CenterVertically) {
            StatusDot(vs, Modifier.sharedElementOrSelf("dot-${c.id}"), size = 11.dp)
            Spacer(Modifier.width(8.dp))
            Column(Modifier.weight(1f)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(
                        c.name,
                        style = MaterialTheme.typography.titleMedium,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                        modifier = Modifier.weight(1f, fill = false).sharedBoundsOrSelf("name-${c.id}", text = true),
                    )
                    if (favorite) {
                        Icon(
                            Icons.Rounded.Star, "Épinglé",
                            tint = MaterialTheme.colorScheme.tertiary,
                            modifier = Modifier.padding(start = 4.dp).size(16.dp),
                        )
                    }
                    if (c.updateAvailable) {
                        Surface(
                            color = MaterialTheme.colorScheme.tertiaryContainer,
                            shape = RoundedCornerShape(50),
                            modifier = Modifier.padding(start = 6.dp),
                        ) {
                            Row(Modifier.padding(horizontal = 6.dp, vertical = 2.dp), verticalAlignment = Alignment.CenterVertically) {
                                Icon(Icons.Rounded.NewReleases, null, Modifier.size(12.dp), tint = MaterialTheme.colorScheme.onTertiaryContainer)
                                Text(
                                    " MAJ",
                                    style = MaterialTheme.typography.labelSmall,
                                    color = MaterialTheme.colorScheme.onTertiaryContainer,
                                )
                            }
                        }
                    }
                }
                Text(
                    c.shortImage,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
                Text(
                    Fmt.stateLabel(c) + if (c.running && c.startedAt > 0) " · depuis ${Fmt.since(c.startedAt)}" else "",
                    style = MaterialTheme.typography.labelMedium,
                    color = if (c.hasProblem) status.critical else MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                )
            }
            Box(Modifier.padding(start = 8.dp), contentAlignment = Alignment.CenterEnd) {
                if (busy) {
                    LoadingIndicator(Modifier.size(36.dp))
                } else if (c.running) {
                    Column(horizontalAlignment = Alignment.End) {
                        Text(Fmt.pct(c.cpu, if (c.cpu < 10) 1 else 0), style = MaterialTheme.typography.labelLarge.numeric())
                        Text(
                            Fmt.bytes(c.memUsed),
                            style = MaterialTheme.typography.labelMedium.numeric(),
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                }
            }
        }
    }
}
