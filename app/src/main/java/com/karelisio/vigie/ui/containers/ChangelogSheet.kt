package com.karelisio.vigie.ui.containers

import android.content.Intent
import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.EnterTransition
import androidx.compose.animation.ExitTransition
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.snap
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.scaleIn
import androidx.compose.animation.togetherWith
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.OpenInNew
import androidx.compose.material.icons.rounded.CloudOff
import androidx.compose.material.icons.rounded.ExpandMore
import androidx.compose.material.icons.rounded.NewReleases
import androidx.compose.material.icons.rounded.SystemUpdateAlt
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.Icon
import androidx.compose.material3.LoadingIndicator
import androidx.compose.material3.MaterialShapes
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.material3.toShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.core.net.toUri
import com.karelisio.vigie.appContainer
import com.karelisio.vigie.data.Container
import com.karelisio.vigie.data.Fmt
import com.karelisio.vigie.data.ImageChangelog
import com.karelisio.vigie.data.ImageRelease
import com.karelisio.vigie.ui.components.Chip
import com.karelisio.vigie.ui.components.EmptyState
import com.karelisio.vigie.ui.components.Markdown
import com.karelisio.vigie.ui.components.Springs
import com.karelisio.vigie.ui.components.rememberHaptics
import com.karelisio.vigie.ui.theme.LocalReducedMotion
import kotlinx.coroutines.launch

/** Bouton d'ouverture des nouveautés, mis en avant quand une nouvelle image est publiée. */
@Composable
internal fun ChangelogButton(highlight: Boolean, onClick: () -> Unit) {
    val content: @Composable () -> Unit = {
        Icon(Icons.Rounded.NewReleases, null)
        Spacer(Modifier.width(8.dp))
        Text("Nouveautés")
    }
    if (highlight) {
        FilledTonalButton(onClick = onClick, shapes = ButtonDefaults.shapes(), modifier = Modifier.fillMaxWidth()) { content() }
    } else {
        TextButton(onClick = onClick, shapes = ButtonDefaults.shapes()) { content() }
    }
}

/**
 * Notes de version de l'image publiées sur GitHub : celles postérieures à la version du conteneur
 * en tête, les autres repliées. [onUpdate] reprend le parcours de mise à jour de la carte.
 */
@Composable
internal fun ChangelogSheet(c: Container, canUpdate: Boolean, busy: Boolean, onUpdate: () -> Unit, onDismiss: () -> Unit) {
    val sheet = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    val scope = rememberCoroutineScope()
    val haptics = rememberHaptics()
    val reduced = LocalReducedMotion.current
    var attempt by remember { mutableIntStateOf(0) }
    val result by produceState<Result<ImageChangelog>?>(null, c.id, attempt) {
        value = null
        value = runCatching { appContainer.repository.changelog(c.id) }
    }
    val changelog = result?.getOrNull()

    ModalBottomSheet(onDismissRequest = onDismiss, sheetState = sheet) {
        Column(Modifier.fillMaxWidth().navigationBarsPadding().padding(bottom = 16.dp)) {
            Row(Modifier.padding(horizontal = 24.dp), verticalAlignment = Alignment.CenterVertically) {
                Box(contentAlignment = Alignment.Center) {
                    Surface(shape = MaterialShapes.Sunny.toShape(), color = MaterialTheme.colorScheme.primaryContainer, modifier = Modifier.size(52.dp)) {}
                    Icon(Icons.Rounded.NewReleases, null, tint = MaterialTheme.colorScheme.onPrimaryContainer)
                }
                Spacer(Modifier.width(16.dp))
                Column(Modifier.weight(1f)) {
                    Text("Nouveautés de ${c.name}", style = MaterialTheme.typography.titleLarge, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    Text(
                        listOfNotNull(
                            changelog?.source?.takeIf { it.isNotEmpty() },
                            changelog?.current?.takeIf { it.isNotEmpty() }?.let { "version actuelle $it" },
                        ).joinToString(" · ").ifEmpty { c.imageRef.ifEmpty { c.image } },
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 2,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
            }
            Spacer(Modifier.height(16.dp))
            AnimatedContent(
                targetState = result,
                contentKey = { r -> if (r == null) 0 else if (r.isSuccess) 1 else 2 },
                transitionSpec = {
                    if (reduced) EnterTransition.None togetherWith ExitTransition.None
                    else (fadeIn() + scaleIn(Springs.gentle(), initialScale = 0.96f)) togetherWith fadeOut()
                },
                modifier = Modifier.weight(1f, fill = false),
                label = "changelog",
            ) { r ->
                val cl = r?.getOrNull()
                when {
                    r == null -> Box(Modifier.fillMaxWidth().padding(48.dp), contentAlignment = Alignment.Center) { LoadingIndicator() }
                    cl == null -> EmptyState(Icons.Rounded.CloudOff, "Nouveautés indisponibles", r.exceptionOrNull()?.message ?: "Erreur inconnue") {
                        OutlinedButton(onClick = { attempt++ }) { Text("Réessayer") }
                    }
                    cl.releases.isEmpty() -> EmptyState(
                        Icons.Rounded.NewReleases,
                        "Nouveautés indisponibles",
                        (cl.error ?: "pas de release publiée sur GitHub").replaceFirstChar { it.uppercase() },
                    ) {
                        if (cl.source.isNotEmpty()) OutlinedButton(onClick = { attempt++ }) { Text("Réessayer") }
                    }
                    else -> Releases(cl.releases, c.updateAvailable)
                }
            }
            if (canUpdate) {
                Spacer(Modifier.height(12.dp))
                val label = if (c.self) "Mettre à jour l'agent" else "Mettre à jour"
                // Le panneau se referme, puis la carte demande confirmation (et l'empreinte si besoin).
                fun update() {
                    haptics.tick()
                    scope.launch { sheet.hide() }.invokeOnCompletion { onUpdate() }
                }
                val modifier = Modifier.fillMaxWidth().padding(horizontal = 24.dp).height(56.dp)
                val content: @Composable () -> Unit = {
                    Icon(Icons.Rounded.SystemUpdateAlt, null)
                    Spacer(Modifier.width(8.dp))
                    Text(label)
                }
                if (c.updateAvailable) {
                    Button(onClick = ::update, enabled = !busy, shapes = ButtonDefaults.shapes(), modifier = modifier) { content() }
                } else {
                    FilledTonalButton(onClick = ::update, enabled = !busy, shapes = ButtonDefaults.shapes(), modifier = modifier) { content() }
                }
            }
        }
    }
}

@Composable
private fun Releases(releases: List<ImageRelease>, updateAvailable: Boolean) {
    val (newer, older) = releases.partition { it.newer }
    // Rien de neuf : les versions précédentes sont dépliées d'emblée.
    var expanded by remember { mutableStateOf<Boolean?>(null) }
    val showOlder = expanded ?: newer.isEmpty()
    val reduced = LocalReducedMotion.current
    val haptics = rememberHaptics()
    val rotation by animateFloatAsState(if (showOlder) 180f else 0f, if (reduced) snap() else Springs.snappy(), label = "older")
    LazyColumn(contentPadding = PaddingValues(horizontal = 16.dp, vertical = 4.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        if (newer.isEmpty()) {
            item {
                Text(
                    if (updateAvailable) {
                        "Aucune nouvelle version sur GitHub : la nouvelle image est sans doute une reconstruction (correctifs du système de base)."
                    } else {
                        "Pas de version plus récente que la tienne sur GitHub."
                    },
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(horizontal = 8.dp),
                )
            }
        }
        items(newer, key = { "n-" + it.tag }) { ReleaseCard(it, highlight = true, if (reduced) Modifier else Modifier.animateItem()) }
        if (older.isNotEmpty()) {
            item(key = "older") {
                Surface(
                    onClick = { haptics.tick(); expanded = !showOlder },
                    shape = RoundedCornerShape(20.dp),
                    color = MaterialTheme.colorScheme.surfaceContainerHigh,
                    modifier = Modifier.fillMaxWidth().then(if (reduced) Modifier else Modifier.animateItem()),
                ) {
                    Row(Modifier.padding(horizontal = 16.dp, vertical = 12.dp), verticalAlignment = Alignment.CenterVertically) {
                        Text("Versions précédentes (${older.size})", style = MaterialTheme.typography.titleSmall, modifier = Modifier.weight(1f))
                        Icon(Icons.Rounded.ExpandMore, if (showOlder) "Replier" else "Déplier", Modifier.graphicsLayer { rotationZ = rotation })
                    }
                }
            }
            if (showOlder) {
                items(older, key = { "o-" + it.tag }) { ReleaseCard(it, highlight = false, if (reduced) Modifier else Modifier.animateItem()) }
            }
        }
    }
}

@Composable
private fun ReleaseCard(r: ImageRelease, highlight: Boolean, modifier: Modifier) {
    val context = LocalContext.current
    val scheme = MaterialTheme.colorScheme
    Surface(
        color = if (highlight) scheme.secondaryContainer else scheme.surfaceContainer,
        contentColor = if (highlight) scheme.onSecondaryContainer else scheme.onSurface,
        shape = RoundedCornerShape(24.dp),
        modifier = modifier.fillMaxWidth(),
    ) {
        Column(Modifier.padding(start = 16.dp, end = 16.dp, top = 16.dp, bottom = 4.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(r.tag, style = MaterialTheme.typography.titleMedium, fontFamily = FontFamily.Monospace, modifier = Modifier.weight(1f))
                if (highlight) Chip("Nouveau", scheme.tertiaryContainer, scheme.onTertiaryContainer, icon = Icons.Rounded.NewReleases)
            }
            Text(
                listOfNotNull(r.name.takeIf { it.isNotBlank() && it != r.tag }, r.publishedAt.takeIf { it > 0 }?.let { Fmt.day(it) }).joinToString(" · "),
                style = MaterialTheme.typography.bodySmall,
                color = if (highlight) scheme.onSecondaryContainer else scheme.onSurfaceVariant,
            )
            if (r.body.isNotBlank()) {
                Spacer(Modifier.height(10.dp))
                Markdown(r.body)
            }
            if (r.url.isNotEmpty()) {
                TextButton(
                    onClick = { runCatching { context.startActivity(Intent(Intent.ACTION_VIEW, r.url.toUri())) } },
                    modifier = Modifier.align(Alignment.End),
                ) {
                    Icon(Icons.AutoMirrored.Rounded.OpenInNew, null, Modifier.size(18.dp))
                    Spacer(Modifier.width(6.dp))
                    Text("Voir sur GitHub")
                }
            }
        }
    }
}
