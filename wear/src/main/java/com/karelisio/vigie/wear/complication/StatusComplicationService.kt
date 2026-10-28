package com.karelisio.vigie.wear.complication

import android.app.PendingIntent
import android.content.Intent
import android.graphics.drawable.Icon
import androidx.wear.watchface.complications.data.ComplicationData
import androidx.wear.watchface.complications.data.ComplicationType
import androidx.wear.watchface.complications.data.MonochromaticImage
import androidx.wear.watchface.complications.data.PlainComplicationText
import androidx.wear.watchface.complications.data.ShortTextComplicationData
import androidx.wear.watchface.complications.datasource.ComplicationRequest
import androidx.wear.watchface.complications.datasource.SuspendingComplicationDataSourceService
import com.karelisio.vigie.wear.Level
import com.karelisio.vigie.wear.MainActivity
import com.karelisio.vigie.wear.R
import com.karelisio.vigie.wear.SecureStore
import com.karelisio.vigie.wear.StatusLogic
import com.karelisio.vigie.wear.StatusView
import com.karelisio.vigie.wear.WatchData
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull

/** Complication courte : « OK », « 2 pb » (problèmes en cours) ou « -- » sans données. Un appui ouvre l'appli. */
class StatusComplicationService : SuspendingComplicationDataSourceService() {

    override suspend fun onComplicationRequest(request: ComplicationRequest): ComplicationData? {
        if (request.complicationType != ComplicationType.SHORT_TEXT) return null
        val snapshot = withTimeoutOrNull(TIMEOUT_MS) {
            WatchData.refresh(this@StatusComplicationService, WatchData.Surface.Complication, WatchData.FRESHNESS_MS)
        } ?: WatchData.cached(this)
        val server = withContext(Dispatchers.IO) { SecureStore(this@StatusComplicationService).load() }
        return data(StatusLogic.view(server, snapshot))
    }

    override fun getPreviewData(type: ComplicationType): ComplicationData? {
        if (type != ComplicationType.SHORT_TEXT) return null
        return ShortTextComplicationData.Builder(plain("OK"), plain("Vigie : tout va bien"))
            .setTitle(plain("Vigie"))
            .setMonochromaticImage(icon())
            .build()
    }

    private fun data(view: StatusView): ComplicationData {
        val description = when {
            !view.configured -> "Vigie : pas encore configurée"
            view.updatedAt == 0L -> "Vigie : pas encore de données"
            view.level == Level.Ok && view.problemCount == 0 -> "Vigie : tout va bien"
            else -> "Vigie : ${view.label}, ${StatusLogic.problemsText(view.problemCount)}"
        }
        val open = PendingIntent.getActivity(
            this, 0, Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        return ShortTextComplicationData.Builder(plain(StatusLogic.shortText(view)), plain(description))
            .setTitle(plain("Vigie"))
            .setMonochromaticImage(icon())
            .setTapAction(open)
            .build()
    }

    private fun plain(text: String) = PlainComplicationText.Builder(text).build()

    private fun icon() = MonochromaticImage.Builder(Icon.createWithResource(this, R.drawable.ic_stat_vigie)).build()

    private companion object {
        const val TIMEOUT_MS = 8_000L
    }
}
