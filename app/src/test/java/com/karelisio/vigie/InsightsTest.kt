package com.karelisio.vigie

import com.karelisio.vigie.data.Fmt
import com.karelisio.vigie.data.TrafficMonth
import com.karelisio.vigie.data.TrafficReport
import com.karelisio.vigie.data.VigieJson
import com.karelisio.vigie.data.WeeklyReport
import com.karelisio.vigie.ui.home.lastMonths
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class InsightsTest {
    @Test
    fun volumeUsesDecimalUnits() {
        assertEquals("0 o", Fmt.volume(0))
        assertEquals("1 ko", Fmt.volume(1000))
        assertEquals("1,5 Mo", Fmt.volume(1_500_000))
        assertEquals("850 Go", Fmt.volume(850_000_000_000))
        assertEquals("1,64 To", Fmt.volume(1_640_000_000_000))
        assertEquals("10 To", Fmt.volume(10_000_000_000_000))
    }

    @Test
    fun lastMonthsFillsGapsUpToCurrentMonth() {
        val r = TrafficReport(
            month = "2026-02",
            history = listOf(TrafficMonth("2025-09", tx = 1), TrafficMonth("2025-12", tx = 2), TrafficMonth("2026-02", tx = 3)),
        )
        assertEquals(
            listOf("2025-09" to 1L, "2025-10" to 0L, "2025-11" to 0L, "2025-12" to 2L, "2026-01" to 0L, "2026-02" to 3L),
            lastMonths(r).map { it.month to it.tx },
        )
    }

    @Test
    fun decodesAgentJson() {
        val traffic = VigieJson.decodeFromString<TrafficReport>(
            """{"month":"2026-10","tx":2000000000000,"rx":100000000000,"quota":10000000000000,"pct":20,""" +
                """"projected_tx":4200000000000,"days_left":3.3,"history":[{"month":"2026-10","tx":2000000000000,"rx":100000000000}]}""",
        )
        assertEquals(4_200_000_000_000, traffic.projectedTx)
        assertEquals(3.3, traffic.daysLeft, 0.0)
        assertEquals(1, traffic.history.size)

        val report = VigieJson.decodeFromString<WeeklyReport>(
            """{"from":1,"to":2,"days":7,"services":{"uptime":95,"worst":{"name":"comet","uptime":90},""" +
                """"list":[{"name":"comet","uptime":90},{"name":"aiostreams","uptime":100}]},""" +
                """"incidents":{"total":3,"by_kind":[{"kind":"container","label":"conteneurs","count":2}],"recent":[]},""" +
                """"containers":{"crashes":2,"restarts":1,"list":[{"name":"comet","crashes":2,"restarts":0}]},""" +
                """"updates":["comet"],"disk":{"pct":72.4,"used":724,"total":1000,"change":2.4},""" +
                """"traffic":{"rx":1800000,"tx":3600000,"month_tx":2000000000000,"quota":10000000000000,"month_pct":20}}""",
        )
        assertEquals("comet", report.services.worst?.name)
        assertEquals(2, report.incidents.byKind.single().count)
        assertEquals(2.4, report.disk.change!!, 0.0)
        assertEquals(20.0, report.traffic!!.monthPct, 0.0)
        assertNull(report.oracle)
    }
}
