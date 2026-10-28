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
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
import androidx.compose.material.icons.automirrored.rounded.HelpOutline
import androidx.compose.material.icons.automirrored.rounded.Send
import androidx.compose.material.icons.rounded.Campaign
import androidx.compose.material.icons.rounded.CheckCircle
import androidx.compose.material.icons.rounded.Cloud
import androidx.compose.material.icons.rounded.ErrorOutline
import androidx.compose.material.icons.rounded.Forum
import androidx.compose.material.icons.rounded.Save
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LoadingIndicator
import androidx.compose.material3.MaterialShapes
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.SegmentedButton
import androidx.compose.material3.SegmentedButtonDefaults
import androidx.compose.material3.SingleChoiceSegmentedButtonRow
import androidx.compose.material3.Surface
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.toShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.NavHostController
import com.karelisio.vigie.appContainer
import com.karelisio.vigie.data.AlertChannel
import com.karelisio.vigie.data.ApiException
import com.karelisio.vigie.data.ChannelTestResult
import com.karelisio.vigie.data.ChannelsConfig
import com.karelisio.vigie.data.channels
import com.karelisio.vigie.data.saveChannels
import com.karelisio.vigie.data.testChannel
import com.karelisio.vigie.data.versionAtLeast
import com.karelisio.vigie.ui.components.EmptyState
import com.karelisio.vigie.ui.components.SectionCard
import com.karelisio.vigie.ui.components.Springs
import com.karelisio.vigie.ui.components.cascade
import com.karelisio.vigie.ui.components.rememberHaptics
import com.karelisio.vigie.ui.navigation.LocalSnackbar
import com.karelisio.vigie.ui.theme.Vigie
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

/** Type de canal : présentation et aide pour obtenir ses identifiants. */
private enum class ChannelKind(val type: String, val title: String, val icon: ImageVector, val help: String) {
    Telegram(
        "telegram", "Telegram", Icons.AutoMirrored.Rounded.Send,
        "Dans Telegram, écris à @BotFather et envoie /newbot : il te donne le token du bot. Envoie ensuite un message " +
            "à ton bot, puis ouvre api.telegram.org/bot<token>/getUpdates : le chat id est le nombre qui suit " +
            "\"chat\":{\"id\":. Pour un groupe, ajoute d'abord le bot au groupe (son id commence par -).",
    ),
    Discord(
        "discord", "Discord", Icons.Rounded.Forum,
        "Dans Discord : paramètres du salon › Intégrations › Webhooks › Nouveau webhook, puis « Copier l'URL du webhook ».",
    ),
    Ntfy(
        "ntfy", "ntfy", Icons.Rounded.Campaign,
        "Installe l'appli ntfy et abonne-toi à un topic difficile à deviner (ex. vigie-7f3k9q), puis saisis le même ici. " +
            "Avec ton propre serveur, indique son adresse ; le jeton d'accès (tk_…) ne sert que si le topic est protégé.",
    ),
}

/** Brouillons modifiables, un par type : les secrets y sont vides (l'agent ne renvoie que leur version masquée). */
private fun drafts(saved: ChannelsConfig?): Map<String, AlertChannel> = ChannelKind.entries.associate { k ->
    k.type to (saved?.channels?.firstOrNull { it.type == k.type }?.copy(botToken = "", webhookUrl = "", token = "") ?: AlertChannel(type = k.type))
}

/** Configuration à envoyer : les canaux déjà enregistrés et ceux que l'utilisateur a commencé à remplir. */
private fun configOf(drafts: Map<String, AlertChannel>, saved: ChannelsConfig?): ChannelsConfig = ChannelsConfig(
    ChannelKind.entries.mapNotNull { k ->
        drafts.getValue(k.type).takeIf { d -> saved?.channels?.any { it.type == k.type } == true || d != AlertChannel(type = k.type) }
    },
)

class ChannelsViewModel : ViewModel() {
    private val repo = appContainer.repository
    private val _saved = MutableStateFlow<ChannelsConfig?>(null)
    val saved = _saved.asStateFlow()
    private val _error = MutableStateFlow<String?>(null)
    val error = _error.asStateFlow()
    /** L'agent ne connaît pas les canaux (antérieur à 0.5.0). */
    private val _unsupported = MutableStateFlow(false)
    val unsupported = _unsupported.asStateFlow()

    init {
        load()
    }

    fun load() {
        viewModelScope.launch {
            _error.value = null
            runCatching { repo.channels() }
                .onSuccess { _saved.value = it }
                .onFailure { if ((it as? ApiException)?.code == 404) _unsupported.value = true else _error.value = it.message }
        }
    }

    suspend fun save(config: ChannelsConfig): Result<ChannelsConfig> =
        runCatching { repo.saveChannels(config) }.onSuccess { _saved.value = it }

    suspend fun test(type: String): ChannelTestResult =
        runCatching { repo.testChannel(type) }.getOrElse { ChannelTestResult(type, error = it.message ?: "Échec") }
}

/** Canaux d'alerte secondaires : Telegram, Discord et ntfy, chacun activable avec son niveau. */
@Composable
fun ChannelsScreen(nav: NavHostController, vm: ChannelsViewModel = viewModel()) {
    val saved by vm.saved.collectAsStateWithLifecycle()
    val error by vm.error.collectAsStateWithLifecycle()
    val unsupportedApi by vm.unsupported.collectAsStateWithLifecycle()
    val live by appContainer.repository.live.collectAsStateWithLifecycle()
    val initial = remember(saved) { drafts(saved) }
    var draft by remember(saved) { mutableStateOf(initial) }
    var saving by remember { mutableStateOf(false) }
    var testing by remember { mutableStateOf<String?>(null) }
    var results by remember { mutableStateOf<Map<String, ChannelTestResult>>(emptyMap()) }
    var removing by remember { mutableStateOf<ChannelKind?>(null) }
    val scope = rememberCoroutineScope()
    val snackbar = LocalSnackbar.current
    val haptics = rememberHaptics()
    val dirty = saved != null && draft != initial
    val unsupported = unsupportedApi || (live.agentVersion.isNotEmpty() && !versionAtLeast(live.agentVersion, 0, 5))
    val bottom = WindowInsets.navigationBars.asPaddingValues().calculateBottomPadding() + 96.dp

    suspend fun persist(config: ChannelsConfig): Boolean {
        saving = true
        val r = vm.save(config)
        saving = false
        r.onFailure {
            haptics.reject()
            snackbar.showSnackbar("Échec : ${it.message}")
        }
        return r.isSuccess
    }

    fun test(kind: ChannelKind) {
        haptics.tick()
        scope.launch {
            // Les modifications en cours sont enregistrées d'abord : le test porte sur ce qui est affiché.
            if (dirty && !persist(configOf(draft, saved))) return@launch
            testing = kind.type
            val r = vm.test(kind.type)
            testing = null
            results = results + (kind.type to r)
            if (r.ok) haptics.confirm() else haptics.reject()
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
                    Text("Canaux d'alerte", style = MaterialTheme.typography.headlineSmallEmphasized)
                }
            }
            item {
                Text(
                    "Les alertes notifiées sur ton téléphone partent aussi vers ces canaux, avec les mêmes heures calmes " +
                        "et la même maintenance, même sans Firebase. Jetons et webhooks restent sur l'agent.",
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(horizontal = 4.dp),
                )
            }
            val s = saved
            when {
                unsupported -> item {
                    EmptyState(
                        Icons.Rounded.Campaign, "Indisponible sur cet agent",
                        "Mets l'agent à jour (0.5.0 ou plus) pour envoyer les alertes vers Telegram, Discord ou ntfy.",
                    )
                }
                error != null -> item {
                    EmptyState(Icons.Rounded.Cloud, "Canaux indisponibles", error.orEmpty()) {
                        OutlinedButton(onClick = vm::load, shapes = ButtonDefaults.shapes()) { Text("Réessayer") }
                    }
                }
                s == null -> item {
                    Box(Modifier.fillMaxWidth().padding(48.dp), contentAlignment = Alignment.Center) { LoadingIndicator() }
                }
                else -> items(ChannelKind.entries, key = { it.type }) { kind ->
                    ChannelCard(
                        kind = kind,
                        draft = draft.getValue(kind.type),
                        saved = s.channels.firstOrNull { it.type == kind.type },
                        result = results[kind.type],
                        testing = testing == kind.type,
                        busy = saving || testing != null,
                        onChange = { draft = draft + (kind.type to it) },
                        onTest = { test(kind) },
                        onRemove = { haptics.tick(); removing = kind },
                        modifier = Modifier.cascade(kind.ordinal),
                    )
                }
            }
        }

        AnimatedVisibility(
            dirty && !unsupported,
            modifier = Modifier.align(Alignment.BottomCenter).navigationBarsPadding().padding(16.dp),
            enter = slideInVertically { it } + fadeIn(),
            exit = slideOutVertically { it } + fadeOut(),
        ) {
            Button(
                onClick = {
                    scope.launch {
                        if (persist(configOf(draft, saved))) {
                            haptics.confirm()
                            snackbar.showSnackbar("Canaux enregistrés sur l'agent")
                        }
                    }
                },
                enabled = !saving,
                shapes = ButtonDefaults.shapes(),
                modifier = Modifier.height(56.dp),
            ) {
                if (saving) LoadingIndicator(Modifier.size(24.dp)) else Icon(Icons.Rounded.Save, null)
                Spacer(Modifier.width(8.dp))
                Text("Enregistrer")
            }
        }
    }

    removing?.let { kind ->
        AlertDialog(
            onDismissRequest = { removing = null },
            icon = { Icon(kind.icon, null) },
            title = { Text("Retirer ${kind.title} ?") },
            text = { Text("Les alertes n'y seront plus envoyées et ses identifiants seront effacés de l'agent.") },
            confirmButton = {
                TextButton(onClick = {
                    removing = null
                    scope.launch {
                        val rest = configOf(draft, saved).channels.filterNot { it.type == kind.type }
                        if (persist(ChannelsConfig(rest))) {
                            haptics.confirm()
                            results = results - kind.type
                            snackbar.showSnackbar("${kind.title} retiré")
                        }
                    }
                }) { Text("Retirer") }
            },
            dismissButton = { TextButton(onClick = { removing = null }) { Text("Annuler") } },
        )
    }
}

@Composable
private fun ChannelCard(
    kind: ChannelKind,
    draft: AlertChannel,
    saved: AlertChannel?,
    result: ChannelTestResult?,
    testing: Boolean,
    busy: Boolean,
    onChange: (AlertChannel) -> Unit,
    onTest: () -> Unit,
    onRemove: () -> Unit,
    modifier: Modifier,
) {
    val scheme = MaterialTheme.colorScheme
    val status = Vigie.status
    val haptics = rememberHaptics()
    var help by rememberSaveable(kind) { mutableStateOf(false) }
    val started = saved != null || draft != AlertChannel(type = kind.type)
    SectionCard(modifier.animateContentSize(Springs.gentle())) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Surface(
                shape = MaterialShapes.Cookie9Sided.toShape(),
                color = if (draft.enabled) scheme.primaryContainer else scheme.surfaceContainerHighest,
                contentColor = if (draft.enabled) scheme.onPrimaryContainer else scheme.onSurfaceVariant,
                modifier = Modifier.size(44.dp),
            ) {
                Icon(kind.icon, null, Modifier.padding(11.dp))
            }
            Spacer(Modifier.width(12.dp))
            Column(Modifier.weight(1f)) {
                Text(kind.title, style = MaterialTheme.typography.titleMedium)
                Text(
                    when {
                        !started -> "Non configuré"
                        !draft.enabled -> "Désactivé"
                        draft.minLevel == "critical" -> "Actif · critiques seulement"
                        else -> "Actif · toutes les alertes"
                    },
                    style = MaterialTheme.typography.bodySmall,
                    color = scheme.onSurfaceVariant,
                )
            }
            IconButton(onClick = { haptics.tick(); help = !help }) {
                Icon(Icons.AutoMirrored.Rounded.HelpOutline, if (help) "Masquer l'aide" else "Comment obtenir ces identifiants ?")
            }
            Switch(
                checked = draft.enabled,
                onCheckedChange = { haptics.toggle(it); onChange(draft.copy(enabled = it)) },
                enabled = !busy,
            )
        }
        AnimatedVisibility(help) {
            Surface(shape = RoundedCornerShape(16.dp), color = scheme.surfaceContainerHigh, modifier = Modifier.padding(top = 12.dp)) {
                Text(kind.help, style = MaterialTheme.typography.bodySmall, modifier = Modifier.padding(12.dp))
            }
        }
        Spacer(Modifier.height(12.dp))
        Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
            when (kind) {
                ChannelKind.Telegram -> {
                    SecretField(draft.botToken, { onChange(draft.copy(botToken = it)) }, "Token du bot", saved?.botToken.orEmpty(), enabled = !busy)
                    OutlinedTextField(
                        draft.chatId, { onChange(draft.copy(chatId = it.trim())) },
                        label = { Text("Chat id") },
                        placeholder = { Text("123456789") },
                        singleLine = true,
                        enabled = !busy,
                        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Ascii, autoCorrectEnabled = false),
                        modifier = Modifier.fillMaxWidth(),
                    )
                }
                ChannelKind.Discord -> SecretField(
                    draft.webhookUrl, { onChange(draft.copy(webhookUrl = it)) }, "URL du webhook", saved?.webhookUrl.orEmpty(),
                    enabled = !busy, hint = "https://discord.com/api/webhooks/…",
                )
                ChannelKind.Ntfy -> {
                    OutlinedTextField(
                        draft.server, { onChange(draft.copy(server = it.trim())) },
                        label = { Text("Serveur") },
                        placeholder = { Text("https://ntfy.sh") },
                        supportingText = { Text("Vide : ntfy.sh") },
                        singleLine = true,
                        enabled = !busy,
                        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri),
                        modifier = Modifier.fillMaxWidth(),
                    )
                    OutlinedTextField(
                        draft.topic, { onChange(draft.copy(topic = it.trim())) },
                        label = { Text("Topic") },
                        singleLine = true,
                        enabled = !busy,
                        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Ascii, autoCorrectEnabled = false),
                        modifier = Modifier.fillMaxWidth(),
                    )
                    SecretField(
                        draft.token, { onChange(draft.copy(token = it)) }, "Jeton d'accès (facultatif)", saved?.token.orEmpty(),
                        enabled = !busy, hint = "Seulement si le topic est protégé",
                    )
                }
            }
            Text("Alertes à envoyer", style = MaterialTheme.typography.labelLarge, modifier = Modifier.padding(top = 4.dp))
            SingleChoiceSegmentedButtonRow(Modifier.fillMaxWidth()) {
                listOf("all" to "Toutes", "critical" to "Critiques").forEachIndexed { i, (level, label) ->
                    SegmentedButton(
                        selected = draft.minLevel == level,
                        onClick = { haptics.tick(); onChange(draft.copy(minLevel = level)) },
                        shape = SegmentedButtonDefaults.itemShape(i, 2),
                        enabled = !busy,
                    ) { Text(label, maxLines = 1) }
                }
            }
        }
        Spacer(Modifier.height(8.dp))
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            OutlinedButton(onClick = onTest, enabled = started && !busy, shapes = ButtonDefaults.shapes()) {
                if (testing) LoadingIndicator(Modifier.size(18.dp)) else Icon(Icons.AutoMirrored.Rounded.Send, null, Modifier.size(18.dp))
                Spacer(Modifier.width(8.dp))
                Text("Tester")
            }
            Spacer(Modifier.weight(1f))
            if (saved != null) {
                TextButton(onClick = onRemove, enabled = !busy, shapes = ButtonDefaults.shapes()) { Text("Retirer") }
            }
        }
        AnimatedVisibility(result != null) {
            val r = result ?: return@AnimatedVisibility
            Row(Modifier.padding(top = 8.dp), verticalAlignment = Alignment.CenterVertically) {
                Icon(
                    if (r.ok) Icons.Rounded.CheckCircle else Icons.Rounded.ErrorOutline, null,
                    Modifier.size(18.dp), tint = if (r.ok) status.ok else status.critical,
                )
                Spacer(Modifier.width(8.dp))
                Text(
                    if (r.ok) "Message de test envoyé" else r.error.replaceFirstChar { it.uppercase() },
                    style = MaterialTheme.typography.bodyMedium,
                    color = if (r.ok) status.ok else status.critical,
                )
            }
        }
    }
}
