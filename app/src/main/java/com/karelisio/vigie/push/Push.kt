package com.karelisio.vigie.push

import android.content.Context
import com.google.android.gms.tasks.Task
import com.google.firebase.FirebaseApp
import com.google.firebase.messaging.FirebaseMessaging
import com.google.firebase.messaging.FirebaseMessagingService
import com.google.firebase.messaging.RemoteMessage
import com.karelisio.vigie.BuildConfig
import com.karelisio.vigie.appContainer
import com.karelisio.vigie.data.Prefs
import com.karelisio.vigie.data.Repository
import com.karelisio.vigie.data.Server
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

/** Gestion du jeton FCM : récupération et enregistrement auprès de chaque agent. */
class Push(
    private val context: Context,
    private val repository: Repository,
    private val prefs: Prefs,
    private val scope: CoroutineScope,
) {
    val available: Boolean
        get() = BuildConfig.FIREBASE_ENABLED && runCatching { FirebaseApp.getInstance() }.isSuccess

    suspend fun token(): String? {
        if (!available) return null
        return runCatching { FirebaseMessaging.getInstance().token.await() }.getOrNull()?.also { prefs.setFcmToken(it) }
    }

    /** Enregistre le jeton auprès de tous les serveurs (au démarrage et quand il change). */
    suspend fun syncToken(newToken: String? = null) {
        if (!prefs.current().notifications) return
        val token = newToken ?: token() ?: return
        repository.servers.servers.value.forEach { s -> runCatching { repository.registerDevice(s, token) } }
    }

    suspend fun register(server: Server): Result<Unit> = runCatching {
        val token = token() ?: error("Firebase n'est pas configuré dans cette version de l'appli")
        repository.registerDevice(server, token)
    }

    fun onNewToken(token: String) {
        scope.launch {
            prefs.setFcmToken(token)
            syncToken(token)
        }
    }
}

private suspend fun <T> Task<T>.await(): T = suspendCancellableCoroutine { cont ->
    addOnCompleteListener { t ->
        val e = t.exception
        if (e != null) cont.resumeWithException(e) else cont.resume(t.result)
    }
}

class VigieMessagingService : FirebaseMessagingService() {
    override fun onNewToken(token: String) {
        appContainer.push.onNewToken(token)
    }

    override fun onMessageReceived(message: RemoteMessage) {
        val data = message.data
        if (data.isEmpty()) return
        Notifications.showAlert(this, data)
        // Rafraîchit le widget et la tuile avec l'état réel.
        appContainer.surfaces.refreshNow()
    }
}
