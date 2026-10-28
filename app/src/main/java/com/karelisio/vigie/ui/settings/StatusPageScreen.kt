package com.karelisio.vigie.ui.settings

import android.content.ActivityNotFoundException
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
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
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.OpenInNew
import androidx.compose.material.icons.rounded.BarChart
import androidx.compose.material.icons.rounded.CloudOff
import androidx.compose.material.icons.rounded.ContentCopy
import androidx.compose.material.icons.rounded.Inventory2
import androidx.compose.material.icons.rounded.Lan
import androidx.compose.material.icons.rounded.LinkOff
import androidx.compose.material.icons.rounded.Public
import androidx.compose.material.icons.rounded.Save
import androidx.compose.material.icons.rounded.Share
import androidx.compose.material.icons.rounded.Sync
import androidx.compose.material.icons.rounded.WarningAmber
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Checkbox
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.Icon
import androidx.compose.material3.LoadingIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.KeyboardCapitalization
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.core.net.toUri
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.NavHostController
import com.karelisio.vigie.appContainer
import com.karelisio.vigie.data.ApiException
import com.karelisio.vigie.data.Server
import com.karelisio.vigie.data.StatusPageConfig
import com.karelisio.vigie.data.StatusPageItem
import com.karelisio.vigie.data.regenerateStatusPage
import com.karelisio.vigie.data.saveStatusPage
import com.karelisio.vigie.data.statusPage
import com.karelisio.vigie.data.versionAtLeast
import com.karelisio.vigie.security.LocalActionGuard
import com.karelisio.vigie.ui.components.EmptyState
import com.karelisio.vigie.ui.components.ScreenHeader
import com.karelisio.vigie.ui.components.SectionCard
import com.karelisio.vigie.ui.components.cascade
import com.karelisio.vigie.ui.components.rememberHaptics
import com.karelisio.vigie.ui.navigation.LocalSnackbar
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

private const val MAX_ITEMS = 30

class StatusPageViewModel : ViewModel() {
    private val repo = appContainer.repository
    val live = repo.live
    private val _saved = MutableStateFlow<StatusPageConfig?>(null)
    val saved = _saved.asStateFlow()
    private val _error = MutableStateFlow<String?>(null)
    val error = _error.asStateFlow()
    private val _unsupported = MutableStateFlow(false)
    val unsupported = _unsupported.asStateFlow()

    init {
        load()
    }

    fun load() {
        viewModelScope.launch {
            _error.value = null
            runCatching { repo.statusPage() }
                .onSuccess { _saved.value = it }
                .onFailure { if ((it as? ApiException)?.code == 404) _unsupported.value = true else _error.value = it.message }
        }
    }

    suspend fun save(c: StatusPageConfig): Result<StatusPageConfig> = runCatching { repo.saveStatusPage(c) }.onSuccess { _saved.value = it }

    suspend fun regenerate(): Result<StatusPageConfig> = runCatching { repo.regenerateStatusPage() }.onSuccess { _saved.value = it }

    fun server() = appContainer.servers.active
}

/** Page d'état publique : activation, lien à partager, présentation et services affichés sous un nom public. */
@Composable
fun StatusPageScreen(nav: NavHostController, vm: StatusPageViewModel = viewModel()) {
    val saved by vm.saved.collectAsStateWithLifecycle()
    val error by vm.error.collectAsStateWithLifecycle()
    val unsupportedApi by vm.unsupported.collectAsStateWithLifecycle()
    val live by vm.live.collectAsStateWithLifecycle()
    val unsupported = unsupportedApi || (live.agentVersion.isNotEmpty() && !versionAtLeast(live.agentVersion, 0, 6))
    // Modifications en cours (null : aucune) ; la page affiche sinon la configuration enregistrée.
    var edits by remember { mutableStateOf<StatusPageConfig?>(null) }
    val draft = edits ?: saved
    var saving by remember { mutableStateOf(false) }
    var regenerating by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    val snackbar = LocalSnackbar.current
    val guard = LocalActionGuard.current
    val haptics = rememberHaptics()
    val context = LocalContext.current
    val dirty = edits != null && edits != saved
    val bottom = WindowInsets.navigationBars.asPaddingValues().calculateBottomPadding() + 96.dp

    Box(Modifier.fillMaxSize()) {
        LazyColumn(
            Modifier.fillMaxSize(),
            contentPadding = PaddingValues(start = 16.dp, end = 16.dp, bottom = bottom),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            item { ScreenHeader("Page d'état publique", nav) }
            item {
                Text(
                    "Une page web à partager : tes proches voient si tes services fonctionnent, sans compte ni appli. " +
                        "Seuls les noms que tu choisis y figurent : ni adresse, ni erreur, ni nom de conteneur.",
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(horizontal = 4.dp),
                )
            }
            val d = draft
            val s = saved
            when {
                unsupported -> item {
                    EmptyState(Icons.Rounded.Public, "Indisponible sur cet agent", "Mets l'agent à jour (0.6.0 ou plus) pour publier une page d'état.")
                }
                d == null && error != null -> item {
                    EmptyState(Icons.Rounded.CloudOff, "Page d'état indisponible", error.orEmpty()) {
                        OutlinedButton(onClick = vm::load, shapes = ButtonDefaults.shapes()) { Text("Réessayer") }
                    }
                }
                d == null || s == null -> item {
                    Box(Modifier.fillMaxWidth().padding(48.dp), contentAlignment = Alignment.Center) { LoadingIndicator() }
                }
                else -> {
                    item {
                        ToggleRow(
                            Icons.Rounded.Public, "Page publique",
                            if (d.enabled) "Visible par toute personne qui a le lien" else "Le lien affiche « Page introuvable »",
                            d.enabled,
                        ) { on -> haptics.toggle(on); edits = d.copy(enabled = on) }
                    }
                    item {
                        LinkCard(
                            s, vm.server(), dirty, Modifier.cascade(0),
                            onCopy = {
                                haptics.tick()
                                copyLink(context, s.link(vm.server()))
                                scope.launch { snackbar.showSnackbar("Lien copié") }
                            },
                            onShare = { haptics.tick(); shareLink(context, s.title, s.link(vm.server())) },
                            onOpen = {
                                haptics.tick()
                                if (!openLink(context, s.link(vm.server()))) scope.launch { snackbar.showSnackbar("Aucun navigateur pour ouvrir le lien") }
                            },
                            onRegenerate = { haptics.tick(); regenerating = true },
                        )
                    }
                    item {
                        SectionCard(Modifier.cascade(1), title = "Présentation") {
                            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                                OutlinedTextField(
                                    d.title, { edits = d.copy(title = it.take(80)) },
                                    label = { Text("Titre") },
                                    placeholder = { Text("État des services") },
                                    singleLine = true,
                                    keyboardOptions = KeyboardOptions(capitalization = KeyboardCapitalization.Sentences),
                                    modifier = Modifier.fillMaxWidth(),
                                )
                                OutlinedTextField(
                                    d.description, { edits = d.copy(description = it.replace('\n', ' ').take(300)) },
                                    label = { Text("Description (facultative)") },
                                    placeholder = { Text("Par ex. Stremio de la famille") },
                                    minLines = 2,
                                    maxLines = 4,
                                    supportingText = { Text("${d.description.length}/300") },
                                    keyboardOptions = KeyboardOptions(capitalization = KeyboardCapitalization.Sentences),
                                    modifier = Modifier.fillMaxWidth(),
                                )
                            }
                        }
                    }
                    item {
                        ToggleRow(
                            Icons.Rounded.BarChart, "Disponibilité",
                            "Barres sur 90 jours et pourcentage pour chaque service",
                            d.showUptime,
                        ) { on -> haptics.toggle(on); edits = d.copy(showUptime = on) }
                    }
                    item { ItemsCard(d, live.checks.map { it.id.toString() to it.name }, live.containers.map { it.name }, Modifier.cascade(2)) { edits = it } }
                    item {
                        Text(
                            "Avec Pangolin, la ressource de l'agent doit laisser passer /status/ sans connexion : c'est déjà le cas " +
                                "si l'appli s'y connecte avec son token. Les visiteurs ne voient que cette page ; le reste de l'API exige le token.",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.padding(horizontal = 4.dp),
                        )
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
            val c = draft
            val missing = c?.items?.any { it.label.isBlank() } == true
            Button(
                onClick = {
                    if (c == null) return@Button
                    if (missing) {
                        haptics.reject()
                        scope.launch { snackbar.showSnackbar("Donne un nom public à chaque service coché") }
                        return@Button
                    }
                    saving = true
                    scope.launch {
                        // Rendre la page publique est une action sensible.
                        if (c.enabled && saved?.enabled != true && !guard("Publier la page d'état")) {
                            saving = false
                            return@launch
                        }
                        val r = vm.save(c)
                        saving = false
                        if (r.isSuccess) {
                            edits = null
                            haptics.confirm()
                        } else {
                            haptics.reject()
                        }
                        snackbar.showSnackbar(r.fold({ if (it.enabled) "Page d'état enregistrée" else "Page d'état désactivée" }, { "Échec : ${it.message}" }))
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

    if (regenerating) {
        AlertDialog(
            onDismissRequest = { regenerating = false },
            icon = { Icon(Icons.Rounded.LinkOff, null) },
            title = { Text("Changer le lien ?") },
            text = { Text("L'ancien lien cessera aussitôt de fonctionner : il faudra partager le nouveau.") },
            confirmButton = {
                TextButton(onClick = {
                    regenerating = false
                    scope.launch {
                        if (!guard("Changer le lien de la page d'état")) return@launch
                        vm.regenerate().fold(
                            { r ->
                                // Les modifications en cours sont gardées, avec le nouveau lien.
                                edits = edits?.copy(slug = r.slug, url = r.url)
                                haptics.confirm()
                                snackbar.showSnackbar("Nouveau lien créé, l'ancien ne fonctionne plus")
                            },
                            { haptics.reject(); snackbar.showSnackbar("Échec : ${it.message}") },
                        )
                    }
                }) { Text("Changer le lien") }
            },
            dismissButton = { TextButton(onClick = { regenerating = false }) { Text("Annuler") } },
        )
    }
}

/** Lien public : adresse, copier, partager, ouvrir, changer. */
@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun LinkCard(
    saved: StatusPageConfig,
    server: Server?,
    dirty: Boolean,
    modifier: Modifier,
    onCopy: () -> Unit,
    onShare: () -> Unit,
    onOpen: () -> Unit,
    onRegenerate: () -> Unit,
) {
    val link = saved.link(server)
    SectionCard(modifier, title = "Lien") {
        Column(Modifier.animateContentSize(), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            Surface(shape = RoundedCornerShape(16.dp), color = MaterialTheme.colorScheme.surfaceContainerHighest, modifier = Modifier.fillMaxWidth()) {
                Text(
                    link.ifEmpty { "—" },
                    style = MaterialTheme.typography.bodyMedium.copy(fontFamily = FontFamily.Monospace),
                    maxLines = 3,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.padding(horizontal = 14.dp, vertical = 12.dp),
                )
            }
            if (!saved.enabled) {
                Notice(Icons.Rounded.WarningAmber, if (dirty) "Enregistre pour activer la page." else "Page désactivée : ce lien affiche « Page introuvable ».")
            }
            if (saved.url.isEmpty()) {
                Notice(Icons.Rounded.Lan, "VIGIE_PUBLIC_URL n'est pas défini sur l'agent : lien construit avec l'adresse de ce serveur dans l'appli.")
            }
            FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                FilledTonalButton(onClick = onShare, enabled = link.isNotEmpty(), shapes = ButtonDefaults.shapes()) {
                    Icon(Icons.Rounded.Share, null)
                    Spacer(Modifier.width(8.dp))
                    Text("Partager")
                }
                OutlinedButton(onClick = onCopy, enabled = link.isNotEmpty(), shapes = ButtonDefaults.shapes()) {
                    Icon(Icons.Rounded.ContentCopy, null)
                    Spacer(Modifier.width(8.dp))
                    Text("Copier")
                }
                OutlinedButton(onClick = onOpen, enabled = link.isNotEmpty() && saved.enabled, shapes = ButtonDefaults.shapes()) {
                    Icon(Icons.AutoMirrored.Rounded.OpenInNew, null)
                    Spacer(Modifier.width(8.dp))
                    Text("Ouvrir")
                }
            }
            TextButton(onClick = onRegenerate, shapes = ButtonDefaults.shapes()) {
                Icon(Icons.Rounded.Sync, null)
                Spacer(Modifier.width(8.dp))
                Text("Changer le lien")
            }
        }
    }
}

@Composable
private fun Notice(icon: ImageVector, text: String) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        Icon(icon, null, Modifier.size(18.dp), tint = MaterialTheme.colorScheme.onSurfaceVariant)
        Spacer(Modifier.width(8.dp))
        Text(text, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

/**
 * Services et conteneurs affichés : coche pour afficher, nom public modifiable (prérempli avec le nom actuel,
 * que tu choisis ainsi de montrer). Les éléments disparus restent listés pour pouvoir les retirer.
 */
@Composable
private fun ItemsCard(
    d: StatusPageConfig,
    checks: List<Pair<String, String>>,
    containers: List<String>,
    modifier: Modifier,
    onChange: (StatusPageConfig) -> Unit,
) {
    val haptics = rememberHaptics()
    fun toggle(kind: String, ref: String, name: String) {
        val existing = d.items.firstOrNull { it.kind == kind && it.ref == ref }
        haptics.toggle(existing == null)
        onChange(d.copy(items = if (existing != null) d.items - existing else d.items + StatusPageItem(kind, ref, name.take(60))))
    }
    fun rename(item: StatusPageItem, label: String) {
        onChange(d.copy(items = d.items.map { if (it == item) it.copy(label = label.take(60)) else it }))
    }
    val full = d.items.size >= MAX_ITEMS

    SectionCard(modifier, title = "Affiché sur la page") {
        Column(Modifier.animateContentSize(), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Text(
                "Coche ce que tu veux montrer, puis choisis le nom public de chacun.",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Group("Services", Icons.Rounded.Public)
            if (checks.isEmpty()) Empty("Aucun service surveillé.")
            checks.forEach { (id, name) ->
                val item = d.items.firstOrNull { it.kind == StatusPageItem.CHECK && it.ref == id }
                ItemRow(name, item, enabled = item != null || !full, onToggle = { toggle(StatusPageItem.CHECK, id, name) }) { label -> item?.let { rename(it, label) } }
            }
            Group("Conteneurs", Icons.Rounded.Inventory2)
            if (containers.isEmpty()) Empty("Aucun conteneur.")
            containers.forEach { name ->
                val item = d.items.firstOrNull { it.kind == StatusPageItem.CONTAINER && it.ref == name }
                ItemRow(name, item, enabled = item != null || !full, onToggle = { toggle(StatusPageItem.CONTAINER, name, name) }) { label -> item?.let { rename(it, label) } }
            }
            val gone = d.items.filter { item ->
                (item.kind == StatusPageItem.CHECK && checks.none { it.first == item.ref }) ||
                    (item.kind == StatusPageItem.CONTAINER && item.ref !in containers)
            }
            if (gone.isNotEmpty()) {
                Group("Introuvables", Icons.Rounded.WarningAmber)
                gone.forEach { item ->
                    val what = if (item.kind == StatusPageItem.CHECK) "service supprimé : retiré de la page" else "conteneur absent : affiché hors ligne"
                    ItemRow("${item.label} ($what)", item, enabled = true, onToggle = { onChange(d.copy(items = d.items - item)) }) { rename(item, it) }
                }
            }
            if (full) Empty("$MAX_ITEMS éléments au plus.")
        }
    }
}

@Composable
private fun Group(title: String, icon: ImageVector) {
    Row(Modifier.padding(top = 10.dp, bottom = 2.dp), verticalAlignment = Alignment.CenterVertically) {
        Icon(icon, null, Modifier.size(18.dp), tint = MaterialTheme.colorScheme.primary)
        Spacer(Modifier.width(8.dp))
        Text(title, style = MaterialTheme.typography.titleSmall, color = MaterialTheme.colorScheme.primary)
    }
}

@Composable
private fun Empty(text: String) {
    Text(text, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(vertical = 4.dp))
}

@Composable
private fun ItemRow(name: String, item: StatusPageItem?, enabled: Boolean, onToggle: () -> Unit, onRename: (String) -> Unit) {
    Column {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Checkbox(checked = item != null, onCheckedChange = { onToggle() }, enabled = enabled)
            Text(name, style = MaterialTheme.typography.bodyLarge, maxLines = 2, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
        }
        AnimatedVisibility(item != null) {
            OutlinedTextField(
                item?.label.orEmpty(), onRename,
                label = { Text("Nom public") },
                singleLine = true,
                isError = item?.label?.isBlank() == true,
                keyboardOptions = KeyboardOptions(capitalization = KeyboardCapitalization.Sentences),
                modifier = Modifier.fillMaxWidth().padding(start = 48.dp, bottom = 8.dp),
            )
        }
    }
}

private fun copyLink(context: Context, link: String) {
    context.getSystemService(ClipboardManager::class.java).setPrimaryClip(ClipData.newPlainText("Page d'état", link))
}

private fun shareLink(context: Context, title: String, link: String) {
    val text = "${title.ifBlank { "État des services" }} : $link"
    val send = Intent(Intent.ACTION_SEND).setType("text/plain").putExtra(Intent.EXTRA_TEXT, text)
    context.startActivity(Intent.createChooser(send, "Partager la page d'état").addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
}

private fun openLink(context: Context, link: String): Boolean = try {
    context.startActivity(Intent(Intent.ACTION_VIEW, link.toUri()).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
    true
} catch (_: ActivityNotFoundException) {
    false
}
