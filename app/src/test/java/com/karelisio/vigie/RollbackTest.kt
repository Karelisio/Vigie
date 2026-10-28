package com.karelisio.vigie

import com.karelisio.vigie.data.AgentJob
import com.karelisio.vigie.data.CleanupPreview
import com.karelisio.vigie.data.Container
import com.karelisio.vigie.data.ImageStatus
import com.karelisio.vigie.data.RollbackPoint
import com.karelisio.vigie.data.TimelineEvent
import com.karelisio.vigie.data.VigieJson
import com.karelisio.vigie.data.summary
import com.karelisio.vigie.ui.containers.groupTimeline
import com.karelisio.vigie.ui.containers.rollbackExplanation
import com.karelisio.vigie.ui.settings.rollbackNote
import kotlinx.serialization.builtins.MapSerializer
import kotlinx.serialization.builtins.serializer
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/** Contrat avec l'agent (GET /api/v1/containers/{id}/rollback et champs associés, agent 0.6.0). */
class RollbackTest {
    private val ready = """{"container":"comet","image_ref":"g0ldyy/comet:latest","old_image_id":"sha256:0ld",
        "old_digests":["g0ldyy/comet@sha256:d1d1"],"old_created":1788000000000,"old_version":"1.0",
        "new_image_id":"sha256:n3w","new_digests":["g0ldyy/comet@sha256:d2d2"],"new_version":"2.0",
        "updated_at":1793100000000,"rolled_back_at":0,"ignored":false,"state":"ready",
        "old_label":"1.0","new_label":"2.0","image_present":false,"downloadable":true}"""

    @Test
    fun decodesReadyPoint() {
        val p = VigieJson.decodeFromString(RollbackPoint.serializer(), ready)
        assertTrue(p.ready)
        assertFalse(p.rolledBack)
        assertTrue(p.reachable)
        assertEquals("1.0", p.oldLabel)
        assertEquals("2.0", p.newLabel)
        assertEquals(listOf("g0ldyy/comet@sha256:d1d1"), p.oldDigests)
        assertEquals(1793100000000, p.updatedAt)
        assertEquals("Version précédente : 1.0, mise à jour le 28/10", p.summary { "28/10" })
        // Image supprimée du disque : la confirmation prévient du nouveau téléchargement.
        assertTrue(rollbackExplanation("comet", p).contains("re-téléchargée"))
        assertTrue(rollbackExplanation("comet", p).contains("version 2.0 sera ignorée"))
    }

    @Test
    fun decodesRolledBackAndIgnored() {
        val p = VigieJson.decodeFromString(
            RollbackPoint.serializer(),
            """{"container":"comet","old_digests":null,"state":"rolled_back","rolled_back_at":1793200000000,"ignored":true,
               "ignored_digest":"sha256:d2d2","old_label":"1.0","new_label":"2.0","image_present":true,"downloadable":false}""",
        )
        assertTrue(p.rolledBack)
        assertTrue(p.ignored)
        assertFalse(p.ready)
        assertTrue(p.oldDigests.isEmpty())
        // Image locale disparue et pas de digest : retour impossible.
        assertFalse(p.copy(imagePresent = false).reachable)
        assertEquals("stale", VigieJson.decodeFromString(RollbackPoint.serializer(), """{"state":"stale"}""").state)
    }

    @Test
    fun ignoredUpdateState() {
        val c = VigieJson.decodeFromString(
            Container.serializer(),
            """{"id":"abc","name":"comet","update_available":false,"update_ignored":true}""",
        )
        assertTrue(c.updateIgnored)
        assertFalse(VigieJson.decodeFromString(Container.serializer(), """{"id":"abc","name":"comet"}""").updateIgnored)
        val images = VigieJson.decodeFromString(
            MapSerializer(String.serializer(), ImageStatus.serializer()),
            """{"comet":{"update_available":false,"checked_at":1,"ignored":true},"redis":{"update_available":true,"checked_at":1}}""",
        )
        assertTrue(images.getValue("comet").ignored)
        assertFalse(images.getValue("redis").ignored)
    }

    @Test
    fun rollbackJobConcernsContainer() {
        val job = VigieJson.decodeFromString(
            AgentJob.serializer(),
            """{"id":"x","kind":"rollback","action":"rollback","target":"comet","phase":"done","progress":1,
               "message":"comet est revenu en version 1.0","done":true,"ok":true,"results":[{"name":"comet","ok":true,"updated":true}]}""",
        )
        assertTrue(job.concerns(Container(id = "abc", name = "comet")))
        assertFalse(job.concerns(Container(id = "def", name = "redis")))
    }

    @Test
    fun cleanupMentionsRollbackImages() {
        val p = VigieJson.decodeFromString(
            CleanupPreview.serializer(),
            """{"images":{"count":3,"size":900},"volumes":{"count":0,"size":0},"build_cache":{"count":0,"size":0},
               "volumes_supported":true,"rollback_images":{"count":2,"size":400}}""",
        )
        assertEquals(2, p.rollbackImages.count)
        assertTrue(rollbackNote(p.rollbackImages.count).contains("2 versions précédentes"))
        assertEquals("", rollbackNote(0))
        // Agent plus ancien : champ absent.
        assertEquals(0, VigieJson.decodeFromString(CleanupPreview.serializer(), """{"images":{"count":1,"size":1}}""").rollbackImages.count)
    }

    @Test
    fun timelineShowsUpdateAndRollback() {
        fun ev(ts: Long, action: String, detail: String = "") = TimelineEvent(ts, action, detail)
        val g = groupTimeline(
            listOf(
                ev(9_000_000, "rollback", "2.0 → 1.0"), ev(8_999_000, "start"), ev(8_998_000, "create"),
                ev(5_000_000, "update", "1.0 → 2.0"), ev(4_990_000, "start"), ev(4_980_000, "create"), ev(4_970_000, "stop"),
            ),
        )
        assertEquals(listOf("Revenu à la version précédente", "Mis à jour depuis Vigie"), g.map { it.title })
        assertEquals(listOf("rollback", "update"), g.map { it.kind })
        assertEquals("2.0 → 1.0", g[0].detail)
        assertEquals("1.0 → 2.0", g[1].detail)
    }
}
