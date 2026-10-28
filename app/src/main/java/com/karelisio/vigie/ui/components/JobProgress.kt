package com.karelisio.vigie.ui.components

import androidx.compose.animation.animateContentSize
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.CheckCircle
import androidx.compose.material.icons.rounded.ErrorOutline
import androidx.compose.material3.ExperimentalMaterial3ExpressiveApi
import androidx.compose.material3.Icon
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.LinearWavyProgressIndicator
import androidx.compose.material3.LoadingIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.karelisio.vigie.data.AgentJob
import com.karelisio.vigie.ui.theme.LocalReducedMotion
import com.karelisio.vigie.ui.theme.Vigie

/** Avancement d'une opération de l'agent : indicateur ondulé, message de phase, bilan par conteneur. */
@OptIn(ExperimentalMaterial3ExpressiveApi::class)
@Composable
fun JobProgress(job: AgentJob, modifier: Modifier = Modifier, showResults: Boolean = true) {
    val status = Vigie.status
    val reduced = LocalReducedMotion.current
    val progress by animateFloatAsState(job.progress.toFloat().coerceIn(0f, 1f), Springs.gentle(), label = "job")
    Column(modifier.animateContentSize()) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            when {
                !job.done -> LoadingIndicator(Modifier.size(28.dp))
                job.ok -> Icon(Icons.Rounded.CheckCircle, null, tint = status.ok, modifier = Modifier.size(24.dp))
                else -> Icon(Icons.Rounded.ErrorOutline, null, tint = status.critical, modifier = Modifier.size(24.dp))
            }
            Spacer(Modifier.width(10.dp))
            Text(
                job.message.ifEmpty { "En cours…" },
                style = MaterialTheme.typography.bodyMedium,
                color = if (job.done && !job.ok) status.critical else MaterialTheme.colorScheme.onSurface,
                maxLines = 3,
                overflow = TextOverflow.Ellipsis,
            )
        }
        if (!job.done) {
            Spacer(Modifier.height(10.dp))
            when {
                reduced -> LinearProgressIndicator(progress = { progress }, modifier = Modifier.fillMaxWidth())
                job.phase == "queued" -> LinearWavyProgressIndicator(Modifier.fillMaxWidth())
                else -> LinearWavyProgressIndicator(progress = { progress }, modifier = Modifier.fillMaxWidth())
            }
        }
        if (job.done && showResults && job.results.size > 1) {
            Spacer(Modifier.height(8.dp))
            job.results.forEach { r ->
                Row(Modifier.padding(vertical = 2.dp), verticalAlignment = Alignment.CenterVertically) {
                    StatusDot(if (!r.ok) "critical" else if (r.updated) "ok" else "neutral", size = 8.dp, breathe = false)
                    Spacer(Modifier.width(8.dp))
                    Text(
                        if (r.message.startsWith(r.name)) r.message else "${r.name} : ${r.message}",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 2,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
            }
        }
    }
}

/** Bandeau flottant affiché sur les écrans principaux pendant une opération. */
@Composable
fun JobBanner(job: AgentJob, modifier: Modifier = Modifier) {
    Surface(
        color = MaterialTheme.colorScheme.surfaceContainerHighest,
        shape = RoundedCornerShape(24.dp),
        shadowElevation = 6.dp,
        modifier = modifier.fillMaxWidth(),
    ) {
        JobProgress(job, Modifier.padding(horizontal = 16.dp, vertical = 12.dp), showResults = false)
    }
}
