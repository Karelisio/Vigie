package com.karelisio.vigie.wear

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.withContext
import kotlinx.serialization.KSerializer
import kotlinx.serialization.json.Json
import okhttp3.Call
import okhttp3.Callback
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import java.io.IOException
import java.util.concurrent.TimeUnit
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

/** Erreur de lecture de l'API, avec sa cause pour l'affichage. */
class FetchException(val error: FetchError, val detail: String = "") : IOException(error.name + " " + detail)

/** Lecture directe de l'API de l'agent depuis la montre (Wi-Fi, LTE ou via le téléphone). */
object AgentClient {
    private val http = OkHttpClient.Builder()
        .connectTimeout(10, TimeUnit.SECONDS)
        .readTimeout(15, TimeUnit.SECONDS)
        .callTimeout(20, TimeUnit.SECONDS)
        .build()

    private val json = Json {
        ignoreUnknownKeys = true
        coerceInputValues = true
        explicitNulls = false
    }

    suspend fun overview(server: WatchServer): Overview = get(server, "/api/v1/overview", Overview.serializer())

    suspend fun system(server: WatchServer): SystemSnapshot = get(server, "/api/v1/system", SystemSnapshot.serializer())

    private suspend fun <T> get(server: WatchServer, path: String, serializer: KSerializer<T>): T {
        val url = (server.baseUrl + path).toHttpUrlOrNull() ?: throw FetchException(FetchError.Server, "adresse invalide")
        val request = Request.Builder()
            .url(url)
            .header("Authorization", "Bearer ${server.token}")
            .header("User-Agent", "VigieWear/${BuildConfig.VERSION_NAME}")
            .header("Accept", "application/json")
            .build()
        val response = try {
            http.newCall(request).await()
        } catch (e: IOException) {
            throw FetchException(FetchError.Unreachable, e.message.orEmpty())
        }
        return response.use { r ->
            when {
                r.code == 401 || r.code == 403 -> throw FetchException(FetchError.Unauthorized)
                !r.isSuccessful -> throw FetchException(FetchError.Server, "HTTP ${r.code}")
                else -> withContext(Dispatchers.IO) {
                    runCatching { json.decodeFromString(serializer, r.body.string()) }
                        .getOrElse { throw FetchException(FetchError.Server, "réponse illisible") }
                }
            }
        }
    }

    private suspend fun Call.await(): Response = suspendCancellableCoroutine { cont ->
        cont.invokeOnCancellation { cancel() }
        enqueue(object : Callback {
            override fun onResponse(call: Call, response: Response) = cont.resume(response) { _, r, _ -> r.close() }

            override fun onFailure(call: Call, e: IOException) {
                if (!cont.isCancelled) cont.resumeWithException(e)
            }
        })
    }
}
