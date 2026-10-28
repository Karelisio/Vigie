package com.karelisio.vigie

import com.karelisio.vigie.data.AgentSettings
import com.karelisio.vigie.data.AutoHealRepair
import com.karelisio.vigie.data.Container
import com.karelisio.vigie.data.autoHeals
import com.karelisio.vigie.data.summary
import com.karelisio.vigie.data.withAutoHeal
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class AutoHealTest {
    private val comet = Container("1", "comet")
    private val redis = Container("2", "redis")
    private val agent = Container("3", "vigie-agent", self = true)
    private val all = listOf(comet, redis, agent)

    @Test
    fun enablingFromAContainerConcernsOnlyIt() {
        // Réparation coupée, « tous » mémorisé : l'activer depuis une fiche ne concerne que ce conteneur.
        val s = AgentSettings(autoHealAll = true, autoHealList = listOf("comet")).withAutoHeal(redis, true, all)
        assertTrue(s.autoHeal && s.autoHeals(redis))
        assertFalse(s.autoHealAll || s.autoHeals(comet))
        val more = s.withAutoHeal(comet, true, all)
        assertEquals(listOf("comet", "redis"), more.autoHealList)
    }

    @Test
    fun removingOneOfAllKeepsTheOthers() {
        val s = AgentSettings(autoHeal = true, autoHealAll = true).withAutoHeal(redis, false, all)
        assertFalse(s.autoHealAll || s.autoHeals(redis))
        assertEquals(listOf("comet"), s.autoHealList)
        assertTrue(s.autoHeals(comet))
    }

    @Test
    fun neverTheAgent() {
        assertFalse(AgentSettings(autoHeal = true, autoHealAll = true).autoHeals(agent))
        assertFalse(AgentSettings(autoHeal = false, autoHealAll = true).autoHeals(comet))
    }

    @Test
    fun summary() {
        assertEquals("Redémarré (healthcheck en échec)", AutoHealRepair(reason = "unhealthy", action = "restart").summary)
        assertEquals("Relancé (mémoire saturée)", AutoHealRepair(reason = "oom", action = "start").summary)
    }
}
