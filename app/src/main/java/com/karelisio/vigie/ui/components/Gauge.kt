package com.karelisio.vigie.ui.components

import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.Spring
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.spring
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInVertically
import androidx.compose.animation.slideOutVertically
import androidx.compose.animation.togetherWith
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.rotate
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.karelisio.vigie.ui.theme.Vigie
import com.karelisio.vigie.ui.theme.numeric
import kotlin.math.roundToInt

/**
 * Compteur dont chaque chiffre défile verticalement (vers le haut quand la valeur monte).
 */
@Composable
fun AnimatedCounter(value: Int, style: TextStyle, modifier: Modifier = Modifier, color: Color = Color.Unspecified, suffix: String = "") {
    val text = value.toString()
    val reduced = Vigie.reducedMotion
    Row(modifier) {
        text.forEachIndexed { i, ch ->
            // Clé = position depuis la droite : les unités restent alignées quand le nombre de chiffres change.
            val pos = text.length - i
            androidx.compose.runtime.key(pos) {
                AnimatedContent(
                    targetState = ch,
                    transitionSpec = {
                        if (reduced) fadeIn() togetherWith fadeOut()
                        else {
                            val up = targetState > initialState
                            (slideInVertically(spring(0.8f, Spring.StiffnessMediumLow)) { h -> if (up) h else -h } + fadeIn()) togetherWith
                                (slideOutVertically(spring(0.8f, Spring.StiffnessMediumLow)) { h -> if (up) -h else h } + fadeOut())
                        }
                    },
                    label = "digit",
                ) { c -> Text(c.toString(), style = style.numeric(), color = color) }
            }
        }
        if (suffix.isNotEmpty()) Text(suffix, style = style.numeric(), color = color)
    }
}

/**
 * Jauge circulaire (270°) : remplissage en ressort, couleur qui glisse du vert au rouge selon le seuil,
 * valeur centrale animée.
 */
@Composable
fun CircularGauge(
    value: Double,
    label: String,
    threshold: Double,
    modifier: Modifier = Modifier,
    caption: String? = null,
) {
    val status = Vigie.status
    val target = (value / 100.0).coerceIn(0.0, 1.0).toFloat()
    val progress by animateFloatAsState(target, spring(dampingRatio = 0.55f, stiffness = Spring.StiffnessVeryLow), label = "gauge")
    val color by animateColorAsState(status.forLevel(value, threshold), spring(stiffness = Spring.StiffnessLow), label = "gaugeColor")
    val track = MaterialTheme.colorScheme.surfaceContainerHighest
    val thresholdMark = MaterialTheme.colorScheme.outline

    Box(
        modifier
            .aspectRatio(1f)
            .semantics { contentDescription = "$label ${value.roundToInt()} %" },
        contentAlignment = Alignment.Center,
    ) {
        Canvas(Modifier.fillMaxSize()) {
            val stroke = size.minDimension * 0.095f
            val inset = stroke / 2 + 2.dp.toPx()
            val arcSize = Size(size.width - inset * 2, size.height - inset * 2)
            val topLeft = Offset(inset, inset)
            drawArc(track, 135f, 270f, false, topLeft, arcSize, style = Stroke(stroke, cap = StrokeCap.Round))
            // Repère du seuil d'alerte.
            val t = (threshold / 100).toFloat().coerceIn(0f, 1f)
            rotate(135f + 270f * t) {
                drawLine(
                    thresholdMark.copy(alpha = 0.7f),
                    Offset(center.x + arcSize.width / 2 - stroke * 0.65f, center.y),
                    Offset(center.x + arcSize.width / 2 + stroke * 0.65f, center.y),
                    strokeWidth = 2.dp.toPx(), cap = StrokeCap.Round,
                )
            }
            if (progress > 0.002f) {
                val sweep = 270f * progress
                // Halo diffus sous l'arc.
                drawArc(color.copy(alpha = 0.16f), 135f, sweep, false, topLeft, arcSize, style = Stroke(stroke * 1.9f, cap = StrokeCap.Round))
                drawArc(
                    Brush.sweepGradient(
                        0f to color.copy(alpha = 0.75f), 0.75f to color, 1f to color.copy(alpha = 0.75f),
                        center = center,
                    ),
                    135f, sweep, false, topLeft, arcSize, style = Stroke(stroke, cap = StrokeCap.Round),
                )
            }
        }
        Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.Center) {
            Row(verticalAlignment = Alignment.Bottom) {
                AnimatedCounter(
                    value = value.roundToInt(),
                    style = MaterialTheme.typography.headlineMedium.copy(fontWeight = FontWeight.Bold),
                    color = MaterialTheme.colorScheme.onSurface,
                )
                Text(
                    "%",
                    style = MaterialTheme.typography.titleSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            Text(label, style = MaterialTheme.typography.labelLarge, color = color, textAlign = TextAlign.Center)
            if (caption != null) {
                Text(
                    caption,
                    style = MaterialTheme.typography.labelSmall.numeric(),
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    textAlign = TextAlign.Center,
                    maxLines = 1,
                )
            }
        }
    }
}
