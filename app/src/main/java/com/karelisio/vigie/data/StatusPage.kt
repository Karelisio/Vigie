package com.karelisio.vigie.data

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/** Élément de la page d'état : un service (check, [ref] = son identifiant) ou un conteneur ([ref] = son nom). */
@Serializable
data class StatusPageItem(val kind: String, val ref: String, val label: String) {
    companion object {
        const val CHECK = "check"
        const val CONTAINER = "container"
    }
}

/** Configuration de la page d'état publique (agent 0.6 et plus). */
@Serializable
data class StatusPageConfig(
    val enabled: Boolean = false,
    /** Identifiant secret de l'adresse, changé seulement par « Changer le lien ». */
    val slug: String = "",
    val title: String = "",
    val description: String = "",
    val items: List<StatusPageItem> = emptyList(),
    @SerialName("show_uptime") val showUptime: Boolean = true,
    /** Adresse publique complète, vide si VIGIE_PUBLIC_URL n'est pas défini sur l'agent. */
    val url: String = "",
) {
    /** Adresse à partager : celle de l'agent, sinon celle du serveur dans l'appli. */
    fun link(server: Server?): String =
        url.ifEmpty { server?.let { "${it.baseUrl}/status/$slug" }.orEmpty() }

    fun input() = StatusPageInput(enabled, title.trim(), description.trim(), items.map { it.copy(label = it.label.trim()) }, showUptime)
}

/** Ce que l'appli envoie : l'identifiant et l'adresse ne se modifient pas ainsi. */
@Serializable
data class StatusPageInput(
    val enabled: Boolean,
    val title: String,
    val description: String,
    val items: List<StatusPageItem>,
    @SerialName("show_uptime") val showUptime: Boolean,
)

private fun Repository.server(): Server = servers.active ?: throw ApiException(0, "Aucun serveur configuré")

suspend fun Repository.statusPage(): StatusPageConfig = api.get(server(), "/api/v1/statuspage")

suspend fun Repository.saveStatusPage(c: StatusPageConfig): StatusPageConfig = api.put(server(), "/api/v1/statuspage", c.input())

/** Nouvel identifiant secret : l'ancien lien cesse aussitôt de fonctionner. */
suspend fun Repository.regenerateStatusPage(): StatusPageConfig = api.postEmpty(server(), "/api/v1/statuspage/regenerate")
