package com.karelisio.vigie.ui.security

import android.content.ClipData
import android.content.ClipboardManager
import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.gestures.detectTapGestures
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
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Cloud
import androidx.compose.material.icons.rounded.ContentCopy
import androidx.compose.material.icons.rounded.Lock
import androidx.compose.material.icons.rounded.NotificationsActive
import androidx.compose.material.icons.rounded.Refresh
import androidx.compose.material.icons.rounded.Terminal
import androidx.compose.material.icons.rounded.Visibility
import androidx.compose.material.icons.rounded.WarningAmber
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LoadingIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
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
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
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
import com.karelisio.vigie.data.SshIp
import com.karelisio.vigie.data.SshLogin
import com.karelisio.vigie.data.SshPoint
import com.karelisio.vigie.data.SshSummary
import com.karelisio.vigie.data.SshUser
import com.karelisio.vigie.data.countryFlag
import com.karelisio.vigie.data.ssh
import com.karelisio.vigie.data.sshMethodLabel
import com.karelisio.vigie.data.versionAtLeast
import com.karelisio.vigie.ui.components.Chip
import com.karelisio.vigie.ui.components.EmptyState
import com.karelisio.vigie.ui.components.ScreenHeader
import com.karelisio.vigie.ui.components.SectionCard
import com.karelisio.vigie.ui.components.cascade
import com.karelisio.vigie.ui.components.rememberHaptics
import com.karelisio.vigie.ui.navigation.LocalSnackbar
import com.karelisio.vigie.ui.settings.ToggleRow
import com.karelisio.vigie.ui.theme.Vigie
import com.karelisio.vigie.ui.theme.numeric
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import java.text.NumberFormat
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

class SshViewModel : ViewModel() {
    private val repo = appContainer.repository
    private val _summary = MutableStateFlow<SshSummary?>(null)
    val summary = _summary.asStateFlow()
    private val _settings = MutableStateFlow<AgentSettings?>(null)
    val settings = _settings.asStateFlow()
    private val _error = MutableStateFlow<String?>(null)
    val error = _error.asStateFlow()
    private val _unsupported = MutableStateFlow(false)
    val unsupported = _unsupported.asStateFlow()
    var refreshing by mutableStateOf(false)
        private set

    init {
        load()
    }

    fun load() {
        viewModelScope.launch {
            refreshing = true
            _error.value = null
            runCatching { repo.ssh() }
                .onSuccess { _summary.value = it }
                .onFailure { if ((it as? ApiException)?.code == 404) _unsupported.value = true else _error.value = it.message }
            runCatching { repo.settings() }.onSuccess { _settings.value = it }
            refreshing = false
        }
    }

    suspend fun update(change: (AgentSettings) -> AgentSettings): Result<Unit> = runCatching {
        val s = _settings.value ?: repo.settings()
        _settings.value = repo.saveSettings(change(s))
        // L'agent relit le journal dès le changement : on recharge l'état un instant après.
        kotlinx.coroutines.delay(1500)
        runCatching { repo.ssh() }.onSuccess { _summary.value = it }
    }
}

/** Connexions SSH : tentatives, adresses, pays, connexions réussies (journal d'authentification de l'hôte). */
@Composable
fun SshScreen(nav: NavHostController, vm: SshViewModel = viewModel()) {
    val summary by vm.summary.collectAsStateWithLifecycle()
    val settings by vm.settings.collectAsStateWithLifecycle()
    val error by vm.error.collectAsStateWithLifecycle()
    val unsupportedApi by vm.unsupported.collectAsStateWithLifecycle()
    val live by appContainer.repository.live.collectAsStateWithLifecycle()
    val unsupported = unsupportedApi || (live.agentVersion.isNotEmpty() && !versionAtLeast(live.agentVersion, 0, 6))
    val scope = rememberCoroutineScope()
    val snackbar = LocalSnackbar.current
    val haptics = rememberHaptics()
    val bottom = WindowInsets.navigationBars.asPaddingValues().calculateBottomPadding() + 32.dp

    fun change(label: String, f: (AgentSettings) -> AgentSettings) {
        scope.launch {
            vm.update(f).onFailure {
                haptics.reject()
                snackbar.showSnackbar("$label : ${it.message}")
            }
        }
    }

    LazyColumn(
        Modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = 16.dp, end = 16.dp, bottom = bottom),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        item {
            ScreenHeader("Connexions SSH", nav) {
                if (!unsupported) {
                    IconButton(onClick = { haptics.tick(); vm.load() }, enabled = !vm.refreshing) {
                        if (vm.refreshing) LoadingIndicator(Modifier.size(24.dp)) else Icon(Icons.Rounded.Refresh, "Actualiser")
                    }
                }
            }
        }
        val s = summary
        when {
            unsupported -> item {
                EmptyState(Icons.Rounded.Terminal, "Indisponible sur cet agent", "Mets l'agent à jour (0.6.0 ou plus) pour suivre les connexions SSH.")
            }
            s == null && error != null -> item {
                EmptyState(Icons.Rounded.Cloud, "Connexions indisponibles", error.orEmpty()) {
                    OutlinedButton(onClick = vm::load, shapes = ButtonDefaults.shapes()) { Text("Réessayer") }
                }
            }
            s == null -> item {
                Box(Modifier.fillMaxWidth().padding(48.dp), contentAlignment = Alignment.Center) { LoadingIndicator() }
            }
            else -> {
                if (!s.ok) {
                    item {
                        StateBanner(s, Modifier.cascade(0)) {
                            haptics.toggle(true)
                            change("Activation") { it.copy(sshWatch = true) }
                        }
                    }
                }
                val hasData = s.stats.since > 0 || s.ok
                if (hasData) {
                    item { StatTiles(s, Modifier.cascade(1)) }
                    item { FailuresChart(s, Modifier.cascade(2)) }
                }
                if (s.topIps.isNotEmpty()) item { TopIps(s.topIps, s.geo, Modifier.cascade(3)) }
                if (s.topUsers.isNotEmpty()) item { TopUsers(s.topUsers, Modifier.cascade(4)) }
                if (hasData) item { Logins(s.logins, Modifier.cascade(5)) }
                item {
                    val st = settings
                    SectionCard(Modifier.cascade(6), title = "Réglages") {
                        Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                            ToggleRow(
                                Icons.Rounded.Visibility, "Surveiller les connexions SSH",
                                "Lecture du journal d'authentification du serveur",
                                st?.sshWatch ?: (s.state != "disabled"), enabled = st != null,
                            ) { on ->
                                haptics.toggle(on)
                                change("Surveillance") { it.copy(sshWatch = on) }
                            }
                            ToggleRow(
                                Icons.Rounded.NotificationsActive, "Alerte à chaque connexion réussie",
                                "Avertissement si l'adresse est nouvelle ; jamais pour les simples tentatives",
                                st?.sshLoginAlert ?: true, enabled = st != null && st.sshWatch,
                            ) { on ->
                                haptics.toggle(on)
                                change("Alerte") { it.copy(sshLoginAlert = on) }
                            }
                        }
                    }
                }
                item { Footer(s, Modifier.cascade(7)) }
            }
        }
    }
}

private fun stateTitle(state: String): String = when (state) {
    "permission" -> "L'agent n'a pas accès au journal"
    "journald" -> "Pas de fichier journal"
    "missing" -> "Journal introuvable"
    "disabled" -> "Surveillance désactivée"
    else -> "Lecture du journal impossible"
}

@Composable
private fun StateBanner(s: SshSummary, modifier: Modifier, onEnable: () -> Unit) {
    val context = LocalContext.current
    val snackbar = LocalSnackbar.current
    val scope = rememberCoroutineScope()
    val haptics = rememberHaptics()
    val scheme = MaterialTheme.colorScheme
    val disabled = s.state == "disabled"
    Surface(
        shape = RoundedCornerShape(28.dp),
        color = if (disabled) scheme.secondaryContainer else Vigie.status.warnContainer,
        contentColor = if (disabled) scheme.onSecondaryContainer else Vigie.status.onWarnContainer,
        modifier = modifier.fillMaxWidth(),
    ) {
        Column(Modifier.padding(20.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Icon(if (s.state == "permission") Icons.Rounded.Lock else Icons.Rounded.WarningAmber, null)
                Spacer(Modifier.width(12.dp))
                Text(stateTitle(s.state), style = MaterialTheme.typography.titleMedium)
            }
            if (s.message.isNotEmpty()) Text(s.message, style = MaterialTheme.typography.bodyMedium)
            if (s.fix.isNotEmpty()) {
                Text("À ajouter au service vigie-agent, sous « cap_drop » :", style = MaterialTheme.typography.labelLarge)
                Surface(shape = RoundedCornerShape(16.dp), color = scheme.surfaceContainerHighest, contentColor = scheme.onSurface) {
                    Text(
                        s.fix,
                        style = MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace),
                        modifier = Modifier.fillMaxWidth().padding(12.dp),
                    )
                }
                FilledTonalButton(
                    onClick = {
                        context.getSystemService(ClipboardManager::class.java)
                            ?.setPrimaryClip(ClipData.newPlainText("compose", s.fix))
                        haptics.confirm()
                        scope.launch { snackbar.showSnackbar("Extrait copié") }
                    },
                    shapes = ButtonDefaults.shapes(),
                ) {
                    Icon(Icons.Rounded.ContentCopy, null)
                    Spacer(Modifier.width(8.dp))
                    Text("Copier l'extrait")
                }
                Text(
                    "Puis, dans Dockhand : modifie la stack, colle ces lignes, et redéploie.",
                    style = MaterialTheme.typography.bodySmall,
                )
            }
            if (disabled) {
                FilledTonalButton(onClick = onEnable, shapes = ButtonDefaults.shapes()) { Text("Activer") }
            }
        }
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun StatTiles(s: SshSummary, modifier: Modifier) {
    val st = s.stats
    val status = Vigie.status
    val tiles = buildList {
        add(Triple("Échecs 24 h", st.failed24h, if (st.failed24h > 0) status.warn else status.ok))
        add(Triple("Échecs 7 jours", st.failed7d, MaterialTheme.colorScheme.onSurface))
        add(Triple("Connexions 7 jours", st.accepted7d, MaterialTheme.colorScheme.primary))
        add(Triple("Adresses 7 jours", st.ips7d, MaterialTheme.colorScheme.onSurface))
        if (s.fail2ban == "ok" || st.bans7d > 0) add(Triple("Bannissements 7 jours", st.bans7d, MaterialTheme.colorScheme.onSurface))
    }
    FlowRow(
        modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.spacedBy(12.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
        maxItemsInEachRow = 2,
    ) {
        tiles.forEach { (label, value, color) -> StatTile(label, value, color, Modifier.weight(1f)) }
    }
}

@Composable
private fun StatTile(label: String, value: Int, color: Color, modifier: Modifier) {
    Surface(shape = RoundedCornerShape(24.dp), color = MaterialTheme.colorScheme.surfaceContainer, modifier = modifier) {
        Column(Modifier.padding(16.dp)) {
            Text(fmtCount(value), style = MaterialTheme.typography.headlineMediumEmphasized.numeric(), color = color)
            Text(label, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}

@Composable
private fun FailuresChart(s: SshSummary, modifier: Modifier) {
    var daily by rememberSaveable { mutableStateOf(false) }
    val haptics = rememberHaptics()
    val series = if (daily) s.daily else s.hourly
    SectionCard(modifier, title = "Échecs de connexion", action = {
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            FilterChip(selected = !daily, onClick = { haptics.tick(); daily = false }, label = { Text("24 h") })
            FilterChip(selected = daily, onClick = { haptics.tick(); daily = true }, label = { Text("30 j") })
        }
    }) {
        val total = series.sumOf { it.count }
        if (series.size < 2 || total == 0) {
            Text(
                if (daily) "Aucun échec sur 30 jours." else "Aucun échec sur 24 heures.",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        } else {
            FailureBars(series, daily)
            Text(
                "${fmtCount(total)} au total" + if (daily) " sur 30 jours" else " sur 24 heures",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(top = 8.dp),
            )
        }
    }
}

/** Barres des échecs par heure ou par jour ; un appui sur une barre affiche son détail. */
@Composable
private fun FailureBars(points: List<SshPoint>, daily: Boolean) {
    val color = Vigie.status.warn
    val empty = MaterialTheme.colorScheme.surfaceContainerHighest
    val reduced = Vigie.reducedMotion
    val haptics = rememberHaptics()
    val grow = remember(daily) { Animatable(if (reduced) 1f else 0f) }
    LaunchedEffect(daily) {
        if (!reduced) grow.animateTo(1f, tween(700, easing = FastOutSlowInEasing))
    }
    var selected by remember(daily, points) { mutableIntStateOf(-1) }
    val top = (points.maxOfOrNull { it.count } ?: 0).coerceAtLeast(1)
    val fmt = remember(daily) { SimpleDateFormat(if (daily) "EEE d MMM" else "EEE HH'h'", Locale.FRANCE) }
    val label = MaterialTheme.typography.labelMedium
    val muted = MaterialTheme.colorScheme.onSurfaceVariant
    Column {
        val sel = points.getOrNull(selected)
        Text(
            if (sel != null) "${fmtCount(sel.count)} · ${fmt.format(Date(sel.ts))}"
            else "Jusqu'à ${fmtCount(top)} par ${if (daily) "jour" else "heure"}",
            style = label, color = muted,
        )
        Spacer(Modifier.height(8.dp))
        Canvas(
            Modifier
                .fillMaxWidth()
                .height(120.dp)
                .pointerInput(points) {
                    detectTapGestures { pos ->
                        if (points.isEmpty()) return@detectTapGestures
                        val i = (pos.x / size.width * points.size).toInt().coerceIn(0, points.lastIndex)
                        selected = if (selected == i) -1 else i
                        haptics.tick()
                    }
                },
        ) {
            val slot = size.width / points.size
            val bar = slot * 0.68f
            val minH = 2.dp.toPx()
            points.forEachIndexed { i, p ->
                val h = if (p.count == 0) minH else maxOf(minH, size.height * p.count / top * grow.value)
                drawRoundRect(
                    color = when {
                        p.count == 0 -> empty
                        selected >= 0 && i != selected -> color.copy(alpha = 0.45f)
                        else -> color
                    },
                    topLeft = Offset(i * slot + (slot - bar) / 2, size.height - h),
                    size = Size(bar, h),
                    cornerRadius = CornerRadius(bar / 3),
                )
            }
        }
        Spacer(Modifier.height(4.dp))
        Row {
            Text(points.firstOrNull()?.let { fmt.format(Date(it.ts)) }.orEmpty(), style = label, color = muted)
            Spacer(Modifier.weight(1f))
            Text(if (daily) "aujourd'hui" else "maintenant", style = label, color = muted)
        }
    }
}

/** « 🇫🇷 France », « Réseau privé » ou « Pays inconnu ». */
private fun countryLabel(code: String, name: String, geo: Boolean): String = when {
    name.isNotEmpty() && code.isNotEmpty() -> "${countryFlag(code)} $name".trim()
    name.isNotEmpty() -> name
    !geo -> ""
    else -> "Pays inconnu"
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun TopIps(list: List<SshIp>, geo: Boolean, modifier: Modifier) {
    val status = Vigie.status
    val scheme = MaterialTheme.colorScheme
    SectionCard(modifier, title = "Adresses les plus insistantes (7 j)") {
        list.take(10).forEachIndexed { i, ip ->
            if (i > 0) HorizontalDivider(Modifier.padding(vertical = 8.dp), color = scheme.outlineVariant.copy(alpha = 0.5f))
            Row(verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text(
                        ip.ip, style = MaterialTheme.typography.bodyMedium.copy(fontFamily = FontFamily.Monospace),
                        maxLines = 1, overflow = TextOverflow.Ellipsis,
                    )
                    val sub = listOf(countryLabel(ip.country, ip.countryName, geo), "vue ${Fmt.ago(ip.last)}").filter { it.isNotEmpty() }
                    Text(sub.joinToString(" · "), style = MaterialTheme.typography.bodySmall, color = scheme.onSurfaceVariant)
                    if (ip.banned || ip.accepted) {
                        FlowRow(Modifier.padding(top = 4.dp), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                            if (ip.accepted) Chip("Connexion réussie", status.criticalContainer, status.onCriticalContainer)
                            if (ip.banned) Chip("Banni", scheme.secondaryContainer, scheme.onSecondaryContainer)
                        }
                    }
                }
                Spacer(Modifier.width(12.dp))
                Text(fmtCount(ip.count), style = MaterialTheme.typography.titleMedium.numeric())
            }
        }
    }
}

@Composable
private fun TopUsers(list: List<SshUser>, modifier: Modifier) {
    val scheme = MaterialTheme.colorScheme
    SectionCard(modifier, title = "Utilisateurs essayés (7 j)") {
        list.take(10).forEachIndexed { i, u ->
            if (i > 0) Spacer(Modifier.height(8.dp))
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    u.user.ifEmpty { "(vide)" },
                    style = MaterialTheme.typography.bodyMedium.copy(fontFamily = FontFamily.Monospace),
                    maxLines = 1, overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f, fill = false),
                )
                if (!u.invalid) {
                    Spacer(Modifier.width(8.dp))
                    Chip("existe", Vigie.status.warnContainer, Vigie.status.onWarnContainer)
                }
                Spacer(Modifier.weight(1f))
                Text(fmtCount(u.count), style = MaterialTheme.typography.titleSmall.numeric(), color = scheme.onSurfaceVariant)
            }
        }
        Text(
            "« existe » : ce compte est présent sur le serveur, seul son mot de passe ou sa clé a été refusé.",
            style = MaterialTheme.typography.bodySmall,
            color = scheme.onSurfaceVariant,
            modifier = Modifier.padding(top = 12.dp),
        )
    }
}

@Composable
private fun Logins(list: List<SshLogin>, modifier: Modifier) {
    val status = Vigie.status
    val scheme = MaterialTheme.colorScheme
    SectionCard(modifier, title = "Connexions réussies (30 j)") {
        if (list.isEmpty()) {
            Text("Aucune connexion réussie.", style = MaterialTheme.typography.bodyMedium, color = scheme.onSurfaceVariant)
        }
        list.take(20).forEachIndexed { i, l ->
            if (i > 0) HorizontalDivider(Modifier.padding(vertical = 8.dp), color = scheme.outlineVariant.copy(alpha = 0.5f))
            Row(verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text("${l.user} · ${l.ip}", style = MaterialTheme.typography.bodyMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    val sub = listOf(Fmt.dateTime(l.ts), sshMethodLabel(l.method), countryLabel(l.country, l.countryName, true))
                        .filter { it.isNotEmpty() && it != "Pays inconnu" }
                    Text(sub.joinToString(" · "), style = MaterialTheme.typography.bodySmall, color = scheme.onSurfaceVariant)
                }
                if (l.newIp) {
                    Spacer(Modifier.width(8.dp))
                    Chip("Nouvelle adresse", status.warnContainer, status.onWarnContainer)
                }
            }
        }
    }
}

@Composable
private fun Footer(s: SshSummary, modifier: Modifier) {
    val lines = buildList {
        if (s.source.isNotEmpty()) add("Source : ${s.source}" + if (s.readAt > 0) ", lue ${Fmt.ago(s.readAt)}" else "")
        when (s.fail2ban) {
            "ok" -> add("fail2ban : bannissements suivis")
            "permission" -> add("fail2ban : journal illisible")
        }
        if (s.geo) add("Pays : service GeoJS (adresses seules, gardées 30 jours en cache)")
        add("Historique : 30 jours")
    }
    Text(
        lines.joinToString("\n"),
        style = MaterialTheme.typography.bodySmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = modifier.padding(horizontal = 8.dp),
    )
}

/** Nombre avec séparateur de milliers : « 12 345 ». */
internal fun fmtCount(n: Int): String = NumberFormat.getIntegerInstance(Locale.FRANCE).format(n)

/** Sous-titre de la ligne « Connexions SSH » de l'écran Sécurité. */
@Composable
internal fun sshSubtitle(default: String): String {
    var text by remember { mutableStateOf(default) }
    LaunchedEffect(Unit) {
        runCatching { appContainer.repository.ssh() }.onSuccess { s -> text = sshSummaryText(s) ?: default }
    }
    return text
}

internal fun sshSummaryText(s: SshSummary): String? = when (s.state) {
    "ok" -> {
        val fails = s.stats.failed24h
        val logins = s.stats.accepted7d
        listOf(
            if (fails > 1) "${fmtCount(fails)} échecs en 24 h" else "$fails échec en 24 h",
            if (logins > 1) "$logins connexions cette semaine" else "$logins connexion cette semaine",
        ).joinToString(" · ")
    }
    "permission" -> "Droits à ajouter pour lire le journal"
    "disabled" -> "Surveillance désactivée"
    "journald", "missing" -> "Journal introuvable"
    else -> null
}
