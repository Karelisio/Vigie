package com.karelisio.vigie.ui.navigation

import androidx.compose.animation.AnimatedContentTransitionScope
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.AnimatedVisibilityScope
import androidx.compose.animation.BoundsTransform
import androidx.compose.animation.EnterTransition
import androidx.compose.animation.ExitTransition
import androidx.compose.animation.SharedTransitionLayout
import androidx.compose.animation.SharedTransitionScope
import androidx.compose.animation.core.Spring
import androidx.compose.animation.core.spring
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.scaleIn
import androidx.compose.animation.scaleOut
import androidx.compose.animation.slideInHorizontally
import androidx.compose.animation.slideInVertically
import androidx.compose.animation.slideOutHorizontally
import androidx.compose.animation.slideOutVertically
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.ui.unit.dp
import androidx.compose.material3.Icon
import androidx.compose.material3.ShortNavigationBar
import androidx.compose.material3.ShortNavigationBarItem
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.compositionLocalOf
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Rect
import androidx.navigation.NavBackStackEntry
import androidx.navigation.NavDestination.Companion.hasRoute
import androidx.navigation.NavDestination.Companion.hierarchy
import androidx.navigation.NavGraph.Companion.findStartDestination
import androidx.navigation.NavHostController
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.currentBackStackEntryAsState
import androidx.navigation.toRoute
import com.karelisio.vigie.ui.checks.ChecksScreen
import com.karelisio.vigie.ui.components.rememberHaptics
import com.karelisio.vigie.ui.containers.ContainerDetailScreen
import com.karelisio.vigie.ui.containers.ContainersScreen
import com.karelisio.vigie.ui.home.HomeScreen
import com.karelisio.vigie.ui.logs.LogsScreen
import com.karelisio.vigie.ui.onboarding.OnboardingScreen
import com.karelisio.vigie.ui.onboarding.ScanScreen
import com.karelisio.vigie.ui.settings.AboutScreen
import com.karelisio.vigie.ui.settings.AlertsScreen
import com.karelisio.vigie.ui.settings.AuditScreen
import com.karelisio.vigie.ui.settings.PermissionsScreen
import com.karelisio.vigie.ui.settings.ServerEditScreen
import com.karelisio.vigie.ui.settings.SettingsScreen
import com.karelisio.vigie.ui.theme.LocalReducedMotion

/** Portées de transition partagée (carte du conteneur → fiche). */
val LocalSharedScope = compositionLocalOf<SharedTransitionScope?> { null }
val LocalNavScope = compositionLocalOf<AnimatedVisibilityScope?> { null }
val LocalSnackbar = staticCompositionLocalOf { SnackbarHostState() }

val SharedBounds = BoundsTransform { _: Rect, _: Rect -> spring(dampingRatio = 0.82f, stiffness = Spring.StiffnessMediumLow) }

private fun NavBackStackEntry.isTopLevel() = TopLevels.any { destination.hasRoute(it.route::class) }

private val tabEnter = fadeIn(tween(220, 60)) + scaleIn(tween(320), initialScale = 0.94f)
private val tabExit = fadeOut(tween(120))

private fun AnimatedContentTransitionScope<NavBackStackEntry>.enter(): EnterTransition =
    if (initialState.isTopLevel() && targetState.isTopLevel()) tabEnter
    else slideInHorizontally(spring(0.9f, Spring.StiffnessMediumLow)) { it / 3 } + fadeIn(tween(220))

private fun AnimatedContentTransitionScope<NavBackStackEntry>.exit(): ExitTransition =
    if (initialState.isTopLevel() && targetState.isTopLevel()) tabExit
    else slideOutHorizontally(spring(0.9f, Spring.StiffnessMediumLow)) { -it / 6 } + fadeOut(tween(180))

private fun AnimatedContentTransitionScope<NavBackStackEntry>.popEnter(): EnterTransition =
    if (initialState.isTopLevel() && targetState.isTopLevel()) tabEnter
    else slideInHorizontally(spring(0.9f, Spring.StiffnessMediumLow)) { -it / 6 } + fadeIn(tween(220))

private fun AnimatedContentTransitionScope<NavBackStackEntry>.popExit(): ExitTransition =
    if (initialState.isTopLevel() && targetState.isTopLevel()) tabExit
    else slideOutHorizontally(spring(0.9f, Spring.StiffnessMediumLow)) { it / 3 } + fadeOut(tween(180)) + scaleOut(targetScale = 0.92f)

fun NavHostController.navigateTab(route: Any) {
    navigate(route) {
        popUpTo(graph.findStartDestination().id) { saveState = true }
        launchSingleTop = true
        restoreState = true
    }
}

@Composable
fun VigieNavHost(nav: NavHostController, start: Any, snackbar: SnackbarHostState) {
    val entry by nav.currentBackStackEntryAsState()
    val showBar = entry?.isTopLevel() == true
    val haptics = rememberHaptics()
    val reduced = LocalReducedMotion.current
    val live by com.karelisio.vigie.appContainer.repository.live.collectAsState()
    val job = live.runningJob
    // Mémorise la dernière opération pour l'animation de sortie du bandeau.
    var shownJob by remember { mutableStateOf(job) }
    androidx.compose.runtime.LaunchedEffect(job) { if (job != null) shownJob = job }
    val showJob = job != null && showBar

    CompositionLocalProvider(LocalSnackbar provides snackbar) {
        SharedTransitionLayout(Modifier.fillMaxSize()) {
            CompositionLocalProvider(LocalSharedScope provides this) {
                Box(Modifier.fillMaxSize()) {
                    NavHost(
                        navController = nav,
                        startDestination = start,
                        modifier = Modifier.fillMaxSize(),
                        enterTransition = { if (reduced) fadeIn() else enter() },
                        exitTransition = { if (reduced) fadeOut() else exit() },
                        popEnterTransition = { if (reduced) fadeIn() else popEnter() },
                        popExitTransition = { if (reduced) fadeOut() else popExit() },
                    ) {
                        screen<OnboardingRoute> { OnboardingScreen(nav, it) }
                        screen<HomeRoute> { HomeScreen(nav) }
                        screen<ContainersRoute> { ContainersScreen(nav) }
                        screen<ChecksRoute> { ChecksScreen(nav) }
                        screen<SettingsRoute> { SettingsScreen(nav) }
                        screen<ContainerRoute> { ContainerDetailScreen(nav, it.toRoute<ContainerRoute>()) }
                        screen<LogsRoute> { LogsScreen(nav, it.toRoute<LogsRoute>()) }
                        screen<ServerEditRoute> { ServerEditScreen(nav, it.toRoute<ServerEditRoute>(), it) }
                        screen<ScanRoute> { ScanScreen(nav) }
                        screen<PermissionsRoute> { PermissionsScreen(nav) }
                        screen<AlertsRoute> { AlertsScreen(nav) }
                        screen<CleanupRoute> { com.karelisio.vigie.ui.settings.CleanupScreen(nav) }
                        screen<AlertSettingsRoute> { com.karelisio.vigie.ui.settings.AlertSettingsScreen(nav) }
                        screen<AutoUpdateRoute> { com.karelisio.vigie.ui.settings.AutoUpdateScreen(nav) }
                        screen<AuditRoute> { AuditScreen(nav) }
                        screen<AboutRoute> { AboutScreen(nav) }
                    }

                    AnimatedVisibility(
                        visible = showBar,
                        modifier = Modifier.align(Alignment.BottomCenter),
                        enter = slideInVertically { it } + fadeIn(),
                        exit = slideOutVertically { it } + fadeOut(),
                    ) {
                        ShortNavigationBar {
                            TopLevels.forEach { tl ->
                                val selected = entry?.destination?.hierarchy?.any { it.hasRoute(tl.route::class) } == true
                                ShortNavigationBarItem(
                                    selected = selected,
                                    onClick = {
                                        if (!selected) {
                                            haptics.tick()
                                            nav.navigateTab(tl.route)
                                        }
                                    },
                                    icon = { Icon(if (selected) tl.selectedIcon else tl.icon, null) },
                                    label = { Text(tl.label) },
                                )
                            }
                        }
                    }
                    AnimatedVisibility(
                        visible = showJob,
                        modifier = Modifier
                            .align(Alignment.BottomCenter)
                            .navigationBarsPadding()
                            .padding(start = 12.dp, end = 12.dp, bottom = 76.dp),
                        enter = slideInVertically { it / 2 } + fadeIn(),
                        exit = slideOutVertically { it / 2 } + fadeOut(),
                    ) {
                        (job ?: shownJob)?.let { com.karelisio.vigie.ui.components.JobBanner(it) }
                    }
                    SnackbarHost(
                        snackbar,
                        Modifier
                            .align(Alignment.BottomCenter)
                            .navigationBarsPadding()
                            .padding(bottom = (if (showBar) 72.dp else 0.dp) + (if (showJob) 84.dp else 0.dp)),
                    )
                }
            }
        }
    }
}

private inline fun <reified T : Any> androidx.navigation.NavGraphBuilder.screen(
    noinline content: @Composable (NavBackStackEntry) -> Unit,
) {
    composable<T> { backStack ->
        val openedAt = remember { android.os.SystemClock.uptimeMillis() }
        CompositionLocalProvider(
            LocalNavScope provides this,
            com.karelisio.vigie.ui.components.LocalScreenOpenedAt provides openedAt,
        ) { content(backStack) }
    }
}


@Composable
fun rememberSnackbar(): SnackbarHostState = remember { SnackbarHostState() }
