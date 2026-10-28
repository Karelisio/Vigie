package com.karelisio.vigie

import android.animation.AnimatorSet
import android.animation.ObjectAnimator
import android.content.Intent
import android.os.Bundle
import android.view.View
import android.view.animation.AnticipateInterpolator
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.SnackbarDuration
import androidx.compose.material3.SnackbarResult
import androidx.compose.material3.Surface
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.rememberCoroutineScope
import kotlinx.coroutines.launch
import androidx.compose.ui.Modifier
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.core.animation.doOnEnd
import androidx.core.splashscreen.SplashScreen.Companion.installSplashScreen
import androidx.fragment.app.FragmentActivity
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.compose.rememberNavController
import com.karelisio.vigie.data.LiveEvent
import com.karelisio.vigie.data.LockMode
import com.karelisio.vigie.data.UserPrefs
import com.karelisio.vigie.push.Notifications
import com.karelisio.vigie.security.LocalActionGuard
import com.karelisio.vigie.security.LockScreen
import com.karelisio.vigie.security.authenticate
import com.karelisio.vigie.security.canAuthenticate
import com.karelisio.vigie.shortcuts.Shortcuts
import com.karelisio.vigie.ui.navigation.ChecksRoute
import com.karelisio.vigie.ui.navigation.ContainerRoute
import com.karelisio.vigie.ui.navigation.ContainersRoute
import com.karelisio.vigie.ui.navigation.HomeRoute
import com.karelisio.vigie.ui.navigation.LogsRoute
import com.karelisio.vigie.ui.navigation.OnboardingRoute
import com.karelisio.vigie.ui.navigation.ServerEditRoute
import com.karelisio.vigie.ui.navigation.VigieNavHost
import com.karelisio.vigie.ui.navigation.navigateTab
import com.karelisio.vigie.ui.navigation.rememberSnackbar
import com.karelisio.vigie.ui.theme.VigieTheme
import com.karelisio.vigie.ui.theme.animationsDisabled
import com.karelisio.vigie.update.UpdateSheet
import kotlinx.coroutines.flow.first

class MainActivity : FragmentActivity() {
    private val pendingIntent = mutableStateOf<Intent?>(null)

    override fun onCreate(savedInstanceState: Bundle?) {
        val splash = installSplashScreen()
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()

        val c = appContainer
        var ready = false
        splash.setKeepOnScreenCondition { !ready }
        splash.setOnExitAnimationListener { provider ->
            val icon = provider.iconView
            if (animationsDisabled()) {
                provider.remove()
                return@setOnExitAnimationListener
            }
            // L'onde de radar de l'icône se déploie, puis l'écran se fond dans l'appli.
            val scaleX = ObjectAnimator.ofFloat(icon, View.SCALE_X, 1f, 1.25f, 3.2f)
            val scaleY = ObjectAnimator.ofFloat(icon, View.SCALE_Y, 1f, 1.25f, 3.2f)
            val iconAlpha = ObjectAnimator.ofFloat(icon, View.ALPHA, 1f, 1f, 0f)
            val bgAlpha = ObjectAnimator.ofFloat(provider.view, View.ALPHA, 1f, 0f).apply { startDelay = 220 }
            AnimatorSet().apply {
                playTogether(scaleX, scaleY, iconAlpha, bgAlpha)
                duration = 520
                interpolator = AnticipateInterpolator(0.6f)
                doOnEnd { provider.remove() }
                start()
            }
        }
        pendingIntent.value = intent

        setContent {
            val prefs by c.prefs.flow.collectAsStateWithLifecycle(initialValue = null)
            val p = prefs
            if (p != null) {
                ready = true
                VigieTheme(dynamicColor = p.dynamicColor) {
                    val unlocked by c.lock.unlocked.collectAsStateWithLifecycle()
                    // Sans empreinte ni code configuré sur l'appareil, le verrou ne peut pas bloquer l'accès.
                    val lockable = remember(p.lockMode) { p.lockMode != LockMode.Off && canAuthenticate(this) }
                    val guard: suspend (String) -> Boolean = remember(p.lockMode, lockable) {
                        if (p.lockMode == LockMode.Actions && lockable) { reason -> authenticate("Confirmer l'action", reason) }
                        else { _ -> true }
                    }
                    CompositionLocalProvider(LocalActionGuard provides guard) {
                        Root(p, pendingIntent.value) { pendingIntent.value = null }
                    }
                    LockScreen(visible = lockable && p.lockMode == LockMode.Open && !unlocked) {
                        if (authenticate("Déverrouiller Vigie")) c.lock.unlock()
                    }
                }
            }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        pendingIntent.value = intent
    }
}

@Composable
private fun Root(prefs: UserPrefs, intent: Intent?, consumed: () -> Unit) {
    val c = appContainer
    val nav = rememberNavController()
    val snackbar = rememberSnackbar()
    val start = remember { if (prefs.onboardingDone && c.servers.active != null) HomeRoute else OnboardingRoute }

    Surface(color = MaterialTheme.colorScheme.surface, modifier = Modifier.fillMaxSize()) {
        VigieNavHost(nav, start, snackbar)
    }

    // Liens profonds : QR « vigie://connect », raccourcis, notifications.
    val guard = LocalActionGuard.current
    val scope = rememberCoroutineScope()
    val context = androidx.compose.ui.platform.LocalContext.current
    LaunchedEffect(intent) {
        val i = intent ?: return@LaunchedEffect
        consumed()
        val data = i.data
        val ready = c.servers.active != null && prefs.onboardingDone
        when {
            data?.scheme == "vigie" && data.host == "connect" -> nav.navigate(ServerEditRoute(link = data.toString()))
            data?.scheme == "vigie" && data.host == "open" && ready -> when (data.path) {
                "/containers" -> nav.navigateTab(ContainersRoute)
                "/checks" -> nav.navigateTab(ChecksRoute)
                else -> nav.navigateTab(HomeRoute)
            }
            data?.scheme == "vigie" && data.host == "logs" && ready -> {
                val id = data.getQueryParameter("id") ?: return@LaunchedEffect
                val name = data.getQueryParameter("name") ?: id
                Shortcuts.reportUsed(context, name)
                nav.navigateTab(ContainersRoute)
                nav.navigate(LogsRoute(id, name))
            }
            i.hasExtra(Notifications.EXTRA_CONTAINER) || i.hasExtra(Notifications.EXTRA_CONTAINER_NAME) -> {
                val id = i.getStringExtra(Notifications.EXTRA_CONTAINER) ?: i.getStringExtra(Notifications.EXTRA_CONTAINER_NAME)!!
                val name = i.getStringExtra(Notifications.EXTRA_CONTAINER_NAME) ?: id.take(12)
                nav.navigateTab(HomeRoute)
                nav.navigate(ContainerRoute(id, name))
                // Bouton « Redémarrer » d'une notification quand le verrou est actif. L'activité étant exportée,
                // l'action n'est jamais lancée sans un appui explicite de l'utilisateur.
                if (i.getBooleanExtra(Notifications.EXTRA_RESTART, false)) scope.launch {
                    if (prefs.lockMode == LockMode.Open) c.lock.unlocked.first { it }
                    val confirmed = snackbar.showSnackbar(
                        "Redémarrer $name ?",
                        actionLabel = "Redémarrer",
                        withDismissAction = true,
                        duration = SnackbarDuration.Long,
                    ) == SnackbarResult.ActionPerformed
                    if (!confirmed || !guard("Redémarrer $name")) return@launch
                    val msg = runCatching { c.repository.action(id, "restart") }
                        .fold({ "$name redémarré" }, { "Échec : ${it.message}" })
                    snackbar.showSnackbar(msg)
                }
            }
            i.hasExtra(Notifications.EXTRA_CHECK) -> nav.navigateTab(ChecksRoute)
        }
    }

    // Raccourcis « Logs de … » pour les conteneurs épinglés du serveur actif.
    val activeId by c.servers.activeId.collectAsState()
    val favorites = prefs.favorites[activeId].orEmpty()
    LaunchedEffect(favorites) { Shortcuts.updateFavorites(context, favorites) }

    // Alertes reçues en direct : petit message en bas de l'écran.
    LaunchedEffect(Unit) {
        c.repository.events.collect { e ->
            if (e is LiveEvent.NewAlert) {
                snackbar.currentSnackbarData?.dismiss()
                snackbar.showSnackbar((if (e.alert.resolved) "✓ " else "⚠ ") + e.alert.title)
            }
        }
    }

    // Vérification de mise à jour au lancement (au plus toutes les 6 h).
    LaunchedEffect(Unit) {
        c.prefs.flow.first()
        runCatching { c.updater.check() }
    }
    val update by c.updater.state.collectAsState()
    UpdateSheet(update, c.updater)
}
