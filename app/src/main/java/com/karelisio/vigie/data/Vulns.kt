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
    /** Failles jugées sans importance par la distribution (agent 0.7.1) : hors du total. */
    val negligible: Int = 0,
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
    val negligible: Int = 0,
    /** Images analysées. */
    val images: Int = 0,
    /** Images avec au moins une faille. */
    val vulnerable: Int = 0,
) {
    val counts: VulnCounts get() = VulnCounts(critical, high, medium, low, unknown, fixable, negligible)
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
    /** Jugée sans importance par la distribution (urgence Debian, priorité Ubuntu) ; [note] l'explique. */
    val negligible: Boolean = false,
    val note: String = "",
) {
    /** Identifiant le plus parlant : la CVE si elle existe. */
    val title: String get() = aliases.firstOrNull() ?: id
    val fixable: Boolean get() = fixed.isNotEmpty() && !negligible
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
    /** Paquets qui ont une version corrigée, toutes failles comprises (agent 0.7.0). */
    val fixes: List<VulnFix> = emptyList(),
    /** Image corrigée par Vigie (agent 0.7.0), null sinon. */
    val patched: PatchedImage? = null,
    /** Dépôt source de l'image (label org.opencontainers.image.source), pour signaler une faille. */
    val source: String = "",
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
        compareBy<Vuln> { it.negligible }.thenBy { severityRank(it.severity) }.thenBy { !it.fixable }.thenByDescending { it.score }.thenBy { it.pkg },
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

// ---- Correction des failles (agent 0.7.0) ----

/** Paquet vulnérable dont une version corrigée est publiée. */
@Serializable
data class VulnFix(
    val ecosystem: String = "",
    @SerialName("package") val pkg: String = "",
    val version: String = "",
    val fixed: String = "",
    val counts: VulnCounts = VulnCounts(),
)

@Serializable
data class PackageChange(val name: String = "", val from: String = "", val to: String = "")

/** Image corrigée par Vigie : paquets mis à niveau dans une image dérivée de l'image d'origine. */
@Serializable
data class PatchedImage(
    /** Date de la correction (ms). */
    val at: Long = 0,
    @SerialName("base_image") val baseImage: String = "",
    @SerialName("base_digests") val baseDigests: List<String> = emptyList(),
    val ref: String = "",
    val packages: List<PackageChange> = emptyList(),
)

/** Analyse d'une nouvelle version, téléchargée sans être installée. */
@Serializable
data class FixCandidate(
    val current: String = "",
    @SerialName("image_id") val imageId: String = "",
    val version: String = "",
    @SerialName("analyzed_at") val analyzedAt: Long = 0,
    /** Failles actuelles absentes de la nouvelle version. */
    val fixed: VulnCounts = VulnCounts(),
    /** Failles nouvelles. */
    val added: VulnCounts = VulnCounts(),
    val remaining: VulnCounts = VulnCounts(),
)

@Serializable
data class UpdateRoute(val available: Boolean = false, val candidate: FixCandidate? = null)

@Serializable
data class PackagePlan(
    /** Paquet source (Debian, Ubuntu) ou d'origine (Alpine). */
    val name: String = "",
    val version: String = "",
    val fixed: String = "",
    /** Paquets installés mis à niveau. */
    val binaries: List<String> = emptyList(),
    val counts: VulnCounts = VulnCounts(),
)

@Serializable
data class PatchRoute(
    val possible: Boolean = false,
    /** apt ou apk. */
    val manager: String = "",
    val packages: List<PackagePlan> = emptyList(),
    /** Failles corrigées attendues. */
    val fixes: VulnCounts = VulnCounts(),
    /** Pourquoi Vigie ne peut pas corriger cette image. */
    val reason: String = "",
)

/** Failles des dépendances de l'application (npm), que seule une nouvelle image corrige. */
@Serializable
data class AppVulns(val ecosystem: String = "", val counts: VulnCounts = VulnCounts(), val fixable: Int = 0)

/** Bilan de la dernière correction d'une image. */
@Serializable
data class FixOutcome(
    val at: Long = 0,
    val route: String = "",
    val from: String = "",
    val image: String = "",
    val packages: Int = 0,
    val fixed: VulnCounts = VulnCounts(),
    val added: VulnCounts = VulnCounts(),
    val remaining: VulnCounts = VulnCounts(),
    val message: String = "",
)

/** Correction proposée pour une image : la voie (update, patch, none) et son explication. */
@Serializable
data class FixPlan(
    @SerialName("image_id") val imageId: String = "",
    val ref: String = "",
    val containers: List<VulnContainer> = emptyList(),
    val os: String = "",
    val counts: VulnCounts = VulnCounts(),
    val route: String = "none",
    val title: String = "",
    val explain: String = "",
    val update: UpdateRoute = UpdateRoute(),
    val patch: PatchRoute = PatchRoute(),
    val app: AppVulns = AppVulns(),
    /** Failles sans version corrigée connue. */
    val unfixed: Int = 0,
    val patched: PatchedImage? = null,
    val last: FixOutcome? = null,
    val source: String = "",
    /** Retour à l'image d'origine après une correction : la nuit ne la refait pas. */
    val declined: Boolean = false,
    /** Image de l'agent Vigie. */
    val self: Boolean = false,
) {
    val canFix: Boolean get() = route == "update" || route == "patch"

    /** Conteneur qui nomme l'opération. */
    val target: String get() = containers.firstOrNull()?.name.orEmpty()

    /** Nouvelle version publiée mais pas encore analysée. */
    val needsAnalysis: Boolean get() = route == "update" && update.available && update.candidate == null

    /** Lien pour signaler les failles de l'application au projet (GitHub : page des tickets). */
    val reportUrl: String get() = when {
        source.isEmpty() -> ""
        source.startsWith("https://github.com/") -> source.removeSuffix("/").removeSuffix(".git") + "/issues"
        else -> source
    }
}

@Serializable
data class PlanTotals(
    val patch: Int = 0,
    val update: Int = 0,
    val none: Int = 0,
    /** Images corrigeables. */
    val fixable: Int = 0,
    val fixes: VulnCounts = VulnCounts(),
)

/** Correction automatique de nuit. */
@Serializable
data class NightlyFix(
    val enabled: Boolean = false,
    val level: String = "high",
    val all: Boolean = false,
    /** Prochaine analyse de nuit, suivie de la correction (ms, 0 si désactivée). */
    val next: Long = 0,
    @SerialName("last_run") val lastRun: Long = 0,
    val message: String = "",
)

/** GET /api/v1/vulns/fixes (agent 0.7.0). */
@Serializable
data class FixPlans(
    /** Mises à jour autorisées sur l'agent (sinon aucune correction possible). */
    val enabled: Boolean = true,
    val busy: Boolean = false,
    val scanning: Boolean = false,
    @SerialName("last_scan") val lastScan: Long = 0,
    val totals: PlanTotals = PlanTotals(),
    val images: List<FixPlan> = emptyList(),
    val nightly: NightlyFix = NightlyFix(),
) {
    fun plan(imageId: String): FixPlan? = images.firstOrNull { it.imageId == imageId }
}

@Serializable
data class FixRequest(
    @SerialName("image_id") val imageId: String = "",
    val container: String = "",
    val route: String = "",
)

/** Gravité minimale corrigée la nuit. */
val FixLevels = listOf("critical" to "Critiques", "high" to "+ élevées", "medium" to "+ moyennes", "low" to "Toutes")

/** « Corrigée par Vigie le 28 oct. 2026 : 2 paquets mis à niveau ». */
fun PatchedImage.summary(date: (Long) -> String): String = buildString {
    append("Corrigée par Vigie")
    if (at > 0) append(" le ${date(at)}")
    if (packages.isNotEmpty()) append(" : " + if (packages.size == 1) "1 paquet mis à niveau" else "${packages.size} paquets mis à niveau")
}

suspend fun Repository.vulnFixes(): FixPlans = api.get(active(), "/api/v1/vulns/fixes")

/** Corrige une image par la voie proposée (ou [route]) : opération « vulnfix » suivie comme une mise à jour. */
suspend fun Repository.fixVulns(container: String, route: String = ""): AgentJob =
    trackJob(api.post(active(), "/api/v1/vulns/fix", FixRequest(container = container, route = route)))

/** Corrige l'une après l'autre toutes les images qui peuvent l'être. */
suspend fun Repository.fixAllVulns(): AgentJob = trackJob(api.postEmpty(active(), "/api/v1/vulns/fix-all"))

/** Télécharge la nouvelle version d'une image sans l'installer et la compare à l'image en service. */
suspend fun Repository.analyzeNewVersion(container: String): AgentJob =
    trackJob(api.post(active(), "/api/v1/vulns/analyze", FixRequest(container = container)))
