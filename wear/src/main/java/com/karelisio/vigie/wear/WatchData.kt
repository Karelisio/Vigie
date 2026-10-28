package com.karelisio.vigie.wear

import android.content.ComponentName
import android.content.Context
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import androidx.core.content.edit
import androidx.wear.tiles.TileService
import androidx.wear.watchface.complications.datasource.ComplicationDataSourceUpdateRequester
import com.karelisio.vigie.wear.complication.StatusComplicationService
import com.karelisio.vigie.wear.tile.StatusTileService
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.Json

/** Données de la montre : accès au serveur, dernier état connu et son rafraîchissement. */
object WatchData {
    /** Fraîcheur visée de la tuile et de la complication. */
    const val FRESHNESS_MS = 15 * 60_000L

    private const val FILE = "vigie_wear_state"
    private const val KEY_SNAPSHOT = "snapshot"

    private val json = Json { ignoreUnknownKeys = true }
    private val mutex = Mutex()
    private val _state = MutableStateFlow<Pair<WatchServer?, Snapshot>?>(null)

    /** Travaux qui doivent survivre au service qui les lance (réception d'un nouvel accès). */
    val background = CoroutineScope(SupervisorJob() + Dispatchers.IO)

    /** Accès et dernier état, pour l'écran de l'appli. */
    val state: StateFlow<Pair<WatchServer?, Snapshot>?> = _state.asStateFlow()

    /** Surfaces à prévenir après une mise à jour (pas celle qui la demande). */
    enum class Surface { App, Tile, Complication }

    suspend fun load(context: Context): Pair<WatchServer?, Snapshot> = withContext(Dispatchers.IO) {
        (SecureStore(context).load() to cached(context)).also { _state.value = it }
    }

    fun cached(context: Context): Snapshot = prefs(context).getString(KEY_SNAPSHOT, null)
        ?.let { runCatching { json.decodeFromString(Snapshot.serializer(), it) }.getOrNull() }
        ?: Snapshot()

    /**
     * Interroge l'agent (`/api/v1/overview` et `/api/v1/system`), sauf si les données ont moins de
     * [maxAgeMs]. En cas d'échec, l'état précédent est gardé avec la raison de l'échec.
     */
    suspend fun refresh(context: Context, from: Surface, maxAgeMs: Long = 0): Snapshot = mutex.withLock {
        withContext(Dispatchers.IO) {
            val server = SecureStore(context).load()
            val previous = cached(context)
            val now = System.currentTimeMillis()
            if (server != null && maxAgeMs > 0 && previous.error == null && now - previous.fetchedAt < maxAgeMs) {
                _state.value = server to previous
                return@withContext previous
            }
            val next = when {
                server == null -> previous.copy(error = FetchError.NotConfigured)
                else -> try {
                    coroutineScope {
                        val system = async { runCatching { AgentClient.system(server) }.getOrNull() }
                        val overview = AgentClient.overview(server)
                        Snapshot(overview = overview, system = system.await() ?: overview.system, fetchedAt = now)
                    }
                } catch (e: FetchException) {
                    val error = if (e.error == FetchError.Unreachable && !online(context)) FetchError.NoNetwork else e.error
                    previous.copy(error = error, errorDetail = e.detail.take(80))
                }
            }
            prefs(context).edit { putString(KEY_SNAPSHOT, json.encodeToString(Snapshot.serializer(), next)) }
            _state.value = server to next
            notifySurfaces(context, from)
            next
        }
    }

    /** Accès reçu du téléphone : les anciennes données ne correspondent plus forcément au serveur. */
    fun setServer(context: Context, server: WatchServer?) {
        val store = SecureStore(context)
        if (server == null) store.clear() else store.save(server)
        prefs(context).edit { remove(KEY_SNAPSHOT) }
        _state.value = server to Snapshot()
        notifySurfaces(context, Surface.App)
    }

    private fun notifySurfaces(context: Context, from: Surface) {
        if (from != Surface.Tile) runCatching { TileService.getUpdater(context).requestUpdate(StatusTileService::class.java) }
        if (from != Surface.Complication) {
            runCatching {
                ComplicationDataSourceUpdateRequester
                    .create(context, ComponentName(context, StatusComplicationService::class.java))
                    .requestUpdateAll()
            }
        }
    }

    private fun online(context: Context): Boolean {
        val cm = context.getSystemService(ConnectivityManager::class.java) ?: return true
        val caps = cm.getNetworkCapabilities(cm.activeNetwork) ?: return false
        return caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
    }

    private fun prefs(context: Context) = context.applicationContext.getSharedPreferences(FILE, Context.MODE_PRIVATE)
}
