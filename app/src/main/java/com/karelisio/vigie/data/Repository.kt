package com.karelisio.vigie.data

import android.os.Build
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.callbackFlow
import kotlinx.coroutines.flow.emptyFlow
import kotlinx.coroutines.flow.flatMapLatest
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import kotlinx.coroutines.channels.awaitClose
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener

/** Événement du flux de logs. */
sealed interface LogEvent {
    data class Lines(val lines: List<LogLine>) : LogEvent
    data class Ended(val reason: String) : LogEvent
    data class Failed(val message: String) : LogEvent
    data object Connected : LogEvent
}

@OptIn(ExperimentalCoroutinesApi::class)
class Repository(
    val servers: ServerStore,
    val api: Api,
    private val cache: StateCache,
    private val scope: CoroutineScope,
    /** Appelé quand l'état d'un serveur est persisté (widget, tuile). */
    private val onPersisted: (Server, CachedState) -> Unit,
) {
    private val sessions = mutableMapOf<String, LiveSession>()
    private val _foreground = MutableStateFlow(false)

    private val activeSession: StateFlow<LiveSession?> = servers.activeId
        .map { id -> id?.let { servers.get(it) }?.let { sessionFor(it) } }
        .stateIn(scope, SharingStarted.Eagerly, servers.active?.let { sessionFor(it) })

    val live: StateFlow<LiveState> = activeSession
        .flatMapLatest { it?.state ?: flowOf(LiveState()) }
        .stateIn(scope, SharingStarted.Eagerly, activeSession.value?.state?.value ?: LiveState())

    val events: Flow<LiveEvent> = activeSession.flatMapLatest { it?.events ?: emptyFlow() }

    val activeServer: Server? get() = servers.active

    init {
        scope.launch {
            activeSession.collect { session ->
                sessions.values.filter { it !== session }.forEach { it.stop() }
                if (_foreground.value) session?.start()
            }
        }
    }

    @Synchronized
    private fun sessionFor(server: Server): LiveSession {
        val existing = sessions[server.id]
        if (existing != null && existing.server == server) return existing
        existing?.stop()
        val initial = cache.read(server.id)?.toLive() ?: LiveState()
        return LiveSession(server, api, scope, initial) { st ->
            val c = CachedState.of(st)
            cache.write(server.id, c)
            onPersisted(server, c)
        }.also { sessions[server.id] = it }
    }

    fun setForeground(fg: Boolean) {
        _foreground.value = fg
        val s = activeSession.value ?: return
        if (fg) s.start() else s.stop()
    }

    fun reconnect() = activeSession.value?.reconnect()

    fun cached(serverId: String): CachedState? = cache.read(serverId)

    fun forget(server: Server) {
        sessions.remove(server.id)?.stop()
        cache.delete(server.id)
        servers.remove(server.id)
    }

    private fun requireServer(): Server = servers.active ?: throw ApiException(0, "Aucun serveur configuré")

    // ---- Requêtes ponctuelles ----

    suspend fun testConnection(server: Server): ServerInfo = api.get(server, "/api/v1/info")

    suspend fun info(): ServerInfo = api.get(requireServer(), "/api/v1/info")

    /** Vue d'ensemble pour le widget et la tuile (hors WebSocket), mise en cache. */
    suspend fun fetchOverview(server: Server): CachedState {
        val ov: Overview = api.get(server, "/api/v1/overview")
        val prev = cache.read(server.id) ?: CachedState()
        // Conteneurs (favoris du widget) et services (widget Services) : facultatifs.
        val containers = runCatching { api.get<List<Container>>(server, "/api/v1/containers") }.getOrNull() ?: prev.containers
        val checks = runCatching { api.get<List<CheckStatus>>(server, "/api/v1/checks") }.getOrNull() ?: prev.checks
        val c = prev.copy(
            overview = ov, system = ov.system ?: prev.system, containers = containers, checks = checks,
            updatedAt = System.currentTimeMillis(),
        )
        cache.write(server.id, c)
        onPersisted(server, c)
        return c
    }

    suspend fun systemHistory(range: String): History<SysPoint> = api.get(requireServer(), "/api/v1/system/history?range=$range")

    suspend fun container(id: String): ContainerDetail = api.get(requireServer(), "/api/v1/containers/$id")

    suspend fun containerHistory(id: String, range: String): History<CtrPoint> =
        api.get(requireServer(), "/api/v1/containers/$id/history?range=$range")

    suspend fun action(id: String, action: String): ActionResult {
        val res: ActionResult = api.postEmpty(requireServer(), "/api/v1/containers/$id/$action")
        activeSession.value?.patchContainer(id) { c ->
            when (action) {
                "stop" -> c.copy(state = "exited", stoppedByUser = true, problem = "", cpu = 0.0)
                else -> c.copy(state = res.state.ifEmpty { "running" }, stoppedByUser = false, problem = "")
            }
        }
        return res
    }

    suspend fun logs(id: String, tail: Int = 500): List<LogLine> =
        api.get<LogsResponse>(requireServer(), "/api/v1/containers/$id/logs?tail=$tail").lines

    /** Flux de logs en direct (WebSocket). */
    fun logStream(id: String, tail: Int = 500): Flow<LogEvent> = callbackFlow {
        val server = requireServer()
        val ws = api.webSocket(server, "/api/v1/containers/$id/logs/stream?tail=$tail", object : WebSocketListener() {
            override fun onOpen(webSocket: WebSocket, response: Response) {
                trySend(LogEvent.Connected)
            }

            override fun onMessage(webSocket: WebSocket, text: String) {
                val obj = runCatching { VigieJson.parseToJsonElement(text) as JsonObject }.getOrNull() ?: return
                when (obj["type"]?.jsonPrimitive?.content) {
                    "lines" -> obj["data"]?.let {
                        trySend(LogEvent.Lines(VigieJson.decodeFromJsonElement(ListSerializer(LogLine.serializer()), it)))
                    }
                    "end" -> trySend(LogEvent.Ended((obj["data"] as? JsonObject)?.get("reason")?.jsonPrimitive?.content ?: "fin du flux"))
                }
            }

            override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                val msg = response?.let { Api.describeHttp(it.code, "") } ?: Api.humanize(t).message ?: "Connexion perdue"
                trySend(LogEvent.Failed(msg))
                channel.close()
            }

            override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
                channel.close()
            }
        })
        awaitClose { ws.close(1000, null) }
    }

    // ---- Checks ----

    suspend fun checks(): List<CheckStatus> =
        api.get<List<CheckStatus>>(requireServer(), "/api/v1/checks").also { activeSession.value?.setChecks(it) }

    suspend fun createCheck(input: CheckInput) {
        api.post<CheckInput, kotlinx.serialization.json.JsonElement>(requireServer(), "/api/v1/checks", input)
        checks()
    }

    suspend fun updateCheck(id: Long, input: CheckInput) {
        api.put<CheckInput, kotlinx.serialization.json.JsonElement>(requireServer(), "/api/v1/checks/$id", input)
        checks()
    }

    suspend fun deleteCheck(id: Long) {
        api.delete<Unit>(requireServer(), "/api/v1/checks/$id", null)
        checks()
    }

    suspend fun runCheck(id: Long): CheckStatus = api.postEmpty<CheckStatus>(requireServer(), "/api/v1/checks/$id/run").also { checks() }

    suspend fun checkHistory(id: Long, range: String, buckets: Int): CheckHistory =
        api.get(requireServer(), "/api/v1/checks/$id/history?range=$range&buckets=$buckets")

    // ---- Réglages, sourdine, chronologie, Oracle, images ----

    suspend fun settings(): AgentSettings = api.get(requireServer(), "/api/v1/settings")

    suspend fun saveSettings(s: AgentSettings): AgentSettings {
        // Un agent plus ancien refuse les champs qu'il ne connaît pas : on n'envoie que ceux de sa version.
        val body = VigieJson.encodeToJsonElement(AgentSettings.serializer(), s).jsonObject
        val known = when {
            !live.value.updatesEnabled -> SETTINGS_0_2
            !live.value.stackActions -> SETTINGS_0_2 + "prune_images"
            versionAtLeast(live.value.agentVersion, 0, 4) -> body.keys
            else -> SETTINGS_0_2 + "prune_images"
        }
        return api.put(requireServer(), "/api/v1/settings", JsonObject(body.filterKeys { it in known }))
    }

    suspend fun mute(id: String, muted: Boolean): AgentSettings {
        val res: AgentSettings = api.post(requireServer(), "/api/v1/containers/$id/mute", MuteRequest(muted))
        activeSession.value?.patchContainer(id) { it.copy(muted = muted) }
        return res
    }

    suspend fun events(id: String, limit: Int = 80): List<TimelineEvent> =
        api.get(requireServer(), "/api/v1/containers/$id/events?limit=$limit")

    suspend fun oracle(): OracleReport = api.get(requireServer(), "/api/v1/oracle")

    suspend fun checkImages(): Map<String, ImageStatus> = api.postEmpty(requireServer(), "/api/v1/images/check")

    // ---- Alertes, journal, notifications ----

    // ---- Mises à jour d'images et actions de stack ----

    /** Message à afficher dans l'appli (bas de l'écran). */
    fun notifyLocal(text: String) {
        activeSession.value?.emit(LiveEvent.Message(text))
    }

    private fun started(job: AgentJob): AgentJob = job.also { activeSession.value?.patchJob(it) }

    suspend fun updateContainer(id: String, pullOnly: Boolean = false): AgentJob =
        started(api.post(requireServer(), "/api/v1/containers/$id/update", UpdateRequest(pullOnly)))

    suspend fun stackAction(project: String, action: String): AgentJob =
        started(api.postEmpty(requireServer(), "/api/v1/stacks/${android.net.Uri.encode(project)}/$action"))

    suspend fun updateAll(): AgentJob = started(api.postEmpty(requireServer(), "/api/v1/updates/apply"))

    suspend fun updateAgent(): AgentJob = started(api.postEmpty(requireServer(), "/api/v1/agent/update"))

    suspend fun alerts(limit: Int = 100): List<Alert> = api.get(requireServer(), "/api/v1/alerts?limit=$limit")

    suspend fun audit(limit: Int = 200): List<AuditEntry> = api.get(requireServer(), "/api/v1/audit?limit=$limit")

    suspend fun registerDevice(server: Server, token: String) {
        api.post<DeviceRegistration, kotlinx.serialization.json.JsonElement>(
            server, "/api/v1/devices", DeviceRegistration(token, deviceName()),
        )
    }

    suspend fun testPush(token: String?) {
        api.post<PushTest, kotlinx.serialization.json.JsonElement>(requireServer(), "/api/v1/devices/test", PushTest(token))
    }

    private fun deviceName() = listOf(Build.MANUFACTURER.replaceFirstChar { it.uppercase() }, Build.MODEL)
        .distinct().joinToString(" ")
}

/** Champs de réglages connus des agents 0.2.x (avant les mises à jour depuis l'appli). */
private val SETTINGS_0_2 = setOf(
    "cpu", "ram", "disk", "duration_min", "quiet_enabled", "quiet_start", "quiet_end",
    "muted", "image_updates", "oracle_watch", "cert_days",
)

/** Compare une version « X.Y.Z » (les versions de développement sont considérées récentes). */
internal fun versionAtLeast(version: String, major: Int, minor: Int): Boolean {
    val parts = version.removePrefix("v").split('.', '-').mapNotNull { it.toIntOrNull() }
    if (parts.size < 2) return version.isNotEmpty()
    return parts[0] > major || (parts[0] == major && parts[1] >= minor)
}
