package com.karelisio.vigie

import com.karelisio.vigie.data.BACKUP_SECRET_MASK
import com.karelisio.vigie.data.BackupConfig
import com.karelisio.vigie.data.BackupItem
import com.karelisio.vigie.data.BackupRecord
import com.karelisio.vigie.data.BackupState
import com.karelisio.vigie.data.VigieJson
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.jsonObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
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
