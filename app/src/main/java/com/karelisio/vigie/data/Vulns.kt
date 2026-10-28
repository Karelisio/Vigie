package com.karelisio.vigie.data

import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.update
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/** Gravités renvoyées par l'agent, de la plus forte à la plus faible. */
val VulnSeverities = listOf("critical", "high", "medium", "low", "unknown")

fun severityRank(severity: String): Int = VulnSeverities.indexOf(severity).let { if (it < 0) VulnSeverities.lastIndex else it }

fun severityLabel(severity: String): String = when (severity) {
    "critical" -> "Critique"
    "high" -> "Élevée"
    "medium" -> "Moyenne"
    "low" -> "Faible"
    else -> "Inconnue"
}

/** Couleur d'état associée (critical, warning, neutral). */
fun severityTone(severity: String): String = when (severity) {
    "critical" -> "critical"
    "high" -> "warning"
    else -> "neutral"
}

@Serializable
data class VulnCounts(
    val critical: Int = 0,
    val high: Int = 0,
    val medium: Int = 0,
    val low: Int = 0,
    val unknown: Int = 0,
    /** Failles avec une version corrigée. */
    val fixable: Int = 0,
) {
    val total: Int get() = critical + high + medium + low + unknown

    fun of(severity: String): Int = when (severity) {
        "critical" -> critical
        "high" -> high
        "medium" -> medium
        "low" -> low
        else -> unknown
    }

    /** Gravité la plus forte présente (« » si aucune faille). */
    val worst: String get() = VulnSeverities.firstOrNull { of(it) > 0 }.orEmpty()
}

/** Failles distinctes de toutes les images. */
@Serializable
data class VulnTotals(
    val critical: Int = 0,
    val high: Int = 0,
    val medium: Int = 0,
    val low: Int = 0,
    val unknown: Int = 0,
    val fixable: Int = 0,
    /** Images analysées. */
    val images: Int = 0,
    /** Images avec au moins une faille. */
    val vulnerable: Int = 0,
) {
    val counts: VulnCounts get() = VulnCounts(critical, high, medium, low, unknown, fixable)
}

@Serializable
data class VulnProgress(val phase: String = "", val done: Int = 0, val total: Int = 0) {
    val fraction: Float? get() = if (total > 0) (done.toFloat() / total).coerceIn(0f, 1f) else null

    val label: String get() = when (phase) {
        "inventory" -> if (total > 0) "Lecture des paquets : image $done sur $total" else "Lecture des paquets…"
        "query" -> "Recherche dans la base OSV…"
        "details" -> if (total > 0) "Détail des failles : $done sur $total" else "Détail des failles…"
        else -> "Analyse en cours…"
    }
}

@Serializable
data class VulnContainer(val id: String = "", val name: String = "", val state: String = "")

@Serializable
data class Vuln(
    val id: String = "",
    /** Identifiants CVE. */
    val aliases: List<String> = emptyList(),
    val summary: String = "",
    val severity: String = "unknown",
    /** Score CVSS, 0 si inconnu. */
    val score: Double = 0.0,
    @SerialName("package") val pkg: String = "",
    /** Version installée. */
    val version: String = "",
    /** Version corrigée, vide si aucune correction n'est publiée. */
    val fixed: String = "",
    val ecosystem: String = "",
    val url: String = "",
) {
    /** Identifiant le plus parlant : la CVE si elle existe. */
    val title: String get() = aliases.firstOrNull() ?: id
    val fixable: Boolean get() = fixed.isNotEmpty()
}

@Serializable
data class VulnImage(
    val id: String = "",
    val ref: String = "",
    val containers: List<VulnContainer> = emptyList(),
    /** Système détecté (os-release). */
    val os: String = "",
    /** Écosystème OSV des paquets du système, vide s'il n'est pas pris en charge. */
    val ecosystem: String = "",
    val packages: Int = 0,
    val counts: VulnCounts = VulnCounts(),
    @SerialName("update_available") val updateAvailable: Boolean = false,
    val error: String = "",
    @SerialName("read_at") val readAt: Long = 0,
    val vulns: List<Vuln> = emptyList(),
    /** Failles non listées par l'agent (au-delà des 200 plus graves). */
    val more: Int = 0,
) {
    /** Nom affiché : le premier conteneur, et le nombre des autres. */
    val title: String get() = when (containers.size) {
        0 -> ref.substringAfterLast('/').ifEmpty { id.take(19) }
        1 -> containers[0].name
        else -> "${containers[0].name} +${containers.size - 1}"
    }

    /** Système et nombre de paquets (« Debian GNU/Linux 12 (bookworm) · 123 paquets »). */
    val details: String get() {
        val system = when {
            os.isEmpty() -> "Système non reconnu"
            ecosystem.isEmpty() || ecosystem == "npm" -> "$os (paquets système non analysés)"
            else -> os
        }
        return "$system · " + if (packages <= 1) "$packages paquet" else "$packages paquets"
    }

    /** Failles triées : les plus graves, puis celles qui ont une correction, puis le score. */
    val sorted: List<Vuln> get() = vulns.sortedWith(
        compareBy<Vuln> { severityRank(it.severity) }.thenBy { !it.fixable }.thenByDescending { it.score }.thenBy { it.pkg },
    )
}

/** État de l'analyse des failles (GET /api/v1/vulns, agent 0.6.0). */
@Serializable
data class VulnReport(
    /** Réglage « vuln_scan » : faux, aucune requête vers OSV. */
    val enabled: Boolean = true,
    val scanning: Boolean = false,
    val progress: VulnProgress? = null,
    /** Fin de la dernière analyse réussie (ms), 0 : jamais. */
    @SerialName("last_scan") val lastScan: Long = 0,
    val duration: Long = 0,
    /** Échec de la dernière tentative. */
    val error: String = "",
    @SerialName("error_at") val errorAt: Long = 0,
    /** Prochaine analyse planifiée (ms), 0 si désactivée. */
    @SerialName("next_scan") val nextScan: Long = 0,
    val totals: VulnTotals = VulnTotals(),
    val images: List<VulnImage> = emptyList(),
) {
    /** Résumé court pour l'écran Sécurité. */
    fun summary(): String {
        val t = totals
        return when {
            scanning && lastScan == 0L -> "Première analyse en cours…"
            !enabled && lastScan == 0L -> "Analyse désactivée"
            lastScan == 0L -> "Pas encore analysé"
            t.counts.total == 0 -> "Aucune faille connue dans ${plural(t.images, "image")}"
            else -> buildList {
                if (t.critical > 0) add(plural(t.critical, "critique"))
                if (t.high > 0) add(plural(t.high, "élevée"))
                if (t.critical + t.high == 0) add(plural(t.counts.total, "faille"))
                add(if (t.fixable == 0) "aucune corrigée" else plural(t.fixable, "corrigeable"))
            }.joinToString(" · ")
        }
    }
}

private fun plural(n: Int, word: String) = "$n $word" + if (n > 1) "s" else ""

/** Dernier état connu par serveur : l'écran Sécurité affiche le résumé sans nouvelle requête. */
private val vulnReports = MutableStateFlow<Map<String, VulnReport>>(emptyMap())

private fun Repository.active(): Server = servers.active ?: throw ApiException(0, "Aucun serveur configuré")

private fun keep(server: Server, report: VulnReport): VulnReport =
    report.also { r -> vulnReports.update { it + (server.id to r) } }

/** Dernier état connu des failles du serveur actif (null tant qu'il n'a pas été chargé). */
fun Repository.vulnReport(): Flow<VulnReport?> =
    combine(servers.activeId, vulnReports) { _, reports -> servers.active?.let { reports[it.id] } }

suspend fun Repository.vulns(): VulnReport = active().let { keep(it, api.get(it, "/api/v1/vulns")) }

/** Lance une analyse (202) ; l'agent renvoie l'état, analyse en cours comprise. 409 si elle est désactivée. */
suspend fun Repository.scanVulns(): VulnReport = active().let { keep(it, api.postEmpty(it, "/api/v1/vulns/scan")) }
