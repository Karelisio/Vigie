package com.karelisio.vigie.push

import android.annotation.SuppressLint
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import com.karelisio.vigie.R
import com.karelisio.vigie.appContainer
import com.karelisio.vigie.data.ActionResult
import com.karelisio.vigie.data.AgentJob
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeout

/** Boutons des notifications (redémarrer, mettre à jour) : agissent sans ouvrir l'appli. */
class AlertActionReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        val action = intent.getStringExtra(Notifications.EXTRA_ACTION) ?: "restart"
        val ref = intent.getStringExtra(Notifications.EXTRA_CONTAINER) ?: return
        val name = intent.getStringExtra(Notifications.EXTRA_CONTAINER_NAME) ?: ref
        val url = intent.getStringExtra(Notifications.EXTRA_SERVER_URL).orEmpty().trimEnd('/')
        val notifId = intent.getIntExtra(EXTRA_NOTIFICATION_ID, 0)
        val pending = goAsync()
        val c = appContainer
        CoroutineScope(Dispatchers.IO).launch {
            val server = c.servers.servers.value.firstOrNull { url.isNotEmpty() && it.baseUrl == url } ?: c.servers.active
            val result = runCatching {
                requireNotNull(server) { "Aucun serveur configuré" }
                withTimeout(40_000) {
                    when (action) {
                        "update" -> c.api.postEmpty<AgentJob>(server, "/api/v1/containers/$ref/update")
                        "update_all" -> c.api.postEmpty<AgentJob>(server, "/api/v1/updates/apply")
                        else -> c.api.postEmpty<ActionResult>(server, "/api/v1/containers/$ref/restart")
                    }
                }
            }
            val (ok, failed) = when (action) {
                "update" -> "Mise à jour de $name lancée" to "Échec de la mise à jour de $name"
                "update_all" -> "Mises à jour lancées" to "Échec des mises à jour"
                else -> "$name redémarré" to "Échec du redémarrage de $name"
            }
            update(
                context, notifId,
                if (result.isSuccess) ok else failed,
                result.exceptionOrNull()?.message ?: when (action) {
                    "restart" -> "Commande envoyée depuis la notification."
                    else -> "Tu recevras une notification à la fin."
                },
                result.isSuccess,
            )
            c.surfaces.refreshNow()
            pending.finish()
        }
    }

    @SuppressLint("MissingPermission")
    private fun update(context: Context, id: Int, title: String, body: String, ok: Boolean) {
        if (!Notifications.canPost(context)) return
        val n = NotificationCompat.Builder(context, Notifications.CHANNEL_RESOLVED)
            .setSmallIcon(R.drawable.ic_stat_vigie)
            .setColor(if (ok) 0xFF1B873F.toInt() else 0xFFC62828.toInt())
            .setContentTitle(title)
            .setContentText(body)
            .setAutoCancel(true)
            .setTimeoutAfter(if (ok) 60_000 else 0)
            .build()
        runCatching { NotificationManagerCompat.from(context).notify(id, n) }
    }

    companion object {
        const val EXTRA_NOTIFICATION_ID = "notification_id"
    }
}
