package com.karelisio.vigie.data

import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.update
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import java.util.Locale
import kotlin.math.abs
import kotlin.math.roundToInt
import kotlin.math.roundToLong

/** Occupation du disque à une heure donnée (octets). */
@Serializable
data class DiskPoint(val ts: Long = 0, val used: Long = 0)

/** Disque principal et sa prévision (agent 0.6.0 et plus). */
@Serializable
data class StorageDisk(
    val mount: String = "/",
    val total: Long = 0,
    val used: Long = 0,
    val free: Long = 0,
    /** ok : croissance nette ; stable : pas de croissance notable ; insufficient : pas assez de recul. */
    val status: String = STATUS_INSUFFICIENT,
    /** Tendance en octets par jour, null sans assez de recul. */
    @SerialName("trend_per_day") val trendPerDay: Long? = null,
    /** Jours avant d'être plein au rythme actuel, null sans croissance nette. */
    @SerialName("days_left") val daysLeft: Double? = null,
    @SerialName("history_hours") val historyHours: Double = 0.0,
    /** Nettoyages ponctuels écartés du calcul. */
    val cleanups: Int = 0,
    /** Un point par heure sur 7 jours, puis l'instant présent. */
    val points: List<DiskPoint> = emptyList(),
) {
    companion object {
        const val STATUS_OK = "ok"
        const val STATUS_STABLE = "stable"
        const val STATUS_INSUFFICIENT = "insufficient"
    }
}

/** Taille d'un volume (octets) à un instant. */
@Serializable
data class SizePoint(val ts: Long = 0, val size: Long = 0)

@Serializable
data class StorageVolume(
    val name: String = "",
    /** Projet compose (stack), vide si le volume n'en vient pas. */
    val project: String = "",
    val size: Long = 0,
    val containers: List<String> = emptyList(),
    /** Croissance ramenée à 24 h et à 7 jours (octets), null sans mesure assez ancienne. */
    @SerialName("growth_day") val growthDay: Long? = null,
    @SerialName("growth_week") val growthWeek: Long? = null,
    /** Croissance quotidienne au-delà du seuil d'alerte. */
    val fast: Boolean = false,
    /** Mesures des 7 derniers jours (une par heure au plus). */
    val history: List<SizePoint> = emptyList(),
)

@Serializable
data class StorageUsage(val count: Int = 0, val size: Long = 0)

/** Réponse de GET /api/v1/storage. */
@Serializable
data class StorageReport(
    val disk: StorageDisk = StorageDisk(),
    /** Volumes nommés, du plus gros au plus petit. */
    val volumes: List<StorageVolume> = emptyList(),
    val anonymous: StorageUsage = StorageUsage(),
    val images: StorageUsage = StorageUsage(),
    @SerialName("build_cache") val buildCache: StorageUsage = StorageUsage(),
    /** Croissance quotidienne (octets) à partir de laquelle un volume est signalé. */
    @SerialName("fast_growth") val fastGrowth: Long = 5L shl 30,
    /** Dernier calcul des volumes (ms), 0 avant le premier. */
    @SerialName("volumes_at") val volumesAt: Long = 0,
    val refreshing: Boolean = false,
    val error: String = "",
    @SerialName("computed_at") val computedAt: Long = 0,
)

/** Dernier état connu par serveur : l'accueil profite du chargement fait par l'écran Stockage, et inversement. */
private val storageStates = MutableStateFlow<Map<String, StorageReport>>(emptyMap())

private fun Repository.storageServer(): Server = servers.active ?: throw ApiException(0, "Aucun serveur configuré")

private fun keep(server: Server, report: StorageReport): StorageReport =
    report.also { r -> storageStates.update { it + (server.id to r) } }

/** Stockage du serveur actif (null tant qu'il n'a pas été chargé). */
fun Repository.storageState(): Flow<StorageReport?> =
    combine(servers.activeId, storageStates) { _, states -> servers.active?.let { states[it.id] } }

suspend fun Repository.storage(): StorageReport = storageServer().let { keep(it, api.get(it, "/api/v1/storage")) }

/** Relance le calcul des volumes (en arrière-plan sur l'agent : suivre [StorageReport.refreshing]). 429 si trop rapproché. */
suspend fun Repository.refreshStorage(): StorageReport =
    storageServer().let { keep(it, api.postEmpty(it, "/api/v1/storage/refresh")) }

/** Textes du stockage, partagés par l'écran Stockage et l'accueil. */
object StorageFmt {
    private val locale = Locale.FRANCE

    /** Taille compacte, comme les notifications de l'agent : « 1,2 Go », « 512 Mo », « 14 Go ». */
    fun size(b: Long): String {
        val units = arrayOf("o", "Ko", "Mo", "Go", "To")
        var v = abs(b).toDouble()
        var i = 0
        while (v >= 1000 && i < units.lastIndex) {
            v /= 1024
            i++
        }
        val sign = if (b < 0) "−" else ""
        if (i == 0) return "$sign${v.roundToLong()} o"
        val rounded = (v * 10).roundToLong() / 10.0
        val text = if (rounded >= 10 || rounded % 1.0 == 0.0) rounded.roundToLong().toString()
        else String.format(locale, "%.1f", rounded)
        return "$sign$text ${units[i]}"
    }

    /** Seuil en Go : « 5 Go », « 2,5 Go ». */
    fun gb(v: Double): String =
        if (v % 1.0 == 0.0) "${v.roundToLong()} Go" else String.format(locale, "%.1f Go", v)

    /** « ~12 jours », « ~1 jour », « moins d'un jour », « plus d'un an ». */
    fun days(days: Double): String {
        val n = days.roundToInt()
        return when {
            days < 1 -> "moins d'un jour"
            n == 1 -> "~1 jour"
            n > 365 -> "plus d'un an"
            else -> "~$n jours"
        }
    }

    /** Croissance : « +1,2 Go/jour », « −300 Mo/jour », « stable » sous 1 Mo. */
    fun growth(bytes: Long, per: String = "jour"): String = when {
        abs(bytes) < (1L shl 20) -> "stable"
        bytes > 0 -> "+${size(bytes)}/$per"
        else -> "${size(bytes)}/$per"
    }

    /** Ligne principale de la prévision du disque. */
    fun forecast(d: StorageDisk): String = when (d.status) {
        StorageDisk.STATUS_OK -> d.daysLeft?.let { if (it < 1) "Plein d'ici demain" else "Plein dans ${days(it)}" } ?: "Croissance lente"
        StorageDisk.STATUS_STABLE -> if ((d.trendPerDay ?: 0) < -(1L shl 20) * 50) "En baisse" else "Pas de croissance notable"
        else -> "Prévision bientôt disponible"
    }

    /** Détail sous la prévision : rythme, recul, nettoyages écartés. */
    fun forecastDetail(d: StorageDisk): String = when (d.status) {
        StorageDisk.STATUS_INSUFFICIENT -> {
            val left = (48 - d.historyHours).coerceAtLeast(1.0).roundToInt()
            "Il faut 48 h de mesures pour estimer la tendance (encore $left h)."
        }
        else -> buildString {
            append("Au rythme actuel : ")
            append(growth(d.trendPerDay ?: 0))
            val span = (d.historyHours / 24).roundToInt()
            append(if (span >= 2) ", sur $span jours" else ", sur ${d.historyHours.roundToInt()} h")
            append(".")
            if (d.cleanups > 0) append(if (d.cleanups == 1) " Un nettoyage a été écarté du calcul." else " ${d.cleanups} nettoyages ont été écartés du calcul.")
        }
    }

    /** Mention de l'accueil, seulement quand le disque sera plein dans moins de 60 jours. */
    fun homeHint(d: StorageDisk): String? {
        val left = d.daysLeft ?: return null
        if (d.status != StorageDisk.STATUS_OK || left >= 60) return null
        return if (left < 1) "Disque plein d'ici demain" else "Disque plein dans ${days(left)}"
    }

    /** Croissance d'un volume sur 24 h et 7 jours : « +1,2 Go/jour · +5 Go/semaine », ou l'attente de recul. */
    fun volumeGrowth(v: StorageVolume): String {
        val parts = listOfNotNull(v.growthDay?.let { growth(it) }, v.growthWeek?.let { growth(it, "semaine") })
        return when {
            parts.isEmpty() -> "Croissance mesurée après 24 h"
            parts.all { it == "stable" } -> "Stable"
            else -> parts.joinToString(" · ")
        }
    }
}
