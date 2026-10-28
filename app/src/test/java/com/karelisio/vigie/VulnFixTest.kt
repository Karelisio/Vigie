package com.karelisio.vigie

import com.karelisio.vigie.data.AgentJob
import com.karelisio.vigie.data.AgentSettings
import com.karelisio.vigie.data.Container
import com.karelisio.vigie.data.FixPlans
import com.karelisio.vigie.data.JobResult
import com.karelisio.vigie.data.RollbackPoint
import com.karelisio.vigie.data.TimelineEvent
import com.karelisio.vigie.data.VigieJson
import com.karelisio.vigie.data.VulnReport
import com.karelisio.vigie.data.settingsKeys
import com.karelisio.vigie.data.summary
import com.karelisio.vigie.ops.OperationText
import com.karelisio.vigie.ui.containers.groupTimeline
import com.karelisio.vigie.ui.containers.rollbackExplanation
import com.karelisio.vigie.ui.security.fixAllExplanation
import com.karelisio.vigie.ui.security.fixExplanation
import com.karelisio.vigie.ui.security.nightlyClock
import kotlinx.serialization.json.jsonObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** Contrat de la correction des failles (GET /api/v1/vulns/fixes et champs associés, agent 0.7.0). */
class VulnFixTest {
    /** Réponse réelle de l'agent (essai de bout en bout contre Docker), raccourcie. */
    private val plansJson = """
        {"enabled":true,"busy":false,"scanning":false,"last_scan":1793225590435,
         "totals":{"patch":2,"update":0,"none":1,"fixable":2,"fixes":{"critical":1,"high":2,"medium":0,"low":0,"unknown":0,"fixable":3}},
         "nightly":{"enabled":true,"level":"high","all":true,"next":1793311800000,"last_run":1793225517048,"message":"1 conteneur corrigé"},
         "images":[
          {"image_id":"sha256:ffc7","ref":"vigietest/distroless:1","containers":[{"id":"db9e","name":"web","state":"created"}],
           "os":"Debian GNU/Linux 12 (bookworm)","counts":{"critical":1,"high":1,"medium":0,"low":0,"unknown":0,"fixable":2},
           "route":"none","title":"Rien à faire pour l'instant",
           "explain":"Image sans shell ni gestionnaire de paquets (type distroless) : Vigie ne peut pas mettre ses paquets à niveau.",
           "update":{"available":false},
           "patch":{"possible":false,"manager":"apt","packages":[],"fixes":{"critical":0,"high":0,"medium":0,"low":0,"unknown":0,"fixable":0},
                    "reason":"Image sans shell ni gestionnaire de paquets (type distroless) : Vigie ne peut pas mettre ses paquets à niveau."},
           "app":{"ecosystem":"npm","counts":{"critical":0,"high":1,"medium":0,"low":0,"unknown":0,"fixable":1},"fixable":1},
           "unfixed":0,"source":"https://github.com/example/web","declined":false,"self":false},
          {"image_id":"sha256:9817","ref":"127.0.0.1:5443/demo/app:latest","containers":[{"id":"391e","name":"demo","state":"running"}],
           "os":"Debian GNU/Linux 12 (bookworm)","counts":{"critical":0,"high":1,"medium":1,"low":0,"unknown":0,"fixable":1},
           "route":"patch","title":"Corriger les paquets système",
           "explain":"Pas de nouvelle version de l'image : Vigie met à niveau 1 paquet système (vigiedemo) dans une image dérivée.",
           "update":{"available":false},
           "patch":{"possible":true,"manager":"apt","packages":[{"name":"vigiedemo","version":"1.1-1","fixed":"1.2-1",
              "binaries":["vigie-demo-lib","vigie-demo-tools"],"counts":{"critical":0,"high":1,"medium":0,"low":0,"unknown":0,"fixable":1}}],
              "fixes":{"critical":0,"high":1,"medium":0,"low":0,"unknown":0,"fixable":1}},
           "app":{"counts":{"critical":0,"high":0,"medium":0,"low":0,"unknown":0,"fixable":0},"fixable":0},"unfixed":1,
           "patched":{"at":1793225468000,"base_image":"sha256:6e70","base_digests":["127.0.0.1:5443/demo/app@sha256:01ea"],
              "ref":"127.0.0.1:5443/demo/app:latest","packages":[{"name":"vigie-demo-tools","from":"1.0-1","to":"1.1-1"}]},
           "last":{"at":1793225517031,"route":"patch","from":"sha256:6e70","image":"sha256:9817","packages":1,
              "fixed":{"critical":1,"high":0,"medium":0,"low":0,"unknown":0,"fixable":1},"added":{"critical":0,"high":0,"medium":0,"low":0,"unknown":0,"fixable":0},
              "remaining":{"critical":0,"high":1,"medium":1,"low":0,"unknown":0,"fixable":1},
              "message":"1 paquet mis à niveau — 1 faille corrigée (dont 1 critique), 2 restantes (1 sans correctif, 1 dont le correctif n'est pas encore dans les dépôts)"},
           "source":"https://github.com/example/demo","declined":false,"self":false,"champ_futur":true},
          {"image_id":"sha256:d6d2","ref":"sha256:d6d2","containers":[{"id":"718c","name":"demo-worker","state":"created"}],
           "os":"Debian GNU/Linux 12 (bookworm)","counts":{"critical":0,"high":1,"medium":1,"low":0,"unknown":0,"fixable":1},
           "route":"update","title":"Installer la nouvelle version","explain":"La nouvelle version corrige 1 faille.",
           "update":{"available":true,"candidate":{"current":"sha256:d6d2","image_id":"sha256:6e70","version":"1.5.0","analyzed_at":1793225247042,
              "fixed":{"critical":0,"high":1,"medium":0,"low":0,"unknown":0,"fixable":1},"added":{"critical":1,"high":0,"medium":0,"low":0,"unknown":0,"fixable":1},
              "remaining":{"critical":1,"high":2,"medium":1,"low":0,"unknown":0,"fixable":3}}},
           "patch":{"possible":true,"manager":"apt","packages":[],"fixes":{"critical":0,"high":0,"medium":0,"low":0,"unknown":0,"fixable":0}},
           "app":{"counts":{"critical":0,"high":0,"medium":0,"low":0,"unknown":0,"fixable":0},"fixable":0},"unfixed":1,
           "source":"https://github.com/example/demo","declined":true,"self":false}
         ]}
    """.trimIndent()

    @Test
    fun decodesPlans() {
        val p = VigieJson.decodeFromString<FixPlans>(plansJson)
        assertTrue(p.enabled)
        assertEquals(2, p.totals.fixable)
        assertEquals(3, p.totals.fixes.fixable)
        assertTrue(p.nightly.enabled)
        assertEquals("1 conteneur corrigé", p.nightly.message)

        val web = p.images[0]
        assertFalse(web.canFix)
        assertEquals("web", web.target)
        assertEquals("npm", web.app.ecosystem)
        assertEquals(1, web.app.fixable)
        assertEquals("https://github.com/example/web/issues", web.reportUrl)
        assertTrue(web.patch.reason.contains("distroless"))

        val demo = p.plan("sha256:9817")!!
        assertTrue(demo.canFix)
        assertEquals("patch", demo.route)
        assertEquals(listOf("vigie-demo-lib", "vigie-demo-tools"), demo.patch.packages[0].binaries)
        assertEquals("1.2-1", demo.patch.packages[0].fixed)
        val patched = demo.patched!!
        assertEquals("Corrigée par Vigie le 28/10 : 1 paquet mis à niveau", patched.summary { "28/10" })
        assertEquals("sha256:6e70", patched.baseImage)
        assertEquals(1, demo.last!!.fixed.critical)
        assertTrue(demo.last!!.message.startsWith("1 paquet mis à niveau"))

        val worker = p.images[2]
        assertEquals("update", worker.route)
        assertFalse(worker.needsAnalysis)
        assertEquals("1.5.0", worker.update.candidate!!.version)
        assertEquals(1, worker.update.candidate!!.added.critical)
        assertTrue(worker.declined)
        assertEquals("", worker.copy(source = "").reportUrl)
        assertEquals("https://gitlab.com/x/y", worker.copy(source = "https://gitlab.com/x/y").reportUrl)
        assertTrue(worker.copy(update = worker.update.copy(candidate = null)).needsAnalysis)
        assertNull(p.plan("sha256:inconnu"))
    }

    @Test
    fun decodesVulnReportFields() {
        val r = VigieJson.decodeFromString<VulnReport>(
            """{"last_scan":1,"images":[{"id":"sha256:a","fixes":[{"ecosystem":"Debian:12","package":"openssl","version":"3.0.11-1",
               "fixed":"3.0.15-1","counts":{"critical":1,"fixable":1}}],"patched":{"at":5,"base_image":"sha256:b","packages":[]},
               "source":"https://github.com/x/y"}]}""",
        )
        val img = r.images[0]
        assertEquals("openssl", img.fixes[0].pkg)
        assertEquals(1, img.fixes[0].counts.critical)
        assertNotNull(img.patched)
        assertEquals("https://github.com/x/y", img.source)
        // Agent 0.6 : champs absents.
        val old = VigieJson.decodeFromString<VulnReport>("""{"images":[{"id":"sha256:a"}]}""").images[0]
        assertTrue(old.fixes.isEmpty())
        assertNull(old.patched)
    }

    @Test
    fun explanations() {
        val p = VigieJson.decodeFromString<FixPlans>(plansJson)
        val demo = p.plan("sha256:9817")!!
        val patch = fixExplanation(demo.copy(containers = demo.containers + demo.containers[0].copy(name = "demo-worker")), "patch")
        assertTrue(patch.startsWith("Vigie met à niveau 1 paquet système (vigiedemo) dans une image dérivée"))
        assertTrue(patch.contains("demo et demo-worker seront recréés à l'identique"))
        assertTrue(patch.contains("revenir à l'image d'origine"))
        val update = fixExplanation(p.images[2].copy(update = p.images[2].update.copy(candidate = null)), "update")
        assertTrue(update.contains("l'analyse avant de l'installer"))
        assertTrue(update.endsWith("Vigie corrige les paquets système à la place."))
        assertEquals(
            "Vigie corrige l'une après l'autre 2 images (2 en mettant à niveau leurs paquets système). " +
                "Chaque conteneur concerné est recréé à l'identique, avec quelques secondes de coupure, et peut revenir en arrière ensuite.",
            fixAllExplanation(p),
        )
        assertEquals("05:00", nightlyClock("04:00"))
        assertEquals("00:30", nightlyClock("23:30"))
        assertEquals("05:00", nightlyClock("n'importe quoi"))
    }

    @Test
    fun settingsSentByAgentVersion() {
        val keys = VigieJson.encodeToJsonElement(AgentSettings.serializer(), AgentSettings()).jsonObject.keys
        assertTrue("vuln_fix_auto" in settingsKeys(keys, "0.7.0", true, true))
        assertTrue("vuln_fix_level" in settingsKeys(keys, "dev", true, true))
        val v06 = settingsKeys(keys, "0.6.0", true, true)
        assertFalse("vuln_fix_auto" in v06)
        assertFalse("vuln_fix_list" in v06)
        assertTrue("vuln_scan" in v06)
        assertFalse("vuln_scan" in settingsKeys(keys, "0.5.2", true, true))
        assertFalse("vuln_fix_all" in settingsKeys(keys, "0.4.0", true, true))
        val defaults = AgentSettings()
        assertFalse(defaults.vulnFixAuto)
        assertEquals("high", defaults.vulnFixLevel)
    }

    @Test
    fun vulnfixJobsAndRollback() {
        val job = AgentJob(id = "1", kind = "vulnfix", action = "vulnfix", target = "demo", done = true, ok = true,
            message = "2 paquets mis à niveau", results = listOf(JobResult("demo", true, true, "demo : 2 paquets"), JobResult("demo-worker", true, true, "")))
        assertTrue(job.concerns(Container(id = "a", name = "demo-worker")))
        assertFalse(job.concerns(Container(id = "b", name = "redis")))
        assertEquals("Correction des failles de demo", OperationText.title(job.copy(done = false)))
        assertEquals("demo : failles corrigées", OperationText.outcome(job).title)
        assertEquals(2, OperationText.outcome(job).details.size)
        assertEquals("demo : rien n'a changé", OperationText.outcome(job.copy(results = listOf(JobResult("demo", true, false, "")))).title)
        assertEquals("Correctifs de la nuit", OperationText.title(job.copy(target = "nightly", done = false)))
        assertEquals("Correctifs de la nuit : 2 corrigés", OperationText.outcome(job.copy(target = "nightly")).title)
        assertEquals("Analyse de la nouvelle version de demo", OperationText.title(job.copy(action = "analyze", done = false)))
        assertEquals("Échec de la correction de demo", OperationText.outcome(job.copy(ok = false)).title)

        val point = VigieJson.decodeFromString(
            RollbackPoint.serializer(),
            """{"kind":"vulnfix","container":"demo","old_image_id":"sha256:a03e","old_version":"1.4.2","new_image_id":"sha256:d6d2",
               "updated_at":1793224979347,"state":"ready","old_label":"1.4.2","new_label":"1.4.2 corrigée","image_present":true,"downloadable":true}""",
        )
        assertTrue(point.vulnfix)
        assertEquals("Image d'origine : 1.4.2, non corrigée (correction du 28/10)", point.summary { "28/10" })
        assertTrue(rollbackExplanation("demo", point).startsWith("demo sera recréé avec son image d'origine (1.4.2)"))
        assertFalse(VigieJson.decodeFromString(RollbackPoint.serializer(), """{"state":"ready"}""").vulnfix)

        val groups = groupTimeline(
            listOf(
                TimelineEvent(ts = 3_000_000, action = "rollback", detail = "1.4.2 corrigée → 1.4.2"),
                TimelineEvent(ts = 1_000_000, action = "vulnfix", detail = "openssl 3.0.11-1 → 3.0.15-1"),
                TimelineEvent(ts = 999_000, action = "create"),
            ),
        )
        assertEquals("Revenu à l'image d'origine", groups[0].title)
        assertEquals("Failles corrigées par Vigie", groups[1].title)
        assertEquals("vulnfix", groups[1].kind)
        assertEquals("openssl 3.0.11-1 → 3.0.15-1", groups[1].detail)
    }
}
