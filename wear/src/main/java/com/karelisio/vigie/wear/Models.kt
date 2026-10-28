package com.karelisio.vigie.wear

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/** Échanges avec l'appli du téléphone (Data Layer). */
object Protocol {
    /** Capacité déclarée dans `res/values/wear.xml` : le téléphone sait que l'appli est installée. */
    const val CAPABILITY = "vigie_wear"

    /** Le téléphone envoie l'accès au serveur ([WatchServer] en JSON) ; réponse « ok » ou un message d'erreur. */
    const val PATH_SERVER = "/vigie/server"

    /** Le téléphone retire l'accès de la montre. */
    const val PATH_FORGET = "/vigie/forget"
}

/** Accès au serveur reçu du téléphone, conservé chiffré sur la montre. */
@Serializable
data class WatchServer(
    val name: String,
    val url: String,
    val token: String,
    @SerialName("read_only") val readOnly: Boolean = false,
) {
    val baseUrl: String get() = url.trimEnd('/')
}

@Serializable
data class Issue(
    val kind: String = "",
    val severity: String = "",
    val target: String = "",
    val message: String = "",
)

@Serializable
data class Counts(val total: Int = 0, val running: Int = 0, val stopped: Int = 0, val problems: Int = 0)

@Serializable
data class CheckCounts(val total: Int = 0, val up: Int = 0, val down: Int = 0)

/** Instantané système (`/api/v1/system`), réduit à ce qu'affiche la montre. */
@Serializable
data class SystemSnapshot(
    val ts: Long = 0,
    val cpu: Double = 0.0,
    val mem: Double = 0.0,
    val disk: Double = 0.0,
)

/** Vue d'ensemble (`/api/v1/overview`). */
@Serializable
data class Overview(
    val status: String = "unknown",
    val issues: List<Issue> = emptyList(),
    val system: SystemSnapshot? = null,
    val containers: Counts = Counts(),
    val checks: CheckCounts = CheckCounts(),
    val server: String = "",
    @SerialName("updated_at") val updatedAt: Long = 0,
)

/** Raison d'un échec de mise à jour des données. */
@Serializable
enum class FetchError { NotConfigured, NoNetwork, Unreachable, Unauthorized, Server }

/** Dernier état connu du serveur, partagé par l'appli, la tuile et la complication. */
@Serializable
data class Snapshot(
    val overview: Overview? = null,
    val system: SystemSnapshot? = null,
    /** Dernière réponse du serveur (ms), 0 si jamais. */
    val fetchedAt: Long = 0,
    /** Échec de la dernière tentative ; les données précédentes restent affichées. */
    val error: FetchError? = null,
    val errorDetail: String = "",
)
