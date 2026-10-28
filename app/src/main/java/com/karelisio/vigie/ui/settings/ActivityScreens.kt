package com.karelisio.vigie.ui.settings

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
import androidx.compose.material.icons.automirrored.rounded.ListAlt
import androidx.compose.material.icons.rounded.CheckCircle
import androidx.compose.material.icons.rounded.ErrorOutline
import androidx.compose.material.icons.rounded.NotificationsNone
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LoadingIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.navigation.NavHostController
import com.karelisio.vigie.BuildConfig
import com.karelisio.vigie.appContainer
import com.karelisio.vigie.data.Alert
import com.karelisio.vigie.data.AuditEntry
import com.karelisio.vigie.data.Fmt
import com.karelisio.vigie.data.ServerInfo
import com.karelisio.vigie.ui.components.EmptyState
import com.karelisio.vigie.ui.components.MiniRadar
import com.karelisio.vigie.ui.components.SectionCard
import com.karelisio.vigie.ui.components.cascade
import com.karelisio.vigie.ui.theme.Vigie

@Composable
private fun SubScreen(nav: NavHostController, title: String, content: @Composable (PaddingValues) -> Unit) {
    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(title) },
                navigationIcon = { IconButton(onClick = { nav.popBackStack() }) { Icon(Icons.AutoMirrored.Rounded.ArrowBack, "Retour") } },
            )
        },
        content = content,
    )
}

/** Charge une liste une fois à l'ouverture. */
@Composable
private fun <T> rememberLoad(block: suspend () -> T): Pair<T?, String?> {
    var value by remember { mutableStateOf<T?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    LaunchedEffect(Unit) { runCatching { block() }.onSuccess { value = it }.onFailure { error = it.message } }
    return value to error
}

@Composable
private fun Loading(error: String?) {
    Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        if (error != null) Text(error, color = MaterialTheme.colorScheme.error) else LoadingIndicator()
    }
}

@Composable
fun AlertsScreen(nav: NavHostController) {
    val (alerts, error) = rememberLoad { appContainer.repository.alerts() }
    SubScreen(nav, "Alertes récentes") { padding ->
        if (alerts == null) return@SubScreen Box(Modifier.padding(padding)) { Loading(error) }
        if (alerts.isEmpty()) {
            EmptyState(Icons.Rounded.NotificationsNone, "Aucune alerte", "Rien à signaler sur les 30 derniers jours.", Modifier.padding(padding))
            return@SubScreen
        }
        LazyColumn(contentPadding = PaddingValues(start = 16.dp, end = 16.dp, top = padding.calculateTopPadding() + 8.dp, bottom = 32.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            itemsIndexed(alerts, key = { _, a -> a.id }) { i, a -> AlertRow(a, Modifier.cascade(i)) }
        }
    }
}

@Composable
private fun AlertRow(a: Alert, modifier: Modifier) {
    val status = Vigie.status
    val color = if (a.resolved) status.ok else if (a.severity == "critical") status.critical else status.warn
    Surface(shape = RoundedCornerShape(20.dp), color = MaterialTheme.colorScheme.surfaceContainer, modifier = modifier.fillMaxWidth()) {
        Row(Modifier.padding(14.dp), verticalAlignment = Alignment.CenterVertically) {
            Icon(if (a.resolved) Icons.Rounded.CheckCircle else Icons.Rounded.ErrorOutline, null, tint = color)
            Spacer(Modifier.width(12.dp))
            Column(Modifier.weight(1f)) {
                Text(a.title, style = MaterialTheme.typography.titleSmall)
                if (a.body.isNotBlank()) Text(a.body, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                Text(Fmt.dateTime(a.ts), style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.outline)
            }
        }
    }
}

@Composable
fun AuditScreen(nav: NavHostController) {
    val (entries, error) = rememberLoad { appContainer.repository.audit() }
    SubScreen(nav, "Journal des actions") { padding ->
        if (entries == null) return@SubScreen Box(Modifier.padding(padding)) { Loading(error) }
        if (entries.isEmpty()) {
            EmptyState(Icons.AutoMirrored.Rounded.ListAlt, "Journal vide", "Les démarrages, arrêts et redémarrages apparaîtront ici.", Modifier.padding(padding))
            return@SubScreen
        }
        LazyColumn(contentPadding = PaddingValues(start = 16.dp, end = 16.dp, top = padding.calculateTopPadding() + 8.dp, bottom = 32.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            itemsIndexed(entries, key = { _, e -> e.id }) { i, e -> AuditRow(e, Modifier.cascade(i)) }
        }
    }
}

private fun actionLabel(a: String) = when (a) {
    "start" -> "Démarrage"
    "stop" -> "Arrêt"
    "restart" -> "Redémarrage"
    "check.create" -> "Check ajouté"
    "check.update" -> "Check modifié"
    "check.delete" -> "Check supprimé"
    else -> a
}

@Composable
private fun AuditRow(e: AuditEntry, modifier: Modifier) {
    val ok = e.result == "ok"
    Surface(shape = RoundedCornerShape(20.dp), color = MaterialTheme.colorScheme.surfaceContainer, modifier = modifier.fillMaxWidth()) {
        Column(Modifier.padding(14.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text("${actionLabel(e.action)} · ${e.target}", style = MaterialTheme.typography.titleSmall, modifier = Modifier.weight(1f))
                Text(if (ok) "OK" else "Échec", style = MaterialTheme.typography.labelMedium, color = if (ok) Vigie.status.ok else Vigie.status.critical)
            }
            if (!ok) Text(e.result, style = MaterialTheme.typography.bodySmall, color = Vigie.status.critical)
            Text(
                "${Fmt.dateTime(e.ts)} · ${e.ip}" + if (e.ua.isNotBlank()) " · ${e.ua.substringBefore(' ')}" else "",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.outline,
            )
        }
    }
}

@Composable
fun AboutScreen(nav: NavHostController) {
    val (info, error) = rememberLoad<ServerInfo> { appContainer.repository.info() }
    SubScreen(nav, "À propos") { padding ->
        Column(
            Modifier.fillMaxSize().padding(padding).verticalScroll(rememberScrollState()).padding(16.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            MiniRadar(Modifier.size(120.dp))
            Text("Vigie", style = MaterialTheme.typography.displaySmallEmphasized)
            Text("Version ${BuildConfig.VERSION_NAME} (${BuildConfig.VERSION_CODE})", color = MaterialTheme.colorScheme.onSurfaceVariant)
            SectionCard(title = "Serveur") {
                if (info == null) {
                    Text(error ?: "Chargement…", color = MaterialTheme.colorScheme.onSurfaceVariant)
                } else {
                    InfoLine("Nom", info.name)
                    InfoLine("Agent", info.version)
                    InfoLine("Hôte", info.host.hostname)
                    InfoLine("Système", info.host.os)
                    InfoLine("Noyau", info.host.kernel)
                    InfoLine("Processeur", "${info.host.cpuModel} · ${info.host.numCpu} cœurs · ${info.host.arch}")
                    info.docker?.let { InfoLine("Docker", "${it.version} (API ${it.apiVersion})") }
                    InfoLine("Agent démarré", Fmt.dateTime(info.agentStarted))
                    InfoLine("Seuils", "CPU ${info.thresholds.cpu.toInt()} % · RAM ${info.thresholds.ram.toInt()} % · disque ${info.thresholds.disk.toInt()} %")
                }
            }
            Text(
                "Code source : github.com/${BuildConfig.GITHUB_REPO}",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

@Composable
private fun InfoLine(label: String, value: String) {
    Row(Modifier.fillMaxWidth().padding(vertical = 3.dp)) {
        Text(label, Modifier.weight(0.35f), style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
        Text(value, Modifier.weight(0.65f), style = MaterialTheme.typography.bodyMedium)
    }
}
