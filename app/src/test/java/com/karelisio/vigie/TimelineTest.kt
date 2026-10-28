package com.karelisio.vigie

import com.karelisio.vigie.data.TimelineEvent
import com.karelisio.vigie.ui.containers.groupTimeline
import org.junit.Assert.assertEquals
import org.junit.Test

class TimelineTest {
    private fun ev(ts: Long, action: String, detail: String = "", source: String = "docker") = TimelineEvent(ts, action, detail, source)

    @Test
    fun crashThenAutoRestart() {
        val g = groupTimeline(listOf(ev(2_000, "start"), ev(1_000, "die", "1")))
        assertEquals(1, g.size)
        assertEquals("Plantage (code 1) puis relance automatique", g[0].title)
        assertEquals("warning", g[0].tone)
    }

    @Test
    fun restartFromVigie() {
        val g = groupTimeline(
            listOf(ev(4_000, "restart"), ev(3_500, "start"), ev(3_000, "stop"), ev(2_900, "die", "143"), ev(2_000, "restart", "ok", "vigie")),
        )
        assertEquals(listOf("Redémarré depuis Vigie"), g.map { it.title })
    }

    @Test
    fun separatesDistantEpisodesNewestFirst() {
        val g = groupTimeline(
            listOf(ev(10_000_000, "health", "unhealthy"), ev(5_000_000, "die", "137"), ev(5_001_000, "stop"), ev(1_000, "start")),
        )
        assertEquals(listOf("Healthcheck en échec", "Arrêté", "Démarré"), g.map { it.title })
    }

    @Test
    fun recreate() {
        val g = groupTimeline(listOf(ev(1, "die", "0"), ev(2, "stop"), ev(3, "destroy"), ev(4, "create"), ev(5, "start")))
        assertEquals("Recréé (mise à jour ou redéploiement)", g.single().title)
    }
}
