package com.karelisio.vigie.data

import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.update
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/** Fournisseurs debrid pris en charge par l'agent : identifiant → nom affiché. */
val DebridProviders = linkedMapOf(
    "realdebrid" to "Real-Debrid",
    "alldebrid" to "AllDebrid",
    "torbox" to "TorBox",
    "premiumize" to "Premiumize",
)

/** Compte debrid et résultat de sa dernière vérification (agent 0.5 et plus). La clé n'arrive que masquée. */
@Serializable
data class DebridAccount(
    val id: String = "",
    val provider: String = "",
    val label: String = "",
    val key: String = "",
    val ok: Boolean = false,
    val premium: Boolean = false,
    /** Fin du premium (ms), 0 si inconnue. */
    val expires: Long = 0,
    @SerialName("days_left") val daysLeft: Int = 0,
    val username: String = "",
    val error: String = "",
    /** Clé refusée par le fournisseur. */
    val refused: Boolean = false,
    /** 0 : pas encore vérifié. */
    @SerialName("checked_at") val checkedAt: Long = 0,
) {
    val providerName: String get() = DebridProviders[provider] ?: provider

    /** Fournisseur, suivi du libellé s'il en diffère. */
    val title: String get() = if (label.isEmpty() || label == providerName) providerName else "$providerName · $label"
}

/** Vérification d'un service debrid : état (`up`, `degraded`, `down`) et latence. */
@Serializable
data class DebridPoint(val ts: Long = 0, @SerialName("s") val state: String = "", @SerialName("ms") val latencyMs: Long = 0)

/** Panne d'un service debrid ; [end] à 0 : en cours. */
@Serializable
data class DebridIncident(val start: Long = 0, val end: Long = 0, val cause: String = "")

/** État d'un fournisseur debrid, déduit des vérifications de ses comptes (agent 0.6 et plus). */
@Serializable
data class DebridService(
    val provider: String = "",
    val name: String = "",
    /** `up`, `degraded` ou `down`. */
    val state: String = "",
    val detail: String = "",
    @SerialName("latency_ms") val latencyMs: Long = 0,
    /** Début de l'état actuel (ms). */
    val since: Long = 0,
    @SerialName("checked_at") val checkedAt: Long = 0,
    /** Vérifications en panne d'affilée : à partir de 2, la panne est confirmée. */
    val failures: Int = 0,
    /** Disponibilité sur 24 h en %, -1 sans données. */
    @SerialName("uptime_24h") val uptime: Double = -1.0,
    val history: List<DebridPoint> = emptyList(),
    val incidents: List<DebridIncident> = emptyList(),
) {
    /** Ton de l'état pour les couleurs : ok, warning ou critical. */
    val tone: String get() = when (state) {
        "up" -> "ok"
        "degraded" -> "warning"
        "down" -> "critical"
        else -> "neutral"
    }

    /** Résumé court : « En ligne · 230 ms », « Ralenti : … », « En panne depuis 14:05 ». */
    fun summary(now: Long = System.currentTimeMillis()): String = when (state) {
        "up" -> "En ligne · $latencyMs ms"
        "degraded" -> "Ralenti" + detail.takeIf { it.isNotEmpty() }?.let { " : $it" }.orEmpty()
        "down" -> "En panne depuis " + if (now - since < 20 * 3_600_000L) Fmt.time(since) else Fmt.dateTime(since)
        else -> "En attente de vérification"
    }

    /** Historique de 24 h regroupé en [count] barres (une panne compte comme un échec). */
    fun buckets(now: Long, count: Int = 48): List<UptimeBucket> {
        val span = 24 * 3_600_000L
        val start = now - span
        val step = span / count
        val totals = IntArray(count)
        val oks = IntArray(count)
        history.forEach { p ->
            if (p.ts in start..now) {
                val i = ((p.ts - start) / step).toInt().coerceIn(0, count - 1)
                totals[i]++
                if (p.state != "down") oks[i]++
            }
        }
        return List(count) { UptimeBucket(ts = start + it * step, total = totals[it], ok = oks[it]) }
    }
}

@Serializable
data class DebridState(val accounts: List<DebridAccount> = emptyList(), val services: List<DebridService> = emptyList())

/** Compte envoyé à l'agent : sans [id], nouveau compte ; clé vide ou masquée, clé actuelle conservée. */
@Serializable
data class DebridAccountInput(val id: String? = null, val provider: String, val label: String, val key: String)

/** Dernier état connu par serveur : l'accueil voit tout de suite les changements faits dans la gestion des comptes. */
private val debridStates = MutableStateFlow<Map<String, DebridState>>(emptyMap())

private fun Repository.active(): Server = servers.active ?: throw ApiException(0, "Aucun serveur configuré")

private fun keep(server: Server, state: DebridState): DebridState =
    state.also { s -> debridStates.update { it + (server.id to s) } }

/** État debrid du serveur actif (null tant qu'il n'a pas été chargé, ou si l'agent ne le gère pas). */
fun Repository.debridState(): Flow<DebridState?> =
    combine(servers.activeId, debridStates) { _, states -> servers.active?.let { states[it.id] } }

suspend fun Repository.debrid(): DebridState = active().let { keep(it, api.get(it, "/api/v1/debrid")) }

/** Remplace la liste des comptes ; l'agent les vérifie avant de répondre. */
suspend fun Repository.saveDebridAccounts(accounts: List<DebridAccountInput>): DebridState =
    active().let { keep(it, api.put(it, "/api/v1/debrid/accounts", accounts)) }

suspend fun Repository.checkDebrid(): DebridState = active().let { keep(it, api.postEmpty(it, "/api/v1/debrid/check")) }
