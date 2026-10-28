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
import androidx.compose.foundation.selection.toggleable
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
import androidx.compose.material.icons.rounded.CheckCircle
import androidx.compose.material.icons.rounded.Cloud
import androidx.compose.material.icons.rounded.ErrorOutline
import androidx.compose.material.icons.rounded.Healing
import androidx.compose.material.icons.rounded.Inventory2
import androidx.compose.material.icons.rounded.Save
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Checkbox
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LoadingIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
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
import com.karelisio.vigie.data.AgentSettings
import com.karelisio.vigie.data.AutoHealRepair
import com.karelisio.vigie.data.Container
import com.karelisio.vigie.data.Fmt
import com.karelisio.vigie.data.autoHealHistory
import com.karelisio.vigie.data.summary
import com.karelisio.vigie.data.versionAtLeast
import com.karelisio.vigie.ui.components.EmptyState
import com.karelisio.vigie.ui.components.SectionCard
import com.karelisio.vigie.ui.components.StatusDot
import com.karelisio.vigie.ui.components.cascade
import com.karelisio.vigie.ui.components.rememberHaptics
import com.karelisio.vigie.ui.components.visualStatus
import com.karelisio.vigie.ui.navigation.LocalSnackbar
import com.karelisio.vigie.ui.theme.Vigie
import kotlinx.coroutines.launch

/** Réparation automatique : activation, délai, limite par heure, conteneurs concernés et dernières réparations. */
@Composable
fun AutoHealScreen(nav: NavHostController, vm: AlertSettingsViewModel = viewModel()) {
    val saved by vm.saved.collectAsStateWithLifecycle()
    val error by vm.error.collectAsStateWithLifecycle()
    val live by appContainer.repository.live.collectAsStateWithLifecycle()
    var draft by remember(saved) { mutableStateOf(saved) }
    var saving by remember { mutableStateOf(false) }
    var repairs by remember { mutableStateOf<Result<List<AutoHealRepair>>?>(null) }
    val scope = rememberCoroutineScope()
    val snackbar = LocalSnackbar.current
    val haptics = rememberHaptics()
    val dirty = draft != null && draft != saved
    val bottom = WindowInsets.navigationBars.asPaddingValues().calculateBottomPadding() + 96.dp
    // Version encore inconnue (pas connecté) : les réglages diront le reste.
    val blocked = live.agentVersion.isNotEmpty() && !versionAtLeast(live.agentVersion, 0, 5)
    LaunchedEffect(blocked) { if (!blocked) repairs = runCatching { appContainer.repository.autoHealHistory() } }

    Box(Modifier.fillMaxSize()) {
        LazyColumn(
            Modifier.fillMaxSize(),
            contentPadding = PaddingValues(start = 16.dp, end = 16.dp, bottom = bottom),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            item {
                Row(Modifier.statusBarsPadding().padding(top = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                    IconButton(onClick = { nav.popBackStack() }) { Icon(Icons.AutoMirrored.Rounded.ArrowBack, "Retour") }
                    Text("Réparation automatique", style = MaterialTheme.typography.headlineSmallEmphasized)
                }
            }
            val s = draft
            when {
                blocked -> item {
                    EmptyState(
                        Icons.Rounded.Healing, "Indisponible sur cet agent",
                        "Ton agent (${live.agentVersion}) ne gère pas encore la réparation automatique : mets-le à jour (0.5.0 ou plus).",
                    )
                }
                error != null -> item {
                    EmptyState(Icons.Rounded.Cloud, "Réglages indisponibles", error.orEmpty()) {
                        OutlinedButton(onClick = vm::load) { Text("Réessayer") }
                    }
                }
                s == null -> item {
                    Box(Modifier.fillMaxWidth().padding(48.dp), contentAlignment = Alignment.Center) { LoadingIndicator() }
                }
                else -> {
                    item { HealCard(s, Modifier.cascade(0)) { draft = it } }
                    if (s.autoHeal) {
                        item { HealContainersCard(s, live.containers, Modifier.cascade(1)) { draft = it } }
                    }
                    item { RepairsCard(repairs, Modifier.cascade(2)) }
                }
            }
        }

        AnimatedVisibility(
            dirty && !blocked,
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
}

/** Activation, délai avant redémarrage et nombre de réparations par heure. */
@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun HealCard(s: AgentSettings, modifier: Modifier, onChange: (AgentSettings) -> Unit) {
    val haptics = rememberHaptics()
    SectionCard(modifier, title = "Réparation automatique") {
        ToggleRow(
            Icons.Rounded.Healing, "Réparer automatiquement",
            "Redémarre un conteneur dont le healthcheck échoue, relance celui qui s'arrête anormalement",
            s.autoHeal,
        ) { on -> haptics.toggle(on); onChange(s.copy(autoHeal = on)) }
        AnimatedVisibility(s.autoHeal) {
            Column(Modifier.padding(top = 12.dp)) {
                Text("Healthcheck en échec depuis", style = MaterialTheme.typography.labelLarge)
                Spacer(Modifier.height(4.dp))
                FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    listOf(1, 3, 5, 10).forEach { m ->
                        FilterChip(
                            selected = s.autoHealDelay == m,
                            onClick = { haptics.tick(); onChange(s.copy(autoHealDelay = m)) },
                            label = { Text("$m min") },
                        )
                    }
                }
                Spacer(Modifier.height(8.dp))
                Text("Au plus, par heure et par conteneur", style = MaterialTheme.typography.labelLarge)
                Spacer(Modifier.height(4.dp))
                FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    listOf(1, 3, 5).forEach { n ->
                        FilterChip(
                            selected = s.autoHealMax == n,
                            onClick = { haptics.tick(); onChange(s.copy(autoHealMax = n)) },
                            label = { Text(if (n == 1) "1 réparation" else "$n réparations") },
                        )
                    }
                }
                Text(
                    "Un conteneur planté est relancé 20 secondes après son arrêt. Au-delà de la limite, Vigie abandonne " +
                        "et t'envoie une alerte, jusqu'à ce que le conteneur fonctionne de nouveau. Jamais l'agent Vigie, " +
                        "ni un conteneur que tu as arrêté.",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
    }
}

/** Tous les conteneurs, ou une sélection (avec leur état actuel). */
@Composable
private fun HealContainersCard(s: AgentSettings, containers: List<Container>, modifier: Modifier, onChange: (AgentSettings) -> Unit) {
    val haptics = rememberHaptics()
    SectionCard(modifier, title = "Conteneurs") {
        ToggleRow(
            Icons.Rounded.Inventory2, "Tous les conteneurs", "Y compris ceux créés plus tard",
            s.autoHealAll,
        ) { on -> haptics.toggle(on); onChange(s.copy(autoHealAll = on)) }
        AnimatedVisibility(!s.autoHealAll) {
            Column(Modifier.padding(top = 8.dp)) {
                val current = containers.filter { !it.self }.associateBy { it.name }
                // Les conteneurs choisis puis supprimés restent listés, pour pouvoir les décocher.
                val names = (current.keys + s.autoHealList).sorted()
                if (names.isEmpty()) {
                    Text(
                        "Aucun conteneur pour l'instant.",
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                names.forEach { name ->
                    val checked = name in s.autoHealList
                    val ctr = current[name]
                    Row(
                        Modifier
                            .fillMaxWidth()
                            .clip(RoundedCornerShape(16.dp))
                            .toggleable(checked, role = Role.Checkbox) { on ->
                                haptics.toggle(on)
                                onChange(s.copy(autoHealList = (if (on) s.autoHealList + name else s.autoHealList - name).sorted()))
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
                        if (ctr == null) {
                            Text(
                                "absent",
                                style = MaterialTheme.typography.labelMedium,
                                color = MaterialTheme.colorScheme.outline,
                                modifier = Modifier.padding(end = 8.dp),
                            )
                        } else {
                            StatusDot(ctr.visualStatus(), Modifier.padding(end = 8.dp), breathe = false)
                        }
                    }
                }
            }
        }
    }
}

/** Réparations des dernières 24 heures, tous conteneurs confondus. */
@Composable
private fun RepairsCard(repairs: Result<List<AutoHealRepair>>?, modifier: Modifier) {
    SectionCard(modifier, title = "Dernières 24 heures") {
        val list = repairs?.getOrNull()
        when {
            repairs == null -> Text("Chargement…", color = MaterialTheme.colorScheme.onSurfaceVariant)
            list == null -> Text(
                "Historique indisponible : ${repairs.exceptionOrNull()?.message}",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            list.isEmpty() -> Text(
                "Aucune réparation.",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            else -> list.forEach { RepairRow(it, showName = true) }
        }
    }
}

/** Une réparation : ce qui a été fait, quand, et si le conteneur est revenu. */
@Composable
internal fun RepairRow(r: AutoHealRepair, showName: Boolean) {
    val color = if (r.ok) Vigie.status.ok else Vigie.status.critical
    Row(Modifier.fillMaxWidth().padding(vertical = 6.dp), verticalAlignment = Alignment.CenterVertically) {
        Icon(if (r.ok) Icons.Rounded.CheckCircle else Icons.Rounded.ErrorOutline, if (r.ok) "Réussie" else "Sans effet", tint = color)
        Spacer(Modifier.width(12.dp))
        Column(Modifier.weight(1f)) {
            Text(if (showName) r.container else r.summary, style = MaterialTheme.typography.bodyLarge)
            Text(
                (if (showName) "${r.summary} · " else "") + Fmt.dateTime(r.ts),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            r.error?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = color) }
        }
    }
}
