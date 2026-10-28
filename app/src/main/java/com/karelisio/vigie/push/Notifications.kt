package com.karelisio.vigie.push

import android.Manifest
import android.app.NotificationChannel
import android.app.NotificationChannelGroup
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.Color
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat
import com.karelisio.vigie.MainActivity
import com.karelisio.vigie.R

object Notifications {
    const val CHANNEL_CRITICAL = "alerts_critical"
    const val CHANNEL_RESOLVED = "alerts_resolved"
    const val CHANNEL_UPDATES = "updates"
    private const val GROUP = "vigie_alerts"

    const val EXTRA_CONTAINER = "container_id"
    const val EXTRA_CONTAINER_NAME = "container_name"
    const val EXTRA_CHECK = "check_id"
    const val EXTRA_SERVER_URL = "server_url"
    /** Action demandée depuis une notification : restart, update ou update_all. */
    const val EXTRA_ACTION = "action"

    fun createChannels(context: Context) {
        val nm = context.getSystemService(NotificationManager::class.java)
        nm.createNotificationChannelGroup(NotificationChannelGroup(GROUP, "Alertes"))
        nm.createNotificationChannels(
            listOf(
                NotificationChannel(CHANNEL_CRITICAL, "Incidents", NotificationManager.IMPORTANCE_HIGH).apply {
                    description = "Conteneur tombé, redémarrages en boucle, seuils dépassés, service injoignable"
                    group = GROUP
                    enableLights(true)
                    lightColor = Color.RED
                    enableVibration(true)
                    vibrationPattern = longArrayOf(0, 120, 80, 120)
                },
                NotificationChannel(CHANNEL_RESOLVED, "Retours à la normale", NotificationManager.IMPORTANCE_DEFAULT).apply {
                    description = "Quand un incident est résolu"
                    group = GROUP
                },
                NotificationChannel(CHANNEL_UPDATES, "Mises à jour de l'appli", NotificationManager.IMPORTANCE_LOW).apply {
                    description = "Téléchargement des nouvelles versions"
                },
            ),
        )
    }

    fun canPost(context: Context): Boolean =
        Build.VERSION.SDK_INT < 33 ||
            ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED

    /** Affiche une alerte reçue de l'agent. */
    @android.annotation.SuppressLint("MissingPermission") // vérifiée par canPost()
    fun showAlert(context: Context, data: Map<String, String>, directActions: Boolean = true) {
        if (!canPost(context)) return
        val resolved = data["resolved"] == "true"
        val severity = data["severity"].orEmpty()
        val title = data["title"].orEmpty().ifEmpty { "Vigie" }
        val body = data["body"].orEmpty()
        val server = data["server"].orEmpty()
        val key = "${data["kind"]}:${data["target"]}"
        val intent = Intent(context, MainActivity::class.java).apply {
            flags = Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP
            data["container_id"]?.let { putExtra(EXTRA_CONTAINER, it) }
            if (data["kind"] == "container") putExtra(EXTRA_CONTAINER_NAME, data["target"])
            data["check_id"]?.let { putExtra(EXTRA_CHECK, it) }
            data["url"]?.let { putExtra(EXTRA_SERVER_URL, it) }
            if (data["kind"] == "image") setData(android.net.Uri.parse("vigie://open/containers"))
        }
        val pi = PendingIntent.getActivity(
            context, key.hashCode(), intent, PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        val color = when {
            resolved -> 0xFF1B873F.toInt()
            severity == "critical" -> 0xFFC62828.toInt()
            else -> 0xFF006A6A.toInt()
        }
        val builder = NotificationCompat.Builder(context, if (resolved) CHANNEL_RESOLVED else CHANNEL_CRITICAL)
            .setSmallIcon(R.drawable.ic_stat_vigie)
            .setColor(color)
            .setContentTitle(title)
            .setContentText(body.ifEmpty { server })
            .setSubText(server.takeIf { it.isNotEmpty() })
            .setStyle(NotificationCompat.BigTextStyle().bigText(body))
            .setCategory(if (resolved) NotificationCompat.CATEGORY_STATUS else NotificationCompat.CATEGORY_ERROR)
            .setPriority(if (resolved) NotificationCompat.PRIORITY_DEFAULT else NotificationCompat.PRIORITY_HIGH)
            .setWhen(data["ts"]?.toLongOrNull() ?: System.currentTimeMillis())
            .setShowWhen(true)
            .setAutoCancel(true)
            .setGroup(GROUP)
            .setContentIntent(pi)
        val target = data["target"].orEmpty()
        val button: Triple<String, String, Int>? = when {
            resolved -> null
            data["kind"] == "container" && target.isNotEmpty() -> Triple("restart", "Redémarrer", R.drawable.ic_restart)
            data["kind"] == "image" && target.contains(", ") -> Triple("update_all", "Tout mettre à jour", R.drawable.ic_update)
            data["kind"] == "image" && target.isNotEmpty() -> Triple("update", "Mettre à jour", R.drawable.ic_update)
            else -> null
        }
        if (button != null) {
            val (action, label, icon) = button
            val ref = if (data["kind"] == "container") data["container_id"] ?: target else target
            // Verrou actif : le bouton ouvre l'appli, qui demande confirmation et empreinte avant d'agir.
            val pending = if (directActions) {
                val i = Intent(context, AlertActionReceiver::class.java).apply {
                    putExtra(EXTRA_ACTION, action)
                    putExtra(EXTRA_CONTAINER, ref)
                    putExtra(EXTRA_CONTAINER_NAME, target)
                    putExtra(EXTRA_SERVER_URL, data["url"])
                    putExtra(AlertActionReceiver.EXTRA_NOTIFICATION_ID, key.hashCode())
                }
                PendingIntent.getBroadcast(context, key.hashCode(), i, PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
            } else {
                val i = Intent(intent).apply {
                    putExtra(EXTRA_ACTION, action)
                    if (action != "update_all") putExtra(EXTRA_CONTAINER_NAME, target)
                }
                PendingIntent.getActivity(context, key.hashCode() + 1, i, PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
            }
            builder.addAction(icon, label, pending)
        }
        val n = builder.build()
        // Même identifiant pour l'incident et sa résolution : la résolution remplace l'alerte.
        runCatching { NotificationManagerCompat.from(context).notify(key.hashCode(), n) }
    }
}
