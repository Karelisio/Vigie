package com.karelisio.vigie.ui.navigation

import androidx.compose.animation.SharedTransitionScope
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier

/** Limites partagées entre écrans (morphing de conteneur à conteneur). Sans effet hors navigation. */
@Composable
fun Modifier.sharedBoundsOrSelf(key: String, text: Boolean = false): Modifier {
    val shared = LocalSharedScope.current ?: return this
    val anim = LocalNavScope.current ?: return this
    return with(shared) {
        this@sharedBoundsOrSelf.sharedBounds(
            sharedContentState = rememberSharedContentState(key),
            animatedVisibilityScope = anim,
            boundsTransform = SharedBounds,
            resizeMode = if (text) SharedTransitionScope.ResizeMode.scaleToBounds() else SharedTransitionScope.ResizeMode.RemeasureToBounds,
        )
    }
}

/** Élément partagé identique des deux côtés (pastille d'état…). */
@Composable
fun Modifier.sharedElementOrSelf(key: String): Modifier {
    val shared = LocalSharedScope.current ?: return this
    val anim = LocalNavScope.current ?: return this
    return with(shared) {
        this@sharedElementOrSelf.sharedElement(
            sharedContentState = rememberSharedContentState(key),
            animatedVisibilityScope = anim,
            boundsTransform = SharedBounds,
        )
    }
}
