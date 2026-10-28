package com.karelisio.vigie.security

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.scaleOut
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Fingerprint
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.ExperimentalMaterial3ExpressiveApi
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialShapes
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.toShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.launch

/** Écran de verrouillage : recouvre l'appli tant que l'utilisateur n'est pas authentifié. */
@OptIn(ExperimentalMaterial3ExpressiveApi::class)
@Composable
fun LockScreen(visible: Boolean, unlock: suspend () -> Unit) {
    val scope = rememberCoroutineScope()
    // Demande l'empreinte dès que l'écran apparaît.
    LaunchedEffect(visible) { if (visible) unlock() }
    AnimatedVisibility(visible, enter = fadeIn(), exit = fadeOut() + scaleOut(targetScale = 1.08f)) {
        Surface(color = MaterialTheme.colorScheme.surface, modifier = Modifier.fillMaxSize()) {
            Column(
                Modifier.fillMaxSize().padding(32.dp),
                horizontalAlignment = Alignment.CenterHorizontally,
                verticalArrangement = Arrangement.Center,
            ) {
                Box(Modifier.size(112.dp), contentAlignment = Alignment.Center) {
                    Surface(
                        color = MaterialTheme.colorScheme.primaryContainer,
                        shape = MaterialShapes.Cookie9Sided.toShape(),
                        modifier = Modifier.fillMaxSize(),
                    ) {}
                    Icon(
                        Icons.Rounded.Fingerprint, null,
                        tint = MaterialTheme.colorScheme.onPrimaryContainer,
                        modifier = Modifier.size(52.dp),
                    )
                }
                Spacer(Modifier.height(28.dp))
                Text("Vigie est verrouillée", style = MaterialTheme.typography.headlineSmall, textAlign = TextAlign.Center)
                Spacer(Modifier.height(8.dp))
                Text(
                    "Authentifie-toi pour voir l'état de ton serveur.",
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    textAlign = TextAlign.Center,
                )
                Spacer(Modifier.height(32.dp))
                Button(onClick = { scope.launch { unlock() } }, shapes = ButtonDefaults.shapes()) {
                    Text("Déverrouiller")
                }
            }
        }
    }
}
