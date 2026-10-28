package com.karelisio.vigie.tile

import android.annotation.SuppressLint
import android.app.PendingIntent
import android.content.Intent
import android.graphics.drawable.Icon
import android.os.Build
import android.service.quicksettings.Tile
import android.service.quicksettings.TileService
import com.karelisio.vigie.MainActivity
import com.karelisio.vigie.R
import com.karelisio.vigie.appContainer
import com.karelisio.vigie.data.CachedState
import com.karelisio.vigie.data.Fmt
import com.karelisio.vigie.watch.watchMoment
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull

/** Tuile des réglages rapides : état du serveur actif, un appui ouvre l'appli. */
class VigieTileService : TileService() {
    private var scope: CoroutineScope? = null

    override fun onStartListening() {
        super.onStartListening()
        val c = appContainer
        val server = c.servers.active
        render(server?.name, server?.let { c.cache.read(it.id) }, refreshing = server != null)
        if (server == null) return
        val s = CoroutineScope(SupervisorJob() + Dispatchers.Main).also { scope = it }
        s.launch {
            val fresh = withContext(Dispatchers.IO) {
                withTimeoutOrNull(8_000) { runCatching { c.repository.fetchOverview(server) }.getOrNull() }
            }
            render(server.name, fresh ?: c.cache.read(server.id), refreshing = false)
        }
    }

    override fun onStopListening() {
        scope?.cancel()
        scope = null
        super.onStopListening()
    }

    private fun render(name: String?, state: CachedState?, refreshing: Boolean) {
        val tile = qsTile ?: return
        val ov = state?.overview
        val stale = state != null && System.currentTimeMillis() - state.updatedAt > 10 * 60_000
        // Panne constatée par la surveillance depuis le téléphone, après les dernières données reçues.
        val outage = appContainer.servers.active?.takeIf { !refreshing }?.let { appContainer.watch.outageSince(it.id, state?.updatedAt ?: 0) }
        tile.label = name ?: "Vigie"
        val subtitle = when {
            name == null -> "Aucun serveur"
            outage != null -> "Injoignable depuis ${watchMoment(outage, System.currentTimeMillis())}"
            ov == null -> if (refreshing) "Connexion…" else "Hors ligne"
            stale && !refreshing -> "Hors ligne · ${Fmt.ago(state.updatedAt)}"
            ov.status == "ok" -> "Tout va bien · CPU ${ov.system?.cpu?.toInt() ?: 0} %"
            ov.status == "warning" -> "À surveiller"
            else -> ov.issues.size.let { if (it <= 1) "1 problème" else "$it problèmes" }
        }
        if (Build.VERSION.SDK_INT >= 29) tile.subtitle = subtitle
        else tile.label = "${name ?: "Vigie"} · $subtitle"
        tile.state = when {
            ov == null || stale -> Tile.STATE_UNAVAILABLE
            ov.status == "ok" -> Tile.STATE_ACTIVE
            else -> Tile.STATE_INACTIVE
        }
        tile.icon = Icon.createWithResource(this, if (outage == null && (ov == null || ov.status == "ok")) R.drawable.ic_tile else R.drawable.ic_widget_alert)
        tile.contentDescription = "Vigie : $subtitle"
        tile.updateTile()
    }

    @SuppressLint("StartActivityAndCollapseDeprecated")
    override fun onClick() {
        super.onClick()
        val intent = Intent(this, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        if (Build.VERSION.SDK_INT >= 34) {
            startActivityAndCollapse(PendingIntent.getActivity(this, 0, intent, PendingIntent.FLAG_IMMUTABLE))
        } else {
            @Suppress("DEPRECATION")
            startActivityAndCollapse(intent)
        }
    }
}
