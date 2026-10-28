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

@Serializable
data class DebridState(val accounts: List<DebridAccount> = emptyList())

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
