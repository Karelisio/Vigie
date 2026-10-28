package com.karelisio.vigie.data

import android.content.Context
import kotlinx.serialization.Serializable
import java.io.File

/** Dernier état connu d'un serveur, conservé sur disque pour l'affichage hors ligne et le widget. */
@Serializable
data class CachedState(
    val overview: Overview? = null,
    val system: SystemSnapshot? = null,
    val containers: List<Container> = emptyList(),
    val checks: List<CheckStatus> = emptyList(),
    val thresholds: Thresholds = Thresholds(),
    val updatedAt: Long = 0,
) {
    fun toLive(): LiveState = LiveState(
        connection = Connection.Idle,
        overview = overview,
        system = system ?: overview?.system,
        containers = containers,
        checks = checks,
        thresholds = thresholds,
        updatedAt = updatedAt,
        loaded = overview != null,
    )

    companion object {
        fun of(s: LiveState) = CachedState(s.overview, s.system, s.containers, s.checks, s.thresholds, s.updatedAt)
    }
}

class StateCache(context: Context) {
    private val dir = File(context.filesDir, "cache").apply { mkdirs() }

    private fun file(serverId: String) = File(dir, "state-$serverId.json")

    fun read(serverId: String): CachedState? = runCatching {
        file(serverId).takeIf { it.exists() }?.readText()?.let { VigieJson.decodeFromString<CachedState>(it) }
    }.getOrNull()

    @Synchronized
    fun write(serverId: String, state: CachedState) {
        if (state.updatedAt == 0L) return
        runCatching {
            val tmp = File(dir, "state-$serverId.tmp")
            tmp.writeText(VigieJson.encodeToString(CachedState.serializer(), state))
            tmp.renameTo(file(serverId))
        }
    }

    fun delete(serverId: String) {
        file(serverId).delete()
    }
}
