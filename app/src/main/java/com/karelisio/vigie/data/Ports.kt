package com.karelisio.vigie.data

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.withPermit
import kotlinx.coroutines.withContext
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import java.net.Inet4Address
import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.Socket

/** Port en écoute sur le serveur (agent 0.6.0, GET /api/v1/ports). */
@Serializable
data class OpenPort(
    val port: Int = 0,
    val proto: String = "tcp",
    val address: String = "",
    val addresses: List<String> = emptyList(),
    /** « public » (joignable de l'extérieur) ou « local ». */
    val scope: String = "local",
    @SerialName("interface") val iface: String = "",
    val container: String = "",
    @SerialName("container_id") val containerId: String = "",
    @SerialName("target_port") val targetPort: Int = 0,
    @SerialName("host_network") val hostNetwork: Boolean = false,
    /** Nom probable d'un processus de l'hôte (« SSH »…). */
    val process: String = "",
    val pangolin: Boolean = false,
    val allowed: Boolean = false,
    @SerialName("bypasses_pangolin") val bypassesPangolin: Boolean = false,
    /** Alerte « nouveau port » en cours. */
    val alerted: Boolean = false,
    /** « socket » (vue sur l'hôte) ou « docker » (port publié sans socket visible). */
    val source: String = "socket",
    val since: Long = 0,
) {
    /** Clé de la liste des ports autorisés : « 443/tcp ». */
    val key: String get() = "$port/$proto"
    val isPublic: Boolean get() = scope == "public"
    val isTcp: Boolean get() = proto == "tcp"

    /** Ce qui écoute : conteneur, processus connu, ou processus de l'hôte. */
    val owner: String get() = when {
        container.isNotEmpty() -> container
        process.isNotEmpty() -> process
        else -> "Processus de l'hôte"
    }

    /** À surveiller : public, ni Pangolin ni autorisé. */
    val needsReview: Boolean get() = isPublic && !pangolin && !allowed
}

@Serializable
data class PortsReport(
    val ts: Long = 0,
    val ports: List<OpenPort> = emptyList(),
    @SerialName("host_ip") val hostIp: String = "",
    val pangolin: List<String> = emptyList(),
    val docker: Boolean = true,
    val error: String = "",
) {
    val public: List<OpenPort> get() = ports.filter { it.isPublic }
    val local: List<OpenPort> get() = ports.filterNot { it.isPublic }
}

private fun Repository.requireActive(): Server = servers.active ?: throw ApiException(0, "Aucun serveur configuré")

/** Ports ouverts sur le serveur ; [refresh] relance l'analyse sur l'agent. */
suspend fun Repository.ports(refresh: Boolean = false): PortsReport =
    api.get(requireActive(), "/api/v1/ports" + if (refresh) "?refresh=1" else "")

/** Ajoute ou retire un port de la liste des ports autorisés (réglage port_allow), en partant des réglages à jour. */
suspend fun Repository.setPortAllowed(key: String, allowed: Boolean): AgentSettings {
    val current = settings()
    val list = if (allowed) (current.portAllow + key).distinct().sorted() else current.portAllow - key
    return saveSettings(current.copy(portAllow = list))
}

/** Résultat du test de joignabilité depuis le téléphone. */
data class ReachReport(
    /** Nom d'hôte de l'URL du serveur. */
    val host: String,
    /** Adresse IP testée (résolution DNS), vide si la résolution a échoué. */
    val ip: String,
    /** Port → joignable (connexion TCP établie en moins de 3 s). */
    val results: Map<Int, Boolean>,
    val error: String? = null,
)

/** Hôte (sans port ni schéma) d'une URL de serveur. */
internal fun hostOf(url: String): String =
    url.substringAfter("://").substringBefore('/').substringBefore('?').let { h ->
        when {
            h.startsWith("[") -> h.substringAfter('[').substringBefore(']')
            h.count { it == ':' } == 1 -> h.substringBefore(':')
            else -> h
        }
    }

/**
 * Tente une connexion TCP depuis le téléphone vers chaque port (délai [timeoutMs], [parallel] à la fois),
 * sur l'adresse IP de l'hôte de l'URL du serveur (IPv4 de préférence).
 */
suspend fun probePorts(host: String, ports: List<Int>, timeoutMs: Int = 3000, parallel: Int = 8): ReachReport =
    withContext(Dispatchers.IO) {
        val address = runCatching {
            val all = InetAddress.getAllByName(host)
            all.firstOrNull { it is Inet4Address } ?: all.first()
        }.getOrElse {
            return@withContext ReachReport(host, "", emptyMap(), "Adresse de $host introuvable (DNS)")
        }
        val gate = Semaphore(parallel)
        val results = coroutineScope {
            ports.distinct().map { port ->
                async {
                    gate.withPermit {
                        port to runCatching {
                            Socket().use { it.connect(InetSocketAddress(address, port), timeoutMs) }
                            true
                        }.getOrDefault(false)
                    }
                }
            }.awaitAll()
        }.toMap()
        ReachReport(host, address.hostAddress.orEmpty(), results)
    }
