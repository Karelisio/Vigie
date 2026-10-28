package com.karelisio.vigie

import com.karelisio.vigie.data.AgentJob
import com.karelisio.vigie.data.AgentSettings
import com.karelisio.vigie.data.Container
import com.karelisio.vigie.data.JobResult
import com.karelisio.vigie.data.autoUpdates
import com.karelisio.vigie.data.withAutoUpdate
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class AutoUpdateTest {
    private val comet = Container("1", "comet")
    private val redis = Container("2", "redis")
    private val sonarr = Container("3", "sonarr", updateAvailable = true)
    private val agent = Container("4", "vigie-agent", self = true)
    private val all = listOf(comet, redis, sonarr, agent)

    @Test
    fun enablingAContainerTurnsNightlyUpdatesOn() {
        val s = AgentSettings().withAutoUpdate(redis, true, all)
        assertTrue(s.autoUpdate && s.autoUpdates(redis))
        assertFalse(s.autoUpdates(comet) || s.autoUpdates(agent))
        assertEquals(listOf("comet", "redis"), AgentSettings(autoUpdateList = listOf("comet")).withAutoUpdate(redis, true, all).autoUpdateList)
    }

    @Test
    fun removingOneOfAllKeepsTheOthers() {
        val s = AgentSettings(autoUpdate = true, autoUpdateAll = true).withAutoUpdate(redis, false, all)
        assertFalse(s.autoUpdateAll)
        assertEquals(listOf("comet", "sonarr"), s.autoUpdateList)
        assertFalse(s.autoUpdates(redis))
    }

    @Test
    fun agentHasItsOwnSwitch() {
        val s = AgentSettings(autoUpdate = true, autoUpdateAll = true)
        assertFalse(s.autoUpdates(agent))
        assertTrue(s.withAutoUpdate(agent, true, all).autoUpdates(agent))
        assertFalse(AgentSettings(autoUpdateAgent = true).autoUpdates(agent))
    }

    @Test
    fun nightlyJobConcernsUpdatedContainers() {
        val job = AgentJob(kind = "auto", target = "nightly", results = listOf(JobResult("comet", ok = true, updated = true)))
        assertTrue(job.concerns(comet) && job.concerns(sonarr))
        assertFalse(job.concerns(redis))
    }
}
