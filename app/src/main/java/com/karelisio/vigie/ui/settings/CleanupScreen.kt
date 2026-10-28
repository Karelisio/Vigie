package com.karelisio.vigie.ui.settings

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
import androidx.compose.material.icons.rounded.Build
import androidx.compose.material.icons.rounded.CleaningServices
import androidx.compose.material.icons.rounded.Cloud
import androidx.compose.material.icons.rounded.FolderDelete
import androidx.compose.material.icons.rounded.Layers
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Checkbox
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LoadingIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Surface
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
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.NavHostController
import com.karelisio.vigie.appContainer
import com.karelisio.vigie.data.CleanupCategory
import com.karelisio.vigie.data.CleanupOptions
import com.karelisio.vigie.data.CleanupPreview
import com.karelisio.vigie.data.Fmt
import com.karelisio.vigie.security.LocalActionGuard
import com.karelisio.vigie.ui.components.EmptyState
import com.karelisio.vigie.ui.components.JobProgress
import com.karelisio.vigie.ui.components.SectionCard
import com.karelisio.vigie.ui.components.cascade
import com.karelisio.vigie.ui.components.rememberHaptics
import com.karelisio.vigie.ui.navigation.LocalSnackbar
import com.karelisio.vigie.ui.theme.numeric
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

class CleanupViewModel : ViewModel() {
    private val repo = appContainer.repository
    val live = repo.live
    private val _preview = MutableStateFlow<CleanupPreview?>(null)
    val preview = _preview.asStateFlow()
    private val _error = MutableStateFlow<String?>(null)
    val error = _error.asStateFlow()
    /** Identifiant de l'opération lancée depuis cet écran. */
    var jobId by mutableStateOf<String?>(null)
        private set

    init {
        load()
    }

    fun load() {
        viewModelScope.launch {
            _error.value = null
            _preview.value = null
            runCatching { repo.cleanupPreview() }
                .onSuccess { _preview.value = it }
                .onFailure { _error.value = it.message }
        }
    }

    suspend fun run(options: CleanupOptions): Result<Unit> = runCatching {
        jobId = repo.cleanup(options).id
    }
}

@Composable
fun CleanupScreen(nav: NavHostController, vm: CleanupViewModel = viewModel()) {
    val preview by vm.preview.collectAsStateWithLifecycle()
    val error by vm.error.collectAsStateWithLifecycle()
    val live by vm.live.collectAsStateWithLifecycle()
    var images by rememberSaveable { mutableStateOf(true) }
    var buildCache by rememberSaveable { mutableStateOf(true) }
    var volumes by rememberSaveable { mutableStateOf(false) }
    var confirm by remember { mutableStateOf(false) }
    val job = live.jobs.firstOrNull { it.id == vm.jobId }
    val scope = rememberCoroutineScope()
    val snackbar = LocalSnackbar.current
    val guard = LocalActionGuard.current
    val haptics = rememberHaptics()

    // Une fois le nettoyage terminé, l'estimation est recalculée.
    LaunchedEffect(job?.done) { if (job?.done == true) vm.load() }

    LazyColumn(
        Modifier.fillMaxSize().navigationBarsPadding(),
        contentPadding = PaddingValues(start = 16.dp, end = 16.dp, bottom = 32.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        item {
            Row(Modifier.statusBarsPadding().padding(top = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                IconButton(onClick = { nav.popBackStack() }) { Icon(Icons.AutoMirrored.Rounded.ArrowBack, "Retour") }
                Text("Nettoyage du disque", style = MaterialTheme.typography.headlineSmallEmphasized)
            }
        }
        item {
            Text(
                "Supprime ce que Docker garde sans s'en servir. Les conteneurs, même arrêtés, et leurs images ne sont jamais touchés.",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(horizontal = 4.dp),
            )
        }
        if (job != null) {
            item { SectionCard(Modifier.cascade(0), title = "Nettoyage") { JobProgress(job) } }
        }
        val p = preview
        when {
            error != null -> item {
                EmptyState(Icons.Rounded.Cloud, "Estimation impossible", error.orEmpty()) {
                    OutlinedButton(onClick = vm::load) { Text("Réessayer") }
                }
            }
            p == null -> item {
                Column(Modifier.fillMaxWidth().padding(32.dp), horizontalAlignment = Alignment.CenterHorizontally) {
                    LoadingIndicator()
                    Spacer(Modifier.height(12.dp))
                    Text("Calcul de l'espace récupérable…", color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }
            else -> {
                item {
                    CleanupRow(
                        Icons.Rounded.Layers, "Images inutilisées",
                        "Anciennes versions et images sans conteneur. Elles seront retéléchargées si besoin." +
                            rollbackNote(p.rollbackImages.count),
                        p.images, images, Modifier.cascade(1),
                    ) { images = it }
                }
                item {
                    CleanupRow(
                        Icons.Rounded.Build, "Cache de construction",
                        "Fichiers intermédiaires des images construites sur le serveur.",
                        p.buildCache, buildCache, Modifier.cascade(2),
                    ) { buildCache = it }
                }
                if (p.volumesSupported) {
                    item {
                        CleanupRow(
                            Icons.Rounded.FolderDelete, "Volumes anonymes orphelins",
                            "Données laissées par des conteneurs supprimés. Les volumes nommés ne sont jamais supprimés. Vérifie avant de cocher.",
                            p.volumes, volumes, Modifier.cascade(3),
                        ) { volumes = it }
                    }
                }
                item {
                    val total = (if (images) p.images.size else 0) + (if (buildCache) p.buildCache.size else 0) +
                        (if (volumes && p.volumesSupported) p.volumes.size else 0)
                    Button(
                        onClick = { haptics.tick(); confirm = true },
                        enabled = total > 0 && live.runningJob == null,
                        shapes = ButtonDefaults.shapes(),
                        modifier = Modifier.fillMaxWidth().height(56.dp).cascade(4),
                    ) {
                        Icon(Icons.Rounded.CleaningServices, null)
                        Spacer(Modifier.width(8.dp))
                        Text(if (total > 0) "Libérer environ ${Fmt.bytes(total)}" else "Rien à libérer")
                    }
                }
            }
        }
    }

    if (confirm) {
        AlertDialog(
            onDismissRequest = { confirm = false },
            icon = { Icon(Icons.Rounded.CleaningServices, null) },
            title = { Text("Nettoyer le disque ?") },
            text = {
                Text(
                    "Les éléments cochés seront supprimés définitivement." +
                        if (volumes) " Les volumes anonymes orphelins et leurs données seront perdus." else "",
                )
            },
            confirmButton = {
                TextButton(onClick = {
                    confirm = false
                    scope.launch {
                        if (!guard("Nettoyer le disque")) return@launch
                        vm.run(CleanupOptions(images, volumes, buildCache)).exceptionOrNull()?.let {
                            haptics.reject()
                            snackbar.showSnackbar("Échec : ${it.message}")
                        }
                    }
                }) { Text("Nettoyer") }
            },
            dismissButton = { TextButton(onClick = { confirm = false }) { Text("Annuler") } },
        )
    }
}

/** Versions précédentes gardées pour revenir en arrière, supprimées avec les images inutilisées (agent 0.6.0). */
internal fun rollbackNote(count: Int): String = when {
    count <= 0 -> ""
    count == 1 -> " Dont 1 version précédente gardée pour revenir en arrière (retéléchargée au besoin)."
    else -> " Dont $count versions précédentes gardées pour revenir en arrière (retéléchargées au besoin)."
}

@Composable
private fun CleanupRow(
    icon: ImageVector,
    title: String,
    description: String,
    category: CleanupCategory,
    checked: Boolean,
    modifier: Modifier,
    onChange: (Boolean) -> Unit,
) {
    val haptics = rememberHaptics()
    val empty = category.count == 0
    Surface(
        onClick = { if (!empty) { haptics.toggle(!checked); onChange(!checked) } },
        shape = RoundedCornerShape(24.dp),
        color = MaterialTheme.colorScheme.surfaceContainer,
        modifier = modifier.fillMaxWidth(),
    ) {
        Row(Modifier.padding(start = 16.dp, end = 8.dp, top = 14.dp, bottom = 14.dp), verticalAlignment = Alignment.CenterVertically) {
            Icon(icon, null, tint = MaterialTheme.colorScheme.onSurfaceVariant)
            Spacer(Modifier.width(16.dp))
            Column(Modifier.weight(1f)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(title, style = MaterialTheme.typography.titleMedium, modifier = Modifier.weight(1f, fill = false))
                    Spacer(Modifier.width(8.dp))
                    Text(
                        if (empty) "vide" else Fmt.bytes(category.size),
                        style = MaterialTheme.typography.titleSmall.numeric(),
                        color = if (empty) MaterialTheme.colorScheme.outline else MaterialTheme.colorScheme.primary,
                    )
                }
                Text(description, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                AnimatedVisibility(!empty) {
                    Text(
                        if (category.count == 1) "1 élément" else "${category.count} éléments",
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
            Box(Modifier.padding(start = 4.dp)) {
                Checkbox(checked = checked && !empty, onCheckedChange = if (empty) null else { v -> onChange(v) }, enabled = !empty)
            }
        }
    }
}
