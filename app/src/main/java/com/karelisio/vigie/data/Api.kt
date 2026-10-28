package com.karelisio.vigie.data

import com.karelisio.vigie.BuildConfig
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.withContext
import kotlinx.serialization.KSerializer
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlinx.serialization.serializer
import okhttp3.Call
import okhttp3.Callback
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import java.io.IOException
import java.net.SocketTimeoutException
import java.net.UnknownHostException
import java.util.concurrent.TimeUnit
import javax.net.ssl.SSLException
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

val VigieJson = Json {
    ignoreUnknownKeys = true
    explicitNulls = false
    coerceInputValues = true
    encodeDefaults = true
}

/** Erreur d'API avec un message lisible en français. */
class ApiException(val code: Int, message: String) : IOException(message) {
    val unauthorized get() = code == 401
}

@Serializable
private data class ErrorBody(val error: String = "")

class Api(val http: OkHttpClient = defaultClient()) {

    suspend inline fun <reified T> get(server: Server, path: String): T = request(server, "GET", path, null, serializer())

    suspend inline fun <reified B, reified T> post(server: Server, path: String, body: B): T =
        request(server, "POST", path, VigieJson.encodeToString(serializer<B>(), body), serializer())

    suspend inline fun <reified T> postEmpty(server: Server, path: String): T = request(server, "POST", path, "", serializer())

    suspend inline fun <reified B, reified T> put(server: Server, path: String, body: B): T =
        request(server, "PUT", path, VigieJson.encodeToString(serializer<B>(), body), serializer())

    suspend inline fun <reified B> delete(server: Server, path: String, body: B?) {
        request(server, "DELETE", path, body?.let { VigieJson.encodeToString(serializer<B>(), it) }, serializer<Unit>())
    }

    suspend fun <T> request(server: Server, method: String, path: String, body: String?, deserializer: KSerializer<T>): T {
        val builder = Request.Builder()
            .url(server.baseUrl + path)
            .header("Authorization", "Bearer ${server.token}")
            .header("User-Agent", USER_AGENT)
            .header("Accept", "application/json")
        val rb = body?.toRequestBody(JSON_TYPE)
        builder.method(method, if (method == "GET") null else rb ?: "".toRequestBody(JSON_TYPE))
        val response = try {
            http.newCall(builder.build()).await()
        } catch (e: IOException) {
            throw humanize(e)
        }
        return response.use { r ->
            val text = withContext(Dispatchers.IO) { r.body.string() }
            if (!r.isSuccessful) {
                val msg = runCatching { VigieJson.decodeFromString<ErrorBody>(text).error }.getOrNull().orEmpty()
                throw ApiException(r.code, describeHttp(r.code, msg))
            }
            @Suppress("UNCHECKED_CAST")
            if (deserializer.descriptor.serialName == "kotlin.Unit") Unit as T
            else VigieJson.decodeFromString(deserializer, text)
        }
    }

    fun webSocket(server: Server, path: String, listener: WebSocketListener): WebSocket {
        val req = Request.Builder()
            .url(server.baseUrl.replaceFirst("http", "ws") + path)
            .header("Authorization", "Bearer ${server.token}")
            .header("User-Agent", USER_AGENT)
            .build()
        return http.newWebSocket(req, listener)
    }

    companion object {
        val USER_AGENT = "Vigie-Android/${BuildConfig.VERSION_NAME}"
        private val JSON_TYPE = "application/json; charset=utf-8".toMediaType()

        fun defaultClient(): OkHttpClient = OkHttpClient.Builder()
            .connectTimeout(10, TimeUnit.SECONDS)
            .readTimeout(30, TimeUnit.SECONDS)
            .writeTimeout(30, TimeUnit.SECONDS)
            .pingInterval(20, TimeUnit.SECONDS)
            .retryOnConnectionFailure(true)
            .build()

        fun describeHttp(code: Int, msg: String): String = when (code) {
            401 -> "Token refusé par l'agent"
            403 -> "Accès refusé (vérifie le contournement SSO dans Pangolin)"
            404 -> msg.ifEmpty { "Introuvable (l'URL pointe-t-elle vers l'agent ?)" }
            429 -> msg.ifEmpty { "Trop de requêtes, réessaie dans un instant" }
            502, 503, 504 -> msg.ifEmpty { "Agent injoignable derrière le proxy ($code)" }
            else -> msg.ifEmpty { "Erreur $code" }
        }

        fun humanize(e: Throwable): IOException = when (e) {
            is ApiException -> e
            is UnknownHostException -> IOException("Adresse introuvable (DNS)", e)
            is SocketTimeoutException -> IOException("Délai dépassé", e)
            is SSLException -> IOException("Erreur HTTPS : certificat invalide ?", e)
            is IOException -> IOException(e.message?.takeIf { it.isNotBlank() } ?: "Connexion impossible", e)
            else -> IOException(e.message ?: "Erreur inconnue", e)
        }
    }
}

suspend fun Call.await(): Response = suspendCancellableCoroutine { cont ->
    enqueue(object : Callback {
        override fun onResponse(call: Call, response: Response) = cont.resume(response)
        override fun onFailure(call: Call, e: IOException) {
            if (!cont.isCancelled) cont.resumeWithException(e)
        }
    })
    cont.invokeOnCancellation { runCatching { cancel() } }
}
