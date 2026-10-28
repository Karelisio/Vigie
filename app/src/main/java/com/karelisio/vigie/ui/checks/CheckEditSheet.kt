package com.karelisio.vigie.ui.checks

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.EnterTransition
import androidx.compose.animation.ExitTransition
import androidx.compose.animation.expandVertically
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.shrinkVertically
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AssistChip
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.FilterChip
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.SegmentedButton
import androidx.compose.material3.SegmentedButtonDefaults
import androidx.compose.material3.SingleChoiceSegmentedButtonRow
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import com.karelisio.vigie.data.CheckInput
import com.karelisio.vigie.data.CheckStatus
import com.karelisio.vigie.ui.components.rememberHaptics
import com.karelisio.vigie.ui.theme.Vigie
import kotlinx.coroutines.launch

/** [stremio] : addon testé par une vraie recherche de liens (agent 0.4 et plus), sinon simple page web. */
private data class Preset(val name: String, val sub: String, val path: String, val keyword: String = "\"id\"", val stremio: Boolean = false)

private val Presets = listOf(
    Preset("AIOStreams", "aiostreams", "/stremio/manifest.json", stremio = true),
    Preset("Comet", "comet", "/manifest.json", stremio = true),
    Preset("AIOMetadata", "aiometadata", "/manifest.json"),
)

private const val DEFAULT_STREMIO_ID = "tt0111161"
private val StremioIdRe = Regex("""tt\d{1,10}(:\d{1,5}:\d{1,5})?""")

/** Création / modification d'un check : page web, ou addon Stremio si l'agent le permet ([stremioSupported]). */
@Composable
fun CheckEditSheet(
    initial: CheckStatus?,
    baseDomain: String?,
    stremioSupported: Boolean,
    onDismiss: () -> Unit,
    onSave: suspend (CheckInput) -> Unit,
) {
    val sheet = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    val scope = rememberCoroutineScope()
    val haptics = rememberHaptics()
    var name by remember { mutableStateOf(initial?.name.orEmpty()) }
    var url by remember { mutableStateOf(initial?.url ?: "https://") }
    var code by remember { mutableStateOf((initial?.expectedStatus ?: 200).toString()) }
    var keyword by remember { mutableStateOf(initial?.keyword.orEmpty()) }
    var interval by remember { mutableStateOf(initial?.intervalSec ?: 60) }
    var enabled by remember { mutableStateOf(initial?.enabled ?: true) }
    var stremio by remember { mutableStateOf(stremioSupported && initial?.isStremio == true) }
    var stremioId by remember { mutableStateOf(initial?.stremioId?.ifEmpty { null } ?: DEFAULT_STREMIO_ID) }
    var saving by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    val reduced = Vigie.reducedMotion

    val urlValid = (url.startsWith("http://") || url.startsWith("https://")) && url.length > 10
    val manifestValid = url.substringBefore('?').substringBefore('#').endsWith("/manifest.json")
    val codeValid = code.toIntOrNull()?.let { it in 100..599 } == true
    val idValid = StremioIdRe.matches(stremioId.trim())
    val valid = name.isNotBlank() && urlValid && (if (stremio) manifestValid && idValid else codeValid)

    // Une recherche de liens sollicite les services debrid : 5 min au minimum entre deux essais.
    fun setStremio(on: Boolean) {
        stremio = on
        if (on && interval < 300) interval = 900
        if (!on && interval > 900) interval = 900
    }

    ModalBottomSheet(onDismissRequest = onDismiss, sheetState = sheet) {
        Column(
            Modifier
                .fillMaxWidth()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 24.dp)
                .navigationBarsPadding()
                .imePadding()
                .padding(bottom = 16.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            Text(if (initial == null) "Nouveau service" else "Modifier « ${initial.name} »", style = MaterialTheme.typography.headlineSmall)
            if (stremioSupported) {
                SingleChoiceSegmentedButtonRow(Modifier.fillMaxWidth()) {
                    listOf(false to "Page web", true to "Addon Stremio").forEachIndexed { i, (on, label) ->
                        SegmentedButton(
                            selected = stremio == on,
                            onClick = { haptics.tick(); setStremio(on) },
                            shape = SegmentedButtonDefaults.itemShape(i, 2),
                        ) { Text(label) }
                    }
                }
            }
            if (initial == null && baseDomain != null) {
                Text("Addons courants", style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.onSurfaceVariant)
                LazyRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    items(Presets) { p ->
                        AssistChip(
                            onClick = {
                                haptics.tick()
                                name = p.name
                                url = "https://${p.sub}.$baseDomain${p.path}"
                                val asStremio = p.stremio && stremioSupported
                                keyword = if (asStremio) "" else p.keyword
                                setStremio(asStremio)
                            },
                            label = { Text(p.name) },
                        )
                    }
                }
            }
            OutlinedTextField(name, { name = it }, label = { Text("Nom") }, singleLine = true, modifier = Modifier.fillMaxWidth())
            OutlinedTextField(
                url, { url = it.trim() },
                label = { Text("URL") },
                supportingText = {
                    Text(
                        if (stremio) "Le manifest configuré, celui installé dans Stremio (…/manifest.json)."
                        else "Par ex. le manifest.json de l'addon. Une URL interne (http://conteneur:port) fonctionne aussi.",
                    )
                },
                isError = url.length > 8 && !(urlValid && (!stremio || manifestValid)),
                singleLine = true,
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri),
                modifier = Modifier.fillMaxWidth(),
            )
            val enter = if (reduced) EnterTransition.None else expandVertically() + fadeIn()
            val exit = if (reduced) ExitTransition.None else shrinkVertically() + fadeOut()
            AnimatedVisibility(stremio, enter = enter, exit = exit) {
                OutlinedTextField(
                    stremioId, { stremioId = it.trim() },
                    label = { Text("Contenu de test (IMDb)") },
                    supportingText = { Text("Un film (tt0111161) ou un épisode (tt0903747:1:1). L'addon doit renvoyer au moins un lien.") },
                    isError = !idValid,
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
            }
            AnimatedVisibility(!stremio, enter = enter, exit = exit) {
                Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                    OutlinedTextField(
                        code, { code = it.filter(Char::isDigit).take(3) },
                        label = { Text("Code attendu") },
                        isError = !codeValid,
                        singleLine = true,
                        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
                        modifier = Modifier.weight(0.4f),
                    )
                    OutlinedTextField(
                        keyword, { keyword = it },
                        label = { Text("Contient (facultatif)") },
                        singleLine = true,
                        modifier = Modifier.weight(0.6f),
                    )
                }
            }
            Text("Fréquence", style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                val choices = if (stremio) listOf(300 to "5 min", 900 to "15 min", 1800 to "30 min", 3600 to "1 h")
                else listOf(30 to "30 s", 60 to "1 min", 300 to "5 min", 900 to "15 min")
                choices.forEach { (v, l) ->
                    FilterChip(interval == v, onClick = { haptics.tick(); interval = v }, label = { Text(l) })
                }
            }
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text("Surveillance active", Modifier.weight(1f), style = MaterialTheme.typography.bodyLarge)
                Switch(enabled, { haptics.toggle(it); enabled = it })
            }
            error?.let { Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodyMedium) }
            Spacer(Modifier.height(4.dp))
            Button(
                onClick = {
                    saving = true
                    error = null
                    scope.launch {
                        runCatching {
                            onSave(
                                CheckInput(
                                    name.trim(), url.trim(), code.toIntOrNull() ?: 200, keyword.trim(), interval,
                                    if (stremio) 30_000 else 10_000, enabled,
                                    // Un agent antérieur à 0.4 refuse ces champs : ils ne sont alors pas envoyés.
                                    kind = if (!stremioSupported) null else if (stremio) "stremio" else "http",
                                    stremioId = if (stremio) stremioId.trim() else null,
                                ),
                            )
                        }.onFailure {
                            haptics.reject()
                            error = it.message
                            saving = false
                        }.onSuccess { haptics.confirm() }
                    }
                },
                enabled = valid && !saving,
                shapes = ButtonDefaults.shapes(),
                modifier = Modifier.fillMaxWidth().height(56.dp),
            ) { Text(if (saving) "Enregistrement…" else "Enregistrer") }
        }
    }
}
