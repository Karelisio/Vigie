package com.karelisio.vigie.security

import android.content.Context
import android.os.Build
import android.os.SystemClock
import androidx.biometric.BiometricManager
import androidx.biometric.BiometricManager.Authenticators.BIOMETRIC_WEAK
import androidx.biometric.BiometricManager.Authenticators.DEVICE_CREDENTIAL
import androidx.biometric.BiometricPrompt
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.core.content.ContextCompat
import androidx.fragment.app.FragmentActivity
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlin.coroutines.resume

private val authenticators: Int
    get() = if (Build.VERSION.SDK_INT >= 30) BIOMETRIC_WEAK or DEVICE_CREDENTIAL else BIOMETRIC_WEAK

/** Vrai si l'appareil peut authentifier l'utilisateur (empreinte, visage ou code de l'écran de verrouillage). */
fun canAuthenticate(context: Context): Boolean =
    BiometricManager.from(context).canAuthenticate(authenticators) == BiometricManager.BIOMETRIC_SUCCESS

/** Demande l'empreinte (ou le code de l'appareil). Renvoie true si l'utilisateur est authentifié. */
suspend fun FragmentActivity.authenticate(title: String, subtitle: String? = null): Boolean =
    suspendCancellableCoroutine { cont ->
        val prompt = BiometricPrompt(
            this,
            ContextCompat.getMainExecutor(this),
            object : BiometricPrompt.AuthenticationCallback() {
                override fun onAuthenticationSucceeded(result: BiometricPrompt.AuthenticationResult) {
                    if (cont.isActive) cont.resume(true)
                }

                override fun onAuthenticationError(errorCode: Int, errString: CharSequence) {
                    if (cont.isActive) cont.resume(false)
                }
            },
        )
        val info = BiometricPrompt.PromptInfo.Builder()
            .setTitle(title)
            .apply { subtitle?.let(::setSubtitle) }
            .setAllowedAuthenticators(authenticators)
            .apply { if (Build.VERSION.SDK_INT < 30) setNegativeButtonText("Annuler") }
            .build()
        prompt.authenticate(info)
        cont.invokeOnCancellation { runCatching { prompt.cancelAuthentication() } }
    }

/** Verrou de l'appli : se referme après 30 s en arrière-plan. */
class AppLock {
    private val _unlocked = MutableStateFlow(false)
    val unlocked: StateFlow<Boolean> = _unlocked.asStateFlow()
    @Volatile private var backgroundAt = 0L

    fun unlock() {
        _unlocked.value = true
    }

    fun onBackground() {
        backgroundAt = SystemClock.elapsedRealtime()
    }

    fun onForeground() {
        if (backgroundAt > 0 && SystemClock.elapsedRealtime() - backgroundAt > 30_000) _unlocked.value = false
    }
}

/** Garde des actions sensibles (start, stop, restart) : renvoie true si l'action peut avoir lieu. */
val LocalActionGuard = staticCompositionLocalOf<suspend (String) -> Boolean> { { true } }
