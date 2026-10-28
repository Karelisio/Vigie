package com.karelisio.vigie.data

import android.content.Context
import androidx.datastore.preferences.core.booleanPreferencesKey
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.longPreferencesKey
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map

private val Context.dataStore by preferencesDataStore("vigie_prefs")

/** Verrouillage biométrique : désactivé, à l'ouverture, ou seulement pour les actions. */
enum class LockMode { Off, Open, Actions }

data class UserPrefs(
    val onboardingDone: Boolean = false,
    val dynamicColor: Boolean = true,
    val notifications: Boolean = true,
    val lastUpdateCheck: Long = 0,
    val skippedVersion: String = "",
    val fcmToken: String = "",
    val lockMode: LockMode = LockMode.Off,
    val favorites: Map<String, Set<String>> = emptyMap(),
)

/** Réglages non sensibles (DataStore). Les tokens sont dans [ServerStore], chiffrés. */
class Prefs(private val context: Context) {
    private object K {
        val onboarding = booleanPreferencesKey("onboarding_done")
        val dynamic = booleanPreferencesKey("dynamic_color")
        val notifications = booleanPreferencesKey("notifications")
        val lastUpdateCheck = longPreferencesKey("last_update_check")
        val skipped = stringPreferencesKey("skipped_version")
        val fcm = stringPreferencesKey("fcm_token")
        val lock = stringPreferencesKey("lock_mode")
        fun fav(serverId: String) = androidx.datastore.preferences.core.stringSetPreferencesKey("fav_$serverId")
    }

    val flow: Flow<UserPrefs> = context.dataStore.data.map { p ->
        UserPrefs(
            onboardingDone = p[K.onboarding] ?: false,
            dynamicColor = p[K.dynamic] ?: true,
            notifications = p[K.notifications] ?: true,
            lastUpdateCheck = p[K.lastUpdateCheck] ?: 0,
            skippedVersion = p[K.skipped] ?: "",
            fcmToken = p[K.fcm] ?: "",
            lockMode = runCatching { LockMode.valueOf(p[K.lock] ?: "Off") }.getOrDefault(LockMode.Off),
            favorites = p.asMap().entries
                .filter { it.key.name.startsWith("fav_") }
                .associate { (k, v) -> k.name.removePrefix("fav_") to (v as? Set<*>)?.filterIsInstance<String>()?.toSet().orEmpty() },
        )
    }

    suspend fun current(): UserPrefs = flow.first()

    suspend fun setOnboardingDone(done: Boolean) = context.dataStore.edit { it[K.onboarding] = done }
    suspend fun setDynamicColor(on: Boolean) = context.dataStore.edit { it[K.dynamic] = on }
    suspend fun setNotifications(on: Boolean) = context.dataStore.edit { it[K.notifications] = on }
    suspend fun setLastUpdateCheck(ts: Long) = context.dataStore.edit { it[K.lastUpdateCheck] = ts }
    suspend fun setSkippedVersion(v: String) = context.dataStore.edit { it[K.skipped] = v }
    suspend fun setFcmToken(t: String) = context.dataStore.edit { it[K.fcm] = t }
    suspend fun setLockMode(m: LockMode) = context.dataStore.edit { it[K.lock] = m.name }

    /** Épingle ou désépingle un conteneur (par nom, stable aux recréations). Renvoie le nouvel état. */
    suspend fun toggleFavorite(serverId: String, name: String): Boolean {
        var pinned = false
        context.dataStore.edit {
            val cur = it[K.fav(serverId)].orEmpty()
            pinned = name !in cur
            it[K.fav(serverId)] = if (pinned) cur + name else cur - name
        }
        return pinned
    }
}
