package com.karelisio.vigie.wear.tile

import android.content.ComponentName
import androidx.wear.protolayout.ActionBuilders.launchAction
import androidx.wear.protolayout.DimensionBuilders.expand
import androidx.wear.protolayout.LayoutElementBuilders.LayoutElement
import androidx.wear.protolayout.ModifiersBuilders.Clickable
import androidx.wear.protolayout.layout.column
import androidx.wear.protolayout.material3.MaterialScope
import androidx.wear.protolayout.material3.Typography
import androidx.wear.protolayout.material3.buttonGroup
import androidx.wear.protolayout.material3.primaryLayout
import androidx.wear.protolayout.material3.text
import androidx.wear.protolayout.material3.textDataCard
import androidx.wear.protolayout.material3.textEdgeButton
import androidx.wear.protolayout.modifiers.clickable
import androidx.wear.protolayout.modifiers.loadAction
import androidx.wear.protolayout.types.argb
import androidx.wear.protolayout.types.layoutString
import androidx.wear.tiles.Material3TileService
import androidx.wear.tiles.RequestBuilders.TileRequest
import androidx.wear.tiles.TileBuilders.Tile
import androidx.wear.tiles.tile
import androidx.wear.tiles.timeline
import androidx.wear.tiles.timelineEntry
import com.karelisio.vigie.wear.MainActivity
import com.karelisio.vigie.wear.SecureStore
import com.karelisio.vigie.wear.StatusLogic
import com.karelisio.vigie.wear.StatusView
import com.karelisio.vigie.wear.WatchData
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull
import kotlin.time.Duration.Companion.milliseconds

/**
 * Tuile : état global coloré, CPU/RAM/disque et nombre de problèmes. Un appui ouvre l'appli,
 * le bouton du bas actualise. Le système redemande la tuile au bout de 15 minutes.
 */
class StatusTileService : Material3TileService() {

    override suspend fun MaterialScope.tileResponse(requestParams: TileRequest): Tile {
        val service = this@StatusTileService
        val force = requestParams.currentState.lastClickableId == REFRESH_ID
        // Réponse rapide exigée : au-delà de quelques secondes, le dernier état connu est affiché.
        val snapshot = withTimeoutOrNull(TIMEOUT_MS) {
            WatchData.refresh(service, WatchData.Surface.Tile, if (force) 0 else WatchData.FRESHNESS_MS)
        } ?: WatchData.cached(service)
        val server = withContext(Dispatchers.IO) { SecureStore(service).load() }
        val view = StatusLogic.view(server, snapshot)
        return tile(
            timeline = timeline(timelineEntry(layout(view))),
            freshness = WatchData.FRESHNESS_MS.milliseconds,
        )
    }

    private fun MaterialScope.layout(view: StatusView): LayoutElement {
        val open = clickable(action = launchAction(ComponentName(this@StatusTileService, MainActivity::class.java)), id = OPEN_ID)
        val refresh = clickable(action = loadAction(), id = REFRESH_ID)
        if (!view.configured) {
            return primaryLayout(
                titleSlot = { text("Vigie".layoutString) },
                mainSlot = {
                    text(
                        "Envoie l'accès depuis Vigie sur ton téléphone : Réglages → Montre.".layoutString,
                        typography = Typography.BODY_MEDIUM,
                        maxLines = 4,
                    )
                },
                bottomSlot = { textEdgeButton(onClick = open) { text("Ouvrir".layoutString) } },
                onClick = open,
            )
        }
        val color = StatusLogic.color(view.level).argb
        val subtitle = view.errorText?.let { "$it · ${StatusLogic.time(view.updatedAt)}" }
            ?: "${StatusLogic.problemsText(view.problemCount)} · ${StatusLogic.time(view.updatedAt)}"
        return primaryLayout(
            titleSlot = { text(view.serverName.layoutString) },
            mainSlot = {
                column(
                    text(view.label.layoutString, typography = Typography.TITLE_MEDIUM, color = color),
                    text(
                        subtitle.layoutString,
                        typography = Typography.BODY_SMALL,
                        color = if (view.error != null) StatusLogic.AMBER.argb else colorScheme.onSurfaceVariant,
                    ),
                    buttonGroup(height = expand()) {
                        buttonGroupItem { metric(open, "CPU", view.cpu) }
                        buttonGroupItem { metric(open, "RAM", view.ram) }
                        buttonGroupItem { metric(open, "Disque", view.disk) }
                    },
                    width = expand(),
                    height = expand(),
                )
            },
            bottomSlot = { textEdgeButton(onClick = refresh) { text("Actualiser".layoutString) } },
            onClick = open,
        )
    }

    private fun MaterialScope.metric(open: Clickable, label: String, value: Int?): LayoutElement =
        textDataCard(
            onClick = open,
            title = { text((value?.let { "$it%" } ?: "--").layoutString) },
            content = { text(label.layoutString) },
            width = expand(),
            height = expand(),
        )

    private companion object {
        const val OPEN_ID = "open"
        const val REFRESH_ID = "refresh"
        const val TIMEOUT_MS = 8_000L
    }
}
