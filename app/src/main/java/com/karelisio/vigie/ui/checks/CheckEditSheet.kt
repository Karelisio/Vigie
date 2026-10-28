package com.karelisio.vigie.ui.checks

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
import kotlinx.coroutines.launch

private data class Preset(val name: String, val sub: String, val path: String, val keyword: String = "\"id\"")

private val Presets = listOf(
    Preset("AIOStreams", "aiostreams", "/stremio/manifest.json"),
    Preset("Comet", "comet", "/manifest.json"),
    Preset("AIOMetadata", "aiometadata", "/manifest.json"),
)

/** Création / modification d'un check HTTP. */
@Composable
fun CheckEditSheet(
    initial: CheckStatus?,
    baseDomain: String?,
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
    var saving by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }

    val urlValid = (url.startsWith("http://") || url.startsWith("https://")) && url.length > 10
    val codeValid = code.toIntOrNull()?.let { it in 100..599 } == true
    val valid = name.isNotBlank() && urlValid && codeValid

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
            if (initial == null && baseDomain != null) {
                Text("Addons courants", style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.onSurfaceVariant)
                LazyRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    items(Presets) { p ->
                        AssistChip(
                            onClick = {
                                haptics.tick()
                                name = p.name
                                url = "https://${p.sub}.$baseDomain${p.path}"
                                keyword = p.keyword
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
                supportingText = { Text("Par ex. le manifest.json de l'addon. Une URL interne (http://conteneur:port) fonctionne aussi.") },
                isError = url.length > 8 && !urlValid,
                singleLine = true,
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri),
                modifier = Modifier.fillMaxWidth(),
            )
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
            Text("Fréquence", style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                listOf(30 to "30 s", 60 to "1 min", 300 to "5 min", 900 to "15 min").forEach { (v, l) ->
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
                            onSave(CheckInput(name.trim(), url.trim(), code.toInt(), keyword.trim(), interval, 10_000, enabled))
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
