package com.karelisio.vigie.ui.onboarding

import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.animateDpAsState
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInHorizontally
import androidx.compose.animation.slideOutHorizontally
import androidx.compose.animation.togetherWith
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.pager.HorizontalPager
import androidx.compose.foundation.pager.PagerState
import androidx.compose.foundation.pager.rememberPagerState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Inventory2
import androidx.compose.material.icons.rounded.MonitorHeart
import androidx.compose.material.icons.rounded.NotificationsActive
import androidx.compose.material.icons.rounded.Widgets
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialShapes
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.toShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavHostController
import com.karelisio.vigie.appContainer
import com.karelisio.vigie.ui.components.MiniRadar
import com.karelisio.vigie.ui.components.Radar
import com.karelisio.vigie.ui.components.RadarBlip
import com.karelisio.vigie.ui.components.cascade
import com.karelisio.vigie.ui.components.rememberHaptics
import com.karelisio.vigie.ui.navigation.HomeRoute
import com.karelisio.vigie.ui.navigation.OnboardingRoute
import com.karelisio.vigie.ui.settings.Perm
import com.karelisio.vigie.ui.settings.PermBadge
import com.karelisio.vigie.ui.settings.SCAN_RESULT
import com.karelisio.vigie.ui.settings.ServerForm
import com.karelisio.vigie.ui.settings.rememberPermissionState
import com.karelisio.vigie.ui.settings.rememberScanLauncher
import com.karelisio.vigie.ui.theme.Vigie
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlin.math.absoluteValue

private sealed interface Page {
    data object Welcome : Page
    data object Features : Page
    data object Server : Page
    data class Permission(val perm: Perm) : Page
    data object Done : Page
}

private val Pages: List<Page> = listOf(Page.Welcome, Page.Features, Page.Server) +
    Perm.entries.map { Page.Permission(it) } + Page.Done

/** Premier lancement : présentation, connexion au serveur, puis autorisations une par une. */
@Composable
fun OnboardingScreen(nav: NavHostController, entry: androidx.navigation.NavBackStackEntry) {
    val pager = rememberPagerState { Pages.size }
    val scope = rememberCoroutineScope()
    val haptics = rememberHaptics()
    val servers by appContainer.servers.servers.collectAsStateWithLifecycle()
    val perms = rememberPermissionState()
    val hasServer = servers.isNotEmpty()
    val page = Pages[pager.currentPage]
    val canNext = page != Page.Server || hasServer

    fun next() {
        haptics.tick()
        if (pager.currentPage < Pages.lastIndex) scope.launch { pager.animateScrollToPage(pager.currentPage + 1) }
    }

    fun finish() {
        haptics.confirm()
        scope.launch {
            appContainer.prefs.setOnboardingDone(true)
            appContainer.push.syncToken()
            nav.navigate(HomeRoute) { popUpTo(OnboardingRoute) { inclusive = true } }
        }
    }

    Column(Modifier.fillMaxSize().statusBarsPadding().navigationBarsPadding().imePadding()) {
        HorizontalPager(
            state = pager,
            userScrollEnabled = canNext || pager.currentPage > Pages.indexOf(Page.Server),
            modifier = Modifier.weight(1f),
            beyondViewportPageCount = 0,
        ) { i ->
            val p = Pages[i]
            Box(
                Modifier
                    .fillMaxSize()
                    .graphicsLayer {
                        // Léger effet de profondeur pendant le défilement.
                        val off = pager.offsetFor(i)
                        alpha = 1f - off.absoluteValue * 0.6f
                        val s = 1f - off.absoluteValue * 0.08f
                        scaleX = s
                        scaleY = s
                    },
            ) {
                when (p) {
                    Page.Welcome -> WelcomePage(active = pager.currentPage == i)
                    Page.Features -> FeaturesPage(active = pager.currentPage == i)
                    Page.Server -> ServerPage(nav, entry, onSaved = { next() })
                    is Page.Permission -> PermissionPage(p.perm, perms.granted[p.perm] == true) { perms.request(p.perm) }
                    Page.Done -> DonePage(active = pager.currentPage == i)
                }
            }
        }

        // Avance automatiquement quand l'autorisation de la page vient d'être accordée.
        val currentPerm = (page as? Page.Permission)?.perm
        val grantedNow = currentPerm?.let { perms.granted[it] == true } == true
        var wasGranted by remember(currentPerm) { mutableStateOf(grantedNow) }
        LaunchedEffect(grantedNow, currentPerm) {
            if (grantedNow && !wasGranted) {
                delay(900)
                next()
            }
            wasGranted = grantedNow
        }

        Row(
            Modifier.fillMaxWidth().padding(horizontal = 24.dp, vertical = 16.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            PagerDots(pager, Modifier.weight(1f))
            AnimatedContent(
                targetState = when {
                    page == Page.Done -> "Commencer"
                    page is Page.Permission && !grantedNow -> "Plus tard"
                    page == Page.Server && !hasServer -> ""
                    else -> "Suivant"
                },
                transitionSpec = { fadeIn() togetherWith fadeOut() },
                label = "next",
            ) { label ->
                when {
                    label.isEmpty() -> Spacer(Modifier.height(56.dp))
                    label == "Plus tard" -> TextButton(onClick = { next() }, modifier = Modifier.height(56.dp)) { Text(label) }
                    else -> Button(
                        onClick = { if (page == Page.Done) finish() else next() },
                        enabled = canNext,
                        shapes = ButtonDefaults.shapes(),
                        modifier = Modifier.height(56.dp),
                    ) { Text(label) }
                }
            }
        }
    }
}

private fun PagerState.offsetFor(page: Int): Float = (currentPage - page) + currentPageOffsetFraction

@Composable
private fun PagerDots(pager: PagerState, modifier: Modifier) {
    Row(modifier, horizontalArrangement = Arrangement.spacedBy(6.dp), verticalAlignment = Alignment.CenterVertically) {
        repeat(pager.pageCount) { i ->
            val selected = pager.currentPage == i
            val w by animateDpAsState(if (selected) 24.dp else 8.dp, label = "dot")
            val c by animateColorAsState(
                if (selected) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.outlineVariant,
                label = "dotColor",
            )
            Box(Modifier.height(8.dp).width(w).clip(RoundedCornerShape(50)).background(c))
        }
    }
}

@Composable
private fun PageColumn(content: @Composable () -> Unit) {
    Column(
        Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(horizontal = 28.dp, vertical = 16.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center,
    ) { content() }
}

@Composable
internal fun WelcomePage(active: Boolean) {
    val blips = remember {
        listOf(
            RadarBlip("aiostreams", "ok"), RadarBlip("comet", "ok"), RadarBlip("jellyfin", "ok"),
            RadarBlip("prowlarr", "ok"), RadarBlip("pangolin", "ok"), RadarBlip("dockhand", "ok"),
        )
    }
    PageColumn {
        Radar("ok", blips, Modifier.fillMaxWidth(0.82f).cascade(0, always = true, run = active))
        Spacer(Modifier.height(32.dp))
        Text("Vigie", style = MaterialTheme.typography.displayLargeEmphasized, modifier = Modifier.cascade(1, always = true, run = active))
        Spacer(Modifier.height(8.dp))
        Text(
            "Ton VPS, ses conteneurs et tes services Stremio, sous surveillance. En temps réel, depuis ta poche.",
            style = MaterialTheme.typography.bodyLarge,
            textAlign = TextAlign.Center,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.cascade(2, always = true, run = active),
        )
    }
}

@Composable
internal fun FeaturesPage(active: Boolean) {
    val items = listOf(
        Triple(Icons.Rounded.Inventory2, "Conteneurs", "État, santé, CPU/RAM, logs en direct. Démarrer, arrêter, redémarrer d'un geste."),
        Triple(Icons.Rounded.MonitorHeart, "Services", "Vérifie chaque minute que tes addons répondent vraiment, avec l'historique de disponibilité."),
        Triple(Icons.Rounded.NotificationsActive, "Alertes", "Une notification dès qu'un conteneur tombe, boucle, ou qu'une ressource sature."),
        Triple(Icons.Rounded.Widgets, "Partout", "Widget, tuile des réglages rapides et mode hors ligne."),
    )
    val shapes = listOf(MaterialShapes.Cookie9Sided, MaterialShapes.Clover4Leaf, MaterialShapes.SoftBurst, MaterialShapes.Pentagon)
    val t = rememberInfiniteTransition(label = "feat")
    val rot by t.animateFloat(0f, 360f, infiniteRepeatable(tween(24_000, easing = LinearEasing)), label = "rot")
    val reduced = Vigie.reducedMotion
    PageColumn {
        Text("Tout voir, d'un coup d'œil", style = MaterialTheme.typography.headlineMediumEmphasized, textAlign = TextAlign.Center)
        Spacer(Modifier.height(24.dp))
        items.forEachIndexed { i, (icon, title, body) ->
            Row(Modifier.fillMaxWidth().padding(vertical = 10.dp).cascade(i + 1, always = true, run = active), verticalAlignment = Alignment.CenterVertically) {
                Box(contentAlignment = Alignment.Center) {
                    Surface(
                        shape = shapes[i].toShape(),
                        color = listOf(
                            MaterialTheme.colorScheme.primaryContainer,
                            MaterialTheme.colorScheme.secondaryContainer,
                            MaterialTheme.colorScheme.tertiaryContainer,
                            MaterialTheme.colorScheme.surfaceContainerHighest,
                        )[i],
                        modifier = Modifier.size(60.dp).graphicsLayer { rotationZ = if (reduced) 0f else rot * (if (i % 2 == 0) 1 else -1) },
                    ) {}
                    Icon(icon, null, Modifier.size(28.dp))
                }
                Spacer(Modifier.width(16.dp))
                Column {
                    Text(title, style = MaterialTheme.typography.titleMedium)
                    Text(body, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }
        }
    }
}

@Composable
private fun ServerPage(nav: NavHostController, entry: androidx.navigation.NavBackStackEntry, onSaved: () -> Unit) {
    val scan = rememberScanLauncher(nav)
    val scanned by entry.savedStateHandle.getStateFlow<String?>(SCAN_RESULT, null).collectAsStateWithLifecycle()
    var saved by rememberSaveable { mutableStateOf(false) }
    Column(
        Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(horizontal = 24.dp, vertical = 16.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        MiniRadar(Modifier.size(88.dp))
        Spacer(Modifier.height(12.dp))
        Text("Connecte ton serveur", style = MaterialTheme.typography.headlineMediumEmphasized, textAlign = TextAlign.Center)
        Text(
            "Dans Dockhand, ouvre les logs du conteneur vigie-agent : un QR code y est affiché au premier démarrage.",
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            textAlign = TextAlign.Center,
        )
        Spacer(Modifier.height(20.dp))
        ServerForm(
            initial = appContainer.servers.active,
            link = scanned,
            onScan = scan,
            onSaved = {
                saved = true
                onSaved()
            },
            saveLabel = if (saved) "Enregistré" else "Continuer",
        )
    }
}

@Composable
internal fun PermissionPage(perm: Perm, granted: Boolean, onRequest: () -> Unit) {
    val haptics = rememberHaptics()
    PageColumn {
        PermBadge(perm, granted, size = 140.dp)
        Spacer(Modifier.height(32.dp))
        Text(perm.title, style = MaterialTheme.typography.headlineMediumEmphasized, textAlign = TextAlign.Center)
        Spacer(Modifier.height(12.dp))
        Text(perm.why, style = MaterialTheme.typography.bodyLarge, textAlign = TextAlign.Center, color = MaterialTheme.colorScheme.onSurfaceVariant)
        Spacer(Modifier.height(32.dp))
        AnimatedContent(granted, transitionSpec = { fadeIn() togetherWith fadeOut() }, label = "permBtn") { g ->
            if (g) {
                Text("C'est fait", style = MaterialTheme.typography.titleMedium, color = Vigie.status.ok)
            } else {
                Button(onClick = { haptics.tick(); onRequest() }, shapes = ButtonDefaults.shapes(), modifier = Modifier.height(56.dp)) {
                    Icon(perm.icon, null)
                    Spacer(Modifier.width(8.dp))
                    Text("Autoriser")
                }
            }
        }
    }
}

@Composable
internal fun DonePage(active: Boolean) {
    PageColumn {
        Radar("ok", emptyList(), Modifier.fillMaxWidth(0.7f).cascade(0, always = true, run = active))
        Spacer(Modifier.height(32.dp))
        Text("Tout est prêt", style = MaterialTheme.typography.displaySmallEmphasized, modifier = Modifier.cascade(1, always = true, run = active))
        Spacer(Modifier.height(8.dp))
        Text(
            "Ajoute le widget sur ton écran d'accueil et la tuile Vigie dans tes réglages rapides pour garder un œil sur ton serveur.",
            style = MaterialTheme.typography.bodyLarge,
            textAlign = TextAlign.Center,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.cascade(2, always = true, run = active),
        )
    }
}
