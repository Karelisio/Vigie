package com.karelisio.vigie

import com.karelisio.vigie.ui.logs.Level
import com.karelisio.vigie.ui.logs.LogSource
import com.karelisio.vigie.ui.logs.StreamStatus
import com.karelisio.vigie.ui.logs.UiLine
import com.karelisio.vigie.ui.logs.excerptHeader
import com.karelisio.vigie.ui.logs.excerptText
import com.karelisio.vigie.ui.logs.mergeByTime
import com.karelisio.vigie.ui.logs.stackStatus
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class StackLogsTest {
    private var id = 0L
    private fun line(ts: Long, source: String, text: String = "$source@$ts") = UiLine(id++, ts, "stdout", text, Level.None, source)

    @Test
    fun appendsWhenInOrder() {
        val cur = listOf(line(1, "api"), line(2, "db"))
        val merged = mergeByTime(cur, listOf(line(3, "api"), line(5, "db")), 100)
        assertEquals(listOf(1L, 2, 3, 5), merged.map { it.ts })
    }

    @Test
    fun interleavesLateHistory() {
        // L'historique d'un conteneur arrive après celui d'un autre : il s'intercale par horodatage.
        val cur = listOf(line(10, "api"), line(20, "api"), line(30, "api"))
        val merged = mergeByTime(cur, listOf(line(25, "db"), line(5, "db"), line(20, "db", "db-tie")), 100)
        assertEquals(listOf(5L, 10, 20, 20, 25, 30), merged.map { it.ts })
        // À horodatage égal, les lignes déjà affichées restent devant.
        assertEquals(listOf("api", "db"), merged.filter { it.ts == 20L }.map { it.source })
    }

    @Test
    fun keepsMostRecent() {
        val cur = (1L..5).map { line(it, "api") }
        val merged = mergeByTime(cur, listOf(line(0, "db"), line(6, "db")), 4)
        assertEquals(listOf(3L, 4, 5, 6), merged.map { it.ts })
    }

    @Test
    fun overallStatus() {
        fun s(vararg st: StreamStatus) = st.mapIndexed { i, x -> LogSource("c$i", x) }
        assertEquals(StreamStatus.Connecting, stackStatus(emptyList(), paused = false))
        assertEquals(StreamStatus.Live, stackStatus(s(StreamStatus.Ended, StreamStatus.Live, StreamStatus.Failed), paused = false))
        assertEquals(StreamStatus.Paused, stackStatus(s(StreamStatus.Live), paused = true))
        assertEquals(StreamStatus.Connecting, stackStatus(s(StreamStatus.Connecting, StreamStatus.Failed), paused = false))
        assertEquals(StreamStatus.Ended, stackStatus(s(StreamStatus.Ended, StreamStatus.Ended), paused = false))
        assertEquals(StreamStatus.Failed, stackStatus(s(StreamStatus.Ended, StreamStatus.Failed), paused = false))
    }

    @Test
    fun excerpt() {
        val header = excerptHeader("immich", listOf("immich_server", "immich_redis"), 0)
        assertTrue(header.startsWith("immich · immich_server, immich_redis · "))
        assertTrue(excerptHeader("", listOf("vigie-agent"), 0).startsWith("vigie-agent · "))

        val text = excerptText("en-tête", listOf(line(1_000, "api", "boom"), line(2_000, "", "seul")))
        val rows = text.lines()
        assertEquals("en-tête", rows[0])
        assertEquals("", rows[1])
        assertTrue(rows[2].endsWith("  [api] boom"))
        assertTrue(rows[3].endsWith("  seul"))
        assertEquals(4, rows.size)
    }
}
