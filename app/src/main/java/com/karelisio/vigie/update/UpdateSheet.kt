package com.karelisio.vigie.update

import android.content.Intent
import android.net.Uri
import android.provider.Settings
import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.scaleIn
import androidx.compose.animation.togetherWith
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.SystemUpdate
import androidx.compose.material3.Button
import androidx.compose.material3.CircularWavyProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.LoadingIndicator
import androidx.compose.material3.MaterialShapes
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.material3.toShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import com.karelisio.vigie.data.Fmt
import com.karelisio.vigie.ui.components.AnimatedCounter
import com.karelisio.vigie.ui.components.Springs
import kotlinx.coroutines.launch

/** Bottom sheet de mise à jour : changelog, téléchargement avec progression animée, installation. */
@Composable
fun UpdateSheet(state: UpdateState, updater: Updater) {
    if (state is UpdateState.Idle) return
    val scope = rememberCoroutineScope()
    val context = LocalContext.current
    val sheet = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    val busy = state is UpdateState.Downloading || state is UpdateState.Installing

    ModalBottomSheet(
        onDismissRequest = { if (!busy) updater.dismiss() },
        sheetState = sheet,
    ) {
        Column(
            Modifier
                .fillMaxWidth()
                .padding(horizontal = 24.dp)
                .navigationBarsPadding()
                .padding(bottom = 16.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            val release = when (state) {
                is UpdateState.Available -> state.release
                is UpdateState.Downloading -> state.release
                is UpdateState.Installing -> state.release
                is UpdateState.Failed -> state.release
                UpdateState.Idle -> null
            }
            AnimatedContent(
                targetState = state::class,
                transitionSpec = { (fadeIn() + scaleIn(Springs.bouncy(), initialScale = 0.8f)) togetherWith fadeOut() },
                contentAlignment = Alignment.Center,
                label = "updateHeader",
            ) { kind ->
                Box(Modifier.size(120.dp), contentAlignment = Alignment.Center) {
                    when (kind) {
                        UpdateState.Downloading::class -> {
                            val p = (state as? UpdateState.Downloading)?.progress ?: 1f
                            val animated by animateFloatAsState(p, Springs.gentle(), label = "dl")
                            CircularWavyProgressIndicator(progress = { animated }, modifier = Modifier.size(112.dp))
                            AnimatedCounter((animated * 100).toInt(), MaterialTheme.typography.titleLarge, suffix = " %")
                        }
                        UpdateState.Installing::class -> LoadingIndicator(Modifier.size(96.dp))
                        else -> {
                            Surface(
                                shape = MaterialShapes.SoftBurst.toShape(),
                                color = MaterialTheme.colorScheme.primaryContainer,
                                modifier = Modifier.size(104.dp),
                            ) {}
                            Icon(Icons.Rounded.SystemUpdate, null, Modifier.size(44.dp), tint = MaterialTheme.colorScheme.onPrimaryContainer)
                        }
                    }
                }
            }
            Spacer(Modifier.size(12.dp))
            Text(
                when (state) {
                    is UpdateState.Downloading -> "Téléchargement…"
                    is UpdateState.Installing -> "Installation…"
                    is UpdateState.Failed -> "La mise à jour a échoué"
                    else -> "Vigie ${release?.version} est disponible"
                },
                style = MaterialTheme.typography.headlineSmall,
            )
            Text(
                when (state) {
                    is UpdateState.Downloading -> "${Fmt.bytes(state.bytes)} sur ${Fmt.bytes(release?.apkSize ?: 0)}"
                    is UpdateState.Failed -> state.message
                    else -> "Version actuelle : ${updater.currentVersion}"
                },
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            if (release != null && release.changelog.isNotBlank() && state !is UpdateState.Installing) {
                Spacer(Modifier.size(16.dp))
                Surface(
                    color = MaterialTheme.colorScheme.surfaceContainerHigh,
                    shape = MaterialTheme.shapes.large,
                    modifier = Modifier.fillMaxWidth().heightIn(max = 280.dp),
                ) {
                    Text(
                        markdown(release.changelog),
                        style = MaterialTheme.typography.bodyMedium,
                        modifier = Modifier.verticalScroll(rememberScrollState()).padding(16.dp),
                    )
                }
            }
            Spacer(Modifier.size(20.dp))
            if (!busy && release != null) {
                if (!updater.canInstall()) {
                    Text(
                        "Autorise d'abord Vigie à installer des applications.",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                    Spacer(Modifier.size(8.dp))
                }
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp, Alignment.End)) {
                    TextButton(onClick = { scope.launch { updater.skip(release) } }) { Text("Ignorer") }
                    TextButton(onClick = { updater.dismiss() }) { Text("Plus tard") }
                    Spacer(Modifier.width(4.dp))
                    Button(
                        onClick = {
                            if (!updater.canInstall()) {
                                context.startActivity(
                                    Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:${context.packageName}")),
                                )
                            } else {
                                scope.launch { updater.downloadAndInstall(release) }
                            }
                        },
                    ) { Text(if (state is UpdateState.Failed) "Réessayer" else if (updater.canInstall()) "Mettre à jour" else "Autoriser") }
                }
            }
        }
    }
}

/** Rendu minimal du changelog Markdown (titres, puces, gras). */
fun markdown(src: String): AnnotatedString = buildAnnotatedString {
    src.lines().forEachIndexed { i, raw ->
        if (i > 0) append('\n')
        val line = raw.trimEnd()
        when {
            line.startsWith("#") -> withStyle(SpanStyle(fontWeight = FontWeight.Bold)) { append(line.trimStart('#', ' ')) }
            line.trimStart().startsWith("- ") || line.trimStart().startsWith("* ") -> {
                append("  •  ")
                appendInline(line.trimStart().drop(2))
            }
            else -> appendInline(line)
        }
    }
}

private fun AnnotatedString.Builder.appendInline(text: String) {
    val parts = text.split("**")
    parts.forEachIndexed { i, p ->
        val clean = p.replace(Regex("\\[([^]]+)]\\([^)]+\\)"), "$1").replace("`", "")
        if (i % 2 == 1) withStyle(SpanStyle(fontWeight = FontWeight.SemiBold)) { append(clean) } else append(clean)
    }
}
