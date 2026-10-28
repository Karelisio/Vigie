package com.karelisio.vigie.watch

import android.annotation.SuppressLint
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.edit
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.NetworkType
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import com.karelisio.vigie.MainActivity
import com.karelisio.vigie.R
import com.karelisio.vigie.appContainer
import com.karelisio.vigie.data.Api
import com.karelisio.vigie.data.Server
import com.karelisio.vigie.data.await
import com.karelisio.vigie.push.Notifications
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.Json
import okhttp3.OkHttpClient
import okhttp3.Request
import java.io.IOException
import java.util.concurrent.TimeUnit

/**
 * Surveillance des serveurs depuis le téléphone : si le VPS tombe, l'agent ne peut plus rien envoyer.
 * Réglage, état persistant de chaque serveur et planification du passage périodique.
 */
class ServerWatch(private val context: Context) {
    private val prefs = context.getSharedPreferences(FILE, Context.MODE_PRIVATE)
    private val json = Json { ignoreUnknownKeys = true }

    private val _enabled = MutableStateFlow(prefs.getBoolean(KEY_ENABLED, true))
    val enabled: StateFlow<Boolean> = _enabled.asStateFlow()

    private val _states = MutableStateFlow(load())

    /** État de chaque serveur, par identifiant. */
    val states: StateFlow<Map<String, WatchState>> = _states.asStateFlow()

    fun setEnabled(on: Boolean) {
        prefs.edit { putBoolean(KEY_ENABLED, on) }
        _enabled.value = on
        if (!on) save(emptyMap())
        schedule()
    }

    /** Planifie le passage toutes les 15 min, ou l'annule si la surveillance est coupée. */
    fun schedule() {
        val wm = WorkManager.getInstance(context)
        if (!_enabled.value) {
            wm.cancelUniqueWork(WORK)
            return
        }
        val req = PeriodicWorkRequestBuilder<ServerWatchWorker>(15, TimeUnit.MINUTES)
            .setConstraints(Constraints(requiredNetworkType = NetworkType.CONNECTED))
            .build()
        wm.enqueueUniquePeriodicWork(WORK, ExistingPeriodicWorkPolicy.KEEP, req)
    }

    /** Début de la panne signalée pour ce serveur, si elle est plus récente que ses dernières données. */
    fun outageSince(serverId: String, updatedAt: Long): Long? =
        _states.value[serverId]?.takeIf { it.alerted && it.since > updatedAt }?.since

    internal fun save(states: Map<String, WatchState>) {
        prefs.edit { putString(KEY_STATES, json.encodeToString(states)) }
        _states.value = states
    }

    private fun load(): Map<String, WatchState> =
        prefs.getString(KEY_STATES, null)?.let { runCatching { json.decodeFromString<Map<String, WatchState>>(it) }.getOrNull() }.orEmpty()

    private companion object {
        const val FILE = "vigie_watch"
        const val KEY_ENABLED = "enabled"
        const val KEY_STATES = "states"
        const val WORK = "vigie-watch"
    }
}

/** Passage périodique : `/healthz` de chaque serveur (sans token), un nouvel essai 20 s plus tard. */
class ServerWatchWorker(context: Context, params: WorkerParameters) : CoroutineWorker(context, params) {
    override suspend fun doWork(): Result {
        val c = appContainer
        val servers = c.servers.servers.value
        if (!c.watch.enabled.value || !c.prefs.current().notifications || servers.isEmpty()) {
            c.watch.save(emptyMap())
            return Result.success()
        }
        val client = c.api.http.newBuilder()
            .connectTimeout(WATCH_TIMEOUT_S, TimeUnit.SECONDS)
            .readTimeout(WATCH_TIMEOUT_S, TimeUnit.SECONDS)
            .callTimeout(WATCH_TIMEOUT_S, TimeUnit.SECONDS)
            .followRedirects(false)
            .followSslRedirects(false)
            .retryOnConnectionFailure(false)
            .build()
        val results = coroutineScope { servers.map { s -> async { s to checkTwice(client, s) } }.awaitAll() }
        // Avant de conclure à une panne, le téléphone doit lui-même avoir Internet.
        val online = results.none { it.second is WatchResult.Down } || internetReachable(client)
        val now = System.currentTimeMillis()
        val previous = c.watch.states.value
        val next = results.associate { (server, result) ->
            val (state, notice) = (previous[server.id] ?: WatchState()).next(if (online) result else WatchResult.Unknown, now)
            notice?.let { notify(applicationContext, server, it, now) }
            server.id to state
        }
        c.watch.save(next)
        // Widget et tuile affichent « Injoignable » pendant une panne signalée.
        if (next.mapValues { it.value.alerted } != previous.mapValues { it.value.alerted }) c.surfaces.updateSurfaces()
        return Result.success()
    }

    private suspend fun checkTwice(client: OkHttpClient, server: Server): WatchResult {
        val first = probe(client, server)
        if (first !is WatchResult.Down) return first
        delay(RETRY_DELAY_MS)
        return probe(client, server)
    }

    private suspend fun probe(client: OkHttpClient, server: Server): WatchResult = try {
        val req = Request.Builder()
            .url(server.baseUrl + "/healthz")
            .header("User-Agent", Api.USER_AGENT)
            .header("Accept", "application/json")
            .build()
        val response = client.newCall(req).await()
        withContext(Dispatchers.IO) { response.use { healthResult(it.code, it.peekBody(4096).string()) } }
    } catch (e: IOException) {
        WatchResult.Down(failureCause(e))
    } catch (_: IllegalArgumentException) {
        WatchResult.Down("Adresse du serveur invalide")
    }

    private suspend fun internetReachable(client: OkHttpClient): Boolean = INTERNET_PROBES.any { url ->
        try {
            client.newCall(Request.Builder().url(url).build()).await().use { it.code == 204 }
        } catch (_: IOException) {
            false
        }
    }

    @SuppressLint("MissingPermission") // vérifiée par canPost()
    private fun notify(context: Context, server: Server, notice: WatchNotice, now: Long) {
        if (!Notifications.canPost(context)) return
        // Même identifiant pour l'alerte et le retour : le retour remplace l'alerte.
        val id = "unreachable:${server.id}".hashCode()
        val open = PendingIntent.getActivity(
            context, id,
            Intent(context, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        val builder = when (notice) {
            is WatchNotice.Down -> NotificationCompat.Builder(context, Notifications.CHANNEL_UNREACHABLE)
                .setContentTitle("${server.name} ne répond plus")
                .setContentText(notice.cause)
                .setStyle(NotificationCompat.BigTextStyle().bigText(downMessage(notice.cause, notice.since, now)))
                .setColor(0xFFC62828.toInt())
                .setCategory(NotificationCompat.CATEGORY_ERROR)
                .setPriority(NotificationCompat.PRIORITY_HIGH)
                .setWhen(notice.since)
            is WatchNotice.Back -> NotificationCompat.Builder(context, Notifications.CHANNEL_RESOLVED)
                .setContentTitle("${server.name} répond de nouveau")
                .setContentText(backMessage(notice.since, notice.at))
                .setStyle(NotificationCompat.BigTextStyle().bigText(backMessage(notice.since, notice.at)))
                .setColor(0xFF1B873F.toInt())
                .setCategory(NotificationCompat.CATEGORY_STATUS)
                .setWhen(notice.at)
        }
        val n = builder
            .setSmallIcon(R.drawable.ic_stat_vigie)
            .setSubText(server.host)
            .setShowWhen(true)
            .setAutoCancel(true)
            .setContentIntent(open)
            .build()
        runCatching { NotificationManagerCompat.from(context).notify(id, n) }
    }

    private companion object {
        const val RETRY_DELAY_MS = 20_000L
        val INTERNET_PROBES = listOf(
            "https://connectivitycheck.gstatic.com/generate_204",
            "https://www.google.com/generate_204",
        )
    }
}
