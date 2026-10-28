package com.karelisio.vigie.wear

import com.google.android.gms.tasks.Task
import com.google.android.gms.tasks.Tasks
import com.google.android.gms.wearable.WearableListenerService
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeoutOrNull
import kotlinx.serialization.json.Json

/**
 * Reçoit l'accès au serveur envoyé par l'appli du téléphone. La Data Layer ne relaie que les messages
 * d'une appli de même identifiant et de même signature : personne d'autre ne peut configurer la montre.
 */
class PhoneListenerService : WearableListenerService() {
    private val json = Json { ignoreUnknownKeys = true }

    override fun onRequest(nodeId: String, path: String, request: ByteArray): Task<ByteArray>? = when (path) {
        Protocol.PATH_SERVER -> {
            val server = runCatching { json.decodeFromString(WatchServer.serializer(), request.decodeToString()) }.getOrNull()
            when {
                server == null -> reply("Données illisibles")
                !server.url.startsWith("https://") && !server.url.startsWith("http://") -> reply("Adresse invalide")
                server.token.length < 16 -> reply("Token invalide")
                else -> {
                    WatchData.setServer(this, server)
                    // Premières données tout de suite, pour l'appli, la tuile et la complication.
                    val context = applicationContext
                    WatchData.background.launch { withTimeoutOrNull(20_000) { WatchData.refresh(context, WatchData.Surface.App) } }
                    reply(OK)
                }
            }
        }
        Protocol.PATH_FORGET -> {
            WatchData.setServer(this, null)
            reply(OK)
        }
        else -> null
    }

    private fun reply(text: String): Task<ByteArray> = Tasks.forResult(text.encodeToByteArray())

    private companion object {
        const val OK = "ok"
    }
}
