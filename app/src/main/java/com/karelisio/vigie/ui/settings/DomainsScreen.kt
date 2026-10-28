package com.karelisio.vigie.ui.settings

import androidx.compose.animation.animateColorAsState
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
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Add
import androidx.compose.material.icons.rounded.CloudOff
import androidx.compose.material.icons.rounded.Delete
import androidx.compose.material.icons.rounded.HourglassEmpty
import androidx.compose.material.icons.rounded.Language
import androidx.compose.material.icons.rounded.QuestionMark
import androidx.compose.material.icons.rounded.Refresh
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LoadingIndicator
import androidx.compose.material3.MaterialShapes
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.toShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardCapitalization
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.NavHostController
import com.karelisio.vigie.appContainer
import com.karelisio.vigie.data.ApiException
import com.karelisio.vigie.data.DomainStatus
import com.karelisio.vigie.data.Fmt
import com.karelisio.vigie.data.byUrgency
import com.karelisio.vigie.data.checkDomains
import com.karelisio.vigie.data.cleanDomainInput
import com.karelisio.vigie.data.domains
import com.karelisio.vigie.data.matchingDomain
import com.karelisio.vigie.data.notableStatus
import com.karelisio.vigie.data.summary
import com.karelisio.vigie.data.tone
import com.karelisio.vigie.data.versionAtLeast
import com.karelisio.vigie.ui.components.EmptyState
import com.karelisio.vigie.ui.components.ScreenHeader
import com.karelisio.vigie.ui.components.SectionCard
import com.karelisio.vigie.ui.components.cascade
import com.karelisio.vigie.ui.components.rememberHaptics
import com.karelisio.vigie.ui.components.rememberNow
import com.karelisio.vigie.ui.navigation.LocalSnackbar
import com.karelisio.vigie.ui.theme.Vigie
import com.karelisio.vigie.ui.theme.numeric
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

private const val MAX_DOMAINS = 50

class DomainsViewModel : ViewModel() {
    private val repo = appContainer.repository
    private val _list = MutableStateFlow<List<DomainStatus>?>(null)
    val list = _list.asStateFlow()
    /** Préavis d'alerte en jours (réglage de l'agent). */
    private val _notice = MutableStateFlow(30)
    val notice = _notice.asStateFlow()
    private val _manual = MutableStateFlow<List<String>>(emptyList())
    val manual = _manual.asStateFlow()
    private val _error = MutableStateFlow<String?>(null)
    val error = _error.asStateFlow()
    private val _unsupported = MutableStateFlow(false)
    val unsupported = _unsupported.asStateFlow()
    var busy by mutableStateOf(false)
        private set

    init {
        load()
    }

    fun load() {
        viewModelScope.launch {
            _error.value = null
            runCatching {
                val s = repo.settings()
                _notice.value = s.domainDays
                _manual.value = s.domains
                _list.value = repo.domains().byUrgency()
            }.onFailure { if ((it as? ApiException)?.code == 404) _unsupported.value = true else _error.value = it.message }
        }
    }

    /** Modifie la liste des domaines ajoutés à la main (réglages relus juste avant, pour ne rien écraser). */
    private suspend fun changeManual(change: (List<String>) -> List<String>) {
        val s = repo.settings()
        val saved = repo.saveSettings(s.copy(domains = change(s.domains)))
        _manual.value = saved.domains
        _notice.value = saved.domainDays
    }

    /** Ajoute un domaine puis le vérifie aussitôt. */
    suspend fun add(domain: String): Result<DomainStatus?> {
        busy = true
        return runCatching {
            changeManual { it + domain }
            val list = repo.checkDomains().byUrgency()
            _list.value = list
            list.firstOrNull { domain == it.domain || domain.endsWith(".${it.domain}") }
        }.also { busy = false }
    }

    /** Retire un domaine de la liste ; il reste suivi s'il sert à un service. */
    suspend fun remove(d: DomainStatus): Result<DomainStatus?> {
        busy = true
        return runCatching {
            changeManual { list -> list - list.matchingDomain(d.domain).toSet() }
            val list = repo.domains().byUrgency()
            _list.value = list
            list.firstOrNull { it.domain == d.domain }
        }.also { busy = false }
    }

    suspend fun setNotice(days: Int): Result<Unit> = runCatching {
        val s = repo.settings()
        _notice.value = repo.saveSettings(s.copy(domainDays = days)).domainDays
    }

    suspend fun check(): Result<List<DomainStatus>> {
        busy = true
        return runCatching { repo.checkDomains().byUrgency().also { _list.value = it } }.also { busy = false }
    }
}

/** Noms de domaine : date d'expiration, ajout et retrait à la main, préavis d'alerte, vérification immédiate. */
@OptIn(ExperimentalLayoutApi::class)
@Composable
fun DomainsScreen(nav: NavHostController, vm: DomainsViewModel = viewModel()) {
    val list by vm.list.collectAsStateWithLifecycle()
    val notice by vm.notice.collectAsStateWithLifecycle()
    val manual by vm.manual.collectAsStateWithLifecycle()
    val error by vm.error.collectAsStateWithLifecycle()
    val unsupportedApi by vm.unsupported.collectAsStateWithLifecycle()
    val live by appContainer.repository.live.collectAsStateWithLifecycle()
    val unsupported = unsupportedApi || (live.agentVersion.isNotEmpty() && !versionAtLeast(live.agentVersion, 0, 6))
    var adding by remember { mutableStateOf(false) }
    var removing by remember { mutableStateOf<DomainStatus?>(null) }
    val scope = rememberCoroutineScope()
    val snackbar = LocalSnackbar.current
    val haptics = rememberHaptics()
    val now = rememberNow(60_000)
    val bottom = WindowInsets.navigationBars.asPaddingValues().calculateBottomPadding() + 32.dp

    fun checkNow() {
        haptics.tick()
        scope.launch {
            vm.check().fold(
                { l -> if (l.all { it.error.isEmpty() }) haptics.confirm() else haptics.reject() },
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
        item { ScreenHeader("Noms de domaine", nav) }
        item {
            Text(
                "Vigie lit chaque jour la date d'expiration de tes domaines chez leur registre : ceux de tes services, " +
                    "plus ceux que tu ajoutes. Alerte $notice jours avant, puis 7 jours avant.",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(horizontal = 4.dp),
            )
        }
        val l = list
        when {
            unsupported -> item {
                EmptyState(Icons.Rounded.Language, "Indisponible sur cet agent", "Mets l'agent à jour (0.6.0 ou plus) pour suivre l'expiration de tes domaines.")
            }
            l == null && error != null -> item {
                EmptyState(Icons.Rounded.CloudOff, "Domaines indisponibles", error.orEmpty()) {
                    OutlinedButton(onClick = vm::load, shapes = ButtonDefaults.shapes()) { Text("Réessayer") }
                }
            }
            l == null -> item {
                Box(Modifier.fillMaxWidth().padding(48.dp), contentAlignment = Alignment.Center) { LoadingIndicator() }
            }
            else -> {
                if (l.isEmpty()) {
                    item {
                        EmptyState(
                            Icons.Rounded.Language, "Aucun domaine",
                            "Ajoute un service avec une adresse web, ou un domaine à la main, pour suivre son expiration.",
                        )
                    }
                }
                itemsIndexed(l, key = { _, d -> d.domain }) { i, d ->
                    DomainRow(
                        d, notice, now,
                        Modifier.fillMaxWidth().animateItem().cascade(i),
                        onRemove = if (d.manual) ({ haptics.tick(); removing = d }) else null,
                        enabled = !vm.busy,
                    )
                }
                item {
                    Column(Modifier.fillMaxWidth().cascade(l.size), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                        if (l.isNotEmpty()) {
                            OutlinedButton(
                                onClick = { checkNow() },
                                enabled = !vm.busy,
                                shapes = ButtonDefaults.shapes(),
                                modifier = Modifier.fillMaxWidth().height(52.dp),
                            ) {
                                if (vm.busy) LoadingIndicator(Modifier.size(24.dp)) else Icon(Icons.Rounded.Refresh, null)
                                Spacer(Modifier.width(8.dp))
                                Text(if (vm.busy) "Vérification…" else "Vérifier maintenant")
                            }
                        }
                        FilledTonalButton(
                            onClick = { haptics.tick(); adding = true },
                            enabled = !vm.busy && manual.size < MAX_DOMAINS,
                            shapes = ButtonDefaults.shapes(),
                            modifier = Modifier.fillMaxWidth().height(52.dp),
                        ) {
                            Icon(Icons.Rounded.Add, null)
                            Spacer(Modifier.width(8.dp))
                            Text("Ajouter un domaine")
                        }
                    }
                }
                item {
                    SectionCard(Modifier.cascade(l.size + 1), title = "Préavis d'alerte") {
                        FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                            (listOf(14, 30, 45, 60, 90) + notice).distinct().sorted().forEach { n ->
                                FilterChip(
                                    selected = notice == n,
                                    onClick = {
                                        if (n == notice) return@FilterChip
                                        haptics.tick()
                                        scope.launch {
                                            vm.setNotice(n).exceptionOrNull()?.let { snackbar.showSnackbar("Échec : ${it.message}") }
                                        }
                                    },
                                    label = { Text("$n jours") },
                                )
                            }
                        }
                        Text(
                            "Première alerte à ce délai, puis une alerte critique 7 jours avant et à l'expiration. " +
                                "Une seule fois par seuil ; un renouvellement clôt l'alerte.",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                }
            }
        }
    }

    if (adding) {
        AddDomainDialog(
            known = list.orEmpty().map { it.domain } + manual,
            onDismiss = { adding = false },
        ) { domain ->
            val added = vm.add(domain).getOrThrow()
            adding = false
            scope.launch {
                snackbar.showSnackbar(
                    when {
                        added == null -> "Domaine ajouté"
                        added.error.isNotEmpty() -> "${added.domain} ajouté, mais : ${added.error}"
                        added.daysLeft != null -> "${added.domain} : ${added.summary().replaceFirstChar { it.lowercase() }}"
                        else -> "${added.domain} ajouté"
                    },
                )
            }
        }
    }

    removing?.let { d ->
        val stays = d.services.isNotEmpty()
        AlertDialog(
            onDismissRequest = { removing = null },
            icon = { Icon(Icons.Rounded.Delete, null) },
            title = { Text("Retirer ${d.domain} ?") },
            text = {
                Text(
                    if (stays) "Il restera suivi, car ${d.services.joinToString(", ")} l'utilise."
                    else "Vigie ne surveillera plus son expiration.",
                )
            },
            confirmButton = {
                TextButton(onClick = {
                    removing = null
                    scope.launch {
                        vm.remove(d).fold(
                            { still ->
                                haptics.confirm()
                                snackbar.showSnackbar(if (still != null) "Retiré de ta liste, toujours suivi pour tes services" else "Domaine retiré")
                            },
                            { haptics.reject(); snackbar.showSnackbar("Échec : ${it.message}") },
                        )
                    }
                }) { Text("Retirer") }
            },
            dismissButton = { TextButton(onClick = { removing = null }) { Text("Annuler") } },
        )
    }
}

/** Carte d'un domaine : jours restants en grand (couleur selon l'urgence), échéance, bureau d'enregistrement, source. */
@Composable
private fun DomainRow(d: DomainStatus, notice: Int, now: Long, modifier: Modifier, onRemove: (() -> Unit)?, enabled: Boolean) {
    val scheme = MaterialTheme.colorScheme
    val tone = d.tone(notice)
    Surface(shape = RoundedCornerShape(24.dp), color = scheme.surfaceContainer, modifier = modifier) {
        Row(Modifier.padding(start = 16.dp, top = 16.dp, bottom = 16.dp, end = 8.dp), verticalAlignment = Alignment.CenterVertically) {
            DaysBadge(d, tone)
            Spacer(Modifier.width(14.dp))
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                Text(d.domain, style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
                Text(
                    d.summary(),
                    style = MaterialTheme.typography.bodyMedium,
                    color = if (tone == "critical" || tone == "warning") Vigie.status.of(tone) else scheme.onSurface,
                    maxLines = 3,
                    overflow = TextOverflow.Ellipsis,
                )
                if (d.daysLeft == null && d.checkedAt > 0 && d.error.isEmpty() && d.note.isNotEmpty()) {
                    Text(d.note.replaceFirstChar { it.uppercase() } + ".", style = MaterialTheme.typography.bodySmall, color = scheme.onSurfaceVariant)
                }
                val source = if (d.manual) "Ajouté à la main" else "Utilisé par ${d.services.joinToString(", ")}"
                Text(
                    listOfNotNull(d.registrar.takeIf { it.isNotEmpty() }, source).joinToString(" · "),
                    style = MaterialTheme.typography.bodySmall,
                    color = scheme.onSurfaceVariant,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )
                val details = listOfNotNull(
                    d.notableStatus().takeIf { it.isNotEmpty() }?.joinToString(" · "),
                    d.checkedAt.takeIf { it > 0 }?.let { "vérifié ${Fmt.ago(it, now)}" },
                )
                if (details.isNotEmpty()) {
                    Text(details.joinToString(" · "), style = MaterialTheme.typography.labelSmall, color = scheme.onSurfaceVariant)
                }
            }
            if (onRemove != null) {
                IconButton(onClick = onRemove, enabled = enabled) { Icon(Icons.Rounded.Delete, "Retirer ${d.domain}") }
            }
        }
    }
}

/** Jours restants dans une forme expressive ; sans date, une icône dit pourquoi. */
@Composable
private fun DaysBadge(d: DomainStatus, tone: String) {
    val status = Vigie.status
    val container by animateColorAsState(status.containerOf(tone), label = "domain-tone")
    val left = d.daysLeft
    val description = when {
        left == null -> d.summary()
        left < 0 -> "Expiré"
        left <= 1 -> "$left jour restant"
        else -> "$left jours restants"
    }
    Surface(
        shape = (if (tone == "critical") MaterialShapes.SoftBurst else MaterialShapes.Cookie9Sided).toShape(),
        color = container,
        contentColor = status.onContainerOf(tone),
        modifier = Modifier.size(64.dp).clearAndSetSemantics { contentDescription = description },
    ) {
        Box(contentAlignment = Alignment.Center) {
            when {
                left != null && left >= 0 -> Column(horizontalAlignment = Alignment.CenterHorizontally) {
                    Text(if (left > 999) "999+" else "$left", style = MaterialTheme.typography.titleLarge.numeric())
                    Text(if (left <= 1) "jour" else "jours", style = MaterialTheme.typography.labelSmall)
                }
                left != null -> Text("Expiré", style = MaterialTheme.typography.labelMedium)
                d.checkedAt == 0L && d.error.isEmpty() -> Icon(Icons.Rounded.HourglassEmpty, null)
                d.error.isNotEmpty() -> Icon(Icons.Rounded.CloudOff, null)
                else -> Icon(Icons.Rounded.QuestionMark, null)
            }
        }
    }
}

/** Saisie d'un domaine : vérifié par l'agent dès l'ajout. */
@Composable
private fun AddDomainDialog(known: List<String>, onDismiss: () -> Unit, onSave: suspend (String) -> Unit) {
    var text by remember { mutableStateOf("") }
    var saving by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    val scope = rememberCoroutineScope()
    val haptics = rememberHaptics()
    val clean = cleanDomainInput(text)
    val duplicate = clean != null && known.any { it == clean }

    fun submit() {
        val d = clean ?: return
        saving = true
        error = null
        scope.launch {
            runCatching { onSave(d) }
                .onSuccess { haptics.confirm() }
                .onFailure {
                    haptics.reject()
                    error = it.message
                    saving = false
                }
        }
    }

    AlertDialog(
        onDismissRequest = { if (!saving) onDismiss() },
        icon = { Icon(Icons.Rounded.Language, null) },
        title = { Text("Ajouter un domaine") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedTextField(
                    text, { text = it.take(260); error = null },
                    label = { Text("Nom de domaine") },
                    placeholder = { Text("exemple.fr") },
                    singleLine = true,
                    enabled = !saving,
                    isError = (text.isNotBlank() && clean == null) || duplicate,
                    supportingText = {
                        Text(
                            when {
                                duplicate -> "Déjà suivi."
                                text.isNotBlank() && clean == null -> "Nom de domaine invalide."
                                else -> "Un sous-domaine compte pour son domaine (vigie.exemple.fr → exemple.fr)."
                            },
                        )
                    },
                    keyboardOptions = KeyboardOptions(
                        capitalization = KeyboardCapitalization.None, autoCorrectEnabled = false,
                        keyboardType = KeyboardType.Uri, imeAction = ImeAction.Done,
                    ),
                    keyboardActions = KeyboardActions(onDone = { if (clean != null && !duplicate && !saving) submit() }),
                    modifier = Modifier.fillMaxWidth(),
                )
                error?.let { Text(it, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.error) }
            }
        },
        confirmButton = {
            Button(onClick = { submit() }, enabled = clean != null && !duplicate && !saving, shapes = ButtonDefaults.shapes()) {
                if (saving) LoadingIndicator(Modifier.size(20.dp)) else Text("Ajouter")
            }
        },
        dismissButton = { TextButton(onClick = onDismiss, enabled = !saving) { Text("Annuler") } },
    )
}
