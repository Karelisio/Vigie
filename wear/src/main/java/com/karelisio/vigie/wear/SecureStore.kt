package com.karelisio.vigie.wear

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import androidx.core.content.edit
import kotlinx.serialization.json.Json
import java.security.KeyStore
import java.util.Base64
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/**
 * Accès au serveur (nom, URL, token) chiffré en AES-256-GCM avec une clé de l'Android Keystore,
 * qui ne quitte jamais la montre. Seul le texte chiffré est écrit dans les préférences.
 */
class SecureStore(context: Context) {
    private val prefs = context.applicationContext.getSharedPreferences(FILE, Context.MODE_PRIVATE)
    private val json = Json { ignoreUnknownKeys = true }

    fun load(): WatchServer? {
        val stored = prefs.getString(KEY_SERVER, null) ?: return null
        return runCatching {
            val bytes = Base64.getDecoder().decode(stored)
            val cipher = Cipher.getInstance(TRANSFORMATION)
            cipher.init(Cipher.DECRYPT_MODE, key(), GCMParameterSpec(128, bytes, 0, IV_SIZE))
            val plain = cipher.doFinal(bytes, IV_SIZE, bytes.size - IV_SIZE)
            json.decodeFromString(WatchServer.serializer(), plain.decodeToString())
        }.getOrNull()
    }

    fun save(server: WatchServer) {
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.ENCRYPT_MODE, key())
        val encrypted = cipher.iv + cipher.doFinal(json.encodeToString(WatchServer.serializer(), server).encodeToByteArray())
        prefs.edit { putString(KEY_SERVER, Base64.getEncoder().encodeToString(encrypted)) }
    }

    fun clear() {
        prefs.edit { remove(KEY_SERVER) }
    }

    private fun key(): SecretKey {
        val ks = KeyStore.getInstance(KEYSTORE).apply { load(null) }
        (ks.getKey(ALIAS, null) as? SecretKey)?.let { return it }
        val generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, KEYSTORE)
        generator.init(
            KeyGenParameterSpec.Builder(ALIAS, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setKeySize(256)
                .build(),
        )
        return generator.generateKey()
    }

    private companion object {
        const val FILE = "vigie_wear_secure"
        const val KEY_SERVER = "server"
        const val KEYSTORE = "AndroidKeyStore"
        const val ALIAS = "vigie_wear_server"
        const val TRANSFORMATION = "AES/GCM/NoPadding"
        const val IV_SIZE = 12
    }
}
