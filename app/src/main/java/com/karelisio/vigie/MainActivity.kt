package com.karelisio.vigie

import android.animation.AnimatorSet
import android.animation.ObjectAnimator
import android.content.Intent
import android.os.Bundle
import android.view.View
import android.view.animation.AnticipateInterpolator
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.core.animation.doOnEnd
import androidx.core.splashscreen.SplashScreen.Companion.installSplashScreen
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.compose.rememberNavController
import com.karelisio.vigie.data.LiveEvent
import com.karelisio.vigie.data.UserPrefs
import com.karelisio.vigie.push.Notifications
import com.karelisio.vigie.ui.navigation.ChecksRoute
import com.karelisio.vigie.ui.navigation.ContainerRoute
import com.karelisio.vigie.ui.navigation.HomeRoute
import com.karelisio.vigie.ui.navigation.OnboardingRoute
import com.karelisio.vigie.ui.navigation.ServerEditRoute
import com.karelisio.vigie.ui.navigation.VigieNavHost
import com.karelisio.vigie.ui.navigation.navigateTab
import com.karelisio.vigie.ui.navigation.rememberSnackbar
import com.karelisio.vigie.ui.theme.VigieTheme
import com.karelisio.vigie.ui.theme.animationsDisabled
import com.karelisio.vigie.update.UpdateSheet
import kotlinx.coroutines.flow.first

class MainActivity : ComponentActivity() {
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
                    Root(p, pendingIntent.value) { pendingIntent.value = null }
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

    // Liens profonds : QR « vigie://connect », notifications.
    LaunchedEffect(intent) {
        val i = intent ?: return@LaunchedEffect
        consumed()
        val data = i.data
        when {
            data?.scheme == "vigie" -> nav.navigate(ServerEditRoute(link = data.toString()))
            i.hasExtra(Notifications.EXTRA_CONTAINER) || i.hasExtra(Notifications.EXTRA_CONTAINER_NAME) -> {
                val id = i.getStringExtra(Notifications.EXTRA_CONTAINER) ?: i.getStringExtra(Notifications.EXTRA_CONTAINER_NAME)!!
                val name = i.getStringExtra(Notifications.EXTRA_CONTAINER_NAME) ?: id.take(12)
                nav.navigateTab(HomeRoute)
                nav.navigate(ContainerRoute(id, name))
            }
            i.hasExtra(Notifications.EXTRA_CHECK) -> nav.navigateTab(ChecksRoute)
        }
    }

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
