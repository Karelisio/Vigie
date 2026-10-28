package com.karelisio.vigie.data

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/** Trafic d'un mois (« 2026-10 »), en octets. */
@Serializable
data class TrafficMonth(val month: String = "", val tx: Long = 0, val rx: Long = 0)

/** Trafic du mois en cours et quota sortant (agent 0.4.0 et plus). */
@Serializable
data class TrafficReport(
    val month: String = "",
    val tx: Long = 0,
    val rx: Long = 0,
    /** Octets sortants autorisés par mois, 0 sans quota. */
    val quota: Long = 0,
    val pct: Double = 0.0,
    @SerialName("projected_tx") val projectedTx: Long = 0,
    @SerialName("days_left") val daysLeft: Double = 0.0,
    /** Derniers mois, du plus ancien au plus récent. */
    val history: List<TrafficMonth> = emptyList(),
)

/** Consommation d'un conteneur sur une période (24 h ou 7 jours). */
@Serializable
data class Consumer(
    val name: String = "",
    @SerialName("cpu_avg") val cpuAvg: Double = 0.0,
    @SerialName("cpu_max") val cpuMax: Double = 0.0,
    @SerialName("mem_avg") val memAvg: Long = 0,
    @SerialName("rx_bytes") val rxBytes: Long = 0,
    @SerialName("tx_bytes") val txBytes: Long = 0,
)

@Serializable
data class ServiceUptime(val name: String = "", val uptime: Double = -1.0)

@Serializable
data class ReportServices(
    /** Moyenne des services mesurés, -1 sans mesure. */
    val uptime: Double = -1.0,
    val worst: ServiceUptime? = null,
    val list: List<ServiceUptime> = emptyList(),
)

@Serializable
data class IncidentKind(val kind: String = "", val label: String = "", val count: Int = 0)

@Serializable
data class ReportIncidents(
    val total: Int = 0,
    @SerialName("by_kind") val byKind: List<IncidentKind> = emptyList(),
    val recent: List<Alert> = emptyList(),
)

@Serializable
data class ContainerIncidents(val name: String = "", val crashes: Int = 0, val restarts: Int = 0)

@Serializable
data class ReportContainers(
    val crashes: Int = 0,
    val restarts: Int = 0,
    val list: List<ContainerIncidents> = emptyList(),
)

@Serializable
data class ReportDisk(
    val pct: Double = 0.0,
    val used: Long = 0,
    val total: Long = 0,
    /** Évolution en points sur la période. */
    val change: Double? = null,
)

@Serializable
data class ReportTraffic(
    val rx: Long = 0,
    val tx: Long = 0,
    @SerialName("month_tx") val monthTx: Long = 0,
    val quota: Long = 0,
    @SerialName("month_pct") val monthPct: Double = 0.0,
)

/** Rapport de la semaine (agent 0.4.0 et plus). */
@Serializable
data class WeeklyReport(
    val from: Long = 0,
    val to: Long = 0,
    val days: Int = 7,
    val services: ReportServices = ReportServices(),
    val incidents: ReportIncidents = ReportIncidents(),
    val containers: ReportContainers = ReportContainers(),
    val updates: List<String> = emptyList(),
    val disk: ReportDisk = ReportDisk(),
    val traffic: ReportTraffic? = null,
    val oracle: OracleReport? = null,
)
