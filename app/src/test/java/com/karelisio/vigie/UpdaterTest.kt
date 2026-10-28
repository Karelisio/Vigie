package com.karelisio.vigie

import com.karelisio.vigie.update.GhAsset
import com.karelisio.vigie.update.Updater
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class UpdaterTest {
    private fun asset(name: String) = GhAsset(name = name, size = 1, url = "https://example.com/$name")

    @Test
    fun picksThePhoneApkEvenWhenTheWatchApkComesFirst() {
        val assets = listOf(
            asset("vigie-wear-0.6.0.apk"),
            asset("vigie-wear-0.6.0.apk.sha256"),
            asset("vigie-0.6.0.apk.sha256"),
            asset("vigie-0.6.0.apk"),
        )
        assertEquals("vigie-0.6.0.apk", Updater.phoneApk(assets)?.name)
    }

    @Test
    fun neverPicksTheWatchApk() {
        assertNull(Updater.phoneApk(listOf(asset("vigie-wear-0.6.0.apk"), asset("vigie-wear-0.6.0.apk.sha256"))))
        assertNull(Updater.phoneApk(listOf(asset("Vigie-Wear-0.6.0.APK"))))
        assertNull(Updater.phoneApk(emptyList()))
    }

    @Test
    fun olderReleasesWithASingleApkStillWork() {
        assertEquals("vigie-0.5.0.apk", Updater.phoneApk(listOf(asset("vigie-0.5.0.apk"), asset("vigie-0.5.0.apk.sha256")))?.name)
        // Nom inattendu mais seul APK du téléphone : accepté.
        assertEquals("app-release.apk", Updater.phoneApk(listOf(asset("app-release.apk")))?.name)
    }

    @Test
    fun versionComparison() {
        assertTrue(Updater.isNewer("0.6.0", "0.5.0"))
        assertFalse(Updater.isNewer("0.5.0", "0.6.0"))
        assertTrue(Updater.isNewer("0.6.0", "0.6.0-dev"))
    }
}
