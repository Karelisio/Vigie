package com.karelisio.vigie.watch

import com.karelisio.vigie.data.Fmt
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import java.io.InterruptedIOException
import java.net.ConnectException
import java.net.NoRouteToHostException
import java.net.UnknownHostException
import java.security.cert.CertPathValidatorException
import java.security.cert.CertificateException
import java.util.Calendar
import javax.net.ssl.SSLException
import javax.net.ssl.SSLPeerUnverifiedException

/** Délai de chaque requête de surveillance, en secondes. */
const val WATCH_TIMEOUT_S = 10L

/** Passages consécutifs sans réponse avant l'alerte : 15 à 30 min avec un passage toutes les 15 min. */
const val FAILURES_BEFORE_ALERT = 2

/** Conclusion d'un passage pour un serveur. */
sealed interface WatchResult {
    data object Up : WatchResult
    data class Down(val cause: String) : WatchResult

    /** Le téléphone lui-même n'a pas Internet : rien à conclure. */
    data object Unknown : WatchResult
}

/** État persistant d'un serveur surveillé depuis le téléphone. */
@Serializable
data class WatchState(
    /** Passages consécutifs sans réponse. */
    val failures: Int = 0,
    /** Premier passage sans réponse de la série (ms). */
    val since: Long = 0,
    /** Dernière cause constatée. */
    val cause: String = "",
    /** Alerte « ne répond plus » envoyée, en attente du retour. */
    val alerted: Boolean = false,
    /** Dernier passage concluant (ms). */
    val checkedAt: Long = 0,
)

/** Notification à afficher après un passage. */
sealed interface WatchNotice {
    data class Down(val cause: String, val since: Long) : WatchNotice
    data class Back(val since: Long, val at: Long) : WatchNotice
}

/**
 * Applique le résultat d'un passage : nouvel état et notification éventuelle.
 * Une seule notification par changement d'état : l'alerte au 2e passage consécutif sans réponse, puis le retour.
 */
fun WatchState.next(result: WatchResult, now: Long): Pair<WatchState, WatchNotice?> = when (result) {
    WatchResult.Unknown -> this to null
    WatchResult.Up -> WatchState(checkedAt = now) to if (alerted) WatchNotice.Back(since, now) else null
    is WatchResult.Down -> {
        val s = copy(failures = failures + 1, since = if (failures == 0) now else since, cause = result.cause, checkedAt = now)
        if (!alerted && s.failures >= FAILURES_BEFORE_ALERT) s.copy(alerted = true) to WatchNotice.Down(s.cause, s.since)
        else s to null
    }
}

/** Interprète la réponse de `/healthz` : seul `{"ok":true,…}` de l'agent compte comme une réponse. */
fun healthResult(code: Int, body: String): WatchResult = when (code) {
    in 200..299 -> if (isAgentHealth(body)) WatchResult.Up else WatchResult.Down("Réponse inattendue : ce n'est pas l'agent Vigie qui répond")
    in 300..399 -> WatchResult.Down("Redirigé vers une page de connexion : autorise /healthz dans Pangolin")
    401, 403 -> WatchResult.Down("Accès refusé par le proxy ($code) : autorise /healthz dans Pangolin")
    404 -> WatchResult.Down("Introuvable (404) : l'adresse pointe-t-elle vers l'agent ?")
    502, 503, 504 -> WatchResult.Down("Le proxy (Pangolin) répond mais pas l'agent ($code)")
    in 500..599 -> WatchResult.Down("Erreur du serveur ($code)")
    else -> WatchResult.Down("Réponse inattendue ($code)")
}

private fun isAgentHealth(body: String): Boolean = runCatching {
    Json.parseToJsonElement(body).jsonObject["ok"]?.jsonPrimitive?.booleanOrNull == true
}.getOrDefault(false)

/** Cause lisible d'un échec de connexion (exception d'OkHttp et ses causes). */
fun failureCause(e: Throwable): String {
    val chain = generateSequence(e) { it.cause }.take(10).toList()
    return when {
        chain.any { it is UnknownHostException } -> "Nom de domaine introuvable (DNS)"
        chain.any { it is CertificateException || it is CertPathValidatorException || it is SSLPeerUnverifiedException } ->
            "Certificat HTTPS refusé (expiré ou invalide)"
        chain.any { it is SSLException } -> "Échec de la connexion sécurisée (HTTPS)"
        chain.any { it is InterruptedIOException } -> "Délai dépassé : aucune réponse en $WATCH_TIMEOUT_S s"
        chain.any { it is ConnectException } -> "Connexion refusée"
        chain.any { it is NoRouteToHostException } -> "Serveur injoignable sur le réseau"
        else -> e.message?.takeIf { it.isNotBlank() }?.let { "Connexion impossible ($it)" } ?: "Connexion impossible"
    }
}

/** Heure seule le jour même, sinon date et heure. */
fun watchMoment(ms: Long, now: Long): String {
    fun day(t: Long) = Calendar.getInstance().apply { timeInMillis = t }.let { it.get(Calendar.YEAR) * 1000 + it.get(Calendar.DAY_OF_YEAR) }
    return if (day(ms) == day(now)) Fmt.time(ms) else Fmt.dateTime(ms)
}

/** Corps de l'alerte « ne répond plus ». */
fun downMessage(cause: String, since: Long, now: Long): String =
    "$cause.\nSans réponse depuis ${watchMoment(since, now)}, d'après les vérifications de ton téléphone."

/** Corps de la notification de retour. */
fun backMessage(since: Long, at: Long): String =
    "Sans réponse pendant environ ${Fmt.duration((at - since) / 1000)} (depuis ${watchMoment(since, at)})."
