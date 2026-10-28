package com.karelisio.vigie

import com.karelisio.vigie.data.AgentJob
import com.karelisio.vigie.data.BACKUP_SECRET_MASK
import com.karelisio.vigie.data.BackupConfig
import com.karelisio.vigie.data.BackupItem
import com.karelisio.vigie.data.BackupObject
import com.karelisio.vigie.data.BackupRecord
import com.karelisio.vigie.data.BackupRestoreRequest
import com.karelisio.vigie.data.BackupState
import com.karelisio.vigie.data.VigieJson
import com.karelisio.vigie.data.groupCopies
import com.karelisio.vigie.data.restorable
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** Contrat avec l'agent (champs JSON de /api/v1/backups). */
class BackupsTest {
    @Test
    fun decodesConfigWithMaskedKeys() {
        val s = VigieJson.decodeFromString(
            BackupState.serializer(),
            """{"config":{"schedule":true,"time":"03:30","retention":7,"endpoint":"https://ns.compat.objectstorage.eu-paris-1.oraclecloud.com",
               "region":"eu-paris-1","bucket":"vigie","prefix":"vigie","access_key":"$BACKUP_SECRET_MASK","secret_key":"$BACKUP_SECRET_MASK",
               "items":[{"id":"volume:sonarr_config","stop":true},{"id":"vigie","stop":false}]},"next_run":1793151000000}""",
        )
        assertTrue(s.config.storageReady)
        assertEquals(1793151000000, s.nextRun)
        assertTrue(s.config.items.first().stop)
        assertFalse(BackupConfig(endpoint = "https://x", region = "auto", bucket = "b").storageReady)
    }

    @Test
    fun encodesOnlyFieldsKnownByTheAgent() {
        // L'agent refuse les champs inconnus : l'appli n'envoie que ceux de la configuration.
        val keys = VigieJson.encodeToJsonElement(BackupConfig.serializer(), BackupConfig()).jsonObject.keys
        assertEquals(
            setOf("schedule", "time", "retention", "endpoint", "region", "bucket", "prefix", "access_key", "secret_key", "items"),
            keys,
        )
    }

    @Test
    fun decodesObjectsAndGroupsSafetyCopies() {
        // Agent 0.6 : « safety » marque la copie de sécurité faite avant la dernière restauration.
        val objects = VigieJson.decodeFromString(
            ListSerializer(BackupObject.serializer()),
            """[{"key":"vigie/sonarr_config/avant-restauration_2026-10-28_1405.tar.gz","item":"sonarr_config","size":10,"ts":3,"safety":true},
                {"key":"vigie/sonarr_config/2026-10-28_0330.tar.gz","item":"sonarr_config","size":12,"ts":2,"safety":false},
                {"key":"vigie/sonarr_config/2026-10-27_0330.tar.gz","item":"sonarr_config","size":11,"ts":1,"safety":false},
                {"key":"vigie/vieux/2026-10-01_0330.tar.gz","item":"vieux","size":5,"ts":1,"safety":false},
                {"key":"vigie/vigie/2026-10-28_0330.tar.gz","item":"vigie","size":7,"ts":2}]""",
        )
        assertFalse(objects.last().safety) // agent 0.5 : champ absent
        val items = listOf(
            BackupItem(id = "volume:sonarr_config", kind = "volume", name = "sonarr_config", key = "sonarr_config"),
            BackupItem(id = "vigie", kind = "vigie", name = "Données de Vigie", key = "vigie"),
        )
        val groups = groupCopies(objects, items)
        assertEquals(listOf("sonarr_config", "vieux", "vigie"), groups.map { it.folder })
        val sonarr = groups[0]
        assertEquals("volume:sonarr_config", sonarr.item?.id)
        assertTrue(sonarr.item!!.restorable)
        assertEquals("vigie/sonarr_config/avant-restauration_2026-10-28_1405.tar.gz", sonarr.safety?.key)
        assertEquals(listOf("2026-10-28_0330.tar.gz", "2026-10-27_0330.tar.gz"), sonarr.copies.map { it.key.substringAfterLast('/') })
        assertNull(groups[1].item) // élément disparu : pas de restauration
        assertFalse(groups[2].item!!.restorable) // données de Vigie : à la main
        assertNull(groups[2].safety)
    }

    @Test
    fun restoreRequestAndJob() {
        val body = VigieJson.encodeToJsonElement(
            BackupRestoreRequest.serializer(),
            BackupRestoreRequest("volume:sonarr_config", "vigie/sonarr_config/2026-10-28_0330.tar.gz"),
        ).jsonObject
        assertEquals(setOf("item", "key"), body.keys)
        assertEquals("volume:sonarr_config", body["item"]!!.jsonPrimitive.content)
        // Réponse 202 : l'opération, suivie ensuite en direct (« job »).
        val job = VigieJson.decodeFromString(
            AgentJob.serializer(),
            """{"id":"m1","kind":"restore","action":"restore","target":"sonarr_config","phase":"running","progress":0.35,
                "message":"sonarr_config : copie de sécurité…","done":false,"ok":false,"results":[],"started_at":1}""",
        )
        assertEquals("restore", job.kind)
        assertEquals("sonarr_config", job.target)
        assertFalse(job.done)
    }

    @Test
    fun decodesItemsAndHistory() {
        val items = VigieJson.decodeFromString(
            ListSerializer(BackupItem.serializer()),
            """[{"id":"bind:/srv/radarr","kind":"bind","name":"/srv/radarr","key":"srv_radarr","path":"/data","containers":["radarr"],"size":0}]""",
        )
        assertEquals("srv_radarr", items.single().key)
        val h = VigieJson.decodeFromString(
            ListSerializer(BackupRecord.serializer()),
            """[{"ts":1,"duration":4200,"size":2048,"ok":false,"error":"1 élément sur 2 en échec","trigger":"schedule",
                "items":[{"id":"vigie","name":"Données de Vigie","key":"vigie/vigie/2026-10-28_0330.tar.gz","size":2048,"raw":8192,"ok":true}]}]""",
        )
        assertEquals("schedule", h.single().trigger)
        assertTrue(h.single().items.single().ok)
    }
}
