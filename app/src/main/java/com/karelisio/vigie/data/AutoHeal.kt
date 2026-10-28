package com.karelisio.vigie.data

import kotlinx.serialization.Serializable

/** Réparation automatique effectuée par l'agent (0.5 et plus). */
@Serializable
data class AutoHealRepair(
    val ts: Long = 0,
    val container: String = "",
    /** unhealthy (healthcheck en échec), down (arrêt anormal) ou oom (mémoire saturée). */
    val reason: String = "",
    /** restart ou start. */
    val action: String = "",
    /** Le conteneur est revenu : en marche, et en bonne santé s'il a un healthcheck. */
    val ok: Boolean = false,
    val error: String? = null,
)

private fun Repository.server(): Server = servers.active ?: throw ApiException(0, "Aucun serveur configuré")

/** Réparations des dernières 24 h (50 au plus), la plus récente en premier. */
suspend fun Repository.autoHealHistory(): List<AutoHealRepair> = api.get(server(), "/api/v1/autoheal")

/** Raison d'une réparation, en clair. */
fun autoHealReason(reason: String): String = when (reason) {
    "unhealthy" -> "healthcheck en échec"
    "down" -> "arrêt anormal"
    "oom" -> "mémoire saturée"
    else -> reason
}

/** « Redémarré (healthcheck en échec) ». */
val AutoHealRepair.summary: String
    get() = (if (action == "restart") "Redémarré" else "Relancé") + " (${autoHealReason(reason)})"

/** Vrai si la réparation automatique s'applique au conteneur (jamais à l'agent lui-même). */
fun AgentSettings.autoHeals(c: Container): Boolean = autoHeal && !c.self && (autoHealAll || c.name in autoHealList)

/**
 * Active la réparation automatique pour le conteneur ou la lui retire. L'activer alors qu'elle est
 * coupée ne concerne que lui ; la retirer quand tous sont concernés revient à choisir les autres conteneurs de [all].
 */
fun AgentSettings.withAutoHeal(c: Container, on: Boolean, all: List<Container>): AgentSettings = when {
    on && autoHeal -> copy(autoHealList = (autoHealList + c.name).distinct().sorted())
    on -> copy(autoHeal = true, autoHealAll = false, autoHealList = listOf(c.name))
    autoHealAll -> copy(autoHealAll = false, autoHealList = all.filter { !it.self && it.name != c.name }.map { it.name }.sorted())
    else -> copy(autoHealList = autoHealList - c.name)
}
