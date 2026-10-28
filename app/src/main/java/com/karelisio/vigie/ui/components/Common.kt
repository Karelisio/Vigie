package com.karelisio.vigie.ui.components

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.core.Animatable
import androidx.compose.animation.expandVertically
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.shrinkVertically
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.CloudOff
import androidx.compose.material.icons.rounded.Key
import androidx.compose.material.icons.rounded.Sync
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialShapes
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.toShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.composed
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.karelisio.vigie.data.Connection
import com.karelisio.vigie.data.Fmt
import com.karelisio.vigie.data.LiveState
import com.karelisio.vigie.ui.theme.LocalReducedMotion
import kotlinx.coroutines.delay

/** Carte de section aux coins généreux. */
@Composable
fun SectionCard(
    modifier: Modifier = Modifier,
    title: String? = null,
    action: (@Composable () -> Unit)? = null,
    content: @Composable ColumnScope.() -> Unit,
) {
    Card(
        modifier = modifier.fillMaxWidth(),
        shape = RoundedCornerShape(28.dp),
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceContainer),
    ) {
        Column(Modifier.padding(20.dp)) {
            if (title != null || action != null) {
                Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                    if (title != null) Text(title, style = MaterialTheme.typography.titleMedium, modifier = Modifier.weight(1f))
                    action?.invoke()
                }
                Spacer(Modifier.size(12.dp))
            }
            content()
        }
    }
}

/** Apparition en cascade : chaque élément arrive avec un léger décalage selon son index. */
fun Modifier.cascade(index: Int, key: Any? = Unit): Modifier = composed {
    val reduced = LocalReducedMotion.current
    val p = remember(key) { Animatable(if (reduced) 1f else 0f) }
    LaunchedEffect(key) {
        if (!reduced && p.value < 1f) {
            delay(minOf(index, 12) * 45L)
            p.animateTo(1f, Springs.gentle())
        }
    }
    graphicsLayer {
        alpha = p.value
        translationY = (1f - p.value) * 36.dp.toPx()
        val s = 0.94f + 0.06f * p.value
        scaleX = s
        scaleY = s
    }
}

/** Libellé « mis à jour il y a X » qui se rafraîchit tout seul. */
@Composable
fun rememberNow(periodMs: Long = 10_000): Long {
    var now by remember { mutableLongStateOf(System.currentTimeMillis()) }
    LaunchedEffect(periodMs) {
        while (true) {
            delay(periodMs)
            now = System.currentTimeMillis()
        }
    }
    return now
}

/** Bandeau clair quand les données ne sont plus en direct. */
@Composable
fun ConnectionBanner(state: LiveState, modifier: Modifier = Modifier) {
    val now = rememberNow()
    val visible = state.connection == Connection.Offline || state.connection == Connection.Unauthorized ||
        (state.connection != Connection.Live && state.updatedAt > 0 && now - state.updatedAt > 15_000)
    AnimatedVisibility(visible, modifier, enter = expandVertically() + fadeIn(), exit = shrinkVertically() + fadeOut()) {
        val unauthorized = state.connection == Connection.Unauthorized
        val scheme = MaterialTheme.colorScheme
        Surface(
            color = if (unauthorized) scheme.errorContainer else scheme.secondaryContainer,
            contentColor = if (unauthorized) scheme.onErrorContainer else scheme.onSecondaryContainer,
            shape = RoundedCornerShape(20.dp),
            modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 4.dp),
        ) {
            Row(Modifier.padding(horizontal = 16.dp, vertical = 12.dp), verticalAlignment = Alignment.CenterVertically) {
                Icon(
                    when {
                        unauthorized -> Icons.Rounded.Key
                        state.connection == Connection.Connecting -> Icons.Rounded.Sync
                        else -> Icons.Rounded.CloudOff
                    },
                    null,
                )
                Spacer(Modifier.width(12.dp))
                Column(Modifier.weight(1f)) {
                    Text(
                        when {
                            unauthorized -> "Token refusé"
                            state.connection == Connection.Connecting -> "Reconnexion…"
                            else -> "Hors ligne"
                        },
                        style = MaterialTheme.typography.titleSmall,
                    )
                    Text(
                        if (state.updatedAt > 0) "Dernière mise à jour ${Fmt.ago(state.updatedAt, now)}"
                        else state.error ?: "Aucune donnée encore reçue",
                        style = MaterialTheme.typography.bodySmall,
                    )
                }
            }
        }
    }
}

/** État vide illustré d'une forme expressive. */
@Composable
fun EmptyState(icon: ImageVector, title: String, message: String, modifier: Modifier = Modifier, action: (@Composable () -> Unit)? = null) {
    Column(
        modifier.fillMaxWidth().padding(32.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Box(contentAlignment = Alignment.Center) {
            Surface(
                shape = MaterialShapes.Cookie9Sided.toShape(),
                color = MaterialTheme.colorScheme.primaryContainer,
                modifier = Modifier.size(96.dp),
            ) {}
            Icon(icon, null, Modifier.size(40.dp), tint = MaterialTheme.colorScheme.onPrimaryContainer)
        }
        Text(title, style = MaterialTheme.typography.titleLarge, textAlign = TextAlign.Center)
        Text(message, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant, textAlign = TextAlign.Center)
        action?.invoke()
    }
}

/** Pastille de texte (étiquette). */
@Composable
fun Chip(text: String, color: Color, contentColor: Color, modifier: Modifier = Modifier, icon: ImageVector? = null) {
    Surface(color = color, contentColor = contentColor, shape = RoundedCornerShape(50), modifier = modifier) {
        Row(Modifier.padding(horizontal = 10.dp, vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
            if (icon != null) {
                Icon(icon, null, Modifier.size(14.dp))
                Spacer(Modifier.width(4.dp))
            }
            Text(text, style = MaterialTheme.typography.labelMedium, maxLines = 1)
        }
    }
}

