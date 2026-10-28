package com.karelisio.vigie.ui.components

import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.SizeTransform
import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.scaleIn
import androidx.compose.animation.scaleOut
import androidx.compose.animation.togetherWith
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.ContainedLoadingIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialShapes
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.toShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.Stable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.PathMeasure
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.StrokeJoin
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.unit.dp
import com.karelisio.vigie.ui.theme.Vigie
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

enum class ActionPhase { Idle, Loading, Success, Error }

/** Pilote l'animation d'une action : progression, puis coche ou croix, puis retour au repos. */
@Stable
class ActionRunner(private val scope: CoroutineScope, private val haptics: Haptics) {
    var phase by mutableStateOf(ActionPhase.Idle)
        private set
    var error by mutableStateOf<String?>(null)
        private set
    val busy get() = phase == ActionPhase.Loading
    internal val shaker = ShakeController()

    fun run(block: suspend () -> Unit, onDone: (Result<Unit>) -> Unit = {}) {
        if (busy) return
        haptics.tick()
        phase = ActionPhase.Loading
        error = null
        scope.launch {
            val started = System.currentTimeMillis()
            val result = runCatching { block() }
            // Laisse le morphing de progression se voir, même si l'agent répond très vite.
            val elapsed = System.currentTimeMillis() - started
            if (elapsed < 700) delay(700 - elapsed)
            if (result.isSuccess) {
                phase = ActionPhase.Success
                haptics.confirm()
            } else {
                phase = ActionPhase.Error
                error = result.exceptionOrNull()?.message
                haptics.reject()
                launch { shaker.shake() }
            }
            onDone(result)
            delay(1600)
            phase = ActionPhase.Idle
        }
    }
}

@Composable
fun rememberActionRunner(): ActionRunner {
    val scope = rememberCoroutineScope()
    val haptics = rememberHaptics()
    return remember { ActionRunner(scope, haptics) }
}

enum class ActionTone { Primary, Tonal, Danger }

/**
 * Bouton d'action qui se transforme : pilule → indicateur de chargement à formes changeantes (M3 Expressive)
 * → forme « cookie » avec coche animée, ou croix en cas d'échec.
 */
@Composable
fun MorphActionButton(
    label: String,
    icon: ImageVector,
    runner: ActionRunner,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    tone: ActionTone = ActionTone.Tonal,
    enabled: Boolean = true,
) {
    val scheme = MaterialTheme.colorScheme
    val status = Vigie.status
    val (container, content) = when (tone) {
        ActionTone.Primary -> scheme.primary to scheme.onPrimary
        ActionTone.Tonal -> scheme.secondaryContainer to scheme.onSecondaryContainer
        ActionTone.Danger -> scheme.errorContainer to scheme.onErrorContainer
    }
    AnimatedContent(
        targetState = runner.phase,
        modifier = modifier.height(56.dp).shake(runner.shaker),
        contentAlignment = Alignment.Center,
        transitionSpec = {
            (fadeIn(tween(220, 90)) + scaleIn(Springs.bouncy(), initialScale = 0.4f)) togetherWith
                (fadeOut(tween(120)) + scaleOut(tween(160), targetScale = 0.6f)) using
                SizeTransform(clip = false) { _, _ -> Springs.bouncy() }
        },
        label = "morph",
    ) { phase ->
        when (phase) {
            ActionPhase.Idle -> Surface(
                onClick = onClick,
                enabled = enabled,
                shape = CircleShape,
                color = if (enabled) container else scheme.onSurface.copy(alpha = 0.10f),
                contentColor = if (enabled) content else scheme.onSurface.copy(alpha = 0.38f),
                modifier = Modifier.height(56.dp),
            ) {
                Row(
                    Modifier.padding(horizontal = 22.dp),
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.Center,
                ) {
                    Icon(icon, null, Modifier.size(22.dp))
                    Spacer(Modifier.width(8.dp))
                    Text(label, style = MaterialTheme.typography.labelLarge)
                }
            }
            ActionPhase.Loading -> ContainedLoadingIndicator(
                modifier = Modifier.size(56.dp),
                containerColor = container,
                indicatorColor = content,
            )
            ActionPhase.Success -> ResultBadge(ok = true, color = status.ok)
            ActionPhase.Error -> ResultBadge(ok = false, color = status.critical)
        }
    }
}

@Composable
private fun ResultBadge(ok: Boolean, color: Color) {
    val spin = remember { Animatable(-90f) }
    LaunchedEffect(Unit) { spin.animateTo(0f, Springs.bouncy()) }
    val shape = if (ok) MaterialShapes.Cookie9Sided.toShape() else MaterialShapes.Cookie4Sided.toShape()
    Box(
        Modifier
            .size(56.dp)
            .graphicsLayer { rotationZ = spin.value }
            .clip(shape)
            .background(color),
        contentAlignment = Alignment.Center,
    ) {
        AnimatedMark(ok = ok, color = Color.White, modifier = Modifier.size(26.dp).graphicsLayer { rotationZ = -spin.value })
    }
}

/** Coche ou croix tracée progressivement. */
@Composable
fun AnimatedMark(ok: Boolean, color: Color, modifier: Modifier = Modifier, delayMs: Int = 120) {
    val p = remember { Animatable(0f) }
    LaunchedEffect(ok) {
        p.snapTo(0f)
        p.animateTo(1f, tween(380, delayMs, FastOutSlowInEasing))
    }
    Canvas(modifier) {
        val w = size.width
        val h = size.height
        val stroke = Stroke(w * 0.13f, cap = StrokeCap.Round, join = StrokeJoin.Round)
        if (ok) {
            val path = Path().apply {
                moveTo(w * 0.14f, h * 0.54f)
                lineTo(w * 0.40f, h * 0.78f)
                lineTo(w * 0.88f, h * 0.24f)
            }
            drawPath(trim(path, p.value), color, style = stroke)
        } else {
            val a = (p.value * 2).coerceAtMost(1f)
            val b = (p.value * 2 - 1).coerceIn(0f, 1f)
            drawLine(color, Offset(w * 0.2f, h * 0.2f), Offset(w * (0.2f + 0.6f * a), h * (0.2f + 0.6f * a)), stroke.width, StrokeCap.Round)
            if (b > 0) drawLine(color, Offset(w * 0.8f, h * 0.2f), Offset(w * (0.8f - 0.6f * b), h * (0.2f + 0.6f * b)), stroke.width, StrokeCap.Round)
        }
    }
}

private fun trim(path: Path, fraction: Float): Path {
    val pm = PathMeasure()
    pm.setPath(path, false)
    val out = Path()
    pm.getSegment(0f, pm.length * fraction, out, true)
    return out
}
