package com.karelisio.vigie.ops

import com.karelisio.vigie.data.AgentJob
import com.karelisio.vigie.data.Container
import kotlin.math.roundToInt

/** Portion de la barre de progression (longueur sur [OperationProgress.MAX]). */
data class ProgressSegment(val length: Int, val label: String)

/** Repère sur la barre : un conteneur en échec dans une opération de groupe. */
data class ProgressPoint(val position: Int, val ok: Boolean)

/** Contenu de la notification d'une opération en cours. */
data class OperationProgress(
    val jobId: String,
    val title: String,
    val text: String,
    /** Avancement de 0 à [MAX]. */
    val progress: Int,
    val indeterminate: Boolean,
    val segments: List<ProgressSegment>,
    val points: List<ProgressPoint>,
    /** Texte court de la puce de la barre d'état (Android 16) : « 45 % ». */
    val shortText: String,
) {
    companion object {
        const val MAX = 1000

        /** Au-delà, Android fusionne les segments : une seule barre continue. */
        const val MAX_SEGMENTS = 15

        /** Android n'affiche que quelques repères. */
        const val MAX_POINTS = 4
    }
}

/** Bilan affiché à la fin d'une opération. */
data class OperationOutcome(
    val jobId: String,
    val ok: Boolean,
    val title: String,
    val text: String,
    /** Une ligne par conteneur pour les opérations de groupe. */
    val details: List<String> = emptyList(),
)

/** Construction de la progression et du bilan à partir des opérations de l'agent (sans Android, testable). */
object OperationText {
    private val GROUP_KINDS = setOf("stack", "batch", "auto")

    private val STACK_ACTIONS = mapOf(
        "update" to "mise à jour",
        "pull" to "téléchargement des images",
        "restart" to "redémarrage",
        "start" to "démarrage",
        "stop" to "arrêt",
    )

    /** Titre de la notification pendant l'opération. */
    fun title(job: AgentJob): String = when (job.kind) {
        "update" -> "Mise à jour de ${job.target}"
        "pull" -> "Téléchargement de l'image de ${job.target}"
        "self" -> "Mise à jour de l'agent"
        "stack" -> "Stack ${job.target} : ${STACK_ACTIONS[job.action] ?: job.action}"
        "batch" -> "Mise à jour de tous les conteneurs"
        "auto" -> "Mises à jour de la nuit"
        "cleanup" -> "Nettoyage du disque"
        "backup" -> "Sauvegarde"
        "restore" -> "Restauration" + target(job)
        "rollback" -> "Retour à la version précédente" + target(job)
        else -> "Opération en cours"
    }

    private fun target(job: AgentJob): String =
        job.target.takeIf { it.isNotBlank() && it != "all" }?.let { " de $it" }.orEmpty()

    /**
     * Nombre de conteneurs concernés par une opération de groupe, estimé à son début d'après la liste
     * des conteneurs (0 si inconnu) : il sert à découper la barre en un segment par conteneur.
     * Les mises à jour de la nuit suivent une sélection propre à l'agent : nombre inconnu.
     */
    fun members(job: AgentJob, containers: List<Container>): Int = when (job.kind) {
        "stack" -> containers.count { it.project == job.target }
        "batch" -> containers.count { it.updateAvailable }
        else -> 0
    }

    /** Progression d'une opération en cours ; [members] : voir [members]. */
    fun progress(job: AgentJob, members: Int = 0): OperationProgress {
        val value = (job.progress.coerceIn(0.0, 1.0) * OperationProgress.MAX).roundToInt()
        val queued = job.phase == "queued" || job.phase.isEmpty()
        val indeterminate = queued && value == 0
        val segments = segments(job, members)
        return OperationProgress(
            jobId = job.id,
            title = title(job),
            text = job.message.ifBlank { if (queued) "En file d'attente…" else "En cours…" },
            progress = value,
            indeterminate = indeterminate,
            segments = segments,
            points = if (segments.size > 1 && job.kind in GROUP_KINDS) points(job, segments.size) else emptyList(),
            shortText = if (indeterminate) "…" else "${value / 10} %",
        )
    }

    /** Progression pendant le redémarrage de l'agent, après le lancement de sa mise à jour. */
    fun restarting(job: AgentJob): OperationProgress = OperationProgress(
        jobId = job.id,
        title = title(job),
        text = "L'agent redémarre avec la nouvelle version…",
        progress = OperationProgress.MAX,
        indeterminate = true,
        segments = listOf(ProgressSegment(OperationProgress.MAX, "Redémarrage")),
        points = emptyList(),
        shortText = "…",
    )

    /** Découpage de la barre selon le déroulement de l'opération côté agent. */
    fun segments(job: AgentJob, members: Int = 0): List<ProgressSegment> {
        val max = OperationProgress.MAX
        return when {
            // Téléchargement (70 %), recréation (jusqu'à 85 %), démarrage et vérification.
            job.kind == "update" || (job.kind in GROUP_KINDS && job.action == "update" && members == 1) -> listOf(
                ProgressSegment(700, "Téléchargement"),
                ProgressSegment(150, "Recréation"),
                ProgressSegment(150, "Vérification"),
            )
            job.kind == "self" -> listOf(ProgressSegment(700, "Téléchargement"), ProgressSegment(300, "Remplacement"))
            job.kind in GROUP_KINDS && members in 2..OperationProgress.MAX_SEGMENTS -> {
                val each = max / members
                List(members) { i -> ProgressSegment(if (i == members - 1) max - each * (members - 1) else each, "Conteneur ${i + 1}") }
            }
            else -> listOf(ProgressSegment(max, title(job)))
        }
    }

    /** Repères des conteneurs en échec, à la fin de leur segment (les plus récents si trop nombreux). */
    private fun points(job: AgentJob, count: Int): List<ProgressPoint> = job.results
        .mapIndexedNotNull { i, r ->
            if (r.ok) null else ProgressPoint(((i + 1) * OperationProgress.MAX / count).coerceAtMost(OperationProgress.MAX), false)
        }
        .takeLast(OperationProgress.MAX_POINTS)

    /** Bilan d'une opération terminée. */
    fun outcome(job: AgentJob): OperationOutcome {
        val ok = job.ok
        val upToDate = job.message.contains("déjà à jour")
        val title = when (job.kind) {
            "update" -> when {
                !ok -> "Échec de la mise à jour de ${job.target}"
                upToDate -> "${job.target} est déjà à jour"
                else -> "${job.target} mis à jour"
            }
            "pull" -> if (ok) "Image de ${job.target} téléchargée" else "Échec du téléchargement pour ${job.target}"
            "self" -> when {
                !ok -> "Échec de la mise à jour de l'agent"
                upToDate -> "L'agent est déjà à jour"
                else -> "Mise à jour de l'agent lancée"
            }
            "stack" -> "Stack ${job.target} : " + if (ok) "terminé" else "échec"
            "batch" -> if (ok) "Mises à jour terminées" else "Mises à jour : échec"
            "auto" -> if (ok) "Mises à jour de la nuit terminées" else "Mises à jour de la nuit : échec"
            "cleanup" -> if (ok) "Nettoyage terminé" else "Échec du nettoyage"
            "backup" -> if (ok) "Sauvegarde terminée" else "Échec de la sauvegarde"
            "restore" -> if (ok) "Restauration terminée" else "Échec de la restauration"
            "rollback" -> if (ok) "Retour à la version précédente terminé" else "Échec du retour à la version précédente"
            else -> if (ok) "Opération terminée" else "Échec de l'opération"
        }
        val text = job.message.removePrefix("Échec : ").ifBlank { job.error.orEmpty() }.ifBlank { if (ok) "Terminé" else "Erreur inconnue" }
        val details = if (job.kind in GROUP_KINDS && job.results.size > 1) {
            job.results.map { r ->
                val line = if (r.name.isNotEmpty() && !r.message.startsWith(r.name)) "${r.name} : ${r.message}" else r.message.ifEmpty { r.name }
                (if (r.ok) "✓ " else "✗ ") + line
            }
        } else {
            emptyList()
        }
        return OperationOutcome(job.id, ok, title, text, details)
    }

    /** Bilan de la mise à jour de l'agent, une fois la nouvelle version en ligne (ou l'ancienne rétablie). */
    fun selfUpdated(jobId: String, from: String, to: String): OperationOutcome =
        OperationOutcome(jobId, true, "Agent mis à jour", if (from.isNotEmpty()) "$from → $to" else "Version $to")

    fun selfRolledBack(jobId: String, error: String): OperationOutcome = OperationOutcome(
        jobId, false, "Mise à jour de l'agent annulée",
        "Ancienne version rétablie" + error.takeIf { it.isNotBlank() }?.let { " : $it" }.orEmpty(),
    )

    /** Vrai si l'agent va redémarrer avec une nouvelle version (le résultat final arrive après). */
    fun awaitsRestart(job: AgentJob): Boolean =
        job.kind == "self" && job.done && job.ok && !job.message.contains("déjà à jour")
}
