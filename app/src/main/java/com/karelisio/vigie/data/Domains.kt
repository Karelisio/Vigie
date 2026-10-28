package com.karelisio.vigie.data

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/** Nom de domaine suivi par l'agent (0.6 et plus) : expiration lue chez le registre (RDAP) une fois par jour. */
@Serializable
data class DomainStatus(
    val domain: String = "",
    /** « auto » : détecté depuis un service ou l'adresse de l'agent ; « manual » : ajouté à la main. */
    val source: String = "auto",
    /** Services qui l'utilisent (« Vigie » : l'adresse publique de l'agent). */
    val services: List<String> = emptyList(),
    /** Date d'expiration (ms), 0 si inconnue. */
    val expires: Long = 0,
    /** Jours entiers restants (négatif une fois expiré), null si la date est inconnue. */
    @SerialName("days_left") val daysLeft: Int? = null,
    val registered: Long = 0,
    @SerialName("last_changed") val lastChanged: Long = 0,
    val registrar: String = "",
    /** Statuts RDAP : « client transfer prohibited », « redemption period »… */
    val status: List<String> = emptyList(),
    /** 0 : pas encore vérifié. */
    @SerialName("checked_at") val checkedAt: Long = 0,
    val error: String = "",
    /** Explication d'une date inconnue alors que la vérification a réussi. */
    val note: String = "",
) {
    val manual: Boolean get() = source == "manual"
}

/** Statuts RDAP dignes d'être montrés, en clair. */
private val domainStatusLabels = linkedMapOf(
    "redemption period" to "Période de rédemption",
    "pending delete" to "Suppression imminente",
    "client hold" to "Suspendu",
    "server hold" to "Suspendu par le registre",
    "auto renew period" to "Renouvelé automatiquement",
    "renew period" to "Renouvelé récemment",
    "pending transfer" to "Transfert en cours",
    "client transfer prohibited" to "Transfert verrouillé",
    "server transfer prohibited" to "Transfert verrouillé",
)

/** Urgence : ok, warning (dans le préavis ou vérification impossible), critical (7 jours ou moins, expiré), neutral (inconnu). */
fun DomainStatus.tone(noticeDays: Int): String {
    val left = daysLeft
    return when {
        left != null && left < 7 -> "critical"
        left != null && left < noticeDays -> "warning"
        left != null -> if (error.isNotEmpty()) "warning" else "ok"
        error.isNotEmpty() && checkedAt > 0 -> "warning"
        else -> "neutral"
    }
}

/** Phrase principale : échéance, expiration, date inconnue ou erreur. */
fun DomainStatus.summary(): String {
    val left = daysLeft
    return when {
        checkedAt == 0L && error.isNotEmpty() -> error.replaceFirstChar { it.uppercase() }
        checkedAt == 0L -> "Vérification en attente"
        left != null && left < 0 -> "Expiré le ${Fmt.day(expires)}"
        left != null -> "Expire le ${Fmt.day(expires)}"
        error.isNotEmpty() -> "Vérification impossible : $error"
        else -> "Date d'expiration inconnue"
    }
}

/** Statuts notables, en clair et sans doublon. */
fun DomainStatus.notableStatus(): List<String> =
    status.mapNotNull { domainStatusLabels[it] }.distinct()

/** Ordre d'affichage : les plus urgents d'abord, les dates inconnues à la fin. */
fun List<DomainStatus>.byUrgency(): List<DomainStatus> =
    sortedWith(compareBy<DomainStatus> { it.daysLeft ?: Int.MAX_VALUE }.thenBy { it.domain })

/** Nom de domaine saisi à la main : minuscules, sans schéma ni chemin ; null s'il est invalide. */
fun cleanDomainInput(raw: String): String? {
    val v = raw.trim().lowercase().removePrefix("https://").removePrefix("http://").substringBefore('/').trimEnd('.')
    val re = Regex("^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\\.)+[a-z][a-z0-9-]{0,62}$")
    return v.takeIf { it.length <= 253 && re.matches(it) }
}

/** Entrées des réglages qui désignent [domain] (le domaine lui-même ou l'un de ses sous-domaines). */
fun List<String>.matchingDomain(domain: String): List<String> = filter { it == domain || it.endsWith(".$domain") }

private fun Repository.server(): Server = servers.active ?: throw ApiException(0, "Aucun serveur configuré")

suspend fun Repository.domains(): List<DomainStatus> = api.get(server(), "/api/v1/domains")

/** Interroge les registres maintenant (jusqu'à 2 min) et renvoie le nouvel état. */
suspend fun Repository.checkDomains(): List<DomainStatus> = api.postEmpty(server(), "/api/v1/domains/check")
