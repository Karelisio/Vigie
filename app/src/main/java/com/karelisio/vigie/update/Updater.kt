package com.karelisio.vigie.update

import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.pm.PackageInstaller
import android.os.Build
import com.karelisio.vigie.BuildConfig
import com.karelisio.vigie.data.Prefs
import com.karelisio.vigie.data.VigieJson
import com.karelisio.vigie.data.await
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.withContext
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import okhttp3.OkHttpClient
import okhttp3.Request
import java.io.File
import java.io.IOException

@Serializable
internal data class GhAsset(
    val name: String = "",
    val size: Long = 0,
    @SerialName("browser_download_url") val url: String = "",
)

@Serializable
private data class GhRelease(
    @SerialName("tag_name") val tag: String = "",
    val name: String? = null,
    val body: String? = null,
    @SerialName("html_url") val htmlUrl: String = "",
    @SerialName("published_at") val publishedAt: String? = null,
    val draft: Boolean = false,
    val prerelease: Boolean = false,
    val assets: List<GhAsset> = emptyList(),
)

data class Release(
    val version: String,
    val title: String,
    val changelog: String,
    val apkUrl: String,
    val apkSize: Long,
    val htmlUrl: String,
    val publishedAt: String?,
)

sealed interface UpdateState {
    data object Idle : UpdateState
    data class Available(val release: Release) : UpdateState
    data class Downloading(val release: Release, val progress: Float, val bytes: Long) : UpdateState
    data class Installing(val release: Release) : UpdateState
    data class Failed(val release: Release?, val message: String) : UpdateState
}

/** Mise à jour intégrée : vérifie la dernière release GitHub, télécharge l'APK et l'installe via PackageInstaller. */
class Updater(private val context: Context, http: OkHttpClient, private val prefs: Prefs) {
    private val http = http.newBuilder().pingInterval(0, java.util.concurrent.TimeUnit.SECONDS).build()
    private val _state = MutableStateFlow<UpdateState>(UpdateState.Idle)
    val state: StateFlow<UpdateState> = _state.asStateFlow()

    val currentVersion: String get() = BuildConfig.VERSION_NAME

    /** Vérifie au plus toutes les 6 h, sauf si [force]. Renvoie la release si elle est plus récente. */
    suspend fun check(force: Boolean = false): Release? {
        val p = prefs.current()
        val now = System.currentTimeMillis()
        if (!force && now - p.lastUpdateCheck < SIX_HOURS) return (state.value as? UpdateState.Available)?.release
        val req = Request.Builder()
            .url("https://api.github.com/repos/${BuildConfig.GITHUB_REPO}/releases/latest")
            .header("Accept", "application/vnd.github+json")
            .header("X-GitHub-Api-Version", "2022-11-28")
            .build()
        val rel = http.newCall(req).await().use { r ->
            if (r.code == 404) return null
            if (!r.isSuccessful) throw IOException("GitHub a répondu ${r.code}")
            VigieJson.decodeFromString<GhRelease>(r.body.string())
        }
        prefs.setLastUpdateCheck(now)
        val apk = phoneApk(rel.assets) ?: return null
        val version = rel.tag.removePrefix("v")
        if (!isNewer(version, currentVersion)) return null
        if (!force && version == p.skippedVersion) return null
        val release = Release(
            version = version,
            title = rel.name?.takeIf { it.isNotBlank() } ?: "Vigie $version",
            changelog = rel.body.orEmpty().trim(),
            apkUrl = apk.url,
            apkSize = apk.size,
            htmlUrl = rel.htmlUrl,
            publishedAt = rel.publishedAt,
        )
        _state.value = UpdateState.Available(release)
        return release
    }

    suspend fun skip(release: Release) {
        prefs.setSkippedVersion(release.version)
        _state.value = UpdateState.Idle
    }

    fun dismiss() {
        if (_state.value !is UpdateState.Downloading && _state.value !is UpdateState.Installing) _state.value = UpdateState.Idle
    }

    fun canInstall(): Boolean = context.packageManager.canRequestPackageInstalls()

    suspend fun downloadAndInstall(release: Release) {
        try {
            val file = download(release)
            _state.value = UpdateState.Installing(release)
            install(file)
        } catch (e: Exception) {
            _state.value = UpdateState.Failed(release, e.message ?: "Échec du téléchargement")
        }
    }

    private suspend fun download(release: Release): File = withContext(Dispatchers.IO) {
        val dir = File(context.cacheDir, "updates").apply { mkdirs() }
        dir.listFiles()?.forEach { it.delete() }
        val file = File(dir, "vigie-${release.version}.apk")
        _state.value = UpdateState.Downloading(release, 0f, 0)
        http.newCall(Request.Builder().url(release.apkUrl).build()).await().use { r ->
            if (!r.isSuccessful) throw IOException("Téléchargement refusé (${r.code})")
            val body = r.body
            val total = body.contentLength().takeIf { it > 0 } ?: release.apkSize
            body.byteStream().use { input ->
                file.outputStream().use { out ->
                    val buf = ByteArray(64 * 1024)
                    var read = 0L
                    var lastEmit = 0L
                    while (true) {
                        val n = input.read(buf)
                        if (n < 0) break
                        out.write(buf, 0, n)
                        read += n
                        val now = System.nanoTime()
                        if (now - lastEmit > 50_000_000) {
                            lastEmit = now
                            _state.value = UpdateState.Downloading(release, if (total > 0) read.toFloat() / total else 0f, read)
                        }
                    }
                    _state.value = UpdateState.Downloading(release, 1f, read)
                }
            }
            if (release.apkSize > 0 && file.length() != release.apkSize) throw IOException("Fichier incomplet")
        }
        file
    }

    private suspend fun install(file: File) = withContext(Dispatchers.IO) {
        val installer = context.packageManager.packageInstaller
        val params = PackageInstaller.SessionParams(PackageInstaller.SessionParams.MODE_FULL_INSTALL).apply {
            setAppPackageName(context.packageName)
            if (Build.VERSION.SDK_INT >= 31) setRequireUserAction(PackageInstaller.SessionParams.USER_ACTION_NOT_REQUIRED)
            if (Build.VERSION.SDK_INT >= 34) setPackageSource(PackageInstaller.PACKAGE_SOURCE_DOWNLOADED_FILE)
        }
        val id = installer.createSession(params)
        installer.openSession(id).use { session ->
            session.openWrite("vigie.apk", 0, file.length()).use { out ->
                file.inputStream().use { it.copyTo(out) }
                session.fsync(out)
            }
            val intent = Intent(context, InstallReceiver::class.java).setPackage(context.packageName)
            val flags = PendingIntent.FLAG_UPDATE_CURRENT or if (Build.VERSION.SDK_INT >= 31) PendingIntent.FLAG_MUTABLE else 0
            val pi = PendingIntent.getBroadcast(context, id, intent, flags)
            session.commit(pi.intentSender)
        }
    }

    internal fun onInstallResult(status: Int, message: String?) {
        val release = when (val s = _state.value) {
            is UpdateState.Installing -> s.release
            is UpdateState.Downloading -> s.release
            else -> null
        }
        if (status != PackageInstaller.STATUS_SUCCESS && status != PackageInstaller.STATUS_PENDING_USER_ACTION) {
            _state.value = UpdateState.Failed(release, message ?: "Installation annulée")
        }
    }

    companion object {
        private const val SIX_HOURS = 6 * 60 * 60 * 1000L
        private val PHONE_APK = Regex("vigie-\\d+\\.\\d+\\.\\d+\\.apk", RegexOption.IGNORE_CASE)

        /**
         * APK du téléphone parmi les fichiers de la release (`vigie-X.Y.Z.apk`), jamais celui de la
         * montre (`vigie-wear-X.Y.Z.apk`), qui ne s'installe pas sur un téléphone.
         */
        internal fun phoneApk(assets: List<GhAsset>): GhAsset? {
            val apks = assets.filter { it.name.endsWith(".apk", ignoreCase = true) && !it.name.contains("wear", ignoreCase = true) }
            return apks.firstOrNull { PHONE_APK.matches(it.name) } ?: apks.firstOrNull()
        }

        /** Compare deux versions sémantiques (X.Y.Z, suffixes ignorés). */
        fun isNewer(candidate: String, current: String): Boolean {
            fun parse(v: String) = v.substringBefore('-').split('.').map { it.toIntOrNull() ?: 0 } + listOf(0, 0, 0)
            val a = parse(candidate)
            val b = parse(current)
            for (i in 0 until 3) if (a[i] != b[i]) return a[i] > b[i]
            // Même numéro : une version finale l'emporte sur une version de développement.
            return current.contains('-') && !candidate.contains('-')
        }
    }
}

/** Reçoit le résultat de PackageInstaller et affiche la confirmation système si nécessaire. */
class InstallReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        val status = intent.getIntExtra(PackageInstaller.EXTRA_STATUS, PackageInstaller.STATUS_FAILURE)
        if (status == PackageInstaller.STATUS_PENDING_USER_ACTION) {
            @Suppress("DEPRECATION")
            val confirm = if (Build.VERSION.SDK_INT >= 33) intent.getParcelableExtra(Intent.EXTRA_INTENT, Intent::class.java)
            else intent.getParcelableExtra(Intent.EXTRA_INTENT)
            confirm?.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)?.let { runCatching { context.startActivity(it) } }
        }
        com.karelisio.vigie.appContainer.updater.onInstallResult(status, intent.getStringExtra(PackageInstaller.EXTRA_STATUS_MESSAGE))
    }
}
