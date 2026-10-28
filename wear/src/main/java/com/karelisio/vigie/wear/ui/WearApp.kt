package com.karelisio.vigie.wear.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.background
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Refresh
import androidx.compose.material.icons.rounded.Warning
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.wear.compose.foundation.lazy.TransformingLazyColumn
import androidx.wear.compose.foundation.lazy.TransformingLazyColumnItemScope
import androidx.wear.compose.foundation.lazy.rememberTransformingLazyColumnState
import androidx.wear.compose.material3.AppScaffold
import androidx.wear.compose.material3.Card
import androidx.wear.compose.material3.CardDefaults
import androidx.wear.compose.material3.CircularProgressIndicator
import androidx.wear.compose.material3.ColorScheme
import androidx.wear.compose.material3.EdgeButton
import androidx.wear.compose.material3.EdgeButtonSize
import androidx.wear.compose.material3.Icon
import androidx.wear.compose.material3.ListHeader
import androidx.wear.compose.material3.MaterialTheme
import androidx.wear.compose.material3.ScreenScaffold
import androidx.wear.compose.material3.SurfaceTransformation
import androidx.wear.compose.material3.Text
import androidx.wear.compose.material3.dynamicColorScheme
import androidx.wear.compose.material3.lazy.TransformationSpec
import androidx.wear.compose.material3.lazy.rememberTransformationSpec
import androidx.wear.compose.material3.lazy.transformedHeight
import com.karelisio.vigie.wear.StatusLogic
import com.karelisio.vigie.wear.StatusView
import com.karelisio.vigie.wear.StatusViewModel

/** Teinte sarcelle de Vigie, éclaircie pour le fond noir (si la montre n'impose pas ses couleurs). */
private val VigieColors = ColorScheme(
    primary = Color(0xFF76D6D4),
    onPrimary = Color(0xFF003737),
    primaryContainer = Color(0xFF004F50),
    onPrimaryContainer = Color(0xFF93F2F0),
)

@Composable
fun WearApp(vm: StatusViewModel = viewModel()) {
    val data by vm.state.collectAsStateWithLifecycle()
    val refreshing by vm.refreshing.collectAsStateWithLifecycle()
    val context = LocalContext.current
    val colors = remember { dynamicColorScheme(context) ?: VigieColors }
    // À chaque ouverture : données de moins d'une minute.
    LifecycleResumeEffect(Unit) {
        vm.refresh(maxAgeMs = 60_000)
        onPauseOrDispose { }
    }
    MaterialTheme(colorScheme = colors) {
        AppScaffold {
            val d = data
            if (d == null) {
                Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.Center, horizontalAlignment = Alignment.CenterHorizontally) {
                    CircularProgressIndicator(Modifier.size(32.dp))
                }
            } else {
                StatusScreen(StatusLogic.view(d.first, d.second), refreshing) { vm.refresh() }
            }
        }
    }
}

@Composable
private fun StatusScreen(view: StatusView, refreshing: Boolean, onRefresh: () -> Unit) {
    val listState = rememberTransformingLazyColumnState()
    val spec = rememberTransformationSpec()
    ScreenScaffold(
        scrollState = listState,
        edgeButton = {
            EdgeButton(onClick = onRefresh, buttonSize = EdgeButtonSize.Small, enabled = !refreshing) {
                if (refreshing) {
                    CircularProgressIndicator(Modifier.size(24.dp))
                } else {
                    Icon(Icons.Rounded.Refresh, contentDescription = "Actualiser")
                }
            }
        },
    ) { padding ->
        TransformingLazyColumn(state = listState, contentPadding = padding) {
            item {
                ListHeader(Modifier.fillMaxWidth().transformedHeight(this, spec), transformation = SurfaceTransformation(spec)) {
                    Text(view.serverName, maxLines = 1, overflow = TextOverflow.Ellipsis)
                }
            }
            if (!view.configured) {
                item { InfoCard(spec, "Pas encore configurée", SETUP_TEXT) }
                return@TransformingLazyColumn
            }
            item { StatusCard(spec, view) }
            item { MetricsCard(spec, view) }
            view.errorText?.let { error -> item { InfoCard(spec, error, "Dernières données : ${StatusLogic.time(view.updatedAt)}", warning = true) } }
            items(view.problems.size) { i -> ProblemCard(spec, view.problems[i]) }
            item {
                Text(
                    "Mis à jour à ${StatusLogic.time(view.updatedAt)}",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    textAlign = TextAlign.Center,
                    modifier = Modifier.fillMaxWidth().transformedHeight(this, spec),
                )
            }
        }
    }
}

@Composable
private fun TransformingLazyColumnItemScope.StatusCard(spec: TransformationSpec, view: StatusView) {
    val color = Color(StatusLogic.color(view.level))
    Card(
        modifier = Modifier.fillMaxWidth().transformedHeight(this, spec).semantics { contentDescription = "État : ${view.label}" },
        colors = CardDefaults.cardColors(containerColor = color.copy(alpha = 0.22f)),
        transformation = SurfaceTransformation(spec),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Spacer(Modifier.size(12.dp).background(color, CircleShape))
            Spacer(Modifier.width(10.dp))
            Column {
                Text(view.label, style = MaterialTheme.typography.titleMedium, color = color)
                Text(StatusLogic.problemsText(view.problemCount), style = MaterialTheme.typography.bodySmall)
            }
        }
    }
}

@Composable
private fun TransformingLazyColumnItemScope.MetricsCard(spec: TransformationSpec, view: StatusView) {
    Card(modifier = Modifier.fillMaxWidth().transformedHeight(this, spec), transformation = SurfaceTransformation(spec)) {
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceEvenly) {
            Metric("CPU", view.cpu)
            Metric("RAM", view.ram)
            Metric("Disque", view.disk)
        }
    }
}

@Composable
private fun Metric(label: String, value: Int?) {
    Column(horizontalAlignment = Alignment.CenterHorizontally) {
        Text(StatusLogic.percent(value), style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
        Text(label, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

@Composable
private fun TransformingLazyColumnItemScope.ProblemCard(spec: TransformationSpec, text: String) {
    Card(modifier = Modifier.fillMaxWidth().transformedHeight(this, spec), transformation = SurfaceTransformation(spec)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Icon(Icons.Rounded.Warning, contentDescription = null, tint = Color(StatusLogic.RED), modifier = Modifier.size(18.dp))
            Spacer(Modifier.width(8.dp))
            Text(text, style = MaterialTheme.typography.bodySmall, maxLines = 3, overflow = TextOverflow.Ellipsis)
        }
    }
}

@Composable
private fun TransformingLazyColumnItemScope.InfoCard(spec: TransformationSpec, title: String, body: String, warning: Boolean = false) {
    Card(modifier = Modifier.fillMaxWidth().transformedHeight(this, spec), transformation = SurfaceTransformation(spec)) {
        Text(title, style = MaterialTheme.typography.titleSmall, color = if (warning) Color(StatusLogic.AMBER) else Color.Unspecified)
        Text(body, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

private const val SETUP_TEXT =
    "Sur ton téléphone, ouvre Vigie → Réglages → Montre, puis touche « Envoyer à la montre »."
