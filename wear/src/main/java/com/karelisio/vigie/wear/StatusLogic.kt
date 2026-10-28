package com.karelisio.vigie.wear

import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import kotlin.math.roundToInt

/** État global du serveur. */
enum class Level { Ok, Warning, Critical, Unknown }

/** Ce qu'affichent l'appli, la tuile et la complication. */
data class StatusView(
    val configured: Boolean,
    val serverName: String,
    val level: Level,
    val label: String,
    val cpu: Int?,
    val ram: Int?,
    val disk: Int?,
    /** Conteneurs et services en défaut, alertes système. */
    val problems: List<String>,
    val updatedAt: Long,
    val error: FetchError?,
    val errorText: String?,
) {
    val problemCount: Int get() = problems.size
}

object StatusLogic {
    // Teintes claires, lisibles sur le fond noir de la montre.
    const val GREEN = 0xFF66BB6A.toInt()
    const val AMBER = 0xFFFFCA28.toInt()
    const val RED = 0xFFEF5350.toInt()
    const val GREY = 0xFF9E9E9E.toInt()

    fun level(status: String?): Level = when (status) {
        "ok" -> Level.Ok
        "warning" -> Level.Warning
        "critical" -> Level.Critical
        else -> Level.Unknown
    }

    fun label(level: Level): String = when (level) {
        Level.Ok -> "Tout va bien"
        Level.Warning -> "À surveiller"
        Level.Critical -> "Problème"
        Level.Unknown -> "État inconnu"
    }

    fun color(level: Level): Int = when (level) {
        Level.Ok -> GREEN
        Level.Warning -> AMBER
        Level.Critical -> RED
        Level.Unknown -> GREY
    }

    fun errorText(error: FetchError?, detail: String = ""): String? = when (error) {
        null -> null
        FetchError.NotConfigured -> "Pas encore configurée"
        FetchError.NoNetwork -> "Montre hors connexion"
        FetchError.Unreachable -> "Serveur injoignable"
        FetchError.Unauthorized -> "Accès refusé : renvoie-le depuis le téléphone"
        FetchError.Server -> "Erreur du serveur" + detail.takeIf { it.isNotBlank() }?.let { " ($it)" }.orEmpty()
    }

    fun view(server: WatchServer?, snapshot: Snapshot): StatusView {
        val overview = snapshot.overview
        val system = snapshot.system ?: overview?.system
        // Après une erreur, le dernier état connu reste affiché, avec l'erreur et son heure.
        val level = if (overview == null) Level.Unknown else level(overview.status)
        val error = if (server == null) FetchError.NotConfigured else snapshot.error
        return StatusView(
            configured = server != null,
            serverName = server?.name?.ifBlank { null } ?: overview?.server?.ifBlank { null } ?: "Vigie",
            level = level,
            label = label(level),
            cpu = system?.cpu?.roundToInt(),
            ram = system?.mem?.roundToInt(),
            disk = system?.disk?.roundToInt(),
            problems = overview?.issues.orEmpty().map { i -> i.message.ifBlank { i.target } }.filter { it.isNotBlank() },
            updatedAt = snapshot.fetchedAt,
            error = error,
            errorText = errorText(error, snapshot.errorDetail),
        )
    }

    /** « 2 problèmes », « Aucun problème ». */
    fun problemsText(count: Int): String = when (count) {
        0 -> "Aucun problème"
        1 -> "1 problème"
        else -> "$count problèmes"
    }

    /** Texte court de la complication (7 caractères au plus). */
    fun shortText(view: StatusView): String = when {
        !view.configured || view.updatedAt == 0L -> "--"
        view.problemCount > 0 -> "${view.problemCount} pb"
        view.level == Level.Ok -> "OK"
        view.level == Level.Unknown -> "?"
        else -> "!"
    }

    fun percent(value: Int?): String = value?.let { "$it %" } ?: "--"

    /** Heure de la dernière mise à jour : « 14:32 », ou la date si ce n'est pas aujourd'hui. */
    fun time(ms: Long, now: Long = System.currentTimeMillis()): String {
        if (ms <= 0) return "jamais"
        val day = SimpleDateFormat("yyyyMMdd", Locale.FRANCE)
        val pattern = if (day.format(Date(ms)) == day.format(Date(now))) "HH:mm" else "d MMM HH:mm"
        return SimpleDateFormat(pattern, Locale.FRANCE).format(Date(ms))
    }
}
