package com.karelisio.vigie.data

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.channels.BufferOverflow
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonPrimitive
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener

enum class Connection { Idle, Connecting, Live, Offline, Unauthorized }

/** État temps réel d'un serveur, alimenté par le WebSocket (ou par le cache hors ligne). */
data class LiveState(
    val connection: Connection = Connection.Idle,
    val error: String? = null,
    val overview: Overview? = null,
    val system: SystemSnapshot? = null,
    val containers: List<Container> = emptyList(),
    val checks: List<CheckStatus> = emptyList(),
    val thresholds: Thresholds = Thresholds(),
    /** Dernière donnée reçue (ms) ; 0 si jamais. */
    val updatedAt: Long = 0,
    /** Derniers échantillons système, pour les mini-graphiques temps réel. */
    val recent: List<SystemSnapshot> = emptyList(),
    val loaded: Boolean = false,
) {
    val isLive get() = connection == Connection.Live
    val status get() = overview?.status ?: "unknown"
}

sealed interface LiveEvent {
    data class Docker(val event: DockerEvent) : LiveEvent
    data class NewAlert(val alert: Alert) : LiveEvent
}

/**
 * Connexion WebSocket à `/api/v1/ws` avec reconnexion progressive.
 * Démarrée quand l'appli est au premier plan, arrêtée en arrière-plan.
 */
class LiveSession(
    val server: Server,
    private val api: Api,
    private val scope: CoroutineScope,
    initial: LiveState,
    private val onSnapshot: (LiveState) -> Unit,
) {
    private val _state = MutableStateFlow(initial)
    val state: StateFlow<LiveState> = _state.asStateFlow()

    private val _events = MutableSharedFlow<LiveEvent>(extraBufferCapacity = 32, onBufferOverflow = BufferOverflow.DROP_OLDEST)
    val events: SharedFlow<LiveEvent> = _events.asSharedFlow()

    @Volatile private var socket: WebSocket? = null
    private var loop: Job? = null
    @Volatile private var attempt = 0
    private var lastPersist = 0L

    fun start() {
        if (loop?.isActive == true) return
        loop = scope.launch {
            while (true) {
                if (socket == null) connect()
                delay(1000)
            }
        }
    }

    fun stop() {
        loop?.cancel()
        loop = null
        socket?.close(1000, null)
        socket = null
        _state.update { if (it.connection == Connection.Unauthorized) it else it.copy(connection = Connection.Idle) }
        onSnapshot(_state.value)
    }

    /** Force une reconnexion immédiate (tirer pour rafraîchir). */
    fun reconnect() {
        attempt = 0
        socket?.cancel()
        socket = null
        start()
    }

    private suspend fun connect() {
        if (_state.value.connection == Connection.Unauthorized && attempt > 0) {
            delay(30_000)
        } else if (attempt > 0) {
            delay(BACKOFF[minOf(attempt - 1, BACKOFF.lastIndex)])
        }
        attempt++
        _state.update { it.copy(connection = if (it.connection == Connection.Live) Connection.Live else Connection.Connecting) }
        socket = api.webSocket(server, "/api/v1/ws", Listener())
    }

    private inner class Listener : WebSocketListener() {
        override fun onOpen(webSocket: WebSocket, response: Response) {
            if (webSocket !== socket) return
            attempt = 0
            _state.update { it.copy(connection = Connection.Live, error = null) }
        }

        override fun onMessage(webSocket: WebSocket, text: String) {
            if (webSocket !== socket) return
            runCatching { handle(text) }
        }

        override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
            webSocket.close(1000, null)
        }

        override fun onClosed(webSocket: WebSocket, code: Int, reason: String) = drop(webSocket, null)

        override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
            val code = response?.code ?: 0
            val msg = if (code != 0) Api.describeHttp(code, "") else Api.humanize(t).message
            if (code == 401) {
                _state.update { it.copy(connection = Connection.Unauthorized, error = msg) }
                if (webSocket === socket) socket = null
                onSnapshot(_state.value)
                return
            }
            drop(webSocket, msg)
        }
    }

    private fun drop(ws: WebSocket, error: String?) {
        if (ws !== socket) return
        socket = null
        _state.update { it.copy(connection = Connection.Offline, error = error ?: it.error) }
        onSnapshot(_state.value)
    }

    private fun handle(text: String) {
        val obj = VigieJson.parseToJsonElement(text) as? JsonObject ?: return
        val type = obj["type"]?.jsonPrimitive?.content ?: return
        val data: JsonElement = obj["data"] ?: return
        val now = System.currentTimeMillis()
        when (type) {
            "hello" -> {
                val h = VigieJson.decodeFromJsonElement(Hello.serializer(), data)
                _state.update { it.copy(thresholds = h.thresholds) }
            }
            "overview" -> {
                val o = VigieJson.decodeFromJsonElement(Overview.serializer(), data)
                _state.update { it.copy(overview = o, updatedAt = now, loaded = true) }
            }
            "system" -> {
                val s = VigieJson.decodeFromJsonElement(SystemSnapshot.serializer(), data)
                _state.update { st ->
                    st.copy(
                        system = s,
                        recent = (st.recent + s).takeLast(RECENT),
                        updatedAt = now,
                        overview = st.overview?.copy(system = s),
                    )
                }
            }
            "containers" -> {
                val list = VigieJson.decodeFromJsonElement(kotlinx.serialization.builtins.ListSerializer(Container.serializer()), data)
                _state.update { it.copy(containers = list, updatedAt = now) }
            }
            "checks" -> {
                val list = VigieJson.decodeFromJsonElement(kotlinx.serialization.builtins.ListSerializer(CheckStatus.serializer()), data)
                _state.update { it.copy(checks = list, updatedAt = now) }
            }
            "event" -> _events.tryEmit(LiveEvent.Docker(VigieJson.decodeFromJsonElement(DockerEvent.serializer(), data)))
            "alert" -> _events.tryEmit(LiveEvent.NewAlert(VigieJson.decodeFromJsonElement(Alert.serializer(), data)))
        }
        if (now - lastPersist > PERSIST_EVERY) {
            lastPersist = now
            onSnapshot(_state.value)
        }
    }

    /** Remplace localement un conteneur (retour immédiat après une action). */
    fun patchContainer(id: String, transform: (Container) -> Container) {
        _state.update { st -> st.copy(containers = st.containers.map { if (it.id == id) transform(it) else it }) }
    }

    fun setChecks(list: List<CheckStatus>) = _state.update { it.copy(checks = list) }

    private companion object {
        const val RECENT = 90
        const val PERSIST_EVERY = 15_000L
        val BACKOFF = longArrayOf(1_000, 2_000, 4_000, 8_000, 15_000, 30_000)
    }
}
