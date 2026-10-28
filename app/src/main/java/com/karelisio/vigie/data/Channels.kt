package com.karelisio.vigie.data

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * Canal d'alerte secondaire configuré sur l'agent (0.5 et plus). Les secrets (jeton du bot, webhook,
 * jeton ntfy) n'arrivent que masqués ; renvoyés vides ou masqués, l'agent garde ceux qu'il connaît.
 */
@Serializable
data class AlertChannel(
    /** telegram, discord ou ntfy. */
    val type: String = "",
    val enabled: Boolean = false,
    /** all : toutes les alertes, critical : critiques seulement. */
    @SerialName("min_level") val minLevel: String = "all",
    @SerialName("bot_token") val botToken: String = "",
    @SerialName("chat_id") val chatId: String = "",
    @SerialName("webhook_url") val webhookUrl: String = "",
    val server: String = "",
    val topic: String = "",
    val token: String = "",
)

@Serializable
data class ChannelsConfig(val channels: List<AlertChannel> = emptyList())

@Serializable
data class ChannelTestRequest(val type: String)

@Serializable
data class ChannelTestResult(val type: String = "", val ok: Boolean = false, val error: String = "")

@Serializable
data class ChannelTestResponse(val results: List<ChannelTestResult> = emptyList())

private fun Repository.active(): Server = servers.active ?: throw ApiException(0, "Aucun serveur configuré")

suspend fun Repository.channels(): ChannelsConfig = api.get(active(), "/api/v1/channels")

/** Remplace la configuration des canaux ; l'agent valide et renvoie son message d'erreur tel quel. */
suspend fun Repository.saveChannels(config: ChannelsConfig): ChannelsConfig = api.put(active(), "/api/v1/channels", config)

/** Message d'essai au canal [type], même désactivé. */
suspend fun Repository.testChannel(type: String): ChannelTestResult =
    api.post<ChannelTestRequest, ChannelTestResponse>(active(), "/api/v1/channels/test", ChannelTestRequest(type))
        .results.firstOrNull() ?: ChannelTestResult(type, error = "Aucune réponse de l'agent")
