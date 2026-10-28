package com.karelisio.vigie.data

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

@Serializable
data class Disk(
    val mount: String = "/",
    val total: Long = 0,
    val used: Long = 0,
    val pct: Double = 0.0,
)

@Serializable
data class SystemSnapshot(
    val ts: Long = 0,
    val cpu: Double = 0.0,
    @SerialName("cpu_steal") val cpuSteal: Double = 0.0,
    val iowait: Double = 0.0,
    val cores: List<Double> = emptyList(),
    @SerialName("num_cpu") val numCpu: Int = 0,
    @SerialName("mem_total") val memTotal: Long = 0,
    @SerialName("mem_used") val memUsed: Long = 0,
    val mem: Double = 0.0,
    @SerialName("swap_total") val swapTotal: Long = 0,
    @SerialName("swap_used") val swapUsed: Long = 0,
    @SerialName("disk_total") val diskTotal: Long = 0,
    @SerialName("disk_used") val diskUsed: Long = 0,
    val disk: Double = 0.0,
    val disks: List<Disk> = emptyList(),
    @SerialName("net_rx") val netRx: Double = 0.0,
    @SerialName("net_tx") val netTx: Double = 0.0,
    @SerialName("net_rx_total") val netRxTotal: Long = 0,
    @SerialName("net_tx_total") val netTxTotal: Long = 0,
    val load1: Double = 0.0,
    val load5: Double = 0.0,
    val load15: Double = 0.0,
    val uptime: Long = 0,
    val temp: Double? = null,
    val procs: Int = 0,
)

@Serializable
data class Container(
    val id: String,
    val name: String,
    val image: String = "",
    val state: String = "",
    val status: String = "",
    val health: String = "",
    val cpu: Double = 0.0,
    @SerialName("mem_used") val memUsed: Long = 0,
    @SerialName("mem_limit") val memLimit: Long = 0,
    val mem: Double = 0.0,
    @SerialName("net_rx") val netRx: Double = 0.0,
    @SerialName("net_tx") val netTx: Double = 0.0,
    val pids: Long = 0,
    val created: Long = 0,
    @SerialName("started_at") val startedAt: Long = 0,
    @SerialName("finished_at") val finishedAt: Long = 0,
    @SerialName("exit_code") val exitCode: Int = 0,
    @SerialName("oom_killed") val oomKilled: Boolean = false,
    @SerialName("restart_count") val restartCount: Int = 0,
    @SerialName("restart_policy") val restartPolicy: String = "",
    val project: String = "",
    val service: String = "",
    val ports: List<String> = emptyList(),
    val problem: String = "",
    val ignored: Boolean = false,
    val self: Boolean = false,
    @SerialName("stopped_by_user") val stoppedByUser: Boolean = false,
    @SerialName("image_ref") val imageRef: String = "",
    @SerialName("update_available") val updateAvailable: Boolean = false,
    val muted: Boolean = false,
) {
    val running get() = state == "running"
    val hasProblem get() = problem.isNotEmpty()
    val shortImage get() = image.substringAfterLast('/').ifEmpty { image }
}

@Serializable
data class Issue(
    val kind: String = "",
    val severity: String = "",
    val target: String = "",
    val id: String? = null,
    val message: String = "",
)

@Serializable
data class Counts(
    val total: Int = 0,
    val running: Int = 0,
    val stopped: Int = 0,
    val problems: Int = 0,
)

@Serializable
data class CheckCounts(val total: Int = 0, val up: Int = 0, val down: Int = 0)

@Serializable
data class Overview(
    val status: String = "ok",
    val issues: List<Issue> = emptyList(),
    val system: SystemSnapshot? = null,
    val containers: Counts = Counts(),
    val checks: CheckCounts = CheckCounts(),
    val server: String = "",
    @SerialName("updated_at") val updatedAt: Long = 0,
)

@Serializable
data class Thresholds(val cpu: Double = 90.0, val ram: Double = 90.0, val disk: Double = 90.0)

@Serializable
data class CheckResult(
    val ts: Long = 0,
    val ok: Boolean = false,
    val status: Int = 0,
    @SerialName("latency_ms") val latencyMs: Long = 0,
    val error: String? = null,
    @SerialName("cert_expires") val certExpires: Long = 0,
    /** Liens renvoyés par l'addon (check Stremio, agent 0.4 et plus). */
    val links: Int = 0,
    /** Test Stremio effectué : stream, meta, catalog ou manifest (agent 0.5 et plus). */
    val probe: String = "",
)

@Serializable
data class CheckStatus(
    val id: Long,
    val name: String,
    val url: String,
    @SerialName("expected_status") val expectedStatus: Int = 200,
    val keyword: String = "",
    @SerialName("interval_sec") val intervalSec: Int = 60,
    @SerialName("timeout_ms") val timeoutMs: Int = 10000,
    val enabled: Boolean = true,
    val position: Int = 0,
    val state: String = "pending",
    val last: CheckResult? = null,
    @SerialName("uptime_24h") val uptime24h: Double = -1.0,
    @SerialName("uptime_7d") val uptime7d: Double = -1.0,
    @SerialName("latency_24h") val latency24h: Double = 0.0,
    @SerialName("consecutive_failures") val consecutiveFailures: Int = 0,
    val recent: List<CheckResult> = emptyList(),
    @SerialName("cert_expires") val certExpires: Long = 0,
    /** « http » ou « stremio » (agent 0.4 et plus). */
    val kind: String = "http",
    @SerialName("stremio_id") val stremioId: String = "",
) {
    val isStremio get() = kind == "stremio"
}

@Serializable
data class CheckInput(
    val name: String,
    val url: String,
    @SerialName("expected_status") val expectedStatus: Int = 200,
    val keyword: String = "",
    @SerialName("interval_sec") val intervalSec: Int = 60,
    @SerialName("timeout_ms") val timeoutMs: Int = 10000,
    val enabled: Boolean = true,
    /** Absents (null) pour un agent antérieur à 0.4, qui refuse les champs inconnus. */
    val kind: String? = null,
    @SerialName("stremio_id") val stremioId: String? = null,
)

@Serializable
data class UptimeBucket(val ts: Long = 0, val total: Int = 0, val ok: Int = 0, val latency: Double? = null)

@Serializable
data class CheckHistory(
    val buckets: List<UptimeBucket> = emptyList(),
    val uptime: Double = -1.0,
    val latency: Double = 0.0,
)

@Serializable
data class SysPoint(
    val ts: Long = 0,
    val cpu: Double = 0.0,
    val mem: Double = 0.0,
    val disk: Double = 0.0,
    val rx: Double = 0.0,
    val tx: Double = 0.0,
    val load1: Double = 0.0,
    val temp: Double? = null,
)

@Serializable
data class CtrPoint(
    val ts: Long = 0,
    val cpu: Double = 0.0,
    val mem: Double = 0.0,
    @SerialName("mem_bytes") val memBytes: Long = 0,
)

@Serializable
data class History<T>(@SerialName("bucket_ms") val bucketMs: Long = 0, val points: List<T> = emptyList())

@Serializable
data class LogLine(val ts: Long = 0, val stream: String = "stdout", val line: String = "")

@Serializable
data class LogsResponse(val lines: List<LogLine> = emptyList())

@Serializable
data class HealthInfo(
    val status: String = "",
    @SerialName("failing_streak") val failingStreak: Int = 0,
    @SerialName("last_output") val lastOutput: String? = null,
    @SerialName("last_exit_code") val lastExitCode: Int? = null,
)

@Serializable
data class MountInfo(val type: String = "", val source: String = "", val destination: String = "", val rw: Boolean = true)

@Serializable
data class ContainerDetail(
    val container: Container,
    val networks: Map<String, String> = emptyMap(),
    val mounts: List<MountInfo> = emptyList(),
    @SerialName("image_id") val imageId: String = "",
    val error: String = "",
    val health: HealthInfo? = null,
    @SerialName("cpu_limit") val cpuLimit: Double? = null,
)

@Serializable
data class ActionResult(val ok: Boolean = false, val state: String = "")

@Serializable
data class HostInfo(
    val hostname: String = "",
    val kernel: String = "",
    val os: String = "",
    val arch: String = "",
    @SerialName("num_cpu") val numCpu: Int = 0,
    @SerialName("cpu_model") val cpuModel: String = "",
)

@Serializable
data class DockerVersion(
    @SerialName("Version") val version: String = "",
    @SerialName("ApiVersion") val apiVersion: String = "",
)

@Serializable
data class ServerInfo(
    val name: String = "",
    val version: String = "",
    val host: HostInfo = HostInfo(),
    val docker: DockerVersion? = null,
    @SerialName("agent_started") val agentStarted: Long = 0,
    val thresholds: Thresholds = Thresholds(),
    @SerialName("push_enabled") val pushEnabled: Boolean = false,
    @SerialName("public_url") val publicUrl: String = "",
    @SerialName("updates_enabled") val updatesEnabled: Boolean = false,
    @SerialName("last_self_update") val lastSelfUpdate: SelfUpdateResult? = null,
    /** « admin » (token principal) ou « readonly » (accès partagé, agent 0.5 et plus). */
    val role: String = "admin",
)

@Serializable
data class Alert(
    val id: Long = 0,
    val ts: Long = 0,
    val kind: String = "",
    val severity: String = "",
    val target: String = "",
    val title: String = "",
    val body: String = "",
    val resolved: Boolean = false,
)

@Serializable
data class AuditEntry(
    val id: Long = 0,
    val ts: Long = 0,
    val ip: String = "",
    val action: String = "",
    val target: String = "",
    val result: String = "",
    val ua: String = "",
)

@Serializable
data class DockerEvent(
    val action: String = "",
    val id: String = "",
    val name: String = "",
    val ts: Long = 0,
    @SerialName("exit_code") val exitCode: String? = null,
)

@Serializable
data class Hello(
    val name: String = "",
    val version: String = "",
    val thresholds: Thresholds = Thresholds(),
    @SerialName("updates_enabled") val updatesEnabled: Boolean = false,
    /** Actions de stack disponibles (agent 0.3.0 et plus). */
    val stacks: Boolean = false,
    val role: String = "admin",
)

@Serializable
data class DeviceRegistration(val token: String, val name: String)

@Serializable
data class PushTest(val token: String? = null)

/** Réglages d'alerte modifiables depuis l'appli. */
@Serializable
data class AgentSettings(
    val cpu: Double = 90.0,
    val ram: Double = 90.0,
    val disk: Double = 90.0,
    @SerialName("duration_min") val durationMin: Int = 5,
    @SerialName("quiet_enabled") val quietEnabled: Boolean = false,
    @SerialName("quiet_start") val quietStart: String = "23:00",
    @SerialName("quiet_end") val quietEnd: String = "07:00",
    val muted: List<String> = emptyList(),
    @SerialName("image_updates") val imageUpdates: Boolean = true,
    @SerialName("oracle_watch") val oracleWatch: Boolean = true,
    @SerialName("cert_days") val certDays: Int = 14,
    @SerialName("prune_images") val pruneImages: Boolean = true,
    @SerialName("auto_update") val autoUpdate: Boolean = false,
    @SerialName("auto_update_time") val autoUpdateTime: String = "04:00",
    @SerialName("auto_update_all") val autoUpdateAll: Boolean = false,
    @SerialName("auto_update_list") val autoUpdateList: List<String> = emptyList(),
    @SerialName("auto_update_agent") val autoUpdateAgent: Boolean = false,
    @SerialName("net_quota_tb") val netQuotaTb: Double = 10.0,
    @SerialName("net_quota_alert") val netQuotaAlert: Int = 80,
    @SerialName("maintenance_until") val maintenanceUntil: Long = 0,
    @SerialName("log_rules") val logRules: List<LogRule> = emptyList(),
    @SerialName("weekly_report") val weeklyReport: Boolean = true,
    @SerialName("auto_heal") val autoHeal: Boolean = false,
    @SerialName("auto_heal_all") val autoHealAll: Boolean = false,
    @SerialName("auto_heal_list") val autoHealList: List<String> = emptyList(),
    @SerialName("auto_heal_delay") val autoHealDelay: Int = 3,
    @SerialName("auto_heal_max") val autoHealMax: Int = 3,
    @SerialName("regression_alert") val regressionAlert: Boolean = true,
    @SerialName("disk_forecast_days") val diskForecastDays: Int = 14,
    @SerialName("volume_growth_gb") val volumeGrowthGb: Double = 5.0,
    @SerialName("vuln_scan") val vulnScan: Boolean = true,
    @SerialName("vuln_alert") val vulnAlert: String = "critical",
    @SerialName("port_alert") val portAlert: Boolean = true,
    @SerialName("port_allow") val portAllow: List<String> = emptyList(),
    @SerialName("ssh_watch") val sshWatch: Boolean = true,
    @SerialName("ssh_login_alert") val sshLoginAlert: Boolean = true,
    val domains: List<String> = emptyList(),
    @SerialName("domain_days") val domainDays: Int = 30,
    @SerialName("debrid_down_alert") val debridDownAlert: Boolean = true,
)

/** Alerte quand une ligne de log d'un conteneur contient un texte (ou correspond à une regex). */
@Serializable
data class LogRule(val id: String = "", val container: String = "", val pattern: String = "", val regex: Boolean = false)

@Serializable
data class MuteRequest(val muted: Boolean)

@Serializable
data class OracleMetric(val value: Double = 0.0, val below: Boolean = false, val applies: Boolean = false, val threshold: Double = 20.0)

@Serializable
data class OracleReport(
    val applicable: Boolean = false,
    val risk: String = "unknown",
    val days: Double = 0.0,
    val cpu: OracleMetric = OracleMetric(),
    val mem: OracleMetric = OracleMetric(),
    val net: OracleMetric = OracleMetric(),
    @SerialName("net_mbps") val netMbps: Double = 0.0,
    val summary: String = "",
)

@Serializable
data class TimelineEvent(val ts: Long = 0, val action: String = "", val detail: String = "", val source: String = "docker")

@Serializable
data class ImageStatus(
    @SerialName("update_available") val updateAvailable: Boolean = false,
    @SerialName("checked_at") val checkedAt: Long = 0,
    val error: String? = null,
)

@Serializable
data class UpdateRequest(@SerialName("pull_only") val pullOnly: Boolean = false)

@Serializable
data class JobResult(val name: String = "", val ok: Boolean = false, val updated: Boolean = false, val message: String = "")

/** Opération longue de l'agent (téléchargement d'image, recréation, action de stack), suivie en direct. */
@Serializable
data class AgentJob(
    val id: String = "",
    val kind: String = "",
    val action: String = "",
    val target: String = "",
    val phase: String = "",
    val progress: Double = 0.0,
    val message: String = "",
    val done: Boolean = false,
    val ok: Boolean = false,
    val error: String? = null,
    val results: List<JobResult> = emptyList(),
    @SerialName("started_at") val startedAt: Long = 0,
    @SerialName("finished_at") val finishedAt: Long = 0,
) {
    /** Vrai si l'opération concerne ce conteneur (directement ou via sa stack). */
    fun concerns(c: Container): Boolean = when (kind) {
        "stack" -> c.project == target
        "batch", "auto" -> c.updateAvailable || results.any { it.name == c.name }
        else -> target == c.name
    }
}

@Serializable
data class SelfUpdateResult(val ok: Boolean = false, val error: String? = null, val from: String = "", val ts: Long = 0)

@Serializable
data class MaintenanceRequest(val minutes: Int)

@Serializable
data class CleanupCategory(val count: Int = 0, val size: Long = 0)

/** Espace récupérable estimé par l'agent. */
@Serializable
data class CleanupPreview(
    val images: CleanupCategory = CleanupCategory(),
    val volumes: CleanupCategory = CleanupCategory(),
    @SerialName("build_cache") val buildCache: CleanupCategory = CleanupCategory(),
    @SerialName("volumes_supported") val volumesSupported: Boolean = false,
)

@Serializable
data class CleanupOptions(
    val images: Boolean,
    val volumes: Boolean,
    @SerialName("build_cache") val buildCache: Boolean,
)

/** Accès partagé en lecture seule (le jeton n'est connu qu'à sa création). */
@Serializable
data class SharedAccess(
    val id: String = "",
    val label: String = "",
    val created: Long = 0,
    @SerialName("last_used") val lastUsed: Long = 0,
)

@Serializable
data class SharedAccessCreated(
    val id: String = "",
    val label: String = "",
    val token: String = "",
    val link: String = "",
    @SerialName("qr_png") val qrPng: String = "",
)

@Serializable
data class SharedAccessRequest(val label: String)
