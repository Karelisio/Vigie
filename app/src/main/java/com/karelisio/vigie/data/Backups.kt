package com.karelisio.vigie.data

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/** Valeur renvoyée par l'agent à la place d'une clé enregistrée ; renvoyée telle quelle (ou vide), elle la conserve. */
const val BACKUP_SECRET_MASK = "••••••••"

/** Élément choisi pour la sauvegarde : « volume:<nom> », « bind:<chemin> » ou « vigie ». */
@Serializable
data class BackupSelection(val id: String, val stop: Boolean = false)

/** Configuration des sauvegardes (agent 0.5.0 et plus). Les clés arrivent masquées. */
@Serializable
data class BackupConfig(
    val schedule: Boolean = false,
    val time: String = "03:30",
    val retention: Int = 7,
    val endpoint: String = "",
    val region: String = "",
    val bucket: String = "",
    val prefix: String = "vigie",
    @SerialName("access_key") val accessKey: String = "",
    @SerialName("secret_key") val secretKey: String = "",
    val items: List<BackupSelection> = emptyList(),
) {
    val storageReady get() = endpoint.isNotBlank() && region.isNotBlank() && bucket.isNotBlank() &&
        accessKey.isNotBlank() && secretKey.isNotBlank()
}

@Serializable
data class BackupState(
    val config: BackupConfig = BackupConfig(),
    /** Prochaine sauvegarde planifiée (ms), 0 si aucune. */
    @SerialName("next_run") val nextRun: Long = 0,
)

/** Élément sauvegardable proposé par l'agent. */
@Serializable
data class BackupItem(
    val id: String,
    /** « volume », « bind » ou « vigie ». */
    val kind: String = "",
    val name: String = "",
    /** Dossier de ses copies dans le stockage. */
    val key: String = "",
    val path: String = "",
    val containers: List<String> = emptyList(),
    /** Taille estimée (octets), 0 si inconnue. */
    val size: Long = 0,
)

@Serializable
data class BackupItemResult(
    val id: String = "",
    val name: String = "",
    val key: String = "",
    val size: Long = 0,
    val raw: Long = 0,
    val ok: Boolean = false,
    val error: String = "",
)

/** Bilan d'une sauvegarde. */
@Serializable
data class BackupRecord(
    val ts: Long = 0,
    val duration: Long = 0,
    val size: Long = 0,
    val ok: Boolean = false,
    val error: String = "",
    /** « manual » ou « schedule ». */
    val trigger: String = "",
    val items: List<BackupItemResult> = emptyList(),
)

/** Copie présente dans le stockage. */
@Serializable
data class BackupObject(
    val key: String = "",
    /** Dossier de l'élément (BackupItem.key). */
    val item: String = "",
    val size: Long = 0,
    val ts: Long = 0,
    /** Copie de sécurité faite avant la dernière restauration (agent 0.6.0). */
    val safety: Boolean = false,
)

/** Demande de restauration : élément (BackupItem.id) et copie (BackupObject.key). */
@Serializable
data class BackupRestoreRequest(val item: String, val key: String)

/** Volumes et dossiers se restaurent depuis l'appli ; les données de Vigie, à la main. */
val BackupItem.restorable: Boolean get() = kind == "volume" || kind == "bind"

/** Copies d'un dossier du stockage : la copie de sécurité de la dernière restauration à part. */
data class BackupCopies(
    val folder: String,
    /** Élément actuel correspondant, null s'il n'existe plus. */
    val item: BackupItem?,
    val safety: BackupObject?,
    /** De la plus récente à la plus ancienne. */
    val copies: List<BackupObject>,
)

/** Regroupe les copies par élément, dans l'ordre de l'agent. */
fun groupCopies(objects: List<BackupObject>, items: List<BackupItem>): List<BackupCopies> =
    objects.groupBy { it.item }.map { (folder, list) ->
        BackupCopies(
            folder = folder,
            item = items.firstOrNull { it.key == folder },
            safety = list.filter { it.safety }.maxByOrNull { it.ts },
            copies = list.filterNot { it.safety }.sortedByDescending { it.key },
        )
    }

@Serializable
data class BackupTestResult(val ok: Boolean = false, val message: String = "")

private fun Repository.server(): Server = servers.active ?: throw ApiException(0, "Aucun serveur configuré")

suspend fun Repository.backupState(): BackupState = api.get(server(), "/api/v1/backups/config")

suspend fun Repository.saveBackupConfig(c: BackupConfig): BackupState = api.put(server(), "/api/v1/backups/config", c)

suspend fun Repository.backupItems(): List<BackupItem> = api.get(server(), "/api/v1/backups/items")

suspend fun Repository.backupHistory(): List<BackupRecord> = api.get(server(), "/api/v1/backups/history")

suspend fun Repository.backupObjects(): List<BackupObject> = api.get(server(), "/api/v1/backups/objects")

/** Essaie le stockage avec la configuration affichée, pas encore enregistrée. */
suspend fun Repository.testBackupStorage(c: BackupConfig): BackupTestResult = api.post(server(), "/api/v1/backups/test", c)

suspend fun Repository.runBackup(): AgentJob = api.postEmpty(server(), "/api/v1/backups/run")

/** Lance la restauration d'une copie sur son élément (agent 0.6.0) : opération « restore ». */
suspend fun Repository.restoreBackup(item: String, key: String): AgentJob =
    api.post(server(), "/api/v1/backups/restore", BackupRestoreRequest(item, key))
