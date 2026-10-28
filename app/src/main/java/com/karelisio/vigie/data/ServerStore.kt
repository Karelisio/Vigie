package com.karelisio.vigie.data

import android.content.Context
import android.content.SharedPreferences
import android.net.Uri
import androidx.core.content.edit
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKey
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import java.util.UUID

/** Un serveur surveillé : prévu pour plusieurs serveurs (nom, URL, token). */
@Serializable
data class Server(
    val id: String = UUID.randomUUID().toString(),
    val name: String,
    val url: String,
    val token: String,
) {
    val baseUrl: String get() = url.trimEnd('/')
    val host: String get() = runCatching { Uri.parse(url).host }.getOrNull() ?: url

    /** Accès partagé en lecture seule (jeton créé depuis « Accès partagés »). */
    val readOnly: Boolean get() = token.startsWith("ro_")

    companion object {
        /** Lit un lien `vigie://connect?name=…&url=…&token=…` (contenu du QR code de l'agent). */
        fun fromLink(link: String): Server? {
            val uri = runCatching { Uri.parse(link.trim()) }.getOrNull() ?: return null
            if (uri.scheme != "vigie") return null
            val url = uri.getQueryParameter("url")?.trim().orEmpty()
            val token = uri.getQueryParameter("token")?.trim().orEmpty()
            if (token.length < 32) return null
            val name = uri.getQueryParameter("name")?.trim().orEmpty()
            return Server(name = name.ifEmpty { Uri.parse(url).host ?: "Serveur" }, url = normalizeUrl(url), token = token)
        }

        fun normalizeUrl(raw: String): String {
            var u = raw.trim().trimEnd('/')
            if (u.isNotEmpty() && !u.startsWith("http://") && !u.startsWith("https://")) u = "https://$u"
            return u
        }
    }
}

/** Stockage chiffré (EncryptedSharedPreferences) des serveurs et de leurs tokens. */
class ServerStore(context: Context) {
    private val json = Json { ignoreUnknownKeys = true }

    @Suppress("DEPRECATION")
    private val prefs: SharedPreferences = runCatching { open(context) }.getOrElse {
        // Keystore corrompu (restauration de sauvegarde…) : on repart de zéro.
        context.deleteSharedPreferences(FILE)
        open(context)
    }

    private val _servers = MutableStateFlow(load())
    val servers: StateFlow<List<Server>> = _servers.asStateFlow()

    private val _activeId = MutableStateFlow(prefs.getString(KEY_ACTIVE, null) ?: _servers.value.firstOrNull()?.id)
    val activeId: StateFlow<String?> = _activeId.asStateFlow()

    val active: Server? get() = _servers.value.firstOrNull { it.id == _activeId.value } ?: _servers.value.firstOrNull()

    @Suppress("DEPRECATION")
    private fun open(context: Context): SharedPreferences {
        val key = MasterKey.Builder(context).setKeyScheme(MasterKey.KeyScheme.AES256_GCM).build()
        return EncryptedSharedPreferences.create(
            context, FILE, key,
            EncryptedSharedPreferences.PrefKeyEncryptionScheme.AES256_SIV,
            EncryptedSharedPreferences.PrefValueEncryptionScheme.AES256_GCM,
        )
    }

    private fun load(): List<Server> =
        prefs.getString(KEY_SERVERS, null)?.let { runCatching { json.decodeFromString<List<Server>>(it) }.getOrNull() } ?: emptyList()

    private fun save(list: List<Server>) {
        prefs.edit { putString(KEY_SERVERS, json.encodeToString(list)) }
        _servers.value = list
    }

    fun upsert(server: Server, makeActive: Boolean = true) {
        val list = _servers.value.toMutableList()
        val i = list.indexOfFirst { it.id == server.id || it.baseUrl == server.baseUrl }
        val saved = if (i >= 0) server.copy(id = list[i].id).also { list[i] = it } else server.also { list += it }
        save(list)
        if (makeActive || _activeId.value == null) setActive(saved.id)
    }

    fun remove(id: String) {
        save(_servers.value.filterNot { it.id == id })
        if (_activeId.value == id) setActive(_servers.value.firstOrNull()?.id)
    }

    fun setActive(id: String?) {
        prefs.edit { if (id == null) remove(KEY_ACTIVE) else putString(KEY_ACTIVE, id) }
        _activeId.value = id
    }

    fun get(id: String): Server? = _servers.value.firstOrNull { it.id == id }

    private companion object {
        const val FILE = "vigie_secure"
        const val KEY_SERVERS = "servers"
        const val KEY_ACTIVE = "active"
    }
}
