package com.karelisio.vigie.data

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/** Version de l'image d'un conteneur publiée sur GitHub. */
@Serializable
data class ImageRelease(
    val tag: String = "",
    val name: String = "",
    @SerialName("published_at") val publishedAt: Long = 0,
    /** Notes de version en Markdown (6000 caractères au plus). */
    val body: String = "",
    val url: String = "",
    /** Publiée après la version du conteneur. */
    val newer: Boolean = false,
)

/** Nouveautés de l'image d'un conteneur (agent 0.4.0 et plus). */
@Serializable
data class ImageChangelog(
    /** « github.com/owner/repo », vide si le dépôt est inconnu. */
    val source: String = "",
    /** Version du conteneur, vide si inconnue. */
    val current: String = "",
    val releases: List<ImageRelease> = emptyList(),
    val error: String? = null,
)
