package com.karelisio.vigie.widget

import android.content.ComponentName
import android.content.Context
import android.service.quicksettings.TileService
import androidx.glance.appwidget.updateAll
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.ExistingWorkPolicy
import androidx.work.NetworkType
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import com.karelisio.vigie.appContainer
import com.karelisio.vigie.data.CachedState
import com.karelisio.vigie.tile.VigieTileService
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import java.util.concurrent.TimeUnit

/** Met à jour le widget et la tuile quand l'état du serveur actif change. */
class SurfaceUpdater(private val context: Context, private val scope: CoroutineScope) {
    @Volatile private var lastStatus: String? = null
    @Volatile private var lastPush = 0L

    fun onState(state: CachedState) {
        val status = state.overview?.status
        val now = System.currentTimeMillis()
        if (status == lastStatus && now - lastPush < 60_000) return
        lastStatus = status
        lastPush = now
        updateSurfaces()
    }

    fun updateSurfaces() {
        scope.launch {
            runCatching { VigieWidget().updateAll(context) }
            runCatching { TileService.requestListeningState(context, ComponentName(context, VigieTileService::class.java)) }
        }
    }

    /** Rafraîchissement immédiat (après une notification push, un appui sur le widget…). */
    fun refreshNow() {
        val req = OneTimeWorkRequestBuilder<RefreshWorker>()
            .setConstraints(Constraints(requiredNetworkType = NetworkType.CONNECTED))
            .build()
        WorkManager.getInstance(context).enqueueUniqueWork("vigie-refresh-now", ExistingWorkPolicy.REPLACE, req)
    }

    fun schedulePeriodic() {
        val req = PeriodicWorkRequestBuilder<RefreshWorker>(15, TimeUnit.MINUTES)
            .setConstraints(Constraints(requiredNetworkType = NetworkType.CONNECTED))
            .build()
        WorkManager.getInstance(context).enqueueUniquePeriodicWork("vigie-refresh", ExistingPeriodicWorkPolicy.KEEP, req)
    }
}

/** Récupère la vue d'ensemble du serveur actif hors de l'appli (widget, tuile). */
class RefreshWorker(context: Context, params: WorkerParameters) : CoroutineWorker(context, params) {
    override suspend fun doWork(): Result {
        val c = appContainer
        val server = c.servers.active ?: return Result.success()
        return runCatching { c.repository.fetchOverview(server) }
            .fold(
                onSuccess = { c.surfaces.updateSurfaces(); Result.success() },
                onFailure = { c.surfaces.updateSurfaces(); if (runAttemptCount < 2) Result.retry() else Result.success() },
            )
    }
}
