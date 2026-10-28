package com.karelisio.vigie.ops

import android.app.Notification
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.net.toUri
import com.karelisio.vigie.MainActivity
import com.karelisio.vigie.R
import com.karelisio.vigie.push.Notifications

/**
 * Notifications des opérations : progression continue (« Live Update » sur Android 16) puis bilan.
 *
 * Android 16 et plus : `NotificationCompat.ProgressStyle` produit un `Notification.ProgressStyle` natif
 * (segments, repères, puce dans la barre d'état) et la mise en avant est demandée avec
 * `setRequestPromotedOngoing`. Les règles de mise en avant sont respectées : notification continue avec
 * un titre, style standard (pas de vue personnalisée), pas de couleur de fond, canal d'importance basse
 * (pas minimale). Avant Android 16 : barre de progression classique.
 */
object OperationNotifications {
    /** Même identifiant pour la progression et le bilan : le bilan remplace la progression. */
    const val ID = 0x0B5E

    private const val TEAL = 0xFF006A6A.toInt()
    private const val GREEN = 0xFF1B873F.toInt()
    private const val RED = 0xFFC62828.toInt()

    /** Vrai si la notification d'opération peut s'afficher (autorisation et canal actifs). */
    fun canShow(context: Context): Boolean {
        if (!Notifications.canPost(context)) return false
        val nm = NotificationManagerCompat.from(context)
        if (!nm.areNotificationsEnabled()) return false
        val channel = nm.getNotificationChannelCompat(Notifications.CHANNEL_OPERATIONS) ?: return true
        return channel.importance != NotificationManager.IMPORTANCE_NONE
    }

    fun progress(context: Context, p: OperationProgress, server: String, kind: String, startedAt: Long): Notification {
        val b = NotificationCompat.Builder(context, Notifications.CHANNEL_OPERATIONS)
            .setSmallIcon(R.drawable.ic_stat_vigie)
            .setColor(TEAL)
            .setContentTitle(p.title)
            .setContentText(p.text)
            .setSubText(server.takeIf { it.isNotBlank() })
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .setSilent(true)
            .setCategory(NotificationCompat.CATEGORY_PROGRESS)
            .setPriority(NotificationCompat.PRIORITY_LOW)
            .setWhen(if (startedAt > 0) startedAt else System.currentTimeMillis())
            .setShowWhen(true)
            .setForegroundServiceBehavior(NotificationCompat.FOREGROUND_SERVICE_IMMEDIATE)
            .setContentIntent(openApp(context, kind))
            .setDeleteIntent(
                PendingIntent.getService(
                    context, ID, Intent(context, OperationService::class.java).setAction(OperationService.ACTION_DISMISSED),
                    PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
                ),
            )
            // Android 16 : mise en avant (puce dans la barre d'état, en tête du volet et sur l'écran de verrouillage).
            .setRequestPromotedOngoing(true)
            .setShortCriticalText(p.shortText)
        if (Build.VERSION.SDK_INT >= 36) {
            b.setStyle(
                NotificationCompat.ProgressStyle()
                    .setProgressSegments(p.segments.map { NotificationCompat.ProgressStyle.Segment(it.length).setColor(TEAL) })
                    .setProgressPoints(
                        p.points.map { NotificationCompat.ProgressStyle.Point(it.position).setColor(if (it.ok) GREEN else RED) },
                    )
                    .setProgress(p.progress)
                    .setProgressIndeterminate(p.indeterminate)
                    .setStyledByProgress(true),
            )
        } else {
            b.setProgress(OperationProgress.MAX, p.progress, p.indeterminate)
        }
        return b.build()
    }

    /** Bilan : silencieux en cas de réussite, sur le canal des incidents en cas d'échec. */
    fun outcome(context: Context, o: OperationOutcome, server: String, kind: String, finishedAt: Long): Notification {
        val body = (listOf(o.text) + o.details).joinToString("\n")
        return NotificationCompat.Builder(context, if (o.ok) Notifications.CHANNEL_OPERATIONS else Notifications.CHANNEL_CRITICAL)
            .setSmallIcon(R.drawable.ic_stat_vigie)
            .setColor(if (o.ok) GREEN else RED)
            .setContentTitle(o.title)
            .setContentText(o.text)
            .setSubText(server.takeIf { it.isNotBlank() })
            .setStyle(NotificationCompat.BigTextStyle().bigText(body))
            .setCategory(if (o.ok) NotificationCompat.CATEGORY_STATUS else NotificationCompat.CATEGORY_ERROR)
            .setPriority(if (o.ok) NotificationCompat.PRIORITY_LOW else NotificationCompat.PRIORITY_HIGH)
            .setWhen(if (finishedAt > 0) finishedAt else System.currentTimeMillis())
            .setShowWhen(true)
            .setAutoCancel(true)
            .setContentIntent(openApp(context, kind))
            .build()
    }

    /** Suivi interrompu (délai dépassé, agent redémarré) : le résultat est dans l'appli. */
    fun interrupted(context: Context, title: String, server: String, kind: String): Notification =
        NotificationCompat.Builder(context, Notifications.CHANNEL_OPERATIONS)
            .setSmallIcon(R.drawable.ic_stat_vigie)
            .setColor(TEAL)
            .setContentTitle(title)
            .setContentText("Suivi interrompu : ouvre Vigie pour voir le résultat.")
            .setSubText(server.takeIf { it.isNotBlank() })
            .setCategory(NotificationCompat.CATEGORY_STATUS)
            .setPriority(NotificationCompat.PRIORITY_LOW)
            .setAutoCancel(true)
            .setContentIntent(openApp(context, kind))
            .build()

    /** Ouvre l'appli, sur l'onglet Conteneurs pour les opérations qui les concernent. */
    private fun openApp(context: Context, kind: String): PendingIntent {
        val intent = Intent(context, MainActivity::class.java).apply {
            flags = Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP
            if (kind in setOf("update", "pull", "stack", "batch", "auto", "rollback")) data = "vigie://open/containers".toUri()
        }
        return PendingIntent.getActivity(context, ID, intent, PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
    }
}
