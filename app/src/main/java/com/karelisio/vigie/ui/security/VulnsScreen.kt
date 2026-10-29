package com.karelisio.vigie.ui.security

import androidx.compose.animation.animateContentSize
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
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
import androidx.compose.material.icons.automirrored.rounded.OpenInNew
import androidx.compose.material.icons.rounded.BugReport
import androidx.compose.material.icons.rounded.CheckCircle
import androidx.compose.material.icons.rounded.Cloud
import androidx.compose.material.icons.rounded.Lock
import androidx.compose.material.icons.rounded.Refresh
import androidx.compose.material.icons.rounded.SystemUpdate
import androidx.compose.material.icons.rounded.Verified
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Icon
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.LinearWavyProgressIndicator
import androidx.compose.material3.LoadingIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.SegmentedButton
import androidx.compose.material3.SegmentedButtonDefaults
import androidx.compose.material3.SingleChoiceSegmentedButtonRow
import androidx.compose.material3.Surface
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
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
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.NavHostController
import com.karelisio.vigie.appContainer
import com.karelisio.vigie.data.AgentJob
import com.karelisio.vigie.data.AgentSettings
import com.karelisio.vigie.data.ApiException
import com.karelisio.vigie.data.FixPlan
import com.karelisio.vigie.data.FixPlans
import com.karelisio.vigie.data.Fmt
import com.karelisio.vigie.data.LiveEvent
import com.karelisio.vigie.data.Vuln
import com.karelisio.vigie.data.VulnImage
import com.karelisio.vigie.data.VulnReport
import com.karelisio.vigie.data.VulnSeverities
import com.karelisio.vigie.data.analyzeNewVersion
import com.karelisio.vigie.data.fixAllVulns
import com.karelisio.vigie.data.fixVulns
import com.karelisio.vigie.data.scanVulns
import com.karelisio.vigie.data.severityLabel
import com.karelisio.vigie.data.versionAtLeast
import com.karelisio.vigie.data.vulnFixes
import com.karelisio.vigie.data.vulnReport
import com.karelisio.vigie.data.vulns
import com.karelisio.vigie.security.LocalActionGuard
import com.karelisio.vigie.ui.components.Chip
import com.karelisio.vigie.ui.components.EmptyState
import com.karelisio.vigie.ui.components.JobBanner
import com.karelisio.vigie.ui.components.ScreenHeader
import com.karelisio.vigie.ui.components.SectionCard
import com.karelisio.vigie.ui.components.StatusDot
import com.karelisio.vigie.ui.components.cascade
import com.karelisio.vigie.ui.components.rememberHaptics
import com.karelisio.vigie.ui.components.rememberNow
import com.karelisio.vigie.ui.navigation.ContainerRoute
import com.karelisio.vigie.ui.navigation.LocalSnackbar
import com.karelisio.vigie.ui.theme.LocalReducedMotion
import com.karelisio.vigie.ui.theme.Vigie
import com.karelisio.vigie.ui.theme.numeric
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch

/** Failles par défaut repliées : les plus graves d'abord. */
private const val COLLAPSED = 3

class VulnsViewModel : ViewModel() {
    private val repo = appContainer.repository
    val report = repo.vulnReport().stateIn(viewModelScope, SharingStarted.Eagerly, null)
    private val _error = MutableStateFlow<String?>(null)
    val error = _error.asStateFlow()
    /** L'agent ne connaît pas l'analyse des failles (antérieur à 0.6.0). */
    private val _unsupported = MutableStateFlow(false)
    val unsupported = _unsupported.asStateFlow()
    private val _settings = MutableStateFlow<AgentSettings?>(null)
    val settings = _settings.asStateFlow()
    var starting by mutableStateOf(false)
        private set
    private var poll: Job? = null
    /** État avant une analyse lancée depuis l'écran ; à la fin, [finished] reçoit (avant, après). */
    private var startedFrom: VulnReport? = null
    private val _finished = MutableSharedFlow<Pair<VulnReport, VulnReport>>(extraBufferCapacity = 1)
    val finished = _finished.asSharedFlow()
    /** Correction proposée pour chaque image (agent 0.7.0 ; null avant, ou en lecture seule). */
    private val _plans = MutableStateFlow<FixPlans?>(null)
    val plans = _plans.asStateFlow()

    init {
        load()
        viewModelScope.launch { runCatching { repo.settings() }.onSuccess { _settings.value = it } }
        // Fin d'une correction, d'une mise à jour ou d'un retour arrière : failles et corrections relues.
        viewModelScope.launch {
            repo.events.collect { e ->
                if (e is LiveEvent.JobDone && e.job.kind in REFRESH_KINDS) {
                    delay(1500)
                    load()
                }
            }
        }
    }

    fun load() {
        viewModelScope.launch {
            _error.value = null
            runCatching { repo.vulns() }
                .onSuccess { follow(it) }
                .onFailure { if ((it as? ApiException)?.code == 404) _unsupported.value = true else _error.value = it.message }
        }
        loadPlans()
    }

    private fun loadPlans() {
        val live = repo.live.value
        if (live.readOnly || (live.agentVersion.isNotEmpty() && !versionAtLeast(live.agentVersion, 0, 7))) return
        viewModelScope.launch {
            runCatching { repo.vulnFixes() }
                .onSuccess { _plans.value = it }
                .onFailure { if ((it as? ApiException)?.code == 404) _plans.value = null }
        }
    }

    /** Suit une analyse en cours : état relu toutes les 1,5 s jusqu'à la fin (30 min au plus). */
    private fun follow(r: VulnReport) {
        if (!r.scanning) return finish(r)
        if (poll?.isActive == true) return
        poll = viewModelScope.launch {
            repeat(1200) {
                delay(1500)
                val next = runCatching { repo.vulns() }.getOrNull()
                if (next != null && !next.scanning) return@launch finish(next)
            }
        }
    }

    private fun finish(r: VulnReport) {
        if (poll != null) loadPlans() // analyse terminée : corrections recalculées
        val before = startedFrom ?: return
        startedFrom = null
        _finished.tryEmit(before to r)
    }

    /** Corrige une image par la voie proposée (opération suivie comme une mise à jour). */
    suspend fun fix(plan: FixPlan): Result<AgentJob> = runCatching { repo.fixVulns(plan.target) }

    suspend fun fixAll(): Result<AgentJob> = runCatching { repo.fixAllVulns() }

    suspend fun analyze(plan: FixPlan): Result<AgentJob> = runCatching { repo.analyzeNewVersion(plan.target) }

    private companion object {
        val REFRESH_KINDS = setOf("vulnfix", "rollback", "update", "batch", "auto", "stack")
    }

    suspend fun scan(): Result<VulnReport> {
        starting = true
        val before = report.value ?: VulnReport()
        return runCatching { repo.scanVulns() }
            .onSuccess {
                startedFrom = before
                follow(it)
            }
            .also { starting = false }
    }

    /** Modifie un réglage des failles (affiché tout de suite, rétabli en cas d'échec). */
    suspend fun updateSettings(change: (AgentSettings) -> AgentSettings): Result<AgentSettings> {
        val current = _settings.value ?: return Result.failure(IllegalStateException("Réglages pas encore chargés"))
        val next = change(current)
        _settings.value = next
        return runCatching { repo.saveSettings(next) }
            .onSuccess {
                _settings.value = it
                load()
            }
            .onFailure { _settings.value = current }
    }
}

/** Failles connues des images : résumé, détail par image, analyse à la demande et réglages (agent 0.6.0). */
@Composable
fun VulnsScreen(nav: NavHostController, vm: VulnsViewModel = viewModel()) {
    val report by vm.report.collectAsStateWithLifecycle()
    val error by vm.error.collectAsStateWithLifecycle()
    val unsupportedApi by vm.unsupported.collectAsStateWithLifecycle()
    val settings by vm.settings.collectAsStateWithLifecycle()
    val plans by vm.plans.collectAsStateWithLifecycle()
    val live by appContainer.repository.live.collectAsStateWithLifecycle()
    val unsupported = unsupportedApi || (live.agentVersion.isNotEmpty() && !versionAtLeast(live.agentVersion, 0, 6))
    // Correction des failles : agent 0.7.0, accès complet.
    val fixes = plans.takeIf { !live.readOnly && !unsupported && (live.agentVersion.isEmpty() || versionAtLeast(live.agentVersion, 0, 7)) }
    val busy = live.runningJob != null
    var fixableOnly by rememberSaveable { mutableStateOf(false) }
    var confirmFix by remember { mutableStateOf<FixPlan?>(null) }
    var confirmAll by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    val snackbar = LocalSnackbar.current
    val haptics = rememberHaptics()
    val guard = LocalActionGuard.current
    val uri = LocalUriHandler.current
    val now = rememberNow(30_000)
    val bottom = WindowInsets.navigationBars.asPaddingValues().calculateBottomPadding() + 32.dp

    fun launchJob(label: String, action: suspend () -> Result<AgentJob>) {
        scope.launch {
            if (!guard(label)) return@launch
            action().onFailure {
                haptics.reject()
                snackbar.showSnackbar("Échec : ${it.message}")
            }
        }
    }

    confirmFix?.let { plan ->
        FixDialog(
            plan, plan.route,
            onConfirm = {
                confirmFix = null
                launchJob("Corriger les failles de ${plan.target}") { vm.fix(plan) }
            },
            onDismiss = { confirmFix = null },
        )
    }
    if (confirmAll && fixes != null) {
        FixAllDialog(
            fixes,
            onConfirm = {
                confirmAll = false
                launchJob("Corriger les failles de toutes les images") { vm.fixAll() }
            },
            onDismiss = { confirmAll = false },
        )
    }

    // Analyse lancée depuis l'écran : bilan à la fin (nouvelle analyse réussie ou nouvelle erreur).
    LaunchedEffect(vm) {
        vm.finished.collect { (before, after) ->
            when {
                after.lastScan != before.lastScan -> {
                    haptics.confirm()
                    snackbar.showSnackbar(
                        when (after.totals.fixable) {
                            0 -> "Analyse terminée : aucune faille corrigeable"
                            1 -> "Analyse terminée : 1 faille corrigeable"
                            else -> "Analyse terminée : ${after.totals.fixable} failles corrigeables"
                        },
                    )
                }
                after.errorAt != before.errorAt -> {
                    haptics.reject()
                    snackbar.showSnackbar("Analyse impossible : ${after.error}")
                }
            }
        }
    }

    fun scanNow() {
        haptics.tick()
        scope.launch {
            vm.scan().onFailure {
                haptics.reject()
                snackbar.showSnackbar("Échec : ${it.message}")
            }
        }
    }

    fun saveSettings(change: (AgentSettings) -> AgentSettings) {
        scope.launch {
            vm.updateSettings(change).onFailure {
                haptics.reject()
                snackbar.showSnackbar("Réglage non enregistré : ${it.message}")
            }
        }
    }

    LazyColumn(
        Modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = 16.dp, end = 16.dp, bottom = bottom),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        item { ScreenHeader("Failles connues", nav) }
        item {
            Text(
                "Les paquets de tes images comparés à la base publique OSV. Seuls des noms et versions de paquets lui sont envoyés.",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(horizontal = 4.dp),
            )
        }
        val r = report
        when {
            live.readOnly -> item {
                EmptyState(Icons.Rounded.Lock, "Réservé à l'accès complet", "Un accès partagé en lecture seule ne voit pas les failles du serveur.")
            }
            unsupported -> item {
                EmptyState(
                    Icons.Rounded.BugReport, "Indisponible sur cet agent",
                    "Mets l'agent à jour (0.6.0 ou plus) pour rechercher les failles connues dans tes images.",
                )
            }
            r == null && error != null -> item {
                EmptyState(Icons.Rounded.Cloud, "Failles indisponibles", error.orEmpty()) {
                    OutlinedButton(onClick = vm::load, shapes = ButtonDefaults.shapes()) { Text("Réessayer") }
                }
            }
            r == null -> item {
                Box(Modifier.fillMaxWidth().padding(48.dp), contentAlignment = Alignment.Center) { LoadingIndicator() }
            }
            else -> {
                item { SummaryCard(r, now, vm.starting, ::scanNow, Modifier.cascade(0)) }
                if (fixes != null && (fixes.totals.fixable > 0 || !fixes.enabled)) {
                    item(key = "fix-all") {
                        Column(Modifier.cascade(1)) {
                            FixAllCard(fixes, busy, onFixAll = { confirmAll = true })
                            // Passe « tout corriger » ou de nuit en cours.
                            live.jobs.firstOrNull { it.kind == "vulnfix" && !it.done && (it.target == "all" || it.target == "nightly") }?.let {
                                Spacer(Modifier.height(8.dp))
                                JobBanner(it)
                            }
                        }
                    }
                }
                val vulnerable = r.images.filter { it.counts.total > 0 }
                val clean = r.images.filter { it.counts.total == 0 }
                if (vulnerable.isNotEmpty()) {
                    item {
                        FilterChip(
                            selected = fixableOnly,
                            onClick = { haptics.toggle(!fixableOnly); fixableOnly = !fixableOnly },
                            label = { Text("Seulement les corrigeables") },
                            modifier = Modifier.cascade(1),
                        )
                    }
                }
                itemsIndexed(vulnerable, key = { _, img -> img.id }) { i, img ->
                    val plan = fixes?.plan(img.id)
                    ImageCard(
                        img, fixableOnly,
                        onLink = { url -> haptics.tick(); runCatching { uri.openUri(url) } },
                        onContainer = { id, name -> haptics.tick(); nav.navigate(ContainerRoute(id, name)) },
                        modifier = Modifier.animateItem().cascade(i + 2),
                        fix = plan?.let { p ->
                            {
                                PatchedBadge(p, busy)
                                Spacer(Modifier.height(8.dp))
                                FixSection(
                                    p,
                                    job = live.jobs.firstOrNull { j -> j.kind == "vulnfix" && p.containers.any { c -> j.target == c.name } },
                                    busy = busy,
                                    onFix = { pl, _ -> confirmFix = pl },
                                    onAnalyze = { pl -> launchJob("Analyser la nouvelle version de ${pl.target}") { vm.analyze(pl) } },
                                    onLink = { url -> haptics.tick(); runCatching { uri.openUri(url) } },
                                )
                            }
                        },
                    )
                }
                if (clean.isNotEmpty()) {
                    item(key = "clean") {
                        CleanCard(clean, Modifier.animateItem().cascade(vulnerable.size + 2)) { img ->
                            fixes?.plan(img.id)?.takeIf { it.patched != null }?.let { PatchedBadge(it, busy) }
                        }
                    }
                }
            }
        }
        if (!live.readOnly && !unsupported) {
            settings?.let { s ->
                item(key = "settings") {
                    SettingsCard(
                        s,
                        onScan = { on -> haptics.toggle(on); saveSettings { it.copy(vulnScan = on) } },
                        onLevel = { level -> haptics.tick(); saveSettings { it.copy(vulnAlert = level) } },
                        modifier = Modifier.cascade(r?.images?.size?.plus(3) ?: 3),
                    )
                }
                if (fixes != null && fixes.enabled) {
                    item(key = "night-fix") {
                        NightFixCard(
                            s, fixes.nightly, live.containers,
                            onChange = { next -> saveSettings { next } },
                            modifier = Modifier.cascade(r?.images?.size?.plus(4) ?: 4),
                        )
                    }
                }
            }
        }
    }
}

/** Couleurs d'une gravité : fond et texte des pastilles, couleur des chiffres. */
private data class SeverityColors(val container: Color, val content: Color, val accent: Color)

@Composable
private fun severityColors(severity: String): SeverityColors {
    val status = Vigie.status
    val scheme = MaterialTheme.colorScheme
    return when (severity) {
        "critical" -> SeverityColors(status.criticalContainer, status.onCriticalContainer, status.critical)
        "high" -> SeverityColors(status.warnContainer, status.onWarnContainer, status.warn)
        "medium" -> SeverityColors(scheme.secondaryContainer, scheme.onSecondaryContainer, scheme.secondary)
        else -> SeverityColors(scheme.surfaceContainerHighest, scheme.onSurfaceVariant, scheme.onSurfaceVariant)
    }
}

private fun severityPlural(severity: String, n: Int): String {
    val word = when (severity) {
        "critical" -> "critique"
        "high" -> "élevée"
        "medium" -> "moyenne"
        "low" -> "faible"
        else -> "inconnue"
    }
    return "$n $word" + if (n > 1) "s" else ""
}

@Composable
private fun SummaryCard(r: VulnReport, now: Long, starting: Boolean, onScan: () -> Unit, modifier: Modifier = Modifier) {
    val scheme = MaterialTheme.colorScheme
    val status = Vigie.status
    val reduced = LocalReducedMotion.current
    val t = r.totals
    SectionCard(modifier.animateContentSize()) {
        if (r.lastScan > 0) {
            Row(Modifier.fillMaxWidth()) {
                listOf("critical" to "Critiques", "high" to "Élevées", "medium" to "Moyennes", "low" to "Faibles").forEach { (sev, label) ->
                    val n = t.counts.of(sev)
                    Column(Modifier.weight(1f), horizontalAlignment = Alignment.CenterHorizontally) {
                        Text(
                            "$n",
                            style = MaterialTheme.typography.headlineMedium.numeric(),
                            color = if (n > 0) severityColors(sev).accent else scheme.outline,
                        )
                        Text(label, style = MaterialTheme.typography.labelMedium, color = scheme.onSurfaceVariant)
                    }
                }
            }
            Spacer(Modifier.height(16.dp))
            Row(verticalAlignment = Alignment.CenterVertically) {
                if (t.fixable > 0) {
                    Icon(Icons.Rounded.SystemUpdate, null, Modifier.size(20.dp), tint = status.warn)
                } else {
                    Icon(Icons.Rounded.CheckCircle, null, Modifier.size(20.dp), tint = status.ok)
                }
                Spacer(Modifier.width(8.dp))
                Text(
                    when (t.fixable) {
                        0 -> if (t.counts.total == 0) "Aucune faille connue" else "Aucune faille corrigée pour l'instant"
                        1 -> "1 faille corrigée dans une version plus récente"
                        else -> "${t.fixable} failles corrigées dans une version plus récente"
                    },
                    style = MaterialTheme.typography.titleSmall,
                )
            }
            Spacer(Modifier.height(4.dp))
            val images = if (t.images == 1) "1 image analysée" else "${t.images} images analysées"
            Text(
                "$images · ${Fmt.ago(r.lastScan, now)}" +
                    (if (t.unknown > 0) " · ${t.unknown} de gravité inconnue" else "") +
                    (if (t.negligible > 0) " · ${t.negligible} sans importance, non comptées" else ""),
                style = MaterialTheme.typography.bodySmall,
                color = scheme.onSurfaceVariant,
            )
        } else if (!r.scanning) {
            Text("Pas encore analysé", style = MaterialTheme.typography.titleMedium)
            Spacer(Modifier.height(4.dp))
            Text(
                if (r.enabled) "La première analyse démarre quelques minutes après l'installation de l'agent. Tu peux la lancer maintenant."
                else "L'analyse est désactivée : active-la dans les réglages plus bas.",
                style = MaterialTheme.typography.bodySmall,
                color = scheme.onSurfaceVariant,
            )
        }
        if (r.enabled && r.nextScan > 0 && !r.scanning) {
            Text(
                "Prochaine analyse : ${Fmt.dateTime(r.nextScan)}",
                style = MaterialTheme.typography.bodySmall,
                color = scheme.onSurfaceVariant,
            )
        }
        if (r.error.isNotEmpty() && r.errorAt >= r.lastScan && !r.scanning) {
            Spacer(Modifier.height(8.dp))
            Text(
                "Dernière tentative échouée (${Fmt.ago(r.errorAt, now)}) : ${r.error}",
                style = MaterialTheme.typography.bodySmall,
                color = status.critical,
            )
        }
        if (r.scanning) {
            val p = r.progress
            val fraction by animateFloatAsState(p?.fraction ?: 0f, label = "analyse")
            Spacer(Modifier.height(12.dp))
            Text(p?.label ?: "Analyse en cours…", style = MaterialTheme.typography.bodyMedium)
            Spacer(Modifier.height(8.dp))
            when {
                reduced -> LinearProgressIndicator(progress = { fraction }, modifier = Modifier.fillMaxWidth())
                p?.fraction == null -> LinearWavyProgressIndicator(Modifier.fillMaxWidth())
                else -> LinearWavyProgressIndicator(progress = { fraction }, modifier = Modifier.fillMaxWidth())
            }
        }
        Spacer(Modifier.height(16.dp))
        val busy = r.scanning || starting
        Button(
            onClick = onScan,
            enabled = r.enabled && !busy,
            shapes = ButtonDefaults.shapes(),
            modifier = Modifier.fillMaxWidth().height(52.dp),
        ) {
            if (busy) LoadingIndicator(Modifier.size(24.dp)) else Icon(Icons.Rounded.Refresh, null)
            Spacer(Modifier.width(8.dp))
            Text(if (busy) "Analyse en cours…" else "Analyser maintenant")
        }
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun ImageCard(
    img: VulnImage,
    fixableOnly: Boolean,
    onLink: (String) -> Unit,
    onContainer: (id: String, name: String) -> Unit,
    modifier: Modifier = Modifier,
    /** Correction proposée (agent 0.7.0) ; sinon, lien vers la fiche si une mise à jour est publiée. */
    fix: (@Composable ColumnScope.() -> Unit)? = null,
) {
    val scheme = MaterialTheme.colorScheme
    val status = Vigie.status
    var expanded by rememberSaveable(img.id) { mutableStateOf(false) }
    val list = img.sorted.filter { !fixableOnly || it.fixable }
    val shown = if (expanded) list else list.take(COLLAPSED)
    SectionCard(modifier.animateContentSize()) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            StatusDot(if (img.counts.critical > 0) "critical" else if (img.counts.high > 0) "warning" else "neutral", size = 10.dp, breathe = false)
            Spacer(Modifier.width(10.dp))
            Column(Modifier.weight(1f)) {
                Text(img.title, style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
                Text(img.ref, style = MaterialTheme.typography.bodySmall, color = scheme.onSurfaceVariant, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
        }
        Spacer(Modifier.height(4.dp))
        Text(img.details, style = MaterialTheme.typography.bodySmall, color = scheme.onSurfaceVariant)
        if (img.containers.size > 1) {
            Text(
                "Utilisée par " + img.containers.joinToString(", ") { it.name },
                style = MaterialTheme.typography.bodySmall,
                color = scheme.onSurfaceVariant,
            )
        }
        Spacer(Modifier.height(10.dp))
        FlowRow(horizontalArrangement = Arrangement.spacedBy(6.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
            VulnSeverities.forEach { sev ->
                val n = img.counts.of(sev)
                if (n > 0) {
                    val c = severityColors(sev)
                    Chip(severityPlural(sev, n), c.container, c.content)
                }
            }
            if (img.counts.negligible > 0) {
                Chip("${img.counts.negligible} sans importance", scheme.surfaceContainerHighest, scheme.onSurfaceVariant)
            }
            if (img.counts.fixable > 0) {
                Chip(
                    if (img.counts.fixable == 1) "1 corrigeable" else "${img.counts.fixable} corrigeables",
                    status.okContainer, status.onOkContainer,
                )
            }
            if (img.updateAvailable) {
                Chip("MAJ disponible", scheme.primaryContainer, scheme.onPrimaryContainer, icon = Icons.Rounded.SystemUpdate)
            }
            if (img.patched != null) {
                Chip("Corrigée par Vigie", status.okContainer, status.onOkContainer, icon = Icons.Rounded.Verified)
            }
        }
        if (img.error.isNotEmpty()) {
            Spacer(Modifier.height(8.dp))
            Text("Inventaire incomplet : ${img.error}", style = MaterialTheme.typography.bodySmall, color = status.warn)
        }
        Spacer(Modifier.height(12.dp))
        if (list.isEmpty()) {
            Text("Aucune faille corrigeable dans cette image.", style = MaterialTheme.typography.bodyMedium, color = scheme.onSurfaceVariant)
        }
        Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
            shown.forEach { v -> VulnRow(v) { onLink(v.url) } }
        }
        if (list.size > COLLAPSED) {
            TextButton(onClick = { expanded = !expanded }) {
                Text(if (expanded) "Réduire" else "Voir les ${list.size} failles")
            }
        }
        if (expanded && img.more > 0) {
            Text(
                "Et ${img.more} autres, moins graves, non listées.",
                style = MaterialTheme.typography.bodySmall,
                color = scheme.onSurfaceVariant,
            )
        }
        val target = img.containers.firstOrNull()
        if (fix != null) {
            Spacer(Modifier.height(12.dp))
            fix()
        } else if (img.updateAvailable && target != null) {
            Spacer(Modifier.height(8.dp))
            FilledTonalButton(
                onClick = { onContainer(target.id, target.name) },
                shapes = ButtonDefaults.shapes(),
                modifier = Modifier.fillMaxWidth(),
            ) {
                Icon(Icons.Rounded.SystemUpdate, null)
                Spacer(Modifier.width(8.dp))
                Text("Ouvrir la fiche de ${target.name}", maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
        }
    }
}

@Composable
private fun VulnRow(v: Vuln, onClick: () -> Unit) {
    val scheme = MaterialTheme.colorScheme
    val status = Vigie.status
    val c = if (v.negligible) SeverityColors(scheme.surfaceContainerHighest, scheme.onSurfaceVariant, scheme.outline) else severityColors(v.severity)
    Surface(
        onClick = onClick,
        shape = RoundedCornerShape(16.dp),
        color = scheme.surfaceContainerHigh,
        modifier = Modifier.fillMaxWidth(),
    ) {
        Row(Modifier.padding(12.dp), verticalAlignment = Alignment.Top) {
            Surface(color = c.container, contentColor = c.content, shape = RoundedCornerShape(10.dp), modifier = Modifier.width(64.dp)) {
                Column(Modifier.padding(vertical = 6.dp), horizontalAlignment = Alignment.CenterHorizontally) {
                    Text(
                        if (v.negligible) "Mineure" else severityLabel(v.severity),
                        style = MaterialTheme.typography.labelSmall, maxLines = 1, textAlign = TextAlign.Center,
                    )
                    if (v.score > 0 && !v.negligible) {
                        Text(String.format(java.util.Locale.FRANCE, "%.1f", v.score), style = MaterialTheme.typography.titleSmall.numeric())
                    }
                }
            }
            Spacer(Modifier.width(12.dp))
            Column(Modifier.weight(1f)) {
                Text(v.pkg, style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                Text(
                    when {
                        v.negligible -> "${v.version} · ${v.note.ifEmpty { "sans importance" }}"
                        v.fixable -> "${v.version} → ${v.fixed}"
                        else -> "${v.version} · pas encore corrigée"
                    },
                    style = MaterialTheme.typography.bodySmall.numeric(),
                    color = if (v.fixable) status.ok else scheme.onSurfaceVariant,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )
                Text(
                    if (v.summary.isEmpty()) v.title else "${v.title} · ${v.summary}",
                    style = MaterialTheme.typography.bodySmall,
                    color = scheme.onSurfaceVariant,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            Icon(
                Icons.AutoMirrored.Rounded.OpenInNew, "Ouvrir sur osv.dev",
                Modifier.padding(start = 8.dp).size(18.dp), tint = scheme.outline,
            )
        }
    }
}

/** Images sans faille connue, regroupées ; [extra] : sous chaque image (image corrigée par Vigie). */
@Composable
private fun CleanCard(images: List<VulnImage>, modifier: Modifier = Modifier, extra: @Composable (VulnImage) -> Unit = {}) {
    val scheme = MaterialTheme.colorScheme
    SectionCard(modifier, title = "Sans faille connue") {
        Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
            images.forEach { img ->
                Row(verticalAlignment = Alignment.CenterVertically) {
                    StatusDot(if (img.error.isEmpty()) "ok" else "warning", size = 8.dp, breathe = false)
                    Spacer(Modifier.width(10.dp))
                    Column(Modifier.weight(1f)) {
                        Text(img.title, style = MaterialTheme.typography.bodyLarge, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        Text(
                            if (img.error.isEmpty()) img.details else "Inventaire incomplet : ${img.error}",
                            style = MaterialTheme.typography.bodySmall,
                            color = scheme.onSurfaceVariant,
                            maxLines = 2,
                            overflow = TextOverflow.Ellipsis,
                        )
                    }
                    if (img.updateAvailable) {
                        Icon(Icons.Rounded.SystemUpdate, "MAJ disponible", Modifier.size(18.dp), tint = scheme.primary)
                    }
                }
                Column(Modifier.padding(start = 18.dp)) { extra(img) }
            }
        }
    }
}

@Composable
private fun SettingsCard(s: AgentSettings, onScan: (Boolean) -> Unit, onLevel: (String) -> Unit, modifier: Modifier = Modifier) {
    val scheme = MaterialTheme.colorScheme
    SectionCard(modifier, title = "Réglages") {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f)) {
                Text("Analyse quotidienne", style = MaterialTheme.typography.titleSmall)
                Text(
                    "Chaque nuit après les mises à jour automatiques, et après chaque mise à jour d'image.",
                    style = MaterialTheme.typography.bodySmall,
                    color = scheme.onSurfaceVariant,
                )
            }
            Spacer(Modifier.width(12.dp))
            Switch(checked = s.vulnScan, onCheckedChange = onScan)
        }
        Spacer(Modifier.height(16.dp))
        Text("Me prévenir", style = MaterialTheme.typography.titleSmall)
        Text(
            "Pour les failles corrigées dans une version plus récente, une seule fois par faille.",
            style = MaterialTheme.typography.bodySmall,
            color = scheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(8.dp))
        val levels = listOf("critical" to "Critiques", "high" to "+ élevées", "off" to "Jamais")
        SingleChoiceSegmentedButtonRow(Modifier.fillMaxWidth()) {
            levels.forEachIndexed { i, (key, label) ->
                SegmentedButton(
                    selected = s.vulnAlert == key,
                    onClick = { if (s.vulnAlert != key) onLevel(key) },
                    enabled = s.vulnScan,
                    shape = SegmentedButtonDefaults.itemShape(i, levels.size),
                ) { Text(label, maxLines = 1) }
            }
        }
    }
}
