package com.karelisio.vigie.ui.settings

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
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
import androidx.compose.material.icons.rounded.Add
import androidx.compose.material.icons.rounded.Cloud
import androidx.compose.material.icons.rounded.Delete
import androidx.compose.material.icons.rounded.Edit
import androidx.compose.material.icons.rounded.Key
import androidx.compose.material.icons.rounded.NotificationsActive
import androidx.compose.material.icons.rounded.Refresh
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LoadingIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
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
import com.karelisio.vigie.data.AgentSettings
import com.karelisio.vigie.data.ApiException
import com.karelisio.vigie.data.DebridAccount
import com.karelisio.vigie.data.DebridAccountInput
import com.karelisio.vigie.data.DebridProviders
import com.karelisio.vigie.data.DebridService
import com.karelisio.vigie.data.DebridState
import com.karelisio.vigie.data.Fmt
import com.karelisio.vigie.data.checkDebrid
import com.karelisio.vigie.data.debrid
import com.karelisio.vigie.data.debridState
import com.karelisio.vigie.data.saveDebridAccounts
import com.karelisio.vigie.data.versionAtLeast
import com.karelisio.vigie.ui.components.EmptyState
import com.karelisio.vigie.ui.components.SectionTitle
import com.karelisio.vigie.ui.components.UptimeBars
import com.karelisio.vigie.ui.components.cascade
import com.karelisio.vigie.ui.components.rememberHaptics
import com.karelisio.vigie.ui.components.rememberNow
import com.karelisio.vigie.ui.home.DebridRow
import com.karelisio.vigie.ui.home.ServiceLine
import com.karelisio.vigie.ui.navigation.LocalSnackbar
import com.karelisio.vigie.ui.theme.Vigie
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch

/** Où trouver la clé API de chaque fournisseur. */
private val keyHelp = mapOf(
    "realdebrid" to "Sur real-debrid.com/apitoken",
    "alldebrid" to "Sur alldebrid.com/apikeys (crée une clé « Vigie »)",
    "torbox" to "Sur torbox.app/settings, section API",
    "premiumize" to "Sur premiumize.me/account, « API Key »",
)

private const val MAX_ACCOUNTS = 10

class DebridViewModel : ViewModel() {
    private val repo = appContainer.repository
    val state = repo.debridState().stateIn(viewModelScope, SharingStarted.Eagerly, null)
    private val _error = MutableStateFlow<String?>(null)
    val error = _error.asStateFlow()
    /** L'agent ne connaît pas les comptes debrid (antérieur à 0.5.0). */
    private val _unsupported = MutableStateFlow(false)
    val unsupported = _unsupported.asStateFlow()
    /** Enregistrement ou vérification en cours : l'agent interroge les fournisseurs avant de répondre. */
    var busy by mutableStateOf(false)
        private set

    init {
        load()
    }

    fun load() {
        viewModelScope.launch {
            _error.value = null
            runCatching { repo.debrid() }
                .onFailure { if ((it as? ApiException)?.code == 404) _unsupported.value = true else _error.value = it.message }
        }
    }

    /** Enregistre la liste modifiée par [change] ; les clés actuelles partent masquées, l'agent les garde. */
    suspend fun update(change: (List<DebridAccountInput>) -> List<DebridAccountInput>): Result<DebridState> {
        val current = state.value?.accounts.orEmpty().map { DebridAccountInput(it.id, it.provider, it.label, it.key) }
        busy = true
        return runCatching { repo.saveDebridAccounts(change(current)) }.also { busy = false }
    }

    suspend fun check(): Result<DebridState> {
        busy = true
        return runCatching { repo.checkDebrid() }.also { busy = false }
    }

    /** Réglages de l'agent, pour l'alerte de panne d'un service (agent 0.6 et plus). */
    private val _settings = MutableStateFlow<AgentSettings?>(null)
    val settings = _settings.asStateFlow()

    fun loadSettings() {
        viewModelScope.launch { runCatching { repo.settings() }.onSuccess { _settings.value = it } }
    }

    suspend fun setDownAlert(on: Boolean): Result<AgentSettings> {
        val current = _settings.value ?: return Result.failure(IllegalStateException("réglages indisponibles"))
        _settings.value = current.copy(debridDownAlert = on)
        return runCatching { repo.saveSettings(current.copy(debridDownAlert = on)) }
            .onSuccess { _settings.value = it }
            .onFailure { _settings.value = current }
    }
}

/** État d'un service : pastille et résumé, disponibilité sur 24 h et dernières pannes. */
@Composable
private fun ServiceCard(service: DebridService, now: Long, modifier: Modifier = Modifier) {
    val scheme = MaterialTheme.colorScheme
    Surface(shape = RoundedCornerShape(24.dp), color = scheme.surfaceContainer, modifier = modifier.fillMaxWidth()) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            ServiceLine(service, now)
            if (service.history.isNotEmpty()) {
                UptimeBars(service.buckets(now), height = 24.dp)
                Row {
                    Text("24 h", style = MaterialTheme.typography.labelSmall, color = scheme.onSurfaceVariant, modifier = Modifier.weight(1f))
                    if (service.uptime >= 0) {
                        Text(
                            "${Fmt.uptimePct(service.uptime)} disponible",
                            style = MaterialTheme.typography.labelSmall,
                            color = scheme.onSurfaceVariant,
                        )
                    }
                }
            }
            service.incidents.takeLast(3).reversed().forEach { inc ->
                val end = inc.end.takeIf { it > 0 }
                val length = Fmt.duration(((end ?: now) - inc.start) / 1000)
                Text(
                    buildString {
                        append(Fmt.dateTime(inc.start))
                        append(if (end == null) " · en cours depuis $length" else " · panne de $length")
                        if (inc.cause.isNotEmpty()) append(" · ").append(inc.cause)
                    },
                    style = MaterialTheme.typography.bodySmall,
                    color = if (end == null) Vigie.status.critical else scheme.onSurfaceVariant,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
    }
}

/** Comptes debrid : ajout, modification, suppression et vérification immédiate. */
@Composable
fun DebridScreen(nav: NavHostController, vm: DebridViewModel = viewModel()) {
    val state by vm.state.collectAsStateWithLifecycle()
    val error by vm.error.collectAsStateWithLifecycle()
    val unsupportedApi by vm.unsupported.collectAsStateWithLifecycle()
    val live by appContainer.repository.live.collectAsStateWithLifecycle()
    val unsupported = unsupportedApi || (live.agentVersion.isNotEmpty() && !versionAtLeast(live.agentVersion, 0, 5))
    var adding by remember { mutableStateOf(false) }
    var editing by remember { mutableStateOf<DebridAccount?>(null) }
    var deleting by remember { mutableStateOf<DebridAccount?>(null) }
    val scope = rememberCoroutineScope()
    val snackbar = LocalSnackbar.current
    val haptics = rememberHaptics()
    val now = rememberNow(60_000)
    val bottom = WindowInsets.navigationBars.asPaddingValues().calculateBottomPadding() + 32.dp
    LaunchedEffect(live.agentVersion, live.readOnly) {
        if (!live.readOnly && versionAtLeast(live.agentVersion, 0, 6)) vm.loadSettings()
    }

    fun checkNow() {
        haptics.tick()
        scope.launch {
            vm.check().fold(
                { s -> if (s.accounts.all { it.ok }) haptics.confirm() else haptics.reject() },
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
        item {
            Row(Modifier.statusBarsPadding().padding(top = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                IconButton(onClick = { nav.popBackStack() }) { Icon(Icons.AutoMirrored.Rounded.ArrowBack, "Retour") }
                Text("Comptes debrid", style = MaterialTheme.typography.headlineSmallEmphasized)
            }
        }
        item {
            val v06 = versionAtLeast(live.agentVersion, 0, 6)
            Text(
                if (v06) {
                    "Vigie vérifie tes comptes toutes les 5 minutes : il te prévient si le service ne répond plus, " +
                        "7 jours puis 1 jour avant la fin du premium, ou si une clé est refusée. Les clés restent sur l'agent."
                } else {
                    "Vigie vérifie tes comptes toutes les 6 heures et te prévient 7 jours puis 1 jour avant la fin du premium, " +
                        "ou si une clé est refusée. Les clés restent sur l'agent."
                },
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(horizontal = 4.dp),
            )
        }
        val s = state
        when {
            unsupported -> item {
                EmptyState(
                    Icons.Rounded.Key, "Indisponible sur cet agent",
                    "Mets l'agent à jour (0.5.0 ou plus) pour suivre tes comptes Real-Debrid, AllDebrid, TorBox ou Premiumize.",
                )
            }
            s == null && error != null -> item {
                EmptyState(Icons.Rounded.Cloud, "Comptes indisponibles", error.orEmpty()) {
                    OutlinedButton(onClick = vm::load, shapes = ButtonDefaults.shapes()) { Text("Réessayer") }
                }
            }
            s == null -> item {
                Box(Modifier.fillMaxWidth().padding(48.dp), contentAlignment = Alignment.Center) { LoadingIndicator() }
            }
            s.accounts.isEmpty() -> item {
                EmptyState(Icons.Rounded.Key, "Aucun compte", "Ajoute la clé API de ton service debrid pour suivre la fin de ton premium.") {
                    Button(onClick = { haptics.tick(); adding = true }, shapes = ButtonDefaults.shapes()) {
                        Icon(Icons.Rounded.Add, null)
                        Spacer(Modifier.width(8.dp))
                        Text("Ajouter un compte")
                    }
                }
            }
            else -> {
                if (s.services.isNotEmpty()) {
                    item { SectionTitle("État des services") }
                    items(s.services, key = { "svc-" + it.provider }) { svc ->
                        ServiceCard(svc, now, Modifier.animateItem())
                    }
                    if (!live.readOnly && versionAtLeast(live.agentVersion, 0, 6)) {
                        item {
                            val settings by vm.settings.collectAsStateWithLifecycle()
                            settings?.let { set ->
                                ToggleRow(
                                    Icons.Rounded.NotificationsActive, "Alerte de panne",
                                    "Prévenu quand un service ne répond plus, puis quand il revient",
                                    set.debridDownAlert,
                                ) { on ->
                                    haptics.toggle(on)
                                    scope.launch {
                                        vm.setDownAlert(on).onFailure { snackbar.showSnackbar("Échec : ${it.message}") }
                                    }
                                }
                            }
                        }
                    }
                    item { SectionTitle("Comptes") }
                }
                itemsIndexed(s.accounts, key = { _, a -> a.id }) { i, a ->
                    Surface(
                        onClick = { haptics.tick(); editing = a },
                        enabled = !vm.busy,
                        shape = RoundedCornerShape(24.dp),
                        color = MaterialTheme.colorScheme.surfaceContainer,
                        modifier = Modifier.fillMaxWidth().animateItem().cascade(i),
                    ) {
                        DebridRow(a, now, Modifier.padding(start = 16.dp, top = 16.dp, bottom = 16.dp, end = 4.dp)) {
                            IconButton(onClick = { haptics.tick(); editing = a }, enabled = !vm.busy) { Icon(Icons.Rounded.Edit, "Modifier") }
                            IconButton(onClick = { haptics.tick(); deleting = a }, enabled = !vm.busy) { Icon(Icons.Rounded.Delete, "Supprimer") }
                        }
                    }
                }
                item {
                    Column(Modifier.fillMaxWidth().cascade(s.accounts.size), verticalArrangement = Arrangement.spacedBy(8.dp)) {
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
                        FilledTonalButton(
                            onClick = { haptics.tick(); adding = true },
                            enabled = !vm.busy && s.accounts.size < MAX_ACCOUNTS,
                            shapes = ButtonDefaults.shapes(),
                            modifier = Modifier.fillMaxWidth().height(52.dp),
                        ) {
                            Icon(Icons.Rounded.Add, null)
                            Spacer(Modifier.width(8.dp))
                            Text("Ajouter un compte")
                        }
                    }
                }
            }
        }
    }

    if (adding || editing != null) {
        val initial = editing
        AccountDialog(
            initial = initial,
            onDismiss = { adding = false; editing = null },
        ) { provider, label, key ->
            vm.update { list ->
                if (initial == null) list + DebridAccountInput(provider = provider, label = label, key = key)
                else list.map { if (it.id == initial.id) it.copy(label = label, key = key) else it }
            }.getOrThrow()
            adding = false
            editing = null
            // Portée de l'écran : celle du dialogue, refermé, annulerait le message.
            scope.launch { snackbar.showSnackbar(if (initial == null) "Compte ajouté" else "Compte modifié") }
        }
    }

    deleting?.let { a ->
        AlertDialog(
            onDismissRequest = { deleting = null },
            icon = { Icon(Icons.Rounded.Delete, null) },
            title = { Text("Supprimer ${a.title} ?") },
            text = { Text("Vigie ne surveillera plus ce compte et sa clé sera effacée de l'agent.") },
            confirmButton = {
                TextButton(onClick = {
                    deleting = null
                    scope.launch {
                        vm.update { list -> list.filterNot { it.id == a.id } }.fold(
                            { haptics.confirm(); snackbar.showSnackbar("Compte supprimé") },
                            { haptics.reject(); snackbar.showSnackbar("Échec : ${it.message}") },
                        )
                    }
                }) { Text("Supprimer") }
            },
            dismissButton = { TextButton(onClick = { deleting = null }) { Text("Annuler") } },
        )
    }
}

/**
 * Ajout ou modification d'un compte : fournisseur (fixé une fois le compte créé), nom et clé API.
 * [onSave] enregistre sur l'agent, qui vérifie la clé ; son erreur s'affiche telle quelle.
 */
@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun AccountDialog(
    initial: DebridAccount?,
    onDismiss: () -> Unit,
    onSave: suspend (provider: String, label: String, key: String) -> Unit,
) {
    var provider by remember { mutableStateOf(initial?.provider ?: DebridProviders.keys.first()) }
    var label by remember { mutableStateOf(initial?.label.orEmpty()) }
    var key by remember { mutableStateOf("") }
    var saving by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    val scope = rememberCoroutineScope()
    val haptics = rememberHaptics()
    val name = DebridProviders[provider] ?: provider
    val valid = label.length <= 40 && (initial != null || key.isNotBlank())

    AlertDialog(
        onDismissRequest = { if (!saving) onDismiss() },
        icon = { Icon(Icons.Rounded.Key, null) },
        title = { Text(if (initial == null) "Nouveau compte" else "Modifier le compte") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    DebridProviders.forEach { (id, title) ->
                        FilterChip(
                            selected = provider == id,
                            onClick = { haptics.tick(); provider = id; error = null },
                            label = { Text(title) },
                            enabled = initial == null && !saving,
                        )
                    }
                }
                OutlinedTextField(
                    label, { label = it.take(40); error = null },
                    label = { Text("Nom (facultatif)") },
                    placeholder = { Text(name) },
                    singleLine = true,
                    enabled = !saving,
                    modifier = Modifier.fillMaxWidth(),
                )
                SecretField(
                    key, { key = it; error = null }, "Clé API", initial?.key.orEmpty(),
                    enabled = !saving, hint = keyHelp[provider],
                )
                error?.let { Text(it, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.error) }
            }
        },
        confirmButton = {
            Button(
                onClick = {
                    saving = true
                    error = null
                    scope.launch {
                        runCatching { onSave(provider, label.trim().ifEmpty { name }, key) }
                            .onSuccess { haptics.confirm() }
                            .onFailure {
                                haptics.reject()
                                error = it.message
                                saving = false
                            }
                    }
                },
                enabled = valid && !saving,
                shapes = ButtonDefaults.shapes(),
            ) {
                if (saving) LoadingIndicator(Modifier.size(20.dp)) else Text(if (initial == null) "Ajouter" else "Enregistrer")
            }
        },
        dismissButton = { TextButton(onClick = onDismiss, enabled = !saving) { Text("Annuler") } },
    )
}
