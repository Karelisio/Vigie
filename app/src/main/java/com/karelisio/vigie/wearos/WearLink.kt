package com.karelisio.vigie.wearos

import android.content.Context
import androidx.core.content.edit
import com.google.android.gms.wearable.CapabilityClient
import com.google.android.gms.wearable.Wearable
import com.karelisio.vigie.data.Repository
import com.karelisio.vigie.data.Server
import com.karelisio.vigie.data.VigieJson
import kotlinx.coroutines.tasks.await
import kotlinx.coroutines.withTimeout
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import java.io.IOException

/** État de la montre vu du téléphone. */
sealed interface WatchStatus {
    data object Checking : WatchStatus

    /** Pas d'appli compagnon Wear OS (ni services Google pour les montres) sur ce téléphone. */
    data object Unavailable : WatchStatus

    /** Aucune montre connectée en ce moment. */
    data object NoWatch : WatchStatus

    /** Montre connectée, sans l'appli Vigie. */
    data class NotInstalled(val watch: String) : WatchStatus

    /** Appli Vigie installée sur [watch]. */
    data class Ready(val watch: String, val nodeIds: List<String>) : WatchStatus
}

/** Accès envoyé à la montre (même format que `WatchServer` dans le module `:wear`). */
@Serializable
data class WatchPayload(
    val name: String,
    val url: String,
    val token: String,
    @SerialName("read_only") val readOnly: Boolean,
)

/**
 * Lien avec l'appli Vigie de la montre par la Data Layer (play-services-wearable). Les messages ne
 * sont délivrés qu'à une appli de même identifiant et de même signature, et ne sont pas conservés
 * par les services Google (contrairement aux DataItem) : le token ne transite qu'une fois.
 */
class WearLink(context: Context) {
    private val context = context.applicationContext
    private val prefs = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)

    suspend fun status(): WatchStatus {
        val nodes = try {
            Wearable.getNodeClient(context).connectedNodes.await()
        } catch (_: Exception) {
            // ApiException API_UNAVAILABLE : pas d'appli Wear OS sur le téléphone.
            return WatchStatus.Unavailable
        }
        if (nodes.isEmpty()) return WatchStatus.NoWatch
        val capable = runCatching {
            Wearable.getCapabilityClient(context).getCapability(CAPABILITY, CapabilityClient.FILTER_REACHABLE).await().nodes
        }.getOrDefault(emptySet())
        if (capable.isEmpty()) return WatchStatus.NotInstalled(nodes.first().displayName)
        return WatchStatus.Ready(capable.joinToString { it.displayName }, capable.map { it.id })
    }

    /**
     * Envoie l'accès au serveur actif. Avec [dedicated], un accès en lecture seule « Montre » est créé
     * pour l'occasion (et l'ancien accès de la montre révoqué) ; sinon le token du serveur est envoyé.
     */
    suspend fun send(repository: Repository, server: Server, nodeIds: List<String>, dedicated: Boolean) {
        val created = if (dedicated) repository.createSharedAccess(ACCESS_LABEL) else null
        val payload = WatchPayload(server.name, server.baseUrl, created?.token ?: server.token, readOnly = created != null || server.readOnly)
        try {
            deliver(nodeIds, PATH_SERVER, VigieJson.encodeToString(WatchPayload.serializer(), payload).encodeToByteArray())
        } catch (e: Exception) {
            // Accès inutilisé : on ne le laisse pas traîner.
            created?.let { runCatching { repository.revokeSharedAccess(it.id) } }
            throw e
        }
        val key = KEY_ACCESS + server.id
        val previous = prefs.getString(key, null)
        prefs.edit { if (created != null) putString(key, created.id) else remove(key) }
        if (previous != null && previous != created?.id) runCatching { repository.revokeSharedAccess(previous) }
    }

    /** Retire l'accès de la montre et révoque son accès dédié. */
    suspend fun forget(repository: Repository, server: Server, nodeIds: List<String>) {
        deliver(nodeIds, PATH_FORGET, ByteArray(0))
        val key = KEY_ACCESS + server.id
        prefs.getString(key, null)?.let { id -> runCatching { repository.revokeSharedAccess(id) } }
        prefs.edit { remove(key) }
    }

    /** Vrai si la montre utilise un accès dédié de ce serveur. */
    fun hasDedicatedAccess(server: Server): Boolean = prefs.contains(KEY_ACCESS + server.id)

    private suspend fun deliver(nodeIds: List<String>, path: String, data: ByteArray) {
        val client = Wearable.getMessageClient(context)
        var delivered = 0
        var error: String? = null
        for (id in nodeIds) {
            val reply = runCatching { withTimeout(TIMEOUT_MS) { client.sendRequest(id, path, data).await() }.decodeToString() }
                .getOrElse { "montre injoignable" }
            if (reply == OK) delivered++ else error = reply
        }
        if (delivered == 0) throw IOException(error ?: "montre injoignable")
    }

    private companion object {
        const val CAPABILITY = "vigie_wear"
        const val PATH_SERVER = "/vigie/server"
        const val PATH_FORGET = "/vigie/forget"
        const val OK = "ok"
        const val ACCESS_LABEL = "Montre"
        const val PREFS = "vigie_wear_link"
        const val KEY_ACCESS = "access_"
        const val TIMEOUT_MS = 15_000L
    }
}
