package com.karelisio.vigie.tile

import android.graphics.drawable.Icon
import android.os.Build
import android.service.quicksettings.Tile
import android.service.quicksettings.TileService
import com.karelisio.vigie.R
import com.karelisio.vigie.appContainer
import com.karelisio.vigie.data.AgentSettings
import com.karelisio.vigie.data.Fmt
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull

/** Tuile « Maintenance » : un appui coupe les notifications pendant 1 h (ou les réactive). */
class MaintenanceTileService : TileService() {
    private var scope: CoroutineScope? = null
    private var until = 0L

    override fun onStartListening() {
        super.onStartListening()
        val s = CoroutineScope(SupervisorJob() + Dispatchers.Main).also { scope = it }
        s.launch { refresh() }
    }

    override fun onStopListening() {
        scope?.cancel()
        scope = null
        super.onStopListening()
    }

    private suspend fun refresh() {
        val c = appContainer
        if (c.servers.active == null) return render(null, "Aucun serveur")
        val settings = withContext(Dispatchers.IO) {
            withTimeoutOrNull(8_000) { runCatching { c.repository.settings() }.getOrNull() }
        }
        render(settings, if (settings == null) "Serveur injoignable" else null)
    }

    private fun render(settings: AgentSettings?, error: String?) {
        val tile = qsTile ?: return
        until = settings?.maintenanceUntil ?: 0
        val active = until > System.currentTimeMillis()
        val subtitle = error ?: if (active) "Jusqu'à ${Fmt.time(until)}" else "Notifications actives"
        tile.label = getString(R.string.tile_maintenance_label)
        if (Build.VERSION.SDK_INT >= 29) tile.subtitle = subtitle
        tile.state = when {
            error != null -> Tile.STATE_UNAVAILABLE
            active -> Tile.STATE_ACTIVE
            else -> Tile.STATE_INACTIVE
        }
        tile.icon = Icon.createWithResource(this, R.drawable.ic_tile_maintenance)
        tile.contentDescription = "Maintenance : $subtitle"
        tile.updateTile()
    }

    override fun onClick() {
        super.onClick()
        // Téléphone verrouillé : déverrouillage demandé avant de couper les alertes.
        if (isLocked) unlockAndRun { toggle() } else toggle()
    }

    private fun toggle() {
        val s = scope ?: return
        val minutes = if (until > System.currentTimeMillis()) 0 else 60
        s.launch {
            val r = withContext(Dispatchers.IO) {
                withTimeoutOrNull(10_000) { runCatching { appContainer.repository.setMaintenance(minutes) }.getOrNull() }
            }
            render(r, if (r == null) "Échec, réessaie" else null)
        }
    }
}
