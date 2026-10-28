package com.karelisio.vigie.ui.settings

import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.animateColorAsState
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
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialShapes
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.toShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.navigation.NavHostController
import com.karelisio.vigie.ui.components.AnimatedMark
import com.karelisio.vigie.ui.components.Springs
import com.karelisio.vigie.ui.components.cascade
import com.karelisio.vigie.ui.components.rememberHaptics
import com.karelisio.vigie.ui.theme.Vigie

/** Icône d'autorisation dans une forme expressive ; devient une coche animée une fois accordée. */
@Composable
fun PermBadge(perm: Perm, granted: Boolean, size: Dp = 56.dp) {
    val status = Vigie.status
    val scheme = MaterialTheme.colorScheme
    val color by animateColorAsState(if (granted) status.ok else scheme.primaryContainer, label = "permColor")
    val rotation by animateFloatAsState(if (granted) 0f else -30f, Springs.bouncy(), label = "permRot")
    val haptics = rememberHaptics()
    val was = remember { booleanArrayOf(granted) }
    LaunchedEffect(granted) {
        if (granted && !was[0]) haptics.confirm()
        was[0] = granted
    }
    Box(contentAlignment = Alignment.Center) {
        Surface(
            shape = (if (granted) MaterialShapes.Cookie9Sided else MaterialShapes.Cookie6Sided).toShape(),
            color = color,
            modifier = Modifier.size(size).graphicsLayer { rotationZ = rotation },
        ) {}
        AnimatedContent(
            granted,
            transitionSpec = { (scaleIn(Springs.bouncy()) + fadeIn()) togetherWith fadeOut() },
            label = "permIcon",
        ) { g ->
            if (g) AnimatedMark(ok = true, color = Color.White, modifier = Modifier.size(size * 0.45f))
            else Icon(perm.icon, null, Modifier.size(size * 0.45f), tint = scheme.onPrimaryContainer)
        }
    }
}

@Composable
fun PermissionsScreen(nav: NavHostController) {
    val perms = rememberPermissionState()
    val haptics = rememberHaptics()
    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Autorisations") },
                navigationIcon = { IconButton(onClick = { nav.popBackStack() }) { Icon(Icons.AutoMirrored.Rounded.ArrowBack, "Retour") } },
            )
        },
    ) { padding ->
        Column(
            Modifier.fillMaxSize().padding(padding).verticalScroll(rememberScrollState()).padding(16.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            Text(
                "Vigie ne demande que ce dont elle a besoin. Tu peux changer ces réglages à tout moment.",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Perm.entries.forEachIndexed { i, p ->
                val granted = perms.granted[p] == true
                Surface(shape = RoundedCornerShape(28.dp), color = MaterialTheme.colorScheme.surfaceContainer, modifier = Modifier.fillMaxWidth().cascade(i)) {
                    Row(Modifier.padding(16.dp), verticalAlignment = Alignment.CenterVertically) {
                        PermBadge(p, granted)
                        Spacer(Modifier.width(16.dp))
                        Column(Modifier.weight(1f)) {
                            Text(p.title, style = MaterialTheme.typography.titleMedium)
                            Text(p.why, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                            if (!granted) {
                                Spacer(Modifier.size(8.dp))
                                FilledTonalButton(onClick = { haptics.tick(); perms.request(p) }) { Text("Autoriser") }
                            } else {
                                Text("Accordée", style = MaterialTheme.typography.labelLarge, color = Vigie.status.ok)
                            }
                        }
                    }
                }
            }
        }
    }
}
