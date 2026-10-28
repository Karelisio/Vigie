package com.karelisio.vigie

import com.karelisio.vigie.watch.WatchNotice
import com.karelisio.vigie.watch.WatchResult
import com.karelisio.vigie.watch.WatchState
import com.karelisio.vigie.watch.backMessage
import com.karelisio.vigie.watch.failureCause
import com.karelisio.vigie.watch.healthResult
import com.karelisio.vigie.watch.next
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.IOException
import java.io.InterruptedIOException
import java.net.ConnectException
import java.net.SocketTimeoutException
import java.net.UnknownHostException
import java.security.cert.CertificateExpiredException
import javax.net.ssl.SSLHandshakeException

class ServerWatchTest {
    private val min = 60_000L
    private val down = WatchResult.Down("Délai dépassé")

    /** Enchaîne des passages espacés de 15 min ; renvoie l'état final et les notifications. */
    private fun run(vararg results: WatchResult, from: WatchState = WatchState()): Pair<WatchState, List<WatchNotice>> {
        var state = from
        val notices = mutableListOf<WatchNotice>()
        results.forEachIndexed { i, r ->
            val (s, n) = state.next(r, i * 15 * min)
            state = s
            n?.let { notices += it }
        }
        return state to notices
    }

    @Test
    fun alertsOnSecondConsecutiveFailure() {
        val (one, none) = run(WatchResult.Up, down)
        assertTrue(none.isEmpty())
        assertEquals(1, one.failures)
        assertFalse(one.alerted)

        val (two, notices) = run(WatchResult.Up, down, down)
        assertEquals(listOf<WatchNotice>(WatchNotice.Down("Délai dépassé", 15 * min)), notices)
        assertTrue(two.alerted)
        assertEquals(15 * min, two.since)
    }

    @Test
    fun noRepeatWhileStillDown() {
        val (state, notices) = run(down, down, down, WatchResult.Down("Connexion refusée"), down)
        assertEquals(1, notices.size)
        assertEquals(5, state.failures)
        assertEquals(0L, state.since)
    }

    @Test
    fun phoneOfflineConcludesNothing() {
        val (state, notices) = run(down, WatchResult.Unknown, WatchResult.Unknown)
        assertTrue(notices.isEmpty())
        assertEquals(1, state.failures)
        // La série reprend où elle en était.
        val (after, alert) = state.next(down, 60 * min)
        assertTrue(after.alerted)
        assertEquals(WatchNotice.Down("Délai dépassé", 0), alert)
    }

    @Test
    fun backAfterAlertThenQuiet() {
        val (state, notices) = run(WatchResult.Up, down, down, down, WatchResult.Up, WatchResult.Up)
        assertEquals(2, notices.size)
        assertEquals(WatchNotice.Back(since = 15 * min, at = 60 * min), notices[1])
        assertEquals(WatchState(checkedAt = 75 * min), state)
    }

    @Test
    fun singleFailureRecoversSilently() {
        val (state, notices) = run(down, WatchResult.Up)
        assertTrue(notices.isEmpty())
        assertEquals(0, state.failures)
        assertFalse(state.alerted)
    }

    @Test
    fun unknownKeepsState() {
        val s = WatchState(failures = 3, since = 5, cause = "x", alerted = true, checkedAt = 9)
        val (same, notice) = s.next(WatchResult.Unknown, 100)
        assertEquals(s, same)
        assertNull(notice)
    }

    @Test
    fun healthResponses() {
        assertEquals(WatchResult.Up, healthResult(200, """{"ok":true,"version":"0.5.0"}"""))
        assertTrue((healthResult(200, "<html>Pangolin</html>") as WatchResult.Down).cause.startsWith("Réponse inattendue"))
        assertTrue((healthResult(302, "") as WatchResult.Down).cause.contains("/healthz"))
        assertTrue((healthResult(403, "") as WatchResult.Down).cause.contains("Accès refusé"))
        for (code in listOf(502, 503, 504)) {
            assertEquals(WatchResult.Down("Le proxy (Pangolin) répond mais pas l'agent ($code)"), healthResult(code, "Bad Gateway"))
        }
        assertEquals(WatchResult.Down("Erreur du serveur (500)"), healthResult(500, ""))
        assertTrue((healthResult(404, "") as WatchResult.Down).cause.startsWith("Introuvable"))
    }

    @Test
    fun readableCauses() {
        assertEquals("Nom de domaine introuvable (DNS)", failureCause(UnknownHostException("vigie.example.com")))
        assertEquals("Délai dépassé : aucune réponse en 10 s", failureCause(SocketTimeoutException("connect timed out")))
        // Délai global de l'appel (OkHttp) : InterruptedIOException("timeout").
        assertEquals("Délai dépassé : aucune réponse en 10 s", failureCause(InterruptedIOException("timeout")))
        assertEquals("Connexion refusée", failureCause(ConnectException("Failed to connect")))
        val expired = SSLHandshakeException("handshake").apply { initCause(CertificateExpiredException("expired")) }
        assertEquals("Certificat HTTPS refusé (expiré ou invalide)", failureCause(expired))
        assertEquals("Échec de la connexion sécurisée (HTTPS)", failureCause(SSLHandshakeException("protocol")))
        assertEquals("Connexion impossible (unexpected end of stream)", failureCause(IOException("unexpected end of stream")))
        assertEquals("Connexion impossible", failureCause(IOException()))
    }

    @Test
    fun backMessageGivesDuration() {
        assertTrue(backMessage(since = 0, at = 45 * min).startsWith("Sans réponse pendant environ 45 min"))
    }
}
