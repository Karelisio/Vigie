package com.karelisio.vigie.ui.settings

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.animateContentSize
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInVertically
import androidx.compose.animation.slideOutVertically
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
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.selection.toggleable
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
import androidx.compose.material.icons.automirrored.rounded.Undo
import androidx.compose.material.icons.rounded.Backup
import androidx.compose.material.icons.rounded.Check
import androidx.compose.material.icons.rounded.CheckCircle
import androidx.compose.material.icons.rounded.Cloud
import androidx.compose.material.icons.rounded.CloudSync
import androidx.compose.material.icons.rounded.ErrorOutline
import androidx.compose.material.icons.automirrored.rounded.HelpOutline
import androidx.compose.material.icons.rounded.History
import androidx.compose.material.icons.rounded.Restore
import androidx.compose.material.icons.rounded.Save
import androidx.compose.material.icons.rounded.Schedule
import androidx.compose.material.icons.rounded.Visibility
import androidx.compose.material.icons.rounded.VisibilityOff
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Checkbox
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
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.NavHostController
import com.karelisio.vigie.appContainer
import com.karelisio.vigie.data.AgentJob
import com.karelisio.vigie.data.ApiException
import com.karelisio.vigie.data.BACKUP_SECRET_MASK
import com.karelisio.vigie.data.BackupConfig
import com.karelisio.vigie.data.BackupCopies
import com.karelisio.vigie.data.BackupItem
import com.karelisio.vigie.data.BackupObject
import com.karelisio.vigie.data.BackupRecord
import com.karelisio.vigie.data.BackupSelection
import com.karelisio.vigie.data.BackupState
import com.karelisio.vigie.data.BackupTestResult
import com.karelisio.vigie.data.Fmt
import com.karelisio.vigie.data.backupHistory
import com.karelisio.vigie.data.backupItems
import com.karelisio.vigie.data.backupObjects
import com.karelisio.vigie.data.backupState
import com.karelisio.vigie.data.groupCopies
import com.karelisio.vigie.data.restorable
import com.karelisio.vigie.data.restoreBackup
import com.karelisio.vigie.data.runBackup
import com.karelisio.vigie.data.saveBackupConfig
import com.karelisio.vigie.data.testBackupStorage
import com.karelisio.vigie.data.versionAtLeast
import com.karelisio.vigie.security.LocalActionGuard
import com.karelisio.vigie.ui.components.EmptyState
import com.karelisio.vigie.ui.components.JobProgress
import com.karelisio.vigie.ui.components.SectionCard
import com.karelisio.vigie.ui.components.StatusDot
import com.karelisio.vigie.ui.components.cascade
import com.karelisio.vigie.ui.components.rememberHaptics
import com.karelisio.vigie.ui.navigation.LocalSnackbar
import com.karelisio.vigie.ui.theme.Vigie
import com.karelisio.vigie.ui.theme.numeric
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

/** Vrai si l'agent gère les sauvegardes (0.5 ou plus) et que l'accès permet de les régler. */
@Composable
fun backupsSupported(): Boolean {
    val live = appContainer.repository.live.collectAsStateWithLifecycle()
    // Seul le résultat est observé : pas de recomposition à chaque mise à jour du direct.
    val supported by remember { derivedStateOf { versionAtLeast(live.value.agentVersion, 0, 5) && !live.value.readOnly } }
    return supported
}

class BackupsViewModel : ViewModel() {
    private val repo = appContainer.repository
    val live = repo.live
    private val _state = MutableStateFlow<BackupState?>(null)
    val state = _state.asStateFlow()
    private val _error = MutableStateFlow<String?>(null)
    val error = _error.asStateFlow()
    private val _items = MutableStateFlow<List<BackupItem>?>(null)
    val items = _items.asStateFlow()
    private val _itemsError = MutableStateFlow<String?>(null)
    val itemsError = _itemsError.asStateFlow()
    private val _history = MutableStateFlow<List<BackupRecord>>(emptyList())
    val history = _history.asStateFlow()
    private val _objects = MutableStateFlow<Result<List<BackupObject>>?>(null)
    val objects = _objects.asStateFlow()
    /** Identifiant de la sauvegarde lancée depuis cet écran. */
    var jobId by mutableStateOf<String?>(null)
        private set

    init {
        load()
    }

    fun load() {
        viewModelScope.launch {
            _error.value = null
            runCatching { repo.backupState() }
                .onSuccess {
                    _state.value = it
                    loadItems()
                    refreshHistory()
                }
                .onFailure {
                    _error.value = if (it is ApiException && it.code == 404) {
                        "Ton agent ne gère pas encore les sauvegardes : mets-le à jour (0.5.0 ou plus)."
                    } else it.message
                }
        }
    }

    fun loadItems() {
        viewModelScope.launch {
            _itemsError.value = null
            runCatching { repo.backupItems() }.onSuccess { _items.value = it }.onFailure { _itemsError.value = it.message }
        }
    }

    fun refreshHistory() {
        viewModelScope.launch {
            runCatching { repo.backupHistory() }.onSuccess { _history.value = it }
            runCatching { repo.backupState() }.onSuccess { s -> _state.value = _state.value?.copy(nextRun = s.nextRun) ?: s }
        }
    }

    fun loadObjects() {
        viewModelScope.launch {
            _objects.value = null
            _objects.value = runCatching { repo.backupObjects() }
        }
    }

    suspend fun save(c: BackupConfig): Result<BackupState> =
        runCatching { repo.saveBackupConfig(c) }.onSuccess { _state.value = it }

    suspend fun test(c: BackupConfig): BackupTestResult =
        runCatching { repo.testBackupStorage(c) }.getOrElse { BackupTestResult(false, it.message ?: "Échec du test") }

    suspend fun run(): Result<Unit> = runCatching { jobId = repo.runBackup().id }

    /** Identifiant de la restauration lancée depuis cet écran. */
    var restoreJobId by mutableStateOf<String?>(null)
        private set
    private var restoreShown: String? = null

    suspend fun restore(item: BackupItem, copy: BackupObject): Result<Unit> =
        runCatching { restoreJobId = repo.restoreBackup(item.id, copy.key).id }

    /** Vrai une seule fois, à la fin de la restauration lancée ici (résultat à signaler). */
    fun takeRestoreResult(id: String): Boolean {
        if (id != restoreJobId || id == restoreShown) return false
        restoreShown = id
        return true
    }
}

/** Restauration depuis l'appli : visible hors lecture seule, bloquée (raison) pour un agent trop ancien. */
private data class RestoreState(val visible: Boolean, val blocked: String?, val busy: Boolean, val job: AgentJob?) {
    val allowed get() = visible && blocked == null
}

/** Sauvegardes vers un stockage compatible S3 : stockage, éléments, planification, historique. */
@OptIn(ExperimentalLayoutApi::class)
@Composable
fun BackupsScreen(nav: NavHostController, vm: BackupsViewModel = viewModel()) {
    val state by vm.state.collectAsStateWithLifecycle()
    val error by vm.error.collectAsStateWithLifecycle()
    val items by vm.items.collectAsStateWithLifecycle()
    val itemsError by vm.itemsError.collectAsStateWithLifecycle()
    val history by vm.history.collectAsStateWithLifecycle()
    val objects by vm.objects.collectAsStateWithLifecycle()
    val live by vm.live.collectAsStateWithLifecycle()
    val saved = state?.config
    var draft by remember(saved) { mutableStateOf(saved) }
    var saving by remember { mutableStateOf(false) }
    var picking by remember { mutableStateOf(false) }
    var confirm by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    val snackbar = LocalSnackbar.current
    val guard = LocalActionGuard.current
    val haptics = rememberHaptics()
    val dirty = draft != null && draft != saved
    val bottom = WindowInsets.navigationBars.asPaddingValues().calculateBottomPadding() + 96.dp
    // Sauvegarde lancée ici, ou en cours (planifiée, ou lancée depuis un autre appareil).
    val job = live.jobs.firstOrNull { it.id == vm.jobId } ?: live.jobs.firstOrNull { it.kind == "backup" && !it.done }

    // Une fois la sauvegarde terminée : historique et prochaine échéance à jour.
    LaunchedEffect(job?.done) { if (job?.done == true) vm.refreshHistory() }

    var restoring by remember { mutableStateOf<Pair<BackupItem, BackupObject>?>(null) }
    val restoreJob = live.jobs.firstOrNull { it.id == vm.restoreJobId } ?: live.jobs.firstOrNull { it.kind == "restore" && !it.done }
    val restore = RestoreState(
        visible = !live.readOnly,
        blocked = when {
            !versionAtLeast(live.agentVersion, 0, 6) -> "Mets l'agent à jour (0.6.0 ou plus) pour restaurer depuis l'appli."
            !live.updatesEnabled -> "Restauration désactivée sur cet agent (VIGIE_ALLOW_UPDATES=false)."
            else -> null
        },
        busy = live.runningJob != null,
        job = restoreJob,
    )
    // Fin de la restauration lancée ici : résultat, et copies à jour (nouvelle copie de sécurité).
    LaunchedEffect(restoreJob?.done) {
        val j = restoreJob ?: return@LaunchedEffect
        if (j.done && vm.takeRestoreResult(j.id)) {
            if (j.ok) haptics.confirm() else haptics.reject()
            vm.loadObjects()
            snackbar.showSnackbar(if (j.ok) "Restauration terminée" else "Échec de la restauration : détail dans « Copies dans le stockage »")
        }
    }

    Box(Modifier.fillMaxSize()) {
        LazyColumn(
            Modifier.fillMaxSize(),
            contentPadding = PaddingValues(start = 16.dp, end = 16.dp, bottom = bottom),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            item {
                Row(Modifier.statusBarsPadding().padding(top = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                    IconButton(onClick = { nav.popBackStack() }) { Icon(Icons.AutoMirrored.Rounded.ArrowBack, "Retour") }
                    Text("Sauvegardes", style = MaterialTheme.typography.headlineSmallEmphasized)
                }
            }
            item {
                Text(
                    "Copie les volumes choisis et les données de Vigie vers un stockage compatible S3 : " +
                        "Oracle Object Storage, Backblaze B2, Cloudflare R2, MinIO…",
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(horizontal = 4.dp),
                )
            }
            val d = draft
            when {
                error != null -> item {
                    EmptyState(Icons.Rounded.Cloud, "Sauvegardes indisponibles", error.orEmpty()) {
                        OutlinedButton(onClick = vm::load) { Text("Réessayer") }
                    }
                }
                d == null || saved == null -> item {
                    Box(Modifier.fillMaxWidth().padding(48.dp), contentAlignment = Alignment.Center) { LoadingIndicator() }
                }
                else -> {
                    item {
                        SectionCard(Modifier.cascade(0), title = "Maintenant") {
                            if (job != null) {
                                JobProgress(job)
                                Spacer(Modifier.height(12.dp))
                            }
                            val blocked = when {
                                dirty -> "Enregistre d'abord tes modifications."
                                !saved.storageReady -> "Renseigne le stockage ci-dessous."
                                saved.items.isEmpty() -> "Choisis au moins un élément ci-dessous."
                                live.runningJob != null -> "Une opération est en cours…"
                                else -> null
                            }
                            Button(
                                onClick = { haptics.tick(); confirm = true },
                                enabled = blocked == null,
                                shapes = ButtonDefaults.shapes(),
                                modifier = Modifier.fillMaxWidth().height(56.dp),
                            ) {
                                Icon(Icons.Rounded.Backup, null)
                                Spacer(Modifier.width(8.dp))
                                Text("Sauvegarder maintenant")
                            }
                            Spacer(Modifier.height(8.dp))
                            val next = state?.nextRun ?: 0
                            Text(
                                blocked ?: if (next > 0) "Prochaine sauvegarde : ${Fmt.dateTime(next)}" else "Sauvegarde quotidienne désactivée.",
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                    }
                    item {
                        SectionCard(Modifier.cascade(1), title = "Chaque jour") {
                            ToggleRow(
                                Icons.Rounded.Schedule, "Sauvegarde quotidienne",
                                "À l'heure choisie ; un échec t'est signalé par une alerte",
                                d.schedule,
                            ) { on -> haptics.toggle(on); draft = d.copy(schedule = on) }
                            AnimatedVisibility(d.schedule) {
                                OutlinedButton(
                                    onClick = { picking = true },
                                    shapes = ButtonDefaults.shapes(),
                                    modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
                                ) {
                                    Icon(Icons.Rounded.Schedule, null)
                                    Spacer(Modifier.width(8.dp))
                                    Text("À ${d.time}")
                                }
                            }
                            Spacer(Modifier.height(12.dp))
                            Text("Copies gardées par élément", style = MaterialTheme.typography.labelLarge)
                            Spacer(Modifier.height(4.dp))
                            FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                                (listOf(3, 7, 14, 30) + d.retention).distinct().sorted().forEach { n ->
                                    FilterChip(
                                        selected = d.retention == n,
                                        onClick = { haptics.tick(); draft = d.copy(retention = n) },
                                        label = { Text("$n") },
                                    )
                                }
                            }
                            Text(
                                "Les plus anciennes sont supprimées après chaque copie réussie.",
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                    }
                    item { StorageCard(d, saved, vm, Modifier.cascade(2)) { draft = it } }
                    item {
                        ItemsCard(d, items, itemsError, vm::loadItems, Modifier.cascade(3)) { draft = it }
                    }
                    item { HistoryCard(history, Modifier.cascade(4)) }
                    if (saved.storageReady) {
                        item {
                            ObjectsCard(objects, items.orEmpty(), restore, vm::loadObjects, Modifier.cascade(5)) { item, copy ->
                                haptics.tick()
                                restoring = item to copy
                            }
                        }
                    }
                }
            }
        }

        AnimatedVisibility(
            dirty,
            modifier = Modifier.align(Alignment.BottomCenter).navigationBarsPadding().padding(16.dp),
            enter = slideInVertically { it } + fadeIn(),
            exit = slideOutVertically { it } + fadeOut(),
        ) {
            Button(
                onClick = {
                    val c = draft ?: return@Button
                    saving = true
                    scope.launch {
                        val r = vm.save(c)
                        saving = false
                        if (r.isSuccess) haptics.confirm() else haptics.reject()
                        snackbar.showSnackbar(r.fold({ "Sauvegardes enregistrées sur l'agent" }, { "Échec : ${it.message}" }))
                    }
                },
                enabled = !saving,
                shapes = ButtonDefaults.shapes(),
                modifier = Modifier.height(56.dp),
            ) {
                if (saving) LoadingIndicator(Modifier.height(24.dp)) else Icon(Icons.Rounded.Save, null)
                Spacer(Modifier.width(8.dp))
                Text("Enregistrer")
            }
        }
    }

    val d = draft
    if (picking && d != null) {
        TimeDialog(initial = d.time, onDismiss = { picking = false }) { t ->
            draft = d.copy(time = t)
            picking = false
        }
    }
    if (confirm && saved != null) {
        val stopped = saved.items.filter { it.stop }
            .flatMap { sel -> items.orEmpty().firstOrNull { it.id == sel.id }?.containers.orEmpty() }
            .distinct()
        AlertDialog(
            onDismissRequest = { confirm = false },
            icon = { Icon(Icons.Rounded.Backup, null) },
            title = { Text("Sauvegarder maintenant ?") },
            text = {
                val what = if (saved.items.size == 1) "1 élément sera copié" else "${saved.items.size} éléments seront copiés"
                Text(
                    "$what vers le bucket ${saved.bucket}." +
                        if (stopped.isNotEmpty()) " ${stopped.joinToString(", ")} : arrêt pendant la copie, puis redémarrage." else "",
                )
            },
            confirmButton = {
                TextButton(onClick = {
                    confirm = false
                    scope.launch {
                        if (!guard("Lancer une sauvegarde")) return@launch
                        vm.run().exceptionOrNull()?.let {
                            haptics.reject()
                            snackbar.showSnackbar("Échec : ${it.message}")
                        }
                    }
                }) { Text("Sauvegarder") }
            },
            dismissButton = { TextButton(onClick = { confirm = false }) { Text("Annuler") } },
        )
    }
    restoring?.let { (item, copy) ->
        // Seuls les conteneurs en marche sont arrêtés puis relancés.
        val running = item.containers.filter { name -> live.containers.any { it.name == name && it.state == "running" } }
        RestoreDialog(item, copy, running, onDismiss = { restoring = null }) {
            restoring = null
            scope.launch {
                val what = if (copy.safety) "Annuler la dernière restauration de ${item.name}" else "Restaurer ${item.name}"
                if (!guard(what)) return@launch
                vm.restore(item, copy).exceptionOrNull()?.let {
                    haptics.reject()
                    snackbar.showSnackbar("Échec : ${it.message}")
                }
            }
        }
    }
}

/** Confirmation d'une restauration : ce qui est remplacé, arrêté, et la copie de sécurité faite avant. */
@Composable
private fun RestoreDialog(item: BackupItem, copy: BackupObject, running: List<String>, onDismiss: () -> Unit, onConfirm: () -> Unit) {
    val stops = when (running.size) {
        0 -> null
        1 -> "${running[0]} sera arrêté pendant l'opération, puis relancé."
        else -> "${running.joinToString(", ")} seront arrêtés pendant l'opération, puis relancés."
    }
    AlertDialog(
        onDismissRequest = onDismiss,
        icon = { Icon(if (copy.safety) Icons.AutoMirrored.Rounded.Undo else Icons.Rounded.Restore, null) },
        title = { Text(if (copy.safety) "Revenir à l'état d'avant ?" else "Restaurer ${item.name} ?") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(
                    if (copy.safety) {
                        "${item.name} retrouve le contenu qu'il avait avant la dernière restauration (${copyDate(copy)})."
                    } else {
                        "Tout le contenu actuel de ${item.name} sera remplacé par la copie du ${copyDate(copy)}."
                    },
                )
                stops?.let { Text(it) }
                Text(
                    "Une copie de sécurité de l'état actuel est d'abord envoyée au stockage : tu pourras revenir en arrière.",
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        },
        confirmButton = { TextButton(onClick = onConfirm) { Text(if (copy.safety) "Revenir en arrière" else "Restaurer") } },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Annuler") } },
    )
}

/** Date d'une copie (envoi terminé), ou son nom si elle est inconnue. */
private fun copyDate(o: BackupObject): String = if (o.ts > 0) Fmt.dateTime(o.ts) else o.key.substringAfterLast('/')

/** Champs du stockage S3, test de connexion et aide pour Oracle Object Storage. */
@Composable
private fun StorageCard(d: BackupConfig, saved: BackupConfig, vm: BackupsViewModel, modifier: Modifier, onChange: (BackupConfig) -> Unit) {
    val haptics = rememberHaptics()
    val scope = rememberCoroutineScope()
    val status = Vigie.status
    var showSecret by remember { mutableStateOf(false) }
    var help by remember { mutableStateOf(false) }
    var testing by remember { mutableStateOf(false) }
    var result by remember { mutableStateOf<BackupTestResult?>(null) }
    // L'agent exige la clé secrète quand l'adresse change : elle ne sert qu'au stockage pour lequel elle a été donnée.
    val secretNeeded = d.endpoint.trim().trimEnd('/') != saved.endpoint && d.secretKey == BACKUP_SECRET_MASK
    val edit: (BackupConfig) -> Unit = { result = null; onChange(it) }

    SectionCard(modifier, title = "Stockage S3") {
        Column(Modifier.animateContentSize(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            OutlinedTextField(
                d.endpoint, { edit(d.copy(endpoint = it.trim())) },
                label = { Text("Adresse") },
                placeholder = { Text("https://…") },
                singleLine = true,
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri),
                modifier = Modifier.fillMaxWidth(),
            )
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedTextField(
                    d.region, { edit(d.copy(region = it.trim())) },
                    label = { Text("Région") },
                    placeholder = { Text("eu-paris-1") },
                    singleLine = true,
                    modifier = Modifier.weight(1f),
                )
                OutlinedTextField(
                    d.bucket, { edit(d.copy(bucket = it.trim())) },
                    label = { Text("Bucket") },
                    singleLine = true,
                    modifier = Modifier.weight(1f),
                )
            }
            OutlinedTextField(
                d.prefix, { edit(d.copy(prefix = it.trim())) },
                label = { Text("Préfixe (dossier)") },
                supportingText = { Text("Copies rangées dans ${d.prefix.trim('/').ifEmpty { "…" }}/<élément>/<date>.tar.gz") },
                singleLine = true,
                modifier = Modifier.fillMaxWidth(),
            )
            KeyField("Clé d'accès", d.accessKey, saved.accessKey) { edit(d.copy(accessKey = it)) }
            KeyField(
                "Clé secrète", d.secretKey, saved.secretKey, hidden = !showSecret,
                error = if (secretNeeded) "Nouvelle adresse : saisis à nouveau la clé secrète" else null,
                trailing = {
                    IconButton(onClick = { showSecret = !showSecret }) {
                        Icon(if (showSecret) Icons.Rounded.VisibilityOff else Icons.Rounded.Visibility, if (showSecret) "Masquer" else "Afficher")
                    }
                },
            ) { edit(d.copy(secretKey = it)) }
            Row(verticalAlignment = Alignment.CenterVertically) {
                OutlinedButton(
                    onClick = {
                        haptics.tick()
                        testing = true
                        scope.launch {
                            val r = vm.test(d)
                            testing = false
                            result = r
                            if (r.ok) haptics.confirm() else haptics.reject()
                        }
                    },
                    enabled = d.storageReady && !secretNeeded && !testing,
                    shapes = ButtonDefaults.shapes(),
                ) {
                    if (testing) LoadingIndicator(Modifier.size(20.dp)) else Icon(Icons.Rounded.CloudSync, null)
                    Spacer(Modifier.width(8.dp))
                    Text("Tester la connexion")
                }
                Spacer(Modifier.weight(1f))
                TextButton(onClick = { haptics.tick(); help = !help }) {
                    Icon(Icons.AutoMirrored.Rounded.HelpOutline, null, Modifier.size(18.dp))
                    Spacer(Modifier.width(4.dp))
                    Text("Oracle")
                }
            }
            result?.let { r ->
                Row(verticalAlignment = Alignment.Top) {
                    Icon(
                        if (r.ok) Icons.Rounded.CheckCircle else Icons.Rounded.ErrorOutline, null,
                        tint = if (r.ok) status.ok else status.critical,
                        modifier = Modifier.size(20.dp),
                    )
                    Spacer(Modifier.width(8.dp))
                    Text(r.message, style = MaterialTheme.typography.bodySmall, color = if (r.ok) status.ok else status.critical)
                }
            }
            AnimatedVisibility(help) { OracleHelp() }
        }
    }
}

/**
 * Clé enregistrée sur l'agent, qui ne la renvoie jamais : le champ reste vide (avec un rappel) tant
 * qu'elle n'est pas remplacée. Vidé, il revient à la valeur masquée, que l'agent comprend comme « inchangée ».
 */
@Composable
private fun KeyField(
    label: String,
    value: String,
    saved: String,
    hidden: Boolean = false,
    error: String? = null,
    trailing: (@Composable () -> Unit)? = null,
    onChange: (String) -> Unit,
) {
    val stored = saved == BACKUP_SECRET_MASK
    val hint = error ?: "Laisse vide pour garder celle enregistrée".takeIf { stored && value == BACKUP_SECRET_MASK }
    OutlinedTextField(
        if (value == BACKUP_SECRET_MASK) "" else value,
        { v -> onChange(if (v.isBlank() && stored) BACKUP_SECRET_MASK else v.trim()) },
        label = { Text(label) },
        placeholder = if (stored) ({ Text("Enregistrée sur l'agent") }) else null,
        supportingText = hint?.let { { Text(it) } },
        isError = error != null,
        singleLine = true,
        visualTransformation = if (hidden) PasswordVisualTransformation() else VisualTransformation.None,
        textStyle = MaterialTheme.typography.bodyLarge.copy(fontFamily = FontFamily.Monospace),
        keyboardOptions = KeyboardOptions(
            keyboardType = if (hidden) KeyboardType.Password else KeyboardType.Ascii,
            autoCorrectEnabled = false,
        ),
        trailingIcon = trailing,
        modifier = Modifier.fillMaxWidth(),
    )
}

@Composable
private fun OracleHelp() {
    Column(Modifier.fillMaxWidth().padding(top = 4.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
        Text("Oracle Object Storage (20 Go gratuits)", style = MaterialTheme.typography.titleSmall)
        listOf(
            "Console Oracle › Stockage › Buckets : crée un bucket privé.",
            "Namespace : menu du profil › Location (tenancy) › « Object storage namespace ».",
            "Région : son identifiant, par exemple eu-paris-1 ou eu-marseille-1.",
            "Clés : menu du profil › Mon profil › Customer Secret Keys › Générer. La clé secrète n'est affichée qu'une fois ; la clé d'accès apparaît ensuite dans la liste.",
        ).forEachIndexed { i, step ->
            Text("${i + 1}. $step", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        Text("Adresse :", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        Text(
            "https://<namespace>.compat.objectstorage.<région>.oraclecloud.com",
            style = MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace),
        )
    }
}

/** Éléments à sauvegarder : cases à cocher et arrêt facultatif des conteneurs pendant la copie. */
@Composable
private fun ItemsCard(
    d: BackupConfig,
    items: List<BackupItem>?,
    error: String?,
    retry: () -> Unit,
    modifier: Modifier,
    onChange: (BackupConfig) -> Unit,
) {
    val haptics = rememberHaptics()
    SectionCard(modifier, title = "Éléments à sauvegarder") {
        when {
            error != null -> {
                Text(error, style = MaterialTheme.typography.bodyMedium, color = Vigie.status.critical)
                TextButton(onClick = retry) { Text("Réessayer") }
            }
            items == null -> Box(Modifier.fillMaxWidth().padding(16.dp), contentAlignment = Alignment.Center) { LoadingIndicator() }
            else -> {
                Text(
                    "Pour une application à base SQLite (Sonarr, Radarr…), arrête-la pendant la copie : " +
                        "elle est relancée juste après, même en cas d'erreur.",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(4.dp))
                // Les éléments choisis puis disparus restent listés, pour pouvoir les décocher.
                val known = items.map { it.id }.toSet()
                val missing = d.items.filter { it.id !in known }.map { BackupItem(id = it.id, name = it.id.substringAfter(':')) }
                (items + missing).forEach { item ->
                    val sel = d.items.firstOrNull { it.id == item.id }
                    ItemRow(item, sel, absent = item.id !in known, onToggle = { on ->
                        haptics.toggle(on)
                        onChange(d.copy(items = if (on) d.items + BackupSelection(item.id) else d.items.filter { it.id != item.id }))
                    }, onStop = { stop ->
                        haptics.toggle(stop)
                        onChange(d.copy(items = d.items.map { if (it.id == item.id) it.copy(stop = stop) else it }))
                    })
                }
            }
        }
    }
}

@Composable
private fun ItemRow(item: BackupItem, sel: BackupSelection?, absent: Boolean, onToggle: (Boolean) -> Unit, onStop: (Boolean) -> Unit) {
    val checked = sel != null
    Column(
        Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(16.dp))
            .toggleable(checked, role = Role.Checkbox, onValueChange = onToggle)
            .padding(vertical = 2.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Checkbox(checked = checked, onCheckedChange = null, modifier = Modifier.padding(12.dp))
            Column(Modifier.weight(1f).padding(end = 8.dp)) {
                Text(
                    item.name,
                    style = if (item.kind == "bind") MaterialTheme.typography.bodyLarge.copy(fontFamily = FontFamily.Monospace) else MaterialTheme.typography.bodyLarge,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
                Text(
                    itemDetails(item, absent),
                    style = MaterialTheme.typography.bodySmall,
                    color = if (absent) MaterialTheme.colorScheme.outline else MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            if (item.size > 0) {
                Text(
                    Fmt.bytes(item.size),
                    style = MaterialTheme.typography.labelLarge.numeric(),
                    color = MaterialTheme.colorScheme.primary,
                    modifier = Modifier.padding(end = 12.dp),
                )
            }
        }
        // Les données de Vigie sont copiées de façon cohérente sans rien arrêter.
        AnimatedVisibility(checked && item.kind != "vigie" && !absent) {
            FilterChip(
                selected = sel?.stop == true,
                onClick = { onStop(sel?.stop != true) },
                label = { Text("Arrêter ${item.containers.joinToString(", ").ifEmpty { "le conteneur" }} pendant la copie", maxLines = 1, overflow = TextOverflow.Ellipsis) },
                leadingIcon = if (sel?.stop == true) ({ Icon(Icons.Rounded.Check, null, Modifier.size(18.dp)) }) else null,
                modifier = Modifier.padding(start = 56.dp, end = 12.dp, bottom = 4.dp),
            )
        }
    }
}

private fun itemDetails(item: BackupItem, absent: Boolean): String = when {
    absent -> "Introuvable : plus aucun conteneur ne l'utilise"
    item.kind == "vigie" -> "Historique, réglages et services de Vigie (sans les clés du stockage)"
    else -> listOfNotNull(
        if (item.kind == "volume") "Volume" else "Dossier de l'hôte",
        item.containers.joinToString(", ").takeIf { it.isNotEmpty() },
    ).joinToString(" · ")
}

/** Dernières sauvegardes : date, statut, taille et durée. */
@Composable
private fun HistoryCard(history: List<BackupRecord>, modifier: Modifier) {
    var all by remember { mutableStateOf(false) }
    val status = Vigie.status
    SectionCard(modifier.animateContentSize(), title = "Historique") {
        if (history.isEmpty()) {
            Text(
                "Aucune sauvegarde pour l'instant.",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        (if (all) history else history.take(5)).forEach { r ->
            Row(Modifier.padding(vertical = 6.dp), verticalAlignment = Alignment.Top) {
                StatusDot(if (r.ok) "ok" else "critical", size = 8.dp, breathe = false, modifier = Modifier.padding(top = 2.dp))
                Spacer(Modifier.width(8.dp))
                Column(Modifier.weight(1f)) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Text(Fmt.dateTime(r.ts), style = MaterialTheme.typography.titleSmall, modifier = Modifier.weight(1f))
                        Text(
                            if (r.ok) "Réussie" else "Échec",
                            style = MaterialTheme.typography.labelMedium,
                            color = if (r.ok) status.ok else status.critical,
                        )
                    }
                    Text(
                        listOf(
                            if (r.trigger == "schedule") "planifiée" else "manuelle",
                            if (r.items.size == 1) "1 élément" else "${r.items.size} éléments",
                            Fmt.bytes(r.size),
                            Fmt.duration(r.duration / 1000),
                        ).joinToString(" · "),
                        style = MaterialTheme.typography.bodySmall.numeric(),
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    if (r.error.isNotEmpty()) {
                        Text(r.error, style = MaterialTheme.typography.bodySmall, color = status.critical, maxLines = 4, overflow = TextOverflow.Ellipsis)
                    }
                }
            }
        }
        if (history.size > 5) {
            TextButton(onClick = { all = !all }) { Text(if (all) "Afficher moins" else "Afficher les ${history.size}") }
        }
    }
}

/** Copies présentes dans le stockage, par élément (chargées à la demande), à restaurer depuis l'appli (agent 0.6). */
@Composable
private fun ObjectsCard(
    objects: Result<List<BackupObject>>?,
    items: List<BackupItem>,
    restore: RestoreState,
    load: () -> Unit,
    modifier: Modifier,
    onRestore: (BackupItem, BackupObject) -> Unit,
) {
    var opened by remember { mutableStateOf(false) }
    val haptics = rememberHaptics()
    SectionCard(modifier.animateContentSize(), title = "Copies dans le stockage") {
        restore.job?.let {
            JobProgress(it)
            Spacer(Modifier.height(12.dp))
        }
        if (!opened) {
            Text(
                if (restore.allowed) {
                    "Choisis une copie à restaurer : les conteneurs qui l'utilisent sont arrêtés pendant l'opération, puis relancés."
                } else {
                    "Pour restaurer, télécharge une copie depuis ton stockage (voir le README)."
                },
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            TextButton(onClick = { haptics.tick(); opened = true; load() }) { Text("Afficher les copies") }
            return@SectionCard
        }
        if (restore.visible && restore.blocked != null) {
            Text(restore.blocked, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        val list = objects?.getOrNull()
        when {
            objects == null -> Box(Modifier.fillMaxWidth().padding(16.dp), contentAlignment = Alignment.Center) { LoadingIndicator() }
            list == null -> {
                Text(objects.exceptionOrNull()?.message.orEmpty(), style = MaterialTheme.typography.bodyMedium, color = Vigie.status.critical)
                TextButton(onClick = load) { Text("Réessayer") }
            }
            list.isEmpty() -> Text(
                "Aucune copie pour l'instant.",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            else -> groupCopies(list, items).forEach { CopiesGroup(it, restore, onRestore) }
        }
    }
}

/** Copies d'un élément : celle d'avant la dernière restauration à part, puis les sauvegardes. */
@Composable
private fun CopiesGroup(g: BackupCopies, restore: RestoreState, onRestore: (BackupItem, BackupObject) -> Unit) {
    val item = g.item
    val target = item?.takeIf { restore.allowed && it.restorable }
    Text(item?.name ?: g.folder, style = MaterialTheme.typography.titleSmall, modifier = Modifier.padding(top = 12.dp))
    val note = when {
        !restore.allowed -> null
        item == null -> "Plus aucun conteneur ne l'utilise : restauration impossible depuis l'appli."
        item.kind == "vigie" -> "À restaurer à la main : l'agent utilise ces données en permanence (marche à suivre dans le README, section Sauvegardes)."
        else -> null
    }
    note?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
    g.safety?.let { s ->
        if (target == null) {
            CopyRow(s, "Avant la dernière restauration", enabled = false, onRestore = null)
            return@let
        }
        Surface(
            shape = RoundedCornerShape(16.dp),
            color = MaterialTheme.colorScheme.surfaceContainerHighest,
            modifier = Modifier.fillMaxWidth().padding(vertical = 6.dp),
        ) {
            Column(Modifier.padding(12.dp)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Icon(Icons.Rounded.History, null, tint = MaterialTheme.colorScheme.primary, modifier = Modifier.size(18.dp))
                    Spacer(Modifier.width(8.dp))
                    Text("Avant la dernière restauration", style = MaterialTheme.typography.labelLarge)
                }
                Text(
                    "${copyDate(s)} · ${Fmt.bytes(s.size)}",
                    style = MaterialTheme.typography.bodySmall.numeric(),
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                OutlinedButton(
                    onClick = { onRestore(target, s) },
                    enabled = !restore.busy,
                    shapes = ButtonDefaults.shapes(),
                    modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
                ) {
                    Icon(Icons.AutoMirrored.Rounded.Undo, null, Modifier.size(18.dp))
                    Spacer(Modifier.width(8.dp))
                    Text("Annuler la dernière restauration")
                }
            }
        }
    }
    g.copies.forEach { o -> CopyRow(o, null, enabled = !restore.busy, onRestore = target?.let { { onRestore(it, o) } }) }
}

@Composable
private fun CopyRow(o: BackupObject, label: String?, enabled: Boolean, onRestore: (() -> Unit)?) {
    Row(Modifier.fillMaxWidth().padding(vertical = 2.dp), verticalAlignment = Alignment.CenterVertically) {
        Column(Modifier.weight(1f)) {
            Text(label ?: copyDate(o), style = MaterialTheme.typography.bodyMedium)
            Text(
                listOfNotNull(label?.let { copyDate(o) }, Fmt.bytes(o.size)).joinToString(" · "),
                style = MaterialTheme.typography.bodySmall.numeric(),
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        if (onRestore != null) {
            TextButton(onClick = onRestore, enabled = enabled) { Text("Restaurer") }
        }
    }
}
