package com.karelisio.vigie.ui.settings

import androidx.compose.animation.AnimatedVisibility
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
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.selection.toggleable
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
import androidx.compose.material.icons.rounded.Cloud
import androidx.compose.material.icons.rounded.Inventory2
import androidx.compose.material.icons.rounded.NightsStay
import androidx.compose.material.icons.rounded.Radar
import androidx.compose.material.icons.rounded.Save
import androidx.compose.material.icons.rounded.Schedule
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Checkbox
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LoadingIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.NavHostController
import com.karelisio.vigie.appContainer
import com.karelisio.vigie.data.LiveState
import com.karelisio.vigie.data.versionAtLeast
import com.karelisio.vigie.ui.components.Chip
import com.karelisio.vigie.ui.components.EmptyState
import com.karelisio.vigie.ui.components.SectionCard
import com.karelisio.vigie.ui.components.cascade
import com.karelisio.vigie.ui.components.rememberHaptics
import com.karelisio.vigie.ui.navigation.LocalSnackbar
import kotlinx.coroutines.launch

/** Pourquoi les mises à jour de nuit ne sont pas réglables sur cet agent (null : elles le sont). */
private fun unsupported(live: LiveState): String? = when {
    live.agentVersion.isEmpty() -> null // pas encore connecté : les réglages diront le reste
    !versionAtLeast(live.agentVersion, 0, 4) ->
        "Ton agent (${live.agentVersion}) ne gère pas encore les mises à jour automatiques : mets-le à jour (0.4.0 ou plus)."
    !live.updatesEnabled -> "Les mises à jour sont désactivées sur l'agent (VIGIE_ALLOW_UPDATES=false)."
    else -> null
}

/** Mises à jour automatiques de nuit : activation, heure, conteneurs concernés, agent. */
@Composable
fun AutoUpdateScreen(nav: NavHostController, vm: AlertSettingsViewModel = viewModel()) {
    val saved by vm.saved.collectAsStateWithLifecycle()
    val error by vm.error.collectAsStateWithLifecycle()
    val live by appContainer.repository.live.collectAsStateWithLifecycle()
    var draft by remember(saved) { mutableStateOf(saved) }
    var saving by remember { mutableStateOf(false) }
    var picking by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    val snackbar = LocalSnackbar.current
    val haptics = rememberHaptics()
    val dirty = draft != null && draft != saved
    val bottom = WindowInsets.navigationBars.asPaddingValues().calculateBottomPadding() + 96.dp
    val blocked = unsupported(live)

    Box(Modifier.fillMaxSize()) {
        LazyColumn(
            Modifier.fillMaxSize(),
            contentPadding = PaddingValues(start = 16.dp, end = 16.dp, bottom = bottom),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            item {
                Row(Modifier.statusBarsPadding().padding(top = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                    IconButton(onClick = { nav.popBackStack() }) { Icon(Icons.AutoMirrored.Rounded.ArrowBack, "Retour") }
                    Text("Mises à jour automatiques", style = MaterialTheme.typography.headlineSmallEmphasized)
                }
            }
            val s = draft
            when {
                blocked != null -> item { EmptyState(Icons.Rounded.NightsStay, "Indisponible sur cet agent", blocked) }
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
                        SectionCard(Modifier.cascade(0), title = "Chaque nuit") {
                            ToggleRow(
                                Icons.Rounded.NightsStay, "Mettre à jour la nuit",
                                "Installe les nouvelles images, avec retour à l'ancienne version si un conteneur ne redémarre pas",
                                s.autoUpdate,
                            ) { on -> haptics.toggle(on); draft = s.copy(autoUpdate = on) }
                            AnimatedVisibility(s.autoUpdate) {
                                Column(Modifier.padding(top = 8.dp)) {
                                    OutlinedButton(onClick = { picking = true }, shapes = ButtonDefaults.shapes(), modifier = Modifier.fillMaxWidth()) {
                                        Icon(Icons.Rounded.Schedule, null)
                                        Spacer(Modifier.width(8.dp))
                                        Text("À ${s.autoUpdateTime}")
                                    }
                                    Spacer(Modifier.height(8.dp))
                                    Text(
                                        if (!s.autoUpdateAll && s.autoUpdateList.isEmpty() && !s.autoUpdateAgent) {
                                            "Choisis au moins un conteneur ci-dessous."
                                        } else {
                                            "Le bilan arrive en notification ; pendant les heures calmes, il rejoint le résumé du matin."
                                        },
                                        style = MaterialTheme.typography.bodySmall,
                                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                                    )
                                }
                            }
                        }
                    }
                    if (s.autoUpdate) {
                        item {
                            SectionCard(Modifier.cascade(1), title = "Conteneurs") {
                                ToggleRow(
                                    Icons.Rounded.Inventory2, "Tous les conteneurs", "Y compris ceux créés plus tard",
                                    s.autoUpdateAll,
                                ) { on -> haptics.toggle(on); draft = s.copy(autoUpdateAll = on) }
                                AnimatedVisibility(!s.autoUpdateAll) {
                                    Column(Modifier.padding(top = 8.dp)) {
                                        val current = live.containers.filter { !it.self }.associateBy { it.name }
                                        // Les conteneurs choisis puis supprimés restent listés, pour pouvoir les décocher.
                                        val names = (current.keys + s.autoUpdateList).sorted()
                                        if (names.isEmpty()) {
                                            Text(
                                                "Aucun conteneur pour l'instant.",
                                                style = MaterialTheme.typography.bodyMedium,
                                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                                            )
                                        }
                                        names.forEach { name ->
                                            val checked = name in s.autoUpdateList
                                            val ctr = current[name]
                                            Row(
                                                Modifier
                                                    .fillMaxWidth()
                                                    .clip(RoundedCornerShape(16.dp))
                                                    .toggleable(checked, role = Role.Checkbox) { on ->
                                                        haptics.toggle(on)
                                                        draft = s.copy(autoUpdateList = (if (on) s.autoUpdateList + name else s.autoUpdateList - name).sorted())
                                                    }
                                                    .padding(vertical = 2.dp),
                                                verticalAlignment = Alignment.CenterVertically,
                                            ) {
                                                Checkbox(checked = checked, onCheckedChange = null, modifier = Modifier.padding(12.dp))
                                                Text(
                                                    name,
                                                    style = MaterialTheme.typography.bodyLarge,
                                                    maxLines = 1,
                                                    overflow = TextOverflow.Ellipsis,
                                                    modifier = Modifier.weight(1f),
                                                )
                                                when {
                                                    ctr == null -> Text(
                                                        "absent",
                                                        style = MaterialTheme.typography.labelMedium,
                                                        color = MaterialTheme.colorScheme.outline,
                                                        modifier = Modifier.padding(end = 8.dp),
                                                    )
                                                    ctr.updateAvailable -> Chip(
                                                        "MAJ",
                                                        MaterialTheme.colorScheme.tertiaryContainer,
                                                        MaterialTheme.colorScheme.onTertiaryContainer,
                                                        Modifier.padding(end = 8.dp),
                                                    )
                                                }
                                            }
                                        }
                                    }
                                }
                            }
                        }
                        item {
                            SectionCard(Modifier.cascade(2), title = "Agent Vigie") {
                                ToggleRow(
                                    Icons.Rounded.Radar, "Inclure l'agent Vigie",
                                    "Mis à jour en dernier, après les autres conteneurs (environ 10 secondes de coupure)",
                                    s.autoUpdateAgent,
                                ) { on -> haptics.toggle(on); draft = s.copy(autoUpdateAgent = on) }
                            }
                        }
                    }
                }
            }
        }

        AnimatedVisibility(
            dirty && blocked == null,
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
    if (picking && s != null) {
        TimeDialog(initial = s.autoUpdateTime, onDismiss = { picking = false }) { t ->
            draft = s.copy(autoUpdateTime = t)
            picking = false
        }
    }
}
