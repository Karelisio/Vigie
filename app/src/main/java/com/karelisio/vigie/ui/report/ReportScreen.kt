package com.karelisio.vigie.ui.report

import androidx.compose.animation.core.Animatable
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
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
import androidx.compose.material.icons.automirrored.rounded.ArrowForward
import androidx.compose.material.icons.rounded.ArrowDownward
import androidx.compose.material.icons.rounded.ArrowUpward
import androidx.compose.material.icons.rounded.CheckCircle
import androidx.compose.material.icons.rounded.CloudOff
import androidx.compose.material.icons.rounded.ErrorOutline
import androidx.compose.material.icons.rounded.WarningAmber
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LoadingIndicator
import androidx.compose.material3.MaterialShapes
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.toShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.NavHostController
import com.karelisio.vigie.appContainer
import com.karelisio.vigie.data.ApiException
import com.karelisio.vigie.data.Container
import com.karelisio.vigie.data.Fmt
import com.karelisio.vigie.data.ReportContainers
import com.karelisio.vigie.data.ReportDisk
import com.karelisio.vigie.data.ReportIncidents
import com.karelisio.vigie.data.ReportServices
import com.karelisio.vigie.data.ReportTraffic
import com.karelisio.vigie.data.WeeklyReport
import com.karelisio.vigie.ui.components.Chip
import com.karelisio.vigie.ui.components.CircularGauge
import com.karelisio.vigie.ui.components.EmptyState
import com.karelisio.vigie.ui.components.SectionCard
import com.karelisio.vigie.ui.components.Springs
import com.karelisio.vigie.ui.components.StatusDot
import com.karelisio.vigie.ui.components.cascade
import com.karelisio.vigie.ui.components.rememberHaptics
import com.karelisio.vigie.ui.home.OracleCard
import com.karelisio.vigie.ui.navigation.ContainerRoute
import com.karelisio.vigie.ui.navigation.ContainersRoute
import com.karelisio.vigie.ui.navigation.navigateTab
import com.karelisio.vigie.ui.theme.Vigie
import com.karelisio.vigie.ui.theme.numeric
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import kotlin.math.abs

class ReportViewModel : ViewModel() {
    private val repo = appContainer.repository
    private val _report = MutableStateFlow<WeeklyReport?>(null)
    val report = _report.asStateFlow()
    private val _error = MutableStateFlow<String?>(null)
    val error = _error.asStateFlow()

    init {
        load()
    }

    fun load() {
        viewModelScope.launch {
            _error.value = null
            runCatching { repo.report() }
                .onSuccess { _report.value = it }
                .onFailure {
                    _error.value = if (it is ApiException && it.code == 404) {
                        "Ton agent est trop ancien pour le rapport de la semaine. Mets-le à jour depuis Réglages."
                    } else it.message
                }
        }
    }
}

@Composable
fun ReportScreen(nav: NavHostController, vm: ReportViewModel = viewModel()) {
    val report by vm.report.collectAsStateWithLifecycle()
    val error by vm.error.collectAsStateWithLifecycle()
    val live by appContainer.repository.live.collectAsStateWithLifecycle()
    ReportContent(
        report = report,
        error = error,
        containers = live.containers,
        diskThreshold = live.thresholds.disk,
        onBack = { nav.popBackStack() },
        onRetry = vm::load,
        onContainer = { name -> nav.navigate(ContainerRoute(live.containers.firstOrNull { it.name == name }?.id ?: name, name)) },
        onUpdates = { nav.navigateTab(ContainersRoute) },
    )
}

/** Contenu du rapport, sans état (prévisualisable). */
@Composable
internal fun ReportContent(
    report: WeeklyReport?,
    error: String?,
    containers: List<Container>,
    diskThreshold: Double,
    onBack: () -> Unit,
    onRetry: () -> Unit,
    onContainer: (String) -> Unit,
    onUpdates: () -> Unit,
) {
    val bottom = WindowInsets.navigationBars.asPaddingValues().calculateBottomPadding() + 32.dp
    val r = report
    LazyColumn(
        Modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = 16.dp, end = 16.dp, bottom = bottom),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        item {
            Row(Modifier.statusBarsPadding().padding(top = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Rounded.ArrowBack, "Retour") }
                Column {
                    Text("Rapport de la semaine", style = MaterialTheme.typography.headlineSmallEmphasized)
                    if (r != null) {
                        Text(period(r), style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                }
            }
        }
        when {
            r == null && error != null -> item {
                EmptyState(Icons.Rounded.CloudOff, "Rapport indisponible", error.orEmpty()) {
                    OutlinedButton(onClick = onRetry) { Text("Réessayer") }
                }
            }
            r == null -> item {
                Box(Modifier.fillMaxWidth().padding(48.dp), contentAlignment = Alignment.Center) { LoadingIndicator() }
            }
            else -> {
                item { Verdict(r, Modifier.cascade(0)) }
                item { ServicesSection(r.services, Modifier.cascade(1)) }
                item { IncidentsSection(r.incidents, Modifier.cascade(2)) }
                item { ContainersSection(r.containers, containers, Modifier.cascade(3), onContainer) }
                item { UpdatesSection(r.updates, Modifier.cascade(4), onUpdates) }
                item { HostSection(r.disk, r.traffic, r.days, diskThreshold, Modifier.cascade(5)) }
                r.oracle?.let { item { OracleCard(it, Modifier.cascade(6)) } }
            }
        }
    }
}

private fun period(r: WeeklyReport): String {
    val f = SimpleDateFormat("d MMMM", Locale.FRANCE)
    return "Du ${f.format(Date(r.from))} au ${f.format(Date(r.to))}"
}

/** Ton de la disponibilité d'un service. */
private fun uptimeTone(v: Double): String = when {
    v < 0 -> "neutral"
    v >= 99.9 -> "ok"
    v >= 99 -> "warning"
    else -> "critical"
}

/** Bilan en tête : forme expressive qui éclot en ressort, couleur selon la semaine. */
@Composable
private fun Verdict(r: WeeklyReport, modifier: Modifier) {
    val status = Vigie.status
    val crashes = r.containers.crashes
    val incidents = r.incidents.total
    val (tone, title) = when {
        incidents == 0 && crashes == 0 -> "ok" to "Semaine calme"
        incidents <= 3 && crashes <= 2 -> "warning" to "Quelques incidents"
        else -> "critical" to "Semaine agitée"
    }
    val (shape, icon) = when (tone) {
        "ok" -> MaterialShapes.Cookie9Sided to Icons.Rounded.CheckCircle
        "warning" -> MaterialShapes.SoftBurst to Icons.Rounded.WarningAmber
        else -> MaterialShapes.Burst to Icons.Rounded.ErrorOutline
    }
    val reduced = Vigie.reducedMotion
    val bloom = remember { Animatable(if (reduced) 1f else 0.6f) }
    LaunchedEffect(Unit) { bloom.animateTo(1f, Springs.bouncy()) }
    val facts = buildList {
        add(if (incidents == 0) "aucun incident" else if (incidents == 1) "1 incident" else "$incidents incidents")
        add(if (crashes == 0) "aucun plantage" else if (crashes == 1) "1 plantage" else "$crashes plantages")
        if (r.updates.isNotEmpty()) add(if (r.updates.size == 1) "1 mise à jour disponible" else "${r.updates.size} mises à jour disponibles")
    }
    Surface(shape = RoundedCornerShape(28.dp), color = status.containerOf(tone), contentColor = status.onContainerOf(tone), modifier = modifier.fillMaxWidth()) {
        Row(Modifier.padding(20.dp), verticalAlignment = Alignment.CenterVertically) {
            Box(contentAlignment = Alignment.Center) {
                Surface(
                    shape = shape.toShape(),
                    color = status.of(tone),
                    modifier = Modifier.size(64.dp).graphicsLayer {
                        scaleX = bloom.value
                        scaleY = bloom.value
                        rotationZ = (1f - bloom.value) * -90f
                    },
                ) {}
                Icon(icon, null, Modifier.size(28.dp), tint = MaterialTheme.colorScheme.surface)
            }
            Spacer(Modifier.width(16.dp))
            Column {
                Text(title, style = MaterialTheme.typography.titleLargeEmphasized)
                Text(facts.joinToString(", ").replaceFirstChar { it.uppercase() }, style = MaterialTheme.typography.bodyMedium)
            }
        }
    }
}

@Composable
private fun ServicesSection(s: ReportServices, modifier: Modifier) {
    val status = Vigie.status
    SectionCard(modifier, title = "Disponibilité des services") {
        if (s.list.isEmpty()) {
            Note("Aucun service surveillé.")
            return@SectionCard
        }
        Row(verticalAlignment = Alignment.Bottom) {
            Text(
                Fmt.uptimePct(s.uptime),
                style = MaterialTheme.typography.displaySmallEmphasized.numeric(),
                color = status.of(uptimeTone(s.uptime)),
            )
            Spacer(Modifier.width(8.dp))
            Text("en moyenne", style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(bottom = 6.dp))
        }
        val worst = s.worst
        if (worst != null && s.list.size > 1 && worst.uptime < 99.995) {
            Text("Le moins disponible : ${worst.name} (${Fmt.uptimePct(worst.uptime)})", style = MaterialTheme.typography.bodyMedium)
        }
        Spacer(Modifier.height(8.dp))
        s.list.forEach { svc ->
            Row(Modifier.padding(vertical = 3.dp), verticalAlignment = Alignment.CenterVertically) {
                StatusDot(uptimeTone(svc.uptime), size = 8.dp, breathe = false)
                Spacer(Modifier.width(6.dp))
                Text(svc.name, style = MaterialTheme.typography.bodyMedium, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
                Text(Fmt.uptimePct(svc.uptime), style = MaterialTheme.typography.labelLarge.numeric())
            }
        }
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun IncidentsSection(i: ReportIncidents, modifier: Modifier) {
    val status = Vigie.status
    val scheme = MaterialTheme.colorScheme
    SectionCard(
        modifier,
        title = "Incidents",
        action = {
            val tone = if (i.total == 0) "ok" else "warning"
            Chip(if (i.total == 0) "Aucun" else "${i.total}", status.containerOf(tone), status.onContainerOf(tone))
        },
    ) {
        if (i.total == 0) {
            Note("Aucune alerte cette semaine.")
            return@SectionCard
        }
        FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            i.byKind.forEach { k ->
                Chip("${k.label.replaceFirstChar { it.uppercase() }} · ${k.count}", scheme.secondaryContainer, scheme.onSecondaryContainer)
            }
        }
        Spacer(Modifier.height(12.dp))
        i.recent.take(5).forEach { a ->
            Row(Modifier.padding(vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                StatusDot(if (a.severity == "critical") "critical" else "warning", size = 8.dp, breathe = false)
                Spacer(Modifier.width(6.dp))
                Column(Modifier.weight(1f)) {
                    Text(a.title, style = MaterialTheme.typography.bodyMedium, maxLines = 2, overflow = TextOverflow.Ellipsis)
                    Text(Fmt.dateTime(a.ts), style = MaterialTheme.typography.labelSmall, color = scheme.outline)
                }
            }
        }
    }
}

@Composable
private fun ContainersSection(c: ReportContainers, live: List<Container>, modifier: Modifier, onOpen: (String) -> Unit) {
    val status = Vigie.status
    val haptics = rememberHaptics()
    SectionCard(modifier, title = "Plantages et redémarrages") {
        Row(Modifier.fillMaxWidth()) {
            Counter(c.crashes, if (c.crashes > 1) "plantages" else "plantage", if (c.crashes > 0) status.critical else status.ok, Modifier.weight(1f))
            Counter(c.restarts, if (c.restarts > 1) "redémarrages" else "redémarrage", if (c.restarts > 0) status.warn else status.ok, Modifier.weight(1f))
        }
        if (c.list.isEmpty()) {
            Spacer(Modifier.height(4.dp))
            Note("Tous les conteneurs ont tenu bon.")
            return@SectionCard
        }
        Spacer(Modifier.height(8.dp))
        c.list.take(6).forEach { ci ->
            val parts = buildList {
                if (ci.crashes > 0) add(if (ci.crashes > 1) "${ci.crashes} plantages" else "1 plantage")
                if (ci.restarts > 0) add(if (ci.restarts > 1) "${ci.restarts} redémarrages" else "1 redémarrage")
            }
            val ctr = live.firstOrNull { it.name == ci.name }
            Surface(
                onClick = { haptics.tick(); onOpen(ci.name) },
                shape = RoundedCornerShape(16.dp),
                color = MaterialTheme.colorScheme.surfaceContainerHigh,
                modifier = Modifier.fillMaxWidth().padding(vertical = 3.dp),
            ) {
                Row(Modifier.padding(horizontal = 12.dp, vertical = 10.dp), verticalAlignment = Alignment.CenterVertically) {
                    StatusDot(if (ctr == null) "neutral" else if (ctr.hasProblem) "critical" else if (ctr.running) "ok" else "neutral", size = 8.dp, breathe = false)
                    Spacer(Modifier.width(8.dp))
                    Column(Modifier.weight(1f)) {
                        Text(ci.name, style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        Text(parts.joinToString(" · "), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                    Icon(Icons.AutoMirrored.Rounded.ArrowForward, null, Modifier.size(18.dp), tint = MaterialTheme.colorScheme.outline)
                }
            }
        }
    }
}

@Composable
private fun Counter(n: Int, label: String, color: Color, modifier: Modifier) {
    Row(modifier, verticalAlignment = Alignment.Bottom) {
        Text("$n", style = MaterialTheme.typography.headlineMediumEmphasized.numeric(), color = color)
        Spacer(Modifier.width(6.dp))
        Text(label, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(bottom = 4.dp))
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun UpdatesSection(updates: List<String>, modifier: Modifier, onOpen: () -> Unit) {
    val scheme = MaterialTheme.colorScheme
    SectionCard(
        modifier,
        title = "Mises à jour d'images",
        action = { if (updates.isNotEmpty()) TextButton(onClick = onOpen) { Text("Voir") } },
    ) {
        if (updates.isEmpty()) {
            Note("Toutes les images sont à jour.")
            return@SectionCard
        }
        FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            updates.forEach { Chip(it, scheme.tertiaryContainer, scheme.onTertiaryContainer) }
        }
    }
}

@Composable
private fun HostSection(disk: ReportDisk, traffic: ReportTraffic?, days: Int, diskThreshold: Double, modifier: Modifier) {
    val scheme = MaterialTheme.colorScheme
    SectionCard(modifier, title = "Serveur") {
        Row(verticalAlignment = Alignment.CenterVertically) {
            if (disk.total > 0) {
                val change = disk.change?.let { c ->
                    val sign = if (c > 0) "+" else if (c < 0) "−" else "±"
                    "$sign${String.format(Locale.FRANCE, "%.1f", abs(c))} point${if (abs(c) >= 2) "s" else ""} en $days j"
                }
                CircularGauge(disk.pct, "Disque", diskThreshold, Modifier.weight(1f), caption = change ?: "${Fmt.bytes(disk.used)} / ${Fmt.bytes(disk.total)}")
                Spacer(Modifier.width(16.dp))
            }
            Column(Modifier.weight(1.4f), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                Text("Trafic sur $days jours", style = MaterialTheme.typography.labelLarge)
                if (traffic == null) {
                    Note("Pas encore d'historique.")
                } else {
                    FlowLine(Icons.Rounded.ArrowUpward, "Sortant", Fmt.volume(traffic.tx), scheme.tertiary)
                    FlowLine(Icons.Rounded.ArrowDownward, "Entrant", Fmt.volume(traffic.rx), scheme.primary)
                    if (traffic.quota > 0) {
                        Text(
                            "Ce mois-ci : ${Fmt.volume(traffic.monthTx)}, ${Fmt.pct(traffic.monthPct)} du quota",
                            style = MaterialTheme.typography.bodySmall,
                            color = Vigie.status.forLevel(traffic.monthPct, 100.0),
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun FlowLine(icon: ImageVector, label: String, value: String, color: Color) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        Icon(icon, null, Modifier.size(18.dp), tint = color)
        Spacer(Modifier.width(6.dp))
        Text(label, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.weight(1f))
        Text(value, style = MaterialTheme.typography.titleSmall.numeric())
    }
}

@Composable
private fun Note(text: String) {
    Text(text, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
}
