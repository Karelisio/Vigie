package com.karelisio.vigie

import com.karelisio.vigie.data.DiskPoint
import com.karelisio.vigie.data.StorageDisk
import com.karelisio.vigie.data.StorageFmt
import com.karelisio.vigie.data.StorageReport
import com.karelisio.vigie.data.StorageVolume
import com.karelisio.vigie.data.VigieJson
import com.karelisio.vigie.ui.storage.forecastEnd
import com.karelisio.vigie.ui.storage.sparkValues
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class StorageTest {
    private val gib = 1L shl 30
    private val day = 86_400_000L

    @Test
    fun decodesAgentJson() {
        // Réponse de GET /api/v1/storage (agent 0.6.0), champs nuls compris.
        val r = VigieJson.decodeFromString<StorageReport>(
            """{"disk":{"mount":"/","total":50465865728,"used":32212254720,"free":18253611008,"status":"ok",""" +
                """"trend_per_day":1288490189,"days_left":14.2,"history_hours":168,"cleanups":1,""" +
                """"points":[{"ts":1761600000000,"used":31000000000},{"ts":1761603600000,"used":32212254720}]},""" +
                """"volumes":[{"name":"immich_pgdata","project":"immich","size":3221225472,"containers":["immich_postgres"],""" +
                """"growth_day":1073741824,"growth_week":null,"fast":false,"history":[{"ts":1761600000000,"size":3000000000}]},""" +
                """{"name":"vide","size":0,"containers":[],"growth_day":null,"growth_week":null,"fast":false,"history":[]}],""" +
                """"anonymous":{"count":2,"size":157286400},"images":{"count":12,"size":5368709120},""" +
                """"build_cache":{"count":3,"size":209715200},"fast_growth":5368709120,"volumes_at":1761603000000,""" +
                """"refreshing":true,"computed_at":1761603600000}""",
        )
        assertEquals(StorageDisk.STATUS_OK, r.disk.status)
        assertEquals(1288490189L, r.disk.trendPerDay)
        assertEquals(14.2, r.disk.daysLeft!!, 0.001)
        assertEquals(1, r.disk.cleanups)
        assertEquals(2, r.disk.points.size)
        val v = r.volumes.first()
        assertEquals("immich", v.project)
        assertEquals(listOf("immich_postgres"), v.containers)
        assertEquals(gib, v.growthDay)
        assertNull(v.growthWeek)
        assertEquals("", r.volumes[1].project)
        assertEquals(StorageReport().copy(images = r.images).images.size, 5L * gib)
        assertEquals(209715200L, r.buildCache.size)
        assertEquals(5L * gib, r.fastGrowth)
        assertTrue(r.refreshing)
        assertEquals("", r.error)

        // Sans assez de recul : tendance et jours à null.
        val d = VigieJson.decodeFromString<StorageDisk>(
            """{"mount":"/","total":100,"used":40,"free":60,"status":"insufficient","trend_per_day":null,"days_left":null,"history_hours":12.5,"cleanups":0,"points":[]}""",
        )
        assertNull(d.trendPerDay)
        assertNull(d.daysLeft)
        assertEquals(12.5, d.historyHours, 0.0)
    }

    @Test
    fun formatsSizesAndDays() {
        assertEquals("512 o", StorageFmt.size(512))
        assertEquals("1,2 Go", StorageFmt.size((1.2 * gib).toLong()))
        assertEquals("1 Go", StorageFmt.size(gib))
        assertEquals("14 Go", StorageFmt.size(14 * gib))
        assertEquals("300 Mo", StorageFmt.size(300L shl 20))
        assertEquals("−300 Mo", StorageFmt.size(-(300L shl 20)))
        assertEquals("moins d'un jour", StorageFmt.days(0.4))
        assertEquals("~1 jour", StorageFmt.days(1.3))
        assertEquals("~12 jours", StorageFmt.days(11.6))
        assertEquals("plus d'un an", StorageFmt.days(400.0))
        assertEquals("+1,2 Go/jour", StorageFmt.growth((1.2 * gib).toLong()))
        assertEquals("−300 Mo/jour", StorageFmt.growth(-(300L shl 20)))
        assertEquals("+5 Go/semaine", StorageFmt.growth(5 * gib, "semaine"))
        assertEquals("stable", StorageFmt.growth(1000))
        assertEquals("5 Go", StorageFmt.gb(5.0))
        assertEquals("2,5 Go", StorageFmt.gb(2.5))
    }

    @Test
    fun forecastTexts() {
        val ok = StorageDisk(status = "ok", trendPerDay = (1.5 * gib).toLong(), daysLeft = 9.4, historyHours = 168.0, cleanups = 1)
        assertEquals("Plein dans ~9 jours", StorageFmt.forecast(ok))
        assertEquals("Au rythme actuel : +1,5 Go/jour, sur 7 jours. Un nettoyage a été écarté du calcul.", StorageFmt.forecastDetail(ok))
        assertEquals("Disque plein dans ~9 jours", StorageFmt.homeHint(ok))
        assertEquals("Plein d'ici demain", StorageFmt.forecast(ok.copy(daysLeft = 0.5)))
        assertEquals("Disque plein d'ici demain", StorageFmt.homeHint(ok.copy(daysLeft = 0.5)))
        // L'accueil ne mentionne la prévision que sous 60 jours.
        assertNull(StorageFmt.homeHint(ok.copy(daysLeft = 75.0)))
        assertEquals("~59 jours", StorageFmt.homeHint(ok.copy(daysLeft = 59.0))?.removePrefix("Disque plein dans "))

        val stable = StorageDisk(status = "stable", trendPerDay = 0, historyHours = 60.0)
        assertEquals("Pas de croissance notable", StorageFmt.forecast(stable))
        assertEquals("Au rythme actuel : stable, sur 3 jours.", StorageFmt.forecastDetail(stable))
        assertEquals("En baisse", StorageFmt.forecast(stable.copy(trendPerDay = -gib)))
        assertNull(StorageFmt.homeHint(stable))

        val early = StorageDisk(status = "insufficient", historyHours = 30.2)
        assertEquals("Prévision bientôt disponible", StorageFmt.forecast(early))
        assertEquals("Il faut 48 h de mesures pour estimer la tendance (encore 18 h).", StorageFmt.forecastDetail(early))
        assertNull(StorageFmt.homeHint(early))
    }

    @Test
    fun volumeGrowthTexts() {
        assertEquals("Croissance mesurée après 24 h", StorageFmt.volumeGrowth(StorageVolume()))
        assertEquals("Stable", StorageFmt.volumeGrowth(StorageVolume(growthDay = 0, growthWeek = 100)))
        assertEquals("+1 Go/jour · +3 Go/semaine", StorageFmt.volumeGrowth(StorageVolume(growthDay = gib, growthWeek = 3 * gib)))
        assertEquals("+2 Go/jour", StorageFmt.volumeGrowth(StorageVolume(growthDay = 2 * gib)))
    }

    @Test
    fun projectionStopsAtFullOrThirtyDays() {
        val now = 1_800_000_000_000L
        val base = StorageDisk(
            status = "ok", total = 47 * gib, used = 30 * gib, trendPerDay = gib, daysLeft = 17.0,
            points = listOf(DiskPoint(now - day, 29 * gib), DiskPoint(now, 30 * gib)),
        )
        assertEquals(DiskPoint(now + 17 * day, 47 * gib), forecastEnd(base))
        val slow = base.copy(trendPerDay = 100L shl 20, daysLeft = 170.0)
        assertEquals(DiskPoint(now + 30 * day, 30 * gib + 30 * (100L shl 20)), forecastEnd(slow))
        assertNull(forecastEnd(base.copy(status = "stable", daysLeft = null)))
        assertNull(forecastEnd(base.copy(points = emptyList())))
    }

    @Test
    fun sparklineShowsGrowthAboveMinimum() {
        val (values, max) = sparkValues(listOf(10 * gib, 11 * gib, 12 * gib))
        assertEquals(listOf(0f, gib.toFloat(), 2 * gib.toFloat()), values)
        assertEquals(2 * gib.toFloat(), max)
        // Volume stable : ligne plate en bas, pas d'écart grossi.
        val (flat, floor) = sparkValues(listOf(gib, gib))
        assertEquals(listOf(0f, 0f), flat)
        assertEquals((64L shl 20).toFloat(), floor)
        assertFalse(sparkValues(emptyList()).first.isNotEmpty())
    }
}
