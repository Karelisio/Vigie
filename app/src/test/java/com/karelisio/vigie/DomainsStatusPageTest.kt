package com.karelisio.vigie

import com.karelisio.vigie.data.DomainStatus
import com.karelisio.vigie.data.Server
import com.karelisio.vigie.data.StatusPageConfig
import com.karelisio.vigie.data.StatusPageInput
import com.karelisio.vigie.data.StatusPageItem
import com.karelisio.vigie.data.VigieJson
import com.karelisio.vigie.data.byUrgency
import com.karelisio.vigie.data.cleanDomainInput
import com.karelisio.vigie.data.matchingDomain
import com.karelisio.vigie.data.notableStatus
import com.karelisio.vigie.data.summary
import com.karelisio.vigie.data.tone
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class DomainsStatusPageTest {
    /** Réponse de GET /api/v1/domains (agent 0.6). */
    private val domainsJson = """[
        {"domain":"15122000.xyz","source":"auto","services":["AIOStreams","Comet","Vigie"],"expires":1805016413000,"days_left":138,
         "registered":1678786013000,"last_changed":1769932800000,"registrar":"NameCheap, Inc.",
         "status":["client transfer prohibited","active"],"checked_at":1793200000000},
        {"domain":"example.co.uk","source":"manual","services":[],"expires":0,"days_left":null,"registered":989107200000,
         "last_changed":0,"registrar":"Registrar Ltd","status":["active"],"checked_at":1793200000000,
         "note":"le registre ne publie pas la date d'expiration (RDAP) : vérifie-la chez ton bureau d'enregistrement"},
        {"domain":"bientot.fr","source":"manual","services":[],"expires":1793600000000,"days_left":4,"registered":0,"last_changed":0,
         "registrar":"","status":["redemption period","client hold"],"checked_at":1793200000000,"error":"registre injoignable : délai dépassé"},
        {"domain":"nouveau.net","source":"manual","services":[],"expires":0,"days_left":null,"registered":0,"last_changed":0,
         "registrar":"","status":[],"checked_at":0},
        {"domain":"moi.duckdns.org","source":"manual","services":[],"expires":0,"days_left":null,"registered":0,"last_changed":0,
         "registrar":"","status":[],"checked_at":0,"error":"domaine impossible à vérifier : suffixe privé ou inconnu"}
    ]"""

    @Test
    fun decodesDomainsContract() {
        val list = VigieJson.decodeFromString<List<DomainStatus>>(domainsJson)
        val xyz = list[0]
        assertEquals(138, xyz.daysLeft)
        assertEquals(listOf("AIOStreams", "Comet", "Vigie"), xyz.services)
        assertEquals("NameCheap, Inc.", xyz.registrar)
        assertEquals(1769932800000, xyz.lastChanged)
        assertFalse(xyz.manual)
        assertEquals(listOf("Transfert verrouillé"), xyz.notableStatus())
        assertEquals("ok", xyz.tone(30))
        assertTrue(xyz.summary().startsWith("Expire le "))

        val uk = list[1]
        assertNull(uk.daysLeft)
        assertTrue(uk.manual)
        assertEquals("neutral", uk.tone(30))
        assertEquals("Date d'expiration inconnue", uk.summary())
        assertTrue(uk.note.isNotEmpty())

        // Dernière date connue, malgré l'échec de la dernière vérification.
        val soon = list[2]
        assertEquals("critical", soon.tone(30))
        assertTrue(soon.summary().startsWith("Expire le "))
        assertEquals(listOf("Période de rédemption", "Suspendu"), soon.notableStatus())

        assertEquals("Vérification en attente", list[3].summary())
        assertEquals("Domaine impossible à vérifier : suffixe privé ou inconnu", list[4].summary())
        assertEquals("neutral", list[4].tone(30))

        assertEquals(
            listOf("bientot.fr", "15122000.xyz", "example.co.uk", "moi.duckdns.org", "nouveau.net"),
            list.byUrgency().map { it.domain },
        )
    }

    @Test
    fun domainToneFollowsNotice() {
        fun left(d: Int) = DomainStatus(domain = "a.fr", expires = 1, daysLeft = d, checkedAt = 1)
        assertEquals("ok", left(30).tone(30))
        assertEquals("warning", left(29).tone(30))
        assertEquals("warning", left(7).tone(30))
        assertEquals("critical", left(6).tone(30))
        assertEquals("critical", left(-3).tone(30))
        assertEquals("ok", left(50).tone(45))
        assertTrue(left(-3).summary().startsWith("Expiré le "))
        assertEquals("warning", DomainStatus(domain = "a.fr", checkedAt = 1, error = "délai dépassé").tone(30))
    }

    @Test
    fun cleansDomainInput() {
        assertEquals("www.exemple.fr", cleanDomainInput("  https://WWW.Exemple.fr/chemin "))
        assertEquals("exemple.co.uk", cleanDomainInput("exemple.co.uk."))
        assertNull(cleanDomainInput("exemple"))
        assertNull(cleanDomainInput("exem ple.fr"))
        assertNull(cleanDomainInput("-exemple.fr"))
        assertNull(cleanDomainInput("127.0.0.1"))
        assertEquals(listOf("exemple.fr", "vigie.exemple.fr"), listOf("exemple.fr", "vigie.exemple.fr", "autreexemple.fr").matchingDomain("exemple.fr"))
    }

    @Test
    fun statusPageContract() {
        val c = VigieJson.decodeFromString<StatusPageConfig>(
            """{"enabled":true,"slug":"Zk3v_9aQ-1LmN0pQrStUvW","title":"État des services","description":"Stremio de la famille",
                "items":[{"kind":"check","ref":"7","label":"Recherche"},{"kind":"container","ref":"comet","label":"Comet"}],
                "show_uptime":false,"url":"https://vigie.15122000.xyz/status/Zk3v_9aQ-1LmN0pQrStUvW"}""",
        )
        assertTrue(c.enabled)
        assertFalse(c.showUptime)
        assertEquals(StatusPageItem(StatusPageItem.CONTAINER, "comet", "Comet"), c.items[1])
        assertEquals("https://vigie.15122000.xyz/status/Zk3v_9aQ-1LmN0pQrStUvW", c.link(null))

        // Sans VIGIE_PUBLIC_URL : lien construit avec l'adresse du serveur dans l'appli.
        val server = Server(name = "VPS", url = "https://vigie.15122000.xyz/", token = "x".repeat(40))
        assertEquals("https://vigie.15122000.xyz/status/Zk3v_9aQ-1LmN0pQrStUvW", c.copy(url = "").link(server))
        assertEquals("", c.copy(url = "").link(null))

        // L'appli n'envoie ni l'identifiant ni l'adresse ; les noms publics sont nettoyés.
        val edited = c.copy(title = "  Mes services ", items = listOf(StatusPageItem("check", "7", " Recherche ")))
        assertEquals(
            """{"enabled":true,"title":"Mes services","description":"Stremio de la famille",""" +
                """"items":[{"kind":"check","ref":"7","label":"Recherche"}],"show_uptime":false}""",
            VigieJson.encodeToString(StatusPageInput.serializer(), edited.input()),
        )
        // Agent plus récent : champs inconnus ignorés.
        val future = VigieJson.decodeFromString<StatusPageConfig>("""{"slug":"abc","items":[],"theme":"dark"}""")
        assertTrue(future.showUptime)
        assertEquals("abc", future.slug)
    }
}
