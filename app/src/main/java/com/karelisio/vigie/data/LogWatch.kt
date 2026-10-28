package com.karelisio.vigie.data

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/** Suivi d'une alerte sur les logs par l'agent (0.4 et plus). */
@Serializable
data class LogWatchStatus(
    @SerialName("rule_id") val ruleId: String = "",
    val container: String = "",
    /** Logs du conteneur en cours de lecture. */
    val active: Boolean = false,
    @SerialName("matches_24h") val matches24h: Int = 0,
    @SerialName("last_match") val lastMatch: Long = 0,
    @SerialName("last_line") val lastLine: String = "",
)

private fun Repository.server(): Server = servers.active ?: throw ApiException(0, "Aucun serveur configuré")

suspend fun Repository.logWatch(): List<LogWatchStatus> = api.get(server(), "/api/v1/logwatch")

/**
 * Modifie les alertes sur les logs à partir des réglages actuels de l'agent, sans toucher aux autres.
 * L'agent valide les motifs : son message d'erreur remonte tel quel.
 */
suspend fun Repository.updateLogRules(change: (List<LogRule>) -> List<LogRule>): AgentSettings {
    val current = settings()
    return saveSettings(current.copy(logRules = change(current.logRules)))
}
