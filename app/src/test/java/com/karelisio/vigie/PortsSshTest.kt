package com.karelisio.vigie

import com.karelisio.vigie.data.OpenPort
import com.karelisio.vigie.data.PortsReport
import com.karelisio.vigie.data.SshSummary
import com.karelisio.vigie.data.VigieJson
import com.karelisio.vigie.data.countryFlag
import com.karelisio.vigie.data.hostOf
import com.karelisio.vigie.data.sshMethodLabel
import com.karelisio.vigie.ui.security.portsSummaryText
import com.karelisio.vigie.ui.security.sshSummaryText
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class PortsSshTest {
    /** Réponse de GET /api/v1/ports (agent 0.6.0). */
    private val portsJson = """
        {"ts":1793190000000,"ports":[
          {"port":443,"proto":"tcp","address":"0.0.0.0","addresses":["0.0.0.0","::"],"scope":"public","container":"gerbil",
           "container_id":"g1","target_port":443,"pangolin":true,"allowed":false,"bypasses_pangolin":false,"alerted":false,
           "source":"socket","since":1793100000000},
          {"port":8080,"proto":"tcp","address":"0.0.0.0","addresses":["0.0.0.0"],"scope":"public","container":"comet",
           "container_id":"c1","target_port":8000,"pangolin":false,"allowed":false,"bypasses_pangolin":true,"alerted":true,
           "source":"socket","since":1793180000000},
          {"port":22,"proto":"tcp","address":"0.0.0.0","addresses":["0.0.0.0","::"],"scope":"public","process":"SSH",
           "pangolin":false,"allowed":true,"bypasses_pangolin":false,"alerted":false,"source":"socket","since":1793100000000},
          {"port":51820,"proto":"udp","address":"0.0.0.0","addresses":["0.0.0.0"],"scope":"public","container":"gerbil",
           "pangolin":true,"allowed":false,"bypasses_pangolin":false,"alerted":false,"source":"docker","since":1793100000000},
          {"port":53,"proto":"udp","address":"127.0.0.53","addresses":["127.0.0.53"],"scope":"local","interface":"lo",
           "process":"DNS","pangolin":false,"allowed":false,"bypasses_pangolin":false,"alerted":false,"source":"socket","since":1},
          {"port":9000,"proto":"tcp","address":"10.0.0.5","addresses":["10.0.0.5"],"scope":"public","interface":"ens3",
           "container":"portainer","host_network":true,"pangolin":false,"allowed":false,"bypasses_pangolin":true,"alerted":false,
           "source":"socket","since":2,"futur":"ignoré"}
        ],"host_ip":"10.0.0.5","pangolin":["gerbil","traefik"],"docker":true}
    """.trimIndent()

    @Test
    fun decodesPortsReport() {
        val r = VigieJson.decodeFromString<PortsReport>(portsJson)
        assertEquals(6, r.ports.size)
        assertEquals(5, r.public.size)
        assertEquals("10.0.0.5", r.hostIp)
        val comet = r.ports[1]
        assertEquals("8080/tcp", comet.key)
        assertEquals(8000, comet.targetPort)
        assertTrue(comet.bypassesPangolin && comet.alerted && comet.needsReview)
        assertEquals("comet", comet.owner)
        val ssh = r.ports[2]
        assertEquals("SSH", ssh.owner)
        assertFalse(ssh.needsReview) // autorisé
        val gerbil = r.ports[0]
        assertTrue(gerbil.pangolin && !gerbil.needsReview)
        assertEquals(listOf("0.0.0.0", "::"), gerbil.addresses)
        val dns = r.ports[4]
        assertEquals("lo", dns.iface)
        assertFalse(dns.isPublic || dns.isTcp)
        assertTrue(r.ports[5].hostNetwork)
        assertEquals("Processus de l'hôte", OpenPort(port = 9999).owner)
    }

    @Test
    fun portsSubtitleCountsWhatMatters() {
        val r = VigieJson.decodeFromString<PortsReport>(portsJson)
        assertEquals("5 ports sur Internet, 2 contournent Pangolin", portsSummaryText(r))
        assertEquals("Aucun port ouvert sur Internet", portsSummaryText(PortsReport()))
        val one = PortsReport(ports = listOf(OpenPort(port = 9000, scope = "public")))
        assertEquals("1 port sur Internet, 1 à vérifier", portsSummaryText(one))
        val allowed = PortsReport(ports = listOf(OpenPort(port = 443, scope = "public", pangolin = true)))
        assertEquals("1 port sur Internet", portsSummaryText(allowed))
    }

    /** Réponse de GET /api/v1/ssh (agent 0.6.0). */
    private val sshJson = """
        {"state":"ok","source":"/var/log/auth.log","message":"","fail2ban":"ok","read_at":1793190000000,"geo":true,
         "stats":{"failed_24h":1234,"failed_7d":8000,"accepted_7d":3,"bans_7d":12,"ips_7d":321,"users_7d":150,"since":1792600000000},
         "top_ips":[{"ip":"198.51.100.9","count":900,"last":1793189000000,"banned":true,"accepted":false,"country":"CN","country_name":"Chine"},
                    {"ip":"10.0.0.20","count":2,"last":1793188000000,"banned":false,"accepted":true,"country":"","country_name":"Réseau privé"}],
         "top_users":[{"user":"admin","count":500,"invalid":true},{"user":"root","count":300,"invalid":false}],
         "logins":[{"ts":1793185000000,"user":"ubuntu","ip":"203.0.113.7","method":"publickey","country":"FR","country_name":"France","new_ip":true}],
         "hourly":[{"ts":1793106000000,"count":10},{"ts":1793109600000,"count":0}],
         "daily":[{"ts":1790600000000,"count":100}]}
    """.trimIndent()

    @Test
    fun decodesSshSummary() {
        val s = VigieJson.decodeFromString<SshSummary>(sshJson)
        assertTrue(s.ok)
        assertEquals(1234, s.stats.failed24h)
        assertEquals(12, s.stats.bans7d)
        assertEquals("Chine", s.topIps[0].countryName)
        assertTrue(s.topIps[0].banned)
        assertTrue(s.topIps[1].accepted)
        assertFalse(s.topUsers[1].invalid)
        val login = s.logins.single()
        assertTrue(login.newIp)
        assertEquals("publickey", login.method)
        assertEquals(2, s.hourly.size)
        assertEquals(100, s.daily.single().count)
    }

    @Test
    fun decodesPermissionState() {
        val s = VigieJson.decodeFromString<SshSummary>(
            """{"state":"permission","source":"/var/log/auth.log","message":"L'agent n'a pas le droit de lire /var/log/auth.log",""" +
                """"fix":"    group_add:\n      - \"4\"   # groupe adm","fail2ban":"absent","read_at":0,"geo":true,""" +
                """"stats":{"failed_24h":0,"failed_7d":0,"accepted_7d":0,"bans_7d":0,"ips_7d":0,"users_7d":0,"since":0},""" +
                """"top_ips":[],"top_users":null,"logins":[],"hourly":[],"daily":[]}""",
        )
        assertFalse(s.ok)
        assertTrue(s.fix.contains("group_add:\n      - \"4\""))
        assertEquals(emptyList<Any>(), s.topUsers)
        assertEquals("Droits à ajouter pour lire le journal", sshSummaryText(s))
    }

    @Test
    fun sshSubtitle() {
        val s = VigieJson.decodeFromString<SshSummary>(sshJson)
        assertEquals("1 234 échecs en 24 h · 3 connexions cette semaine", sshSummaryText(s))
        assertEquals("0 échec en 24 h · 1 connexion cette semaine", sshSummaryText(SshSummary(state = "ok", stats = s.stats.copy(failed24h = 0, accepted7d = 1))))
        assertEquals("Surveillance désactivée", sshSummaryText(SshSummary(state = "disabled")))
        assertNull(sshSummaryText(SshSummary(state = "error")))
    }

    @Test
    fun flagsMethodsAndHosts() {
        assertEquals("🇫🇷", countryFlag("FR"))
        assertEquals("🇩🇪", countryFlag("de"))
        assertEquals("", countryFlag(""))
        assertEquals("", countryFlag("F1"))
        assertEquals("Clé SSH", sshMethodLabel("publickey"))
        assertEquals("Mot de passe (interactif)", sshMethodLabel("keyboard-interactive"))
        assertEquals("vigie.15122000.xyz", hostOf("https://vigie.15122000.xyz"))
        assertEquals("vigie.15122000.xyz", hostOf("https://vigie.15122000.xyz:8443/chemin?x=1"))
        assertEquals("2001:db8::1", hostOf("http://[2001:db8::1]:8080/"))
        assertEquals("10.0.0.5", hostOf("10.0.0.5:8080"))
    }
}
