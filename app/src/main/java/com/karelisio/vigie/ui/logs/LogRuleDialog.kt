package com.karelisio.vigie.ui.logs

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.selection.toggleable
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.NotificationAdd
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExposedDropdownMenu
import androidx.compose.material3.ExposedDropdownMenuAnchorType
import androidx.compose.material3.ExposedDropdownMenuBox
import androidx.compose.material3.ExposedDropdownMenuDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.LoadingIndicator
import androidx.compose.material3.LocalTextStyle
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.karelisio.vigie.appContainer
import com.karelisio.vigie.data.LogRule
import com.karelisio.vigie.data.versionAtLeast
import com.karelisio.vigie.ui.components.rememberHaptics
import kotlinx.coroutines.launch

/** Vrai si l'agent connecté gère les alertes sur les logs (0.4 et plus). */
@Composable
fun logAlertsSupported(): Boolean {
    val live = appContainer.repository.live.collectAsStateWithLifecycle()
    // Seul le résultat est observé : pas de recomposition à chaque mise à jour du direct.
    val supported by remember { derivedStateOf { versionAtLeast(live.value.agentVersion, 0, 4) } }
    return supported
}

/**
 * Ajout d'une alerte sur les logs : conteneur, texte recherché, expression régulière ou non.
 * [onSave] enregistre sur l'agent ; son erreur (motif refusé…) est affichée telle quelle.
 */
@Composable
fun LogRuleDialog(
    containers: List<String>,
    initialContainer: String?,
    initialPattern: String,
    onDismiss: () -> Unit,
    onSave: suspend (LogRule) -> Unit,
) {
    val names = remember(containers, initialContainer) { (containers + listOfNotNull(initialContainer)).distinct().sorted() }
    var container by remember { mutableStateOf(initialContainer ?: names.firstOrNull().orEmpty()) }
    var pattern by remember { mutableStateOf(initialPattern.trim()) }
    var regex by remember { mutableStateOf(false) }
    var menu by remember { mutableStateOf(false) }
    var saving by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    val scope = rememberCoroutineScope()
    val haptics = rememberHaptics()
    val valid = container.isNotBlank() && pattern.isNotBlank() && pattern.length <= 200

    AlertDialog(
        onDismissRequest = { if (!saving) onDismiss() },
        icon = { Icon(Icons.Rounded.NotificationAdd, null) },
        title = { Text("Alerte sur les logs") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                ExposedDropdownMenuBox(expanded = menu, onExpandedChange = { menu = it && !saving }) {
                    OutlinedTextField(
                        value = container,
                        onValueChange = {},
                        readOnly = true,
                        singleLine = true,
                        label = { Text("Conteneur") },
                        trailingIcon = { ExposedDropdownMenuDefaults.TrailingIcon(menu) },
                        modifier = Modifier.menuAnchor(ExposedDropdownMenuAnchorType.PrimaryNotEditable).fillMaxWidth(),
                    )
                    ExposedDropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                        names.forEach { name ->
                            DropdownMenuItem(
                                text = { Text(name) },
                                onClick = {
                                    haptics.tick()
                                    container = name
                                    error = null
                                    menu = false
                                },
                            )
                        }
                    }
                }
                OutlinedTextField(
                    value = pattern,
                    onValueChange = { pattern = it.take(200); error = null },
                    singleLine = true,
                    label = { Text(if (regex) "Expression régulière" else "Texte recherché") },
                    supportingText = {
                        Text(
                            if (regex) "Syntaxe RE2, sensible à la casse. Ex. : timeout|refused"
                            else "Majuscules et minuscules confondues",
                        )
                    },
                    isError = error != null,
                    textStyle = LocalTextStyle.current.copy(fontFamily = FontFamily.Monospace),
                    modifier = Modifier.fillMaxWidth(),
                )
                Row(
                    Modifier
                        .fillMaxWidth()
                        .toggleable(regex, enabled = !saving, role = Role.Switch) {
                            haptics.toggle(it)
                            regex = it
                            error = null
                        },
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Text("Expression régulière", style = MaterialTheme.typography.bodyLarge, modifier = Modifier.weight(1f))
                    Switch(checked = regex, onCheckedChange = null)
                }
                Text(
                    "Au plus une notification toutes les 15 min par alerte : les lignes suivantes sont comptées.",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                error?.let {
                    Text(it, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.error)
                }
            }
        },
        confirmButton = {
            Button(
                onClick = {
                    saving = true
                    error = null
                    scope.launch {
                        runCatching { onSave(LogRule(container = container, pattern = pattern.trim(), regex = regex)) }
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
                if (saving) LoadingIndicator(Modifier.size(20.dp)) else Text("Ajouter")
            }
        },
        dismissButton = { TextButton(onClick = onDismiss, enabled = !saving) { Text("Annuler") } },
    )
}
