package com.karelisio.vigie

import com.karelisio.vigie.data.AgentJob
import com.karelisio.vigie.data.Container
import com.karelisio.vigie.data.JobResult
import com.karelisio.vigie.ops.OperationProgress
import com.karelisio.vigie.ops.OperationText
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class OperationProgressTest {
    private fun job(
        kind: String = "update",
        action: String = "update",
        target: String = "nextcloud",
        phase: String = "pulling",
        progress: Double = 0.0,
        message: String = "",
        done: Boolean = false,
        ok: Boolean = false,
        results: List<JobResult> = emptyList(),
    ) = AgentJob(
        id = "j1", kind = kind, action = action, target = target, phase = phase, progress = progress,
        message = message, done = done, ok = ok, results = results, startedAt = 1_000,
    )

    @Test
    fun queuedJobIsIndeterminate() {
        val p = OperationText.progress(job(phase = "queued", message = "En file d'attente…"))
        assertTrue(p.indeterminate)
        assertEquals(0, p.progress)
        assertEquals("Mise à jour de nextcloud", p.title)
        assertEquals("…", p.shortText)
    }

    @Test
    fun containerUpdateFollowsTheAgentPhases() {
        val p = OperationText.progress(job(progress = 0.35, message = "nextcloud : téléchargement 50 %"))
        assertFalse(p.indeterminate)
        assertEquals(350, p.progress)
        assertEquals("35 %", p.shortText)
        assertEquals("nextcloud : téléchargement 50 %", p.text)
        // Téléchargement jusqu'à 70 %, recréation jusqu'à 85 %, puis vérification.
        assertEquals(listOf(700, 150, 150), p.segments.map { it.length })
        assertTrue(p.points.isEmpty())
    }

    @Test
    fun progressIsClamped() {
        assertEquals(OperationProgress.MAX, OperationText.progress(job(progress = 1.7)).progress)
        assertEquals(0, OperationText.progress(job(progress = -1.0, phase = "rollback")).progress)
    }

    @Test
    fun stackGetsOneSegmentPerContainerAndPointsForFailures() {
        val containers = listOf(
            Container("1", "radarr", project = "media"),
            Container("2", "sonarr", project = "media"),
            Container("3", "prowlarr", project = "media"),
            Container("4", "nextcloud", project = "cloud"),
        )
        val running = job(
            kind = "stack", action = "restart", target = "media", phase = "running", progress = 0.67,
            results = listOf(JobResult("prowlarr", ok = true), JobResult("radarr", ok = false, message = "délai dépassé")),
        )
        val members = OperationText.members(running, containers)
        assertEquals(3, members)
        val p = OperationText.progress(running, members)
        assertEquals("Stack media : redémarrage", p.title)
        assertEquals(3, p.segments.size)
        assertEquals(OperationProgress.MAX, p.segments.sumOf { it.length })
        // Seul l'échec (2e conteneur traité) est marqué, à la fin de son segment.
        assertEquals(1, p.points.size)
        assertEquals(666, p.points[0].position)
        assertFalse(p.points[0].ok)
    }

    @Test
    fun updateAllUsesContainersWithANewImage() {
        val containers = listOf(
            Container("1", "a", updateAvailable = true),
            Container("2", "b", updateAvailable = true),
            Container("3", "c"),
        )
        val all = job(kind = "batch", target = "all")
        assertEquals(2, OperationText.members(all, containers))
        assertEquals("Mise à jour de tous les conteneurs", OperationText.progress(all, 2).title)
        // Nombre inconnu ou trop grand : une seule barre.
        assertEquals(1, OperationText.segments(all, 0).size)
        assertEquals(1, OperationText.segments(all, OperationProgress.MAX_SEGMENTS + 1).size)
        // Un seul conteneur : les étapes d'une mise à jour.
        assertEquals(3, OperationText.segments(all, 1).size)
    }

    @Test
    fun failuresMarkersAreCapped() {
        val results = List(8) { JobResult("c$it", ok = false) }
        val p = OperationText.progress(job(kind = "batch", target = "all", phase = "pulling", results = results), 10)
        assertEquals(OperationProgress.MAX_POINTS, p.points.size)
        // Les plus récents sont gardés.
        assertEquals(800, p.points.last().position)
    }

    @Test
    fun otherOperationsHaveATitleAndASingleBar() {
        val cleanup = job(kind = "cleanup", action = "cleanup", target = "disque", phase = "running", progress = 0.5)
        assertEquals("Nettoyage du disque", OperationText.progress(cleanup).title)
        assertEquals(1, OperationText.progress(cleanup).segments.size)
        assertEquals("Sauvegarde", OperationText.title(job(kind = "backup", target = "sauvegarde")))
        assertEquals("Mise à jour de l'agent", OperationText.title(job(kind = "self", target = "vigie-agent")))
        assertEquals("Restauration", OperationText.title(job(kind = "restore", target = "")))
        assertEquals("Opération en cours", OperationText.title(job(kind = "inconnu")))
    }

    @Test
    fun successOutcome() {
        val o = OperationText.outcome(job(phase = "done", progress = 1.0, done = true, ok = true, message = "nextcloud mis à jour"))
        assertTrue(o.ok)
        assertEquals("nextcloud mis à jour", o.title)
        assertEquals("nextcloud mis à jour", o.text)
        assertTrue(o.details.isEmpty())

        val same = OperationText.outcome(job(phase = "done", done = true, ok = true, message = "nextcloud : déjà à jour"))
        assertEquals("nextcloud est déjà à jour", same.title)
    }

    @Test
    fun failureOutcomeShowsTheError() {
        val o = OperationText.outcome(job(phase = "failed", done = true, ok = false, message = "Échec : nextcloud ne démarre pas"))
        assertFalse(o.ok)
        assertEquals("Échec de la mise à jour de nextcloud", o.title)
        assertEquals("nextcloud ne démarre pas", o.text)
        val noMessage = OperationText.outcome(AgentJob(id = "x", kind = "cleanup", done = true, ok = false, error = "Docker injoignable"))
        assertEquals("Échec du nettoyage", noMessage.title)
        assertEquals("Docker injoignable", noMessage.text)
    }

    @Test
    fun groupOutcomeListsEachContainer() {
        val o = OperationText.outcome(
            job(
                kind = "batch", target = "all", phase = "failed", done = true, ok = false, message = "Échec : 1 mis à jour, 1 échec",
                results = listOf(
                    JobResult("radarr", ok = true, updated = true, message = "radarr mis à jour"),
                    JobResult("sonarr", ok = false, message = "téléchargement refusé"),
                ),
            ),
        )
        assertEquals("Mises à jour : échec", o.title)
        assertEquals("1 mis à jour, 1 échec", o.text)
        assertEquals(listOf("✓ radarr mis à jour", "✗ sonarr : téléchargement refusé"), o.details)
    }

    @Test
    fun agentUpdateWaitsForTheRestart() {
        val launched = job(kind = "self", target = "vigie-agent", phase = "done", done = true, ok = true, message = "L'agent redémarre avec la nouvelle version…")
        assertTrue(OperationText.awaitsRestart(launched))
        assertTrue(OperationText.restarting(launched).indeterminate)
        assertFalse(OperationText.awaitsRestart(launched.copy(message = "L'agent est déjà à jour")))
        assertFalse(OperationText.awaitsRestart(launched.copy(ok = false)))
        assertFalse(OperationText.awaitsRestart(launched.copy(done = false)))

        val updated = OperationText.selfUpdated("j1", "0.5.0", "0.6.0")
        assertTrue(updated.ok)
        assertEquals("0.5.0 → 0.6.0", updated.text)
        val rolledBack = OperationText.selfRolledBack("j1", "healthcheck en échec")
        assertFalse(rolledBack.ok)
        assertEquals("Ancienne version rétablie : healthcheck en échec", rolledBack.text)
    }
}
