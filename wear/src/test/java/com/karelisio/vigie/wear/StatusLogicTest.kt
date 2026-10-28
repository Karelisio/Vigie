package com.karelisio.vigie.wear

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class StatusLogicTest {
    private val server = WatchServer("Maison", "https://vigie.example.com", "ro_0123456789abcdef", readOnly = true)

    @Test
    fun notConfigured() {
        val v = StatusLogic.view(null, Snapshot())
        assertFalse(v.configured)
        assertEquals(FetchError.NotConfigured, v.error)
        assertEquals("--", StatusLogic.shortText(v))
    }

    @Test
    fun healthyServer() {
        val snap = Snapshot(
            overview = Overview(status = "ok", system = SystemSnapshot(cpu = 12.4, mem = 45.6, disk = 70.0)),
            fetchedAt = 1_000,
        )
        val v = StatusLogic.view(server, snap)
        assertEquals(Level.Ok, v.level)
        assertEquals("Tout va bien", v.label)
        assertEquals(12, v.cpu)
        assertEquals(46, v.ram)
        assertEquals(70, v.disk)
        assertEquals("Maison", v.serverName)
        assertNull(v.errorText)
        assertEquals("OK", StatusLogic.shortText(v))
        assertEquals("Aucun problème", StatusLogic.problemsText(v.problemCount))
    }

    @Test
    fun problemsAreListedAndCounted() {
        val snap = Snapshot(
            overview = Overview(
                status = "critical",
                issues = listOf(
                    Issue(kind = "container", target = "nextcloud", message = "nextcloud arrêté"),
                    Issue(kind = "check", target = "Comet"),
                ),
            ),
            // L'instantané système récent l'emporte sur celui de la vue d'ensemble.
            system = SystemSnapshot(cpu = 99.0),
            fetchedAt = 1_000,
        )
        val v = StatusLogic.view(server, snap)
        assertEquals(Level.Critical, v.level)
        assertEquals(listOf("nextcloud arrêté", "Comet"), v.problems)
        assertEquals(99, v.cpu)
        assertEquals("2 pb", StatusLogic.shortText(v))
        assertEquals("2 problèmes", StatusLogic.problemsText(2))
    }

    @Test
    fun errorsKeepTheLastKnownState() {
        val snap = Snapshot(overview = Overview(status = "warning"), fetchedAt = 1_000, error = FetchError.Unauthorized)
        val v = StatusLogic.view(server, snap)
        assertEquals(Level.Warning, v.level)
        assertTrue(v.errorText!!.startsWith("Accès refusé"))
        assertEquals("Erreur du serveur (HTTP 502)", StatusLogic.errorText(FetchError.Server, "HTTP 502"))
        assertEquals("Serveur injoignable", StatusLogic.errorText(FetchError.Unreachable))
        assertEquals("Montre hors connexion", StatusLogic.errorText(FetchError.NoNetwork))
    }

    @Test
    fun unknownStatus() {
        assertEquals(Level.Unknown, StatusLogic.level("bizarre"))
        assertEquals(Level.Unknown, StatusLogic.view(server, Snapshot()).level)
        assertEquals("jamais", StatusLogic.time(0))
    }
}
