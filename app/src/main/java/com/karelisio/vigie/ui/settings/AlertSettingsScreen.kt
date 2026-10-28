package com.karelisio.vigie.ui.settings

import androidx.compose.animation.AnimatedVisibility
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
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
import androidx.compose.material.icons.rounded.Bedtime
import androidx.compose.material.icons.rounded.Close
import androidx.compose.material.icons.rounded.DeleteSweep
import androidx.compose.material.icons.rounded.Cloud
import androidx.compose.material.icons.rounded.NewReleases
import androidx.compose.material.icons.rounded.NotificationsOff
import androidx.compose.material.icons.rounded.Save
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.InputChip
import androidx.compose.material3.LoadingIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Slider
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TimePicker
import androidx.compose.material3.rememberTimePickerState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.NavHostController
import com.karelisio.vigie.appContainer
import com.karelisio.vigie.data.AgentSettings
import com.karelisio.vigie.data.ApiException
import com.karelisio.vigie.ui.components.EmptyState
import com.karelisio.vigie.ui.components.SectionCard
import com.karelisio.vigie.ui.components.cascade
import com.karelisio.vigie.ui.components.rememberHaptics
import com.karelisio.vigie.ui.navigation.LocalSnackbar
import com.karelisio.vigie.ui.theme.numeric
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlin.math.roundToInt

class AlertSettingsViewModel : ViewModel() {
    private val repo = appContainer.repository
    private val _saved = MutableStateFlow<AgentSettings?>(null)
    val saved = _saved.asStateFlow()
    private val _error = MutableStateFlow<String?>(null)
    val error = _error.asStateFlow()

    init {
        load()
    }

    fun load() {
        viewModelScope.launch {
            _error.value = null
            runCatching { repo.settings() }
                .onSuccess { _saved.value = it }
                .onFailure {
                    _error.value = if (it is ApiException && it.code == 404) {
                        "Ton agent est trop ancien pour régler les alertes depuis l'appli. Mets-le à jour (Pull + Redeploy dans Dockhand)."
                    } else it.message
                }
        }
    }

    suspend fun save(s: AgentSettings): Result<AgentSettings> = runCatching { repo.saveSettings(s) }.onSuccess { _saved.value = it }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
fun AlertSettingsScreen(nav: NavHostController, vm: AlertSettingsViewModel = viewModel()) {
    val saved by vm.saved.collectAsStateWithLifecycle()
    val error by vm.error.collectAsStateWithLifecycle()
    var draft by remember(saved) { mutableStateOf(saved) }
    var saving by remember { mutableStateOf(false) }
    var picking by remember { mutableStateOf<String?>(null) }
    val scope = rememberCoroutineScope()
    val snackbar = LocalSnackbar.current
    val haptics = rememberHaptics()
    val dirty = draft != null && draft != saved
    val bottom = WindowInsets.navigationBars.asPaddingValues().calculateBottomPadding() + 96.dp

    Box(Modifier.fillMaxSize()) {
        LazyColumn(
            Modifier.fillMaxSize(),
            contentPadding = PaddingValues(start = 16.dp, end = 16.dp, bottom = bottom),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            item {
                Row(Modifier.statusBarsPadding().padding(top = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                    IconButton(onClick = { nav.popBackStack() }) { Icon(Icons.AutoMirrored.Rounded.ArrowBack, "Retour") }
                    Text("Alertes et seuils", style = MaterialTheme.typography.headlineSmallEmphasized)
                }
            }
            val s = draft
            when {
                error != null -> item {
                    EmptyState(Icons.Rounded.Cloud, "Réglages indisponibles", error.orEmpty()) {
                        OutlinedButton(onClick = vm::load) { Text("Réessayer") }
                    }
                }
                s == null -> item {
                    Box(Modifier.fillMaxWidth().padding(48.dp), contentAlignment = Alignment.Center) { LoadingIndicator() }
                }
                else -> {
                    item {
                        SectionCard(Modifier.cascade(0), title = "Seuils d'utilisation") {
                            Text(
                                "Une alerte part quand la valeur reste au-dessus du seuil pendant la durée choisie.",
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                            Spacer(Modifier.height(8.dp))
                            ThresholdSlider("Processeur", s.cpu) { draft = s.copy(cpu = it) }
                            ThresholdSlider("Mémoire", s.ram) { draft = s.copy(ram = it) }
                            ThresholdSlider("Disque", s.disk) { draft = s.copy(disk = it) }
                            Spacer(Modifier.height(8.dp))
                            Text("Durée de dépassement", style = MaterialTheme.typography.labelLarge)
                            Spacer(Modifier.height(4.dp))
                            FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                                listOf(1, 2, 5, 10, 15, 30).forEach { m ->
                                    FilterChip(
                                        selected = s.durationMin == m,
                                        onClick = { haptics.tick(); draft = s.copy(durationMin = m) },
                                        label = { Text("$m min") },
                                    )
                                }
                            }
                        }
                    }
                    item {
                        SectionCard(Modifier.cascade(1), title = "Heures calmes") {
                            ToggleRow(
                                Icons.Rounded.Bedtime, "Pas de notification la nuit",
                                "Les incidents sont résumés en une notification à la fin de la plage",
                                s.quietEnabled,
                            ) { on -> haptics.toggle(on); draft = s.copy(quietEnabled = on) }
                            AnimatedVisibility(s.quietEnabled) {
                                Row(Modifier.padding(top = 8.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                                    OutlinedButton(onClick = { picking = "start" }, modifier = Modifier.weight(1f)) { Text("De ${s.quietStart}") }
                                    OutlinedButton(onClick = { picking = "end" }, modifier = Modifier.weight(1f)) { Text("À ${s.quietEnd}") }
                                }
                            }
                        }
                    }
                    item {
                        SectionCard(Modifier.cascade(2), title = "Autres alertes") {
                            Text("Certificat HTTPS : prévenir", style = MaterialTheme.typography.labelLarge)
                            Spacer(Modifier.height(4.dp))
                            FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                                listOf(7, 14, 21, 30).forEach { d ->
                                    FilterChip(
                                        selected = s.certDays == d,
                                        onClick = { haptics.tick(); draft = s.copy(certDays = d) },
                                        label = { Text("$d jours avant") },
                                    )
                                }
                            }
                            Spacer(Modifier.height(12.dp))
                            ToggleRow(
                                Icons.Rounded.NewReleases, "Mises à jour d'images",
                                "Prévenir quand une nouvelle version d'image est publiée",
                                s.imageUpdates,
                            ) { on -> haptics.toggle(on); draft = s.copy(imageUpdates = on) }
                            Spacer(Modifier.height(8.dp))
                            ToggleRow(
                                Icons.Rounded.Cloud, "Oracle Always Free",
                                "Prévenir si l'instance risque d'être récupérée pour inactivité",
                                s.oracleWatch,
                            ) { on -> haptics.toggle(on); draft = s.copy(oracleWatch = on) }
                        }
                    }
                    item {
                        SectionCard(Modifier.cascade(3), title = "Mises à jour d'images") {
                            ToggleRow(
                                Icons.Rounded.DeleteSweep, "Supprimer l'ancienne image",
                                "Libère l'espace disque après une mise à jour réussie",
                                s.pruneImages,
                            ) { on -> haptics.toggle(on); draft = s.copy(pruneImages = on) }
                        }
                    }
                    item {
                        SectionCard(Modifier.cascade(4), title = "Conteneurs en sourdine") {
                            if (s.muted.isEmpty()) {
                                Text(
                                    "Aucun. Coupe les notifications d'un conteneur avec la cloche de sa fiche.",
                                    style = MaterialTheme.typography.bodyMedium,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                                )
                            } else {
                                FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                                    s.muted.forEach { name ->
                                        InputChip(
                                            selected = false,
                                            onClick = { haptics.tick(); draft = s.copy(muted = s.muted - name) },
                                            label = { Text(name) },
                                            leadingIcon = { Icon(Icons.Rounded.NotificationsOff, null) },
                                            trailingIcon = { Icon(Icons.Rounded.Close, "Réactiver") },
                                        )
                                    }
                                }
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
                    val d = draft ?: return@Button
                    saving = true
                    scope.launch {
                        val r = vm.save(d)
                        saving = false
                        if (r.isSuccess) haptics.confirm() else haptics.reject()
                        snackbar.showSnackbar(r.fold({ "Réglages enregistrés sur l'agent" }, { "Échec : ${it.message}" }))
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

    val s = draft
    if (picking != null && s != null) {
        TimeDialog(
            initial = if (picking == "start") s.quietStart else s.quietEnd,
            onDismiss = { picking = null },
        ) { t ->
            draft = if (picking == "start") s.copy(quietStart = t) else s.copy(quietEnd = t)
            picking = null
        }
    }
}

@Composable
private fun ThresholdSlider(label: String, value: Double, onChange: (Double) -> Unit) {
    val haptics = rememberHaptics()
    Column(Modifier.padding(vertical = 2.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(label, style = MaterialTheme.typography.labelLarge, modifier = Modifier.weight(1f))
            Text("${value.roundToInt()} %", style = MaterialTheme.typography.titleMedium.numeric(), color = MaterialTheme.colorScheme.primary)
        }
        Slider(
            value = value.toFloat(),
            onValueChange = {
                val v = it.roundToInt().toDouble()
                if (v != value) {
                    if (v.toInt() % 5 == 0) haptics.frequentTick()
                    onChange(v)
                }
            },
            valueRange = 30f..99f,
        )
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun TimeDialog(initial: String, onDismiss: () -> Unit, onPick: (String) -> Unit) {
    val (h, m) = initial.split(":").map { it.toIntOrNull() ?: 0 }.let { (it.getOrElse(0) { 0 }) to (it.getOrElse(1) { 0 }) }
    val state = rememberTimePickerState(initialHour = h, initialMinute = m, is24Hour = true)
    AlertDialog(
        onDismissRequest = onDismiss,
        confirmButton = { TextButton(onClick = { onPick("%02d:%02d".format(state.hour, state.minute)) }) { Text("OK") } },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Annuler") } },
        text = { TimePicker(state) },
    )
}
