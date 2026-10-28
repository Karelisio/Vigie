package com.karelisio.vigie.data

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * Version d'un conteneur d'avant sa dernière mise à jour par Vigie (agent 0.6.0 et plus) : le point
 * de retour, et ce qu'il permet.
 */
@Serializable
data class RollbackPoint(
    /** « » : mise à jour ; « vulnfix » : passage à une image corrigée par Vigie (agent 0.7.0), l'ancienne image est l'image d'origine. */
    val kind: String = "",
    val container: String = "",
    @SerialName("image_ref") val imageRef: String = "",
    @SerialName("old_image_id") val oldImageId: String = "",
    @SerialName("old_digests") val oldDigests: List<String> = emptyList(),
    /** Date de construction de l'ancienne image (ms, 0 si inconnue). */
    @SerialName("old_created") val oldCreated: Long = 0,
    @SerialName("old_version") val oldVersion: String = "",
    @SerialName("new_image_id") val newImageId: String = "",
    @SerialName("new_version") val newVersion: String = "",
    /** Date de la mise à jour (ms). */
    @SerialName("updated_at") val updatedAt: Long = 0,
    /** Date du retour à la version précédente (ms), 0 s'il n'a pas eu lieu. */
    @SerialName("rolled_back_at") val rolledBackAt: Long = 0,
    /** La version écartée n'est plus installée par les mises à jour automatiques. */
    val ignored: Boolean = false,
    /** ready (retour possible), rolled_back (déjà revenu) ou stale (le conteneur a changé d'image depuis). */
    val state: String = "",
    /** Version (label OCI), sinon début du digest ou de l'ID de l'image. */
    @SerialName("old_label") val oldLabel: String = "",
    @SerialName("new_label") val newLabel: String = "",
    /** L'ancienne image est encore sur le disque. */
    @SerialName("image_present") val imagePresent: Boolean = false,
    /** Sinon, elle peut être re-téléchargée par son digest. */
    val downloadable: Boolean = false,
) {
    val ready get() = state == "ready"
    val rolledBack get() = state == "rolled_back"

    /** Retour à l'image d'origine d'une image corrigée par Vigie. */
    val vulnfix get() = kind == "vulnfix"

    /** Le retour peut aboutir : image sur le disque ou re-téléchargeable. */
    val reachable get() = imagePresent || downloadable
}

private fun Repository.server(): Server = servers.active ?: throw ApiException(0, "Aucun serveur configuré")

/** Point de retour du conteneur, null s'il n'en a pas (jamais mis à jour par Vigie). */
suspend fun Repository.rollbackPoint(ref: String): RollbackPoint? = try {
    api.get<RollbackPoint>(server(), "/api/v1/containers/$ref/rollback")
} catch (e: ApiException) {
    if (e.code == 404) null else throw e
}

/** Revient à la version précédente : l'opération suit comme une mise à jour (bannière, snackbar). */
suspend fun Repository.rollbackContainer(ref: String): AgentJob =
    api.postEmpty(server(), "/api/v1/containers/$ref/rollback")

/** Autorise de nouveau la version écartée par le retour arrière. */
suspend fun Repository.unignoreRollback(ref: String): RollbackPoint =
    api.request(server(), "DELETE", "/api/v1/containers/$ref/rollback/ignore", null, RollbackPoint.serializer())

/** « Version précédente : 1.0, mise à jour le 28 oct. 2026 14:02 » ou, après une correction des failles, « Image d'origine : 1.0, corrigée le … ». */
fun RollbackPoint.summary(date: (Long) -> String): String = if (vulnfix) {
    "Image d'origine : $oldLabel, non corrigée" + if (updatedAt > 0) " (correction du ${date(updatedAt)})" else ""
} else {
    "Version précédente : $oldLabel" + if (updatedAt > 0) ", mise à jour le ${date(updatedAt)}" else ""
}
