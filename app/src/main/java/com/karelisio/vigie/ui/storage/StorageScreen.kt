package com.karelisio.vigie.ui.storage

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
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.TrendingFlat
import androidx.compose.material.icons.automirrored.rounded.TrendingUp
import androidx.compose.material.icons.rounded.CleaningServices
import androidx.compose.material.icons.rounded.Cloud
import androidx.compose.material.icons.rounded.Folder
import androidx.compose.material.icons.rounded.Refresh
import androidx.compose.material.icons.rounded.Schedule
import androidx.compose.material.icons.rounded.Speed
import androidx.compose.material.icons.rounded.Storage
import androidx.compose.material3.AssistChip
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Icon
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.LoadingIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.SheetValue
import androidx.compose.material3.rememberBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.style.TextOverflow
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
import com.karelisio.vigie.data.StorageDisk
import com.karelisio.vigie.data.StorageFmt
import com.karelisio.vigie.data.StorageReport
import com.karelisio.vigie.data.StorageVolume
import com.karelisio.vigie.data.refreshStorage
import com.karelisio.vigie.data.storage
import com.karelisio.vigie.data.storageState
import com.karelisio.vigie.data.versionAtLeast
import com.karelisio.vigie.ui.components.Chip
import com.karelisio.vigie.ui.components.ChartPoint
import com.karelisio.vigie.ui.components.EmptyState
import com.karelisio.vigie.ui.components.MetricChart
import com.karelisio.vigie.ui.components.ScreenHeader
import com.karelisio.vigie.ui.components.SectionCard
import com.karelisio.vigie.ui.components.Sparkline
import com.karelisio.vigie.ui.components.cascade
import com.karelisio.vigie.ui.components.rememberHaptics
import com.karelisio.vigie.ui.components.rememberNow
import com.karelisio.vigie.ui.navigation.CleanupRoute
import com.karelisio.vigie.ui.navigation.ContainerRoute
import com.karelisio.vigie.ui.navigation.LocalSnackbar
import com.karelisio.vigie.ui.theme.Vigie
import com.karelisio.vigie.ui.theme.numeric
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch

/** Préavis proposés pour l'alerte « disque bientôt plein » (0 : jamais). */
private val forecastChoices = listOf(0, 7, 14, 30)

/** Seuils proposés pour l'alerte de croissance d'un volume, en Go par jour (0 : jamais). */
private val growthChoices = listOf(0.0, 1.0, 2.0, 5.0, 10.0, 20.0)

class StorageViewModel : ViewModel() {
    private val repo = appContainer.repository
    val live = repo.live
    val report = repo.storageState().stateIn(viewModelScope, SharingStarted.Eagerly, null)
    private val _error = MutableStateFlow<String?>(null)
    val error = _error.asStateFlow()
    private val _settings = MutableStateFlow<AgentSettings?>(null)
    val settings = _settings.asStateFlow()
    private var following: Job? = null

    init {
        load()
    }

    fun load() {
        viewModelScope.launch {
            _error.value = null
            runCatching { repo.storage() }.onSuccess(::follow).onFailure { _error.value = it.message }
        }
    }

    /** Réglages des alertes (accès complet seulement). */
    fun loadSettings() {
        viewModelScope.launch { runCatching { repo.settings() }.onSuccess { _settings.value = it } }
    }

    suspend fun refresh(): Result<StorageReport> = runCatching { repo.refreshStorage() }.onSuccess(::follow)

    suspend fun saveSettings(change: (AgentSettings) -> AgentSettings): Result<AgentSettings> {
        val current = _settings.value ?: return Result.failure(IllegalStateException("Réglages pas encore chargés"))
        return runCatching { repo.saveSettings(change(current)) }.onSuccess { _settings.value = it }
    }

    /** Calcul des volumes en cours sur l'agent : l'état est relu toutes les 3 s, 10 min au plus. */
    private fun follow(r: StorageReport) {
        if (!r.refreshing || following?.isActive == true) return
        following = viewModelScope.launch {
            val until = System.currentTimeMillis() + 10 * 60_000
            while (System.currentTimeMillis() < until) {
                delay(3_000)
                val next = runCatching { repo.storage() }.getOrNull() ?: continue
                if (!next.refreshing) break
            }
        }
    }
}

/** Stockage : prévision du disque, volumes Docker et leur croissance, alertes (agent 0.6.0). */
@Composable
fun StorageScreen(nav: NavHostController, vm: StorageViewModel = viewModel()) {
    val report by vm.report.collectAsStateWithLifecycle()
    val error by vm.error.collectAsStateWithLifecycle()
    val settings by vm.settings.collectAsStateWithLifecycle()
    val live by vm.live.collectAsStateWithLifecycle()
    val unsupported = live.agentVersion.isNotEmpty() && !versionAtLeast(live.agentVersion, 0, 6)
    var selected by remember { mutableStateOf<String?>(null) }
    var starting by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    val snackbar = LocalSnackbar.current
    val haptics = rememberHaptics()
    val bottom = WindowInsets.navigationBars.asPaddingValues().calculateBottomPadding() + 32.dp

    LaunchedEffect(live.readOnly, live.agentVersion) {
        if (!live.readOnly && versionAtLeast(live.agentVersion, 0, 6)) vm.loadSettings()
    }

    fun recompute() {
        haptics.tick()
        starting = true
        scope.launch {
            val r = vm.refresh()
            starting = false
            r.onFailure {
                haptics.reject()
                snackbar.showSnackbar(if ((it as? ApiException)?.code == 429) it.message.orEmpty() else "Échec : ${it.message}")
            }
        }
    }

    fun save(change: (AgentSettings) -> AgentSettings) {
        scope.launch {
            vm.saveSettings(change).fold(
                { haptics.confirm() },
                {
                    haptics.reject()
                    snackbar.showSnackbar("Échec : ${it.message}")
                },
            )
        }
    }

    LazyColumn(
        Modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = 16.dp, end = 16.dp, bottom = bottom),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        item { ScreenHeader("Stockage", nav) }
        val r = report
        when {
            unsupported -> item {
                EmptyState(
                    Icons.Rounded.Storage, "Indisponible sur cet agent",
                    "Mets l'agent à jour (0.6.0 ou plus) pour suivre la prévision du disque et la taille des volumes.",
                )
            }
            r == null && error != null -> item {
                EmptyState(Icons.Rounded.Cloud, "Stockage indisponible", error.orEmpty()) {
                    OutlinedButton(onClick = vm::load, shapes = ButtonDefaults.shapes()) { Text("Réessayer") }
                }
            }
            r == null -> item {
                Box(Modifier.fillMaxWidth().padding(48.dp), contentAlignment = Alignment.Center) { LoadingIndicator() }
            }
            else -> {
                item { DiskCard(r.disk, Modifier.cascade(0)) }
                item {
                    DockerCard(
                        r, Modifier.cascade(1),
                        onCleanup = if (live.readOnly) null else ({ nav.navigate(CleanupRoute) }),
                    )
                }
                item {
                    VolumesHeader(
                        r, busy = starting || r.refreshing, Modifier.cascade(2),
                        onRefresh = if (live.readOnly) null else ::recompute,
                    )
                }
                if (r.volumes.isEmpty()) {
                    item {
                        EmptyState(
                            Icons.Rounded.Folder,
                            if (r.volumesAt == 0L) "Pas encore calculé" else "Aucun volume nommé",
                            if (r.volumesAt == 0L) "L'agent mesure les volumes quelques minutes après son démarrage, puis toutes les 6 heures."
                            else "Les volumes déclarés dans tes stacks apparaîtront ici avec leur taille.",
                            Modifier.cascade(3),
                        )
                    }
                } else {
                    itemsIndexed(r.volumes, key = { _, v -> v.name }) { i, v ->
                        VolumeRow(v, Modifier.animateItem().cascade(3 + i)) { haptics.tick(); selected = v.name }
                    }
                }
                val s = settings
                if (!live.readOnly && s != null) {
                    item { AlertsCard(s, Modifier.cascade(4 + r.volumes.size), ::save) }
                }
            }
        }
    }

    val volume = report?.volumes?.firstOrNull { it.name == selected }
    if (volume != null) {
        VolumeSheet(volume, onDismiss = { selected = null }) { name ->
            selected = null
            nav.navigate(ContainerRoute(live.containers.firstOrNull { it.name == name }?.id ?: name, name))
        }
    }
}

/** Couleur de la prévision : rouge sous 7 jours, orange sous 30. */
@Composable
private fun forecastColor(d: StorageDisk): Color {
    val left = d.daysLeft
    return when {
        d.status != StorageDisk.STATUS_OK || left == null -> MaterialTheme.colorScheme.onSurface
        left < 7 -> Vigie.status.critical
        left < 30 -> Vigie.status.warn
        else -> MaterialTheme.colorScheme.onSurface
    }
}

@Composable
private fun DiskCard(d: StorageDisk, modifier: Modifier = Modifier) {
    val scheme = MaterialTheme.colorScheme
    val pct = if (d.total > 0) d.used * 100.0 / d.total else 0.0
    val barColor = Vigie.status.forLevel(pct, 90.0)
    SectionCard(
        modifier,
        title = "Disque ${d.mount}",
        action = { Chip("${StorageFmt.size(d.free)} libres", scheme.secondaryContainer, scheme.onSecondaryContainer) },
    ) {
        Row(verticalAlignment = Alignment.Bottom) {
            Text(StorageFmt.size(d.used), style = MaterialTheme.typography.headlineMediumEmphasized.numeric())
            Spacer(Modifier.width(8.dp))
            Text(
                "utilisés sur ${StorageFmt.size(d.total)}",
                style = MaterialTheme.typography.bodyMedium,
                color = scheme.onSurfaceVariant,
                modifier = Modifier.weight(1f).padding(bottom = 4.dp),
            )
            Text(Fmt.pct(pct), style = MaterialTheme.typography.titleLarge.numeric(), color = barColor)
        }
        Spacer(Modifier.height(10.dp))
        LinearProgressIndicator(
            progress = { (pct / 100).toFloat().coerceIn(0f, 1f) },
            modifier = Modifier.fillMaxWidth().height(8.dp),
            color = barColor,
        )
        Spacer(Modifier.height(16.dp))
        val color = forecastColor(d)
        Row(verticalAlignment = Alignment.Top) {
            Icon(
                when (d.status) {
                    StorageDisk.STATUS_OK -> Icons.AutoMirrored.Rounded.TrendingUp
                    StorageDisk.STATUS_STABLE -> Icons.AutoMirrored.Rounded.TrendingFlat
                    else -> Icons.Rounded.Schedule
                },
                null,
                tint = if (color == scheme.onSurface) scheme.onSurfaceVariant else color,
            )
            Spacer(Modifier.width(12.dp))
            Column {
                Text(StorageFmt.forecast(d), style = MaterialTheme.typography.titleMedium, color = color)
                Text(StorageFmt.forecastDetail(d), style = MaterialTheme.typography.bodySmall, color = scheme.onSurfaceVariant)
            }
        }
        Spacer(Modifier.height(16.dp))
        ForecastChart(d, if (color == scheme.onSurface) scheme.tertiary else color)
    }
}

@Composable
private fun DockerCard(r: StorageReport, modifier: Modifier = Modifier, onCleanup: (() -> Unit)?) {
    SectionCard(modifier, title = "Docker") {
        UsageLine("Images", r.images.count, r.images.size)
        UsageLine("Cache de construction", r.buildCache.count, r.buildCache.size)
        UsageLine("Volumes anonymes", r.anonymous.count, r.anonymous.size)
        if (onCleanup != null) {
            Spacer(Modifier.height(12.dp))
            OutlinedButton(onClick = onCleanup, shapes = ButtonDefaults.shapes(), modifier = Modifier.fillMaxWidth()) {
                Icon(Icons.Rounded.CleaningServices, null)
                Spacer(Modifier.width(8.dp))
                Text("Nettoyer le disque")
            }
        }
    }
}

@Composable
private fun UsageLine(label: String, count: Int, size: Long) {
    Row(Modifier.fillMaxWidth().padding(vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
        Text(label, style = MaterialTheme.typography.bodyMedium, modifier = Modifier.weight(1f))
        Text("$count", style = MaterialTheme.typography.bodySmall.numeric(), color = MaterialTheme.colorScheme.onSurfaceVariant)
        Spacer(Modifier.width(12.dp))
        Text(StorageFmt.size(size), style = MaterialTheme.typography.titleSmall.numeric())
    }
}

@Composable
private fun VolumesHeader(r: StorageReport, busy: Boolean, modifier: Modifier = Modifier, onRefresh: (() -> Unit)?) {
    val now = rememberNow(30_000)
    val scheme = MaterialTheme.colorScheme
    Column(modifier.padding(top = 8.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f)) {
                Text("Volumes", style = MaterialTheme.typography.titleMedium)
                Text(
                    when {
                        busy -> "Calcul en cours…"
                        r.volumesAt == 0L -> "Pas encore calculés"
                        else -> "Calculés ${Fmt.ago(r.volumesAt, now)}"
                    },
                    style = MaterialTheme.typography.bodySmall,
                    color = scheme.onSurfaceVariant,
                )
            }
            if (onRefresh != null) {
                OutlinedButton(onClick = onRefresh, enabled = !busy, shapes = ButtonDefaults.shapes()) {
                    if (busy) LoadingIndicator(Modifier.size(20.dp)) else Icon(Icons.Rounded.Refresh, null, Modifier.size(18.dp))
                    Spacer(Modifier.width(8.dp))
                    Text("Recalculer")
                }
            }
        }
        AnimatedVisibility(r.error.isNotEmpty()) {
            Text(r.error, style = MaterialTheme.typography.bodySmall, color = Vigie.status.critical, modifier = Modifier.padding(top = 4.dp))
        }
    }
}

/** Valeurs d'un mini-graphique de taille : écart au minimum, pour que la croissance se voie. */
internal fun sparkValues(history: List<Long>): Pair<List<Float>, Float> {
    if (history.isEmpty()) return emptyList<Float>() to 1f
    val min = history.min()
    val range = (history.max() - min).coerceAtLeast(64L shl 20)
    return history.map { (it - min).toFloat() } to range.toFloat()
}

@Composable
private fun VolumeRow(v: StorageVolume, modifier: Modifier = Modifier, onClick: () -> Unit) {
    val scheme = MaterialTheme.colorScheme
    val status = Vigie.status
    Surface(onClick = onClick, shape = RoundedCornerShape(24.dp), color = scheme.surfaceContainer, modifier = modifier.fillMaxWidth()) {
        Row(Modifier.padding(16.dp), verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f)) {
                Text(v.name, style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                Text(
                    StorageFmt.volumeGrowth(v),
                    style = MaterialTheme.typography.bodySmall,
                    color = if (v.fast) status.critical else scheme.onSurfaceVariant,
                    maxLines = 1,
                )
                if (v.containers.isNotEmpty()) {
                    Text(
                        v.containers.joinToString(", "),
                        style = MaterialTheme.typography.labelSmall,
                        color = scheme.onSurfaceVariant,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
            }
            if (v.history.size >= 2) {
                val (values, max) = sparkValues(v.history.map { it.size })
                Spacer(Modifier.width(12.dp))
                Sparkline(values, if (v.fast) status.critical else scheme.primary, Modifier.width(64.dp).height(32.dp), maxValue = max)
            }
            Spacer(Modifier.width(12.dp))
            Column(horizontalAlignment = Alignment.End) {
                Text(StorageFmt.size(v.size), style = MaterialTheme.typography.titleMedium.numeric())
                if (v.fast) {
                    Spacer(Modifier.height(4.dp))
                    Chip("Rapide", status.criticalContainer, status.onCriticalContainer, icon = Icons.Rounded.Speed)
                }
            }
        }
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun VolumeSheet(v: StorageVolume, onDismiss: () -> Unit, onContainer: (String) -> Unit) {
    val sheet = rememberBottomSheetState(initialValue = SheetValue.Hidden, enabledValues = setOf(SheetValue.Hidden, SheetValue.Expanded))
    val scheme = MaterialTheme.colorScheme
    val status = Vigie.status
    ModalBottomSheet(onDismissRequest = onDismiss, sheetState = sheet) {
        Column(Modifier.fillMaxWidth().navigationBarsPadding().padding(start = 24.dp, end = 24.dp, bottom = 16.dp)) {
            Text(v.name, style = MaterialTheme.typography.titleLarge, maxLines = 2, overflow = TextOverflow.Ellipsis)
            if (v.project.isNotEmpty()) {
                Text("Stack ${v.project}", style = MaterialTheme.typography.bodySmall, color = scheme.onSurfaceVariant)
            }
            Spacer(Modifier.height(16.dp))
            Row(verticalAlignment = Alignment.Bottom) {
                Text(StorageFmt.size(v.size), style = MaterialTheme.typography.headlineMediumEmphasized.numeric())
                Spacer(Modifier.width(12.dp))
                if (v.fast) Chip("Croissance rapide", status.criticalContainer, status.onCriticalContainer, icon = Icons.Rounded.Speed)
            }
            Spacer(Modifier.height(12.dp))
            GrowthLine("Sur 24 h", v.growthDay?.let { StorageFmt.growth(it) })
            GrowthLine("Sur 7 jours", v.growthWeek?.let { StorageFmt.growth(it, "semaine") })
            Spacer(Modifier.height(16.dp))
            Text("Évolution sur 7 jours", style = MaterialTheme.typography.labelLarge)
            Spacer(Modifier.height(8.dp))
            if (v.history.size >= 2) {
                MetricChart(
                    points = v.history.map { ChartPoint(it.ts, it.size.toDouble()) },
                    color = if (v.fast) status.critical else scheme.primary,
                    spanMs = 7 * 86_400_000L,
                    height = 160.dp,
                    format = { StorageFmt.size(it.toLong()) },
                )
            } else {
                Text(
                    "L'historique apparaît après le prochain calcul (toutes les 6 heures).",
                    style = MaterialTheme.typography.bodySmall,
                    color = scheme.onSurfaceVariant,
                )
            }
            Spacer(Modifier.height(16.dp))
            Text("Conteneurs", style = MaterialTheme.typography.labelLarge)
            if (v.containers.isEmpty()) {
                Text("Aucun conteneur ne l'utilise.", style = MaterialTheme.typography.bodySmall, color = scheme.onSurfaceVariant)
            } else {
                FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    v.containers.forEach { name -> AssistChip(onClick = { onContainer(name) }, label = { Text(name) }) }
                }
            }
        }
    }
}

@Composable
private fun GrowthLine(label: String, value: String?) {
    Row(Modifier.fillMaxWidth().padding(vertical = 2.dp)) {
        Text(label, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.weight(1f))
        Text(value ?: "pas encore de recul", style = MaterialTheme.typography.bodyMedium.numeric())
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun AlertsCard(s: AgentSettings, modifier: Modifier = Modifier, onChange: ((AgentSettings) -> AgentSettings) -> Unit) {
    val haptics = rememberHaptics()
    SectionCard(modifier, title = "Alertes") {
        AlertChoices(
            Icons.AutoMirrored.Rounded.TrendingUp, "Disque bientôt plein",
            "Prévenir quand, au rythme actuel, il sera plein dans moins de :",
            choices = (forecastChoices + s.diskForecastDays).distinct().sorted(),
            selected = s.diskForecastDays,
            label = { if (it == 0) "Jamais" else "$it jours" },
        ) { v -> haptics.tick(); onChange { it.copy(diskForecastDays = v) } }
        Spacer(Modifier.height(16.dp))
        AlertChoices(
            Icons.Rounded.Speed, "Volume qui grossit vite",
            "Prévenir quand un volume prend plus de (par jour) :",
            choices = (growthChoices + s.volumeGrowthGb).distinct().sorted(),
            selected = s.volumeGrowthGb,
            label = { if (it == 0.0) "Jamais" else StorageFmt.gb(it) },
        ) { v -> haptics.tick(); onChange { it.copy(volumeGrowthGb = v) } }
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun <T> AlertChoices(
    icon: ImageVector,
    title: String,
    help: String,
    choices: List<T>,
    selected: T,
    label: (T) -> String,
    onPick: (T) -> Unit,
) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        Icon(icon, null, Modifier.size(20.dp), tint = MaterialTheme.colorScheme.onSurfaceVariant)
        Spacer(Modifier.width(8.dp))
        Text(title, style = MaterialTheme.typography.labelLarge)
    }
    Text(help, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
    Spacer(Modifier.height(4.dp))
    FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        choices.forEach { c ->
            FilterChip(selected = c == selected, onClick = { if (c != selected) onPick(c) }, label = { Text(label(c)) })
        }
    }
}
