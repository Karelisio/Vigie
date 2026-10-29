package com.karelisio.vigie

import com.karelisio.vigie.data.Vuln
import com.karelisio.vigie.data.VulnCounts
import com.karelisio.vigie.data.VulnImage
import com.karelisio.vigie.data.VulnProgress
import com.karelisio.vigie.data.VulnReport
import com.karelisio.vigie.data.VulnTotals
import com.karelisio.vigie.data.VigieJson
import com.karelisio.vigie.data.severityLabel
import com.karelisio.vigie.data.severityRank
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class VulnsTest {
    /** Réponse réelle de GET /api/v1/vulns (agent 0.6.0, essai de bout en bout), raccourcie. */
    private val agentJson = """
        {"enabled":true,"scanning":false,"last_scan":1793211914246,"duration":565,"next_scan":1793250000000,
         "totals":{"critical":3,"high":2,"medium":0,"low":0,"unknown":0,"fixable":3,"images":3,"vulnerable":2},
         "images":[
          {"id":"sha256:1111","ref":"ghcr.io/g0ldyy/comet:latest",
           "containers":[{"id":"aaaa","name":"comet","state":"running"},{"id":"bbbb","name":"comet-worker","state":"exited"}],
           "os":"Debian GNU/Linux 12 (bookworm)","ecosystem":"Debian:12","packages":3,
           "counts":{"critical":2,"high":1,"medium":0,"low":0,"unknown":0,"fixable":1},"update_available":true,"read_at":1793211913865,
           "vulns":[
            {"id":"DEBIAN-CVE-2023-45853","aliases":["CVE-2023-45853"],"summary":"MiniZip integer overflow.","severity":"critical","score":9.8,
             "package":"zlib","version":"1:1.2.13.dfsg-1","fixed":"","ecosystem":"Debian:12","url":"https://osv.dev/vulnerability/DEBIAN-CVE-2023-45853"},
            {"id":"DEBIAN-CVE-2023-4911","aliases":["CVE-2023-4911"],"summary":"Looney Tunables.","severity":"high","score":7.8,
             "package":"glibc","version":"2.36-9+deb12u4","fixed":"","ecosystem":"Debian:12","url":"https://osv.dev/vulnerability/DEBIAN-CVE-2023-4911"},
            {"id":"DEBIAN-CVE-2024-5535","aliases":["CVE-2024-5535"],"summary":"SSL_select_next_proto buffer overread.","severity":"critical","score":9.1,
             "package":"openssl","version":"3.0.11-1~deb12u2","fixed":"3.0.15-1~deb12u1","ecosystem":"Debian:12","url":"https://osv.dev/vulnerability/DEBIAN-CVE-2024-5535"}
           ],"more":0},
          {"id":"sha256:3333","ref":"ghcr.io/karelisio/vigie-agent:latest","containers":[{"id":"cccc","name":"vigie","state":"running"}],
           "os":"Debian GNU/Linux 12 (bookworm)","ecosystem":"Debian:12","packages":1,
           "counts":{"critical":0,"high":0,"medium":0,"low":0,"unknown":0,"fixable":0},"update_available":false,"read_at":1793211914053,"vulns":[],"more":0,
           "champ_futur":42}
         ]}
    """.trimIndent()

    @Test
    fun decodesAgentReport() {
        val r = VigieJson.decodeFromString<VulnReport>(agentJson)
        assertTrue(r.enabled)
        assertFalse(r.scanning)
        assertNull(r.progress)
        assertEquals(1793211914246L, r.lastScan)
        assertEquals(1793250000000L, r.nextScan)
        assertEquals(VulnTotals(critical = 3, high = 2, fixable = 3, images = 3, vulnerable = 2), r.totals)
        val img = r.images.first()
        assertEquals("comet +1", img.title)
        assertEquals("Debian GNU/Linux 12 (bookworm) · 3 paquets", img.details)
        assertTrue(img.updateAvailable)
        assertEquals(VulnCounts(critical = 2, high = 1, fixable = 1), img.counts)
        val v = img.vulns.last()
        assertEquals("openssl", v.pkg)
        assertEquals("3.0.15-1~deb12u1", v.fixed)
        assertEquals("CVE-2024-5535", v.title)
        assertEquals(9.1, v.score, 0.0)
        assertEquals("https://osv.dev/vulnerability/DEBIAN-CVE-2024-5535", v.url)
        // Tri : critiques corrigeables d'abord, puis par score.
        assertEquals(listOf("openssl", "zlib", "glibc"), img.sorted.map { it.pkg })
        assertEquals("vigie", r.images[1].title)
        assertEquals("1 paquet", r.images[1].details.substringAfter(" · "))
    }

    @Test
    fun decodesScanInProgressAndEmptyState() {
        val r = VigieJson.decodeFromString<VulnReport>(
            """{"enabled":true,"scanning":true,"progress":{"phase":"details","done":40,"total":160},"last_scan":0,"duration":0,"next_scan":0,
              |"error":"OSV ne répond pas (délai dépassé)","error_at":1793211936064,"totals":{},"images":[]}""".trimMargin(),
        )
        assertTrue(r.scanning)
        assertEquals(0.25f, r.progress!!.fraction!!, 0f)
        assertEquals("Détail des failles : 40 sur 160", r.progress!!.label)
        assertEquals("OSV ne répond pas (délai dépassé)", r.error)
        assertEquals("Première analyse en cours…", r.summary())
        assertNull(VulnProgress(phase = "query").fraction)
        assertEquals("Recherche dans la base OSV…", VulnProgress(phase = "query").label)
    }

    @Test
    fun summaryForSecurityScreen() {
        assertEquals("Pas encore analysé", VulnReport().summary())
        assertEquals("Analyse désactivée", VulnReport(enabled = false).summary())
        assertEquals("Aucune faille connue dans 4 images", VulnReport(lastScan = 1, totals = VulnTotals(images = 4)).summary())
        assertEquals(
            "3 critiques · 2 élevées · 3 corrigeables",
            VulnReport(lastScan = 1, totals = VulnTotals(critical = 3, high = 2, fixable = 3, images = 3)).summary(),
        )
        assertEquals(
            "1 critique · aucune corrigée",
            VulnReport(lastScan = 1, totals = VulnTotals(critical = 1, images = 1)).summary(),
        )
        assertEquals(
            "5 failles · 1 corrigeable",
            VulnReport(lastScan = 1, totals = VulnTotals(medium = 4, low = 1, fixable = 1, images = 2)).summary(),
        )
    }

    @Test
    fun severityHelpers() {
        assertEquals(
            listOf("critical", "high", "medium", "low", "unknown"),
            listOf("low", "unknown", "critical", "medium", "high").sortedBy { severityRank(it) },
        )
        assertEquals(0, severityRank("critical"))
        assertEquals(4, severityRank("inconnue"))
        assertEquals("Élevée", severityLabel("high"))
        assertEquals("Inconnue", severityLabel("bizarre"))
        assertEquals("high", VulnCounts(high = 2, low = 1).worst)
        assertEquals("", VulnCounts().worst)
        val unsupported = VulnImage(os = "Fedora Linux 40", packages = 12)
        assertEquals("Fedora Linux 40 (paquets système non analysés) · 12 paquets", unsupported.details)
        assertEquals("Système non reconnu · 0 paquet", VulnImage().details)
        assertTrue(Vuln(fixed = "1.2").fixable)
        assertEquals("GHSA-x", Vuln(id = "GHSA-x").title)
    }

    @Test
    fun negligibleVulnsAreSetApart() {
        val json = """{"id":"sha256:1","ref":"comet","counts":{"critical":0,"high":1,"medium":0,"low":0,"unknown":0,"fixable":1,"negligible":2},
            "vulns":[{"id":"DEBIAN-CVE-2019-1010022","severity":"critical","score":9.8,"package":"glibc","version":"2.41","fixed":"",
            "negligible":true,"note":"Sans importance selon Debian"},{"id":"X","severity":"high","package":"openssl","version":"3","fixed":"3.1"}]}"""
        val img = VigieJson.decodeFromString(VulnImage.serializer(), json)
        assertEquals(1, img.counts.total)
        assertEquals(2, img.counts.negligible)
        assertEquals("X", img.sorted.first().id)
        val minor = img.sorted.last()
        assertTrue(minor.negligible)
        assertEquals("Sans importance selon Debian", minor.note)
        assertFalse(minor.copy(fixed = "2.42").fixable)
    }
}
