package com.karelisio.vigie

import com.karelisio.vigie.data.ChannelsConfig
import com.karelisio.vigie.data.DebridAccount
import com.karelisio.vigie.data.DebridAccountInput
import com.karelisio.vigie.data.DebridState
import com.karelisio.vigie.data.VigieJson
import com.karelisio.vigie.ui.home.summary
import com.karelisio.vigie.ui.home.tone
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class DebridTest {
    private val now = 1_800_000_000_000L
    private val day = 86_400_000L

    private fun premium(left: Long) = DebridAccount(provider = "realdebrid", ok = true, premium = true, expires = now + left, checkedAt = now)

    @Test
    fun toneFollowsUrgency() {
        assertEquals("ok", premium(30 * day).tone(now))
        assertEquals("warning", premium(6 * day).tone(now))
        assertEquals("critical", premium(20 * 3_600_000L).tone(now))
        assertEquals("critical", DebridAccount(ok = true, premium = false, checkedAt = now).tone(now))
        assertEquals("critical", DebridAccount(refused = true, error = "clé API refusée", checkedAt = now).tone(now))
        assertEquals("warning", DebridAccount(error = "délai dépassé", checkedAt = now).tone(now))
        assertEquals("neutral", DebridAccount().tone(now))
        assertEquals("ok", DebridAccount(ok = true, premium = true).tone(now))
    }

    @Test
    fun summaryExplainsProblems() {
        assertEquals("Clé API refusée (jeton invalide ou expiré)", DebridAccount(refused = true, error = "clé API refusée (jeton invalide ou expiré)").summary())
        assertEquals("Vérification impossible : délai dépassé", DebridAccount(error = "délai dépassé", checkedAt = now).summary())
        assertEquals("Vérification en attente", DebridAccount().summary())
        assertEquals("Pas de premium actif", DebridAccount(ok = true, checkedAt = now).summary())
    }

    @Test
    fun titleShowsLabelOnlyWhenItDiffers() {
        assertEquals("Real-Debrid", DebridAccount(provider = "realdebrid", label = "Real-Debrid").title)
        assertEquals("TorBox · Famille", DebridAccount(provider = "torbox", label = "Famille").title)
    }

    @Test
    fun decodesAgentJsonAndEncodesInput() {
        val state = VigieJson.decodeFromString<DebridState>(
            """{"accounts":[{"id":"a1","provider":"alldebrid","label":"AllDebrid","key":"••••cdef","ok":true,"premium":true,""" +
                """"expires":1800864000000,"days_left":10,"username":"alice","refused":false,"checked_at":1800000000000}]}""",
        )
        val a = state.accounts.single()
        assertEquals("••••cdef", a.key)
        assertEquals(10, a.daysLeft)
        assertTrue(a.premium)
        assertFalse(a.refused)
        // Nouveau compte : pas d'id envoyé ; l'agent refuse les champs inconnus.
        assertEquals(
            """{"provider":"torbox","label":"TorBox","key":"k"}""",
            VigieJson.encodeToString(DebridAccountInput.serializer(), DebridAccountInput(provider = "torbox", label = "TorBox", key = "k")),
        )
        val channels = VigieJson.decodeFromString<ChannelsConfig>(
            """{"channels":[{"type":"telegram","enabled":true,"min_level":"critical","bot_token":"••••Dsaw","chat_id":"42"}]}""",
        )
        assertEquals("critical", channels.channels.single().minLevel)
        assertEquals("••••Dsaw", channels.channels.single().botToken)
    }

    @Test
    fun servicesParsedAndBucketed() {
        val json = """{"accounts":[],"services":[{"provider":"realdebrid","name":"Real-Debrid","state":"down","detail":"délai dépassé",
            "latency_ms":15000,"since":${now - 600_000},"checked_at":$now,"failures":2,"uptime_24h":50,
            "history":[{"ts":${now - 3_600_000},"s":"up","ms":200},{"ts":${now - 60_000},"s":"down","ms":15000}],
            "incidents":[{"start":${now - 600_000},"end":0,"cause":"délai dépassé"}]}]}"""
        val svc = VigieJson.decodeFromString(DebridState.serializer(), json).services.single()
        assertEquals("critical", svc.tone)
        assertEquals(2, svc.failures)
        assertEquals("down", svc.history.last().state)
        assertEquals(0L, svc.incidents.single().end)
        assertTrue(svc.summary(now).startsWith("En panne depuis "))
        val buckets = svc.buckets(now)
        assertEquals(48, buckets.size)
        assertEquals(2, buckets.sumOf { it.total })
        assertEquals(1, buckets.sumOf { it.ok })
        assertEquals("En ligne · 230 ms", svc.copy(state = "up", latencyMs = 230).summary(now))
        assertEquals("Ralenti : réponse lente (6.2 s)", svc.copy(state = "degraded", detail = "réponse lente (6.2 s)").summary(now))
        // Agent 0.5 : pas de services.
        assertTrue(VigieJson.decodeFromString(DebridState.serializer(), """{"accounts":[]}""").services.isEmpty())
    }
}
