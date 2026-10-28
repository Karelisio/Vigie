package com.karelisio.vigie.data

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/** Connexions SSH de l'hôte (agent 0.6.0, GET /api/v1/ssh). */
@Serializable
data class SshSummary(
    /** ok, permission, missing, journald, disabled ou error. */
    val state: String = "",
    /** Fichier lu sur l'hôte (/var/log/auth.log…). */
    val source: String = "",
    /** Explication et marche à suivre quand la lecture est impossible. */
    val message: String = "",
    /** Extrait à ajouter au service dans le compose (droits manquants). */
    val fix: String = "",
    /** ok, absent ou permission. */
    val fail2ban: String = "absent",
    @SerialName("read_at") val readAt: Long = 0,
    /** Pays des adresses activés sur l'agent. */
    val geo: Boolean = true,
    val stats: SshStats = SshStats(),
    @SerialName("top_ips") val topIps: List<SshIp> = emptyList(),
    @SerialName("top_users") val topUsers: List<SshUser> = emptyList(),
    val logins: List<SshLogin> = emptyList(),
    /** Échecs par heure, 24 dernières heures. */
    val hourly: List<SshPoint> = emptyList(),
    /** Échecs par jour, 30 derniers jours. */
    val daily: List<SshPoint> = emptyList(),
) {
    val ok: Boolean get() = state == "ok"
}

@Serializable
data class SshStats(
    @SerialName("failed_24h") val failed24h: Int = 0,
    @SerialName("failed_7d") val failed7d: Int = 0,
    @SerialName("accepted_7d") val accepted7d: Int = 0,
    @SerialName("bans_7d") val bans7d: Int = 0,
    @SerialName("ips_7d") val ips7d: Int = 0,
    @SerialName("users_7d") val users7d: Int = 0,
    /** Plus ancien événement conservé (ms), 0 sans événement. */
    val since: Long = 0,
)

@Serializable
data class SshIp(
    val ip: String = "",
    val count: Int = 0,
    val last: Long = 0,
    val banned: Boolean = false,
    /** Une connexion a aussi réussi depuis cette adresse. */
    val accepted: Boolean = false,
    val country: String = "",
    @SerialName("country_name") val countryName: String = "",
)

@Serializable
data class SshUser(val user: String = "", val count: Int = 0, val invalid: Boolean = false)

@Serializable
data class SshLogin(
    val ts: Long = 0,
    val user: String = "",
    val ip: String = "",
    val method: String = "",
    val country: String = "",
    @SerialName("country_name") val countryName: String = "",
    @SerialName("new_ip") val newIp: Boolean = false,
)

@Serializable
data class SshPoint(val ts: Long = 0, val count: Int = 0)

private fun Repository.requireActive(): Server = servers.active ?: throw ApiException(0, "Aucun serveur configuré")

suspend fun Repository.ssh(): SshSummary = api.get(requireActive(), "/api/v1/ssh")

/** Drapeau (émoji) d'un code pays ISO à deux lettres ; vide si inconnu. */
fun countryFlag(code: String): String {
    if (code.length != 2 || !code.all { it in 'A'..'Z' || it in 'a'..'z' }) return ""
    return code.uppercase().map { String(Character.toChars(0x1F1E6 + (it - 'A'))) }.joinToString("")
}

/** Méthode d'authentification en clair. */
fun sshMethodLabel(method: String): String = when (method) {
    "publickey" -> "Clé SSH"
    "password" -> "Mot de passe"
    "keyboard-interactive" -> "Mot de passe (interactif)"
    "hostbased" -> "Par hôte"
    "gssapi-with-mic" -> "Kerberos"
    "" -> "—"
    else -> method
}
