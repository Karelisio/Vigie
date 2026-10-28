package com.karelisio.vigie.widget

import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Build
import androidx.compose.runtime.Composable
import androidx.compose.ui.unit.DpSize
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.glance.GlanceId
import androidx.glance.GlanceModifier
import androidx.glance.GlanceTheme
import androidx.glance.LocalContext
import androidx.glance.LocalSize
import androidx.glance.appwidget.action.actionStartActivity
import androidx.glance.action.clickable
import androidx.glance.appwidget.GlanceAppWidget
import androidx.glance.appwidget.GlanceAppWidgetReceiver
import androidx.glance.appwidget.SizeMode
import androidx.glance.appwidget.appWidgetBackground
import androidx.glance.appwidget.cornerRadius
import androidx.glance.appwidget.lazy.LazyColumn
import androidx.glance.appwidget.lazy.items
import androidx.glance.appwidget.provideContent
import androidx.glance.background
import androidx.glance.layout.Alignment
import androidx.glance.layout.Box
import androidx.glance.layout.Column
import androidx.glance.layout.Row
import androidx.glance.layout.Spacer
import androidx.glance.layout.fillMaxSize
import androidx.glance.layout.fillMaxWidth
import androidx.glance.layout.height
import androidx.glance.layout.padding
import androidx.glance.layout.size
import androidx.glance.layout.width
import androidx.glance.text.FontWeight
import androidx.glance.text.Text
import androidx.glance.text.TextStyle
import com.karelisio.vigie.MainActivity
import com.karelisio.vigie.appContainer
import com.karelisio.vigie.data.CachedState
import com.karelisio.vigie.data.CheckStatus
import com.karelisio.vigie.data.Fmt

/** Second widget : état de chaque service surveillé (checks HTTP). */
class ChecksWidget : GlanceAppWidget() {
    override val sizeMode = SizeMode.Responsive(setOf(DpSize(110.dp, 48.dp), DpSize(180.dp, 110.dp), DpSize(260.dp, 170.dp)))

    override suspend fun provideGlance(context: Context, id: GlanceId) {
        val c = appContainer
        val server = c.servers.active
        val state = server?.let { c.cache.read(it.id) }
        provideContent {
            GlanceTheme { ChecksContent(server?.name, state) }
        }
    }
}

private fun CheckStatus.color() = when (state) {
    "up" -> Ok
    "down" -> Crit
    else -> Warn
}

@Composable
private fun ChecksContent(serverName: String?, state: CachedState?) {
    val size = LocalSize.current
    val context = LocalContext.current
    val checks = state?.checks.orEmpty().filter { it.enabled }.sortedWith(compareBy({ it.state == "up" }, { it.position }))
    val down = checks.count { it.state == "down" }
    val up = checks.count { it.state == "up" }
    val headline = when {
        serverName == null -> "Ajoute un serveur"
        state == null -> "En attente…"
        checks.isEmpty() -> "Aucun service"
        down == 0 -> "$up/${checks.size} services OK"
        down == 1 -> "1 service en panne"
        else -> "$down services en panne"
    }
    val headColor = when {
        state == null || checks.isEmpty() -> GlanceTheme.colors.onSurface
        down > 0 -> Crit
        up < checks.size -> Warn
        else -> Ok
    }
    val radius = if (Build.VERSION.SDK_INT >= 31) GlanceModifier.cornerRadius(android.R.dimen.system_app_widget_background_radius)
    else GlanceModifier.cornerRadius(24.dp)
    val open = actionStartActivity(
        Intent(context, MainActivity::class.java).setData(Uri.parse("vigie://open/checks")).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
    )

    Box(
        GlanceModifier
            .fillMaxSize()
            .appWidgetBackground()
            .then(radius)
            .background(GlanceTheme.colors.widgetBackground)
            .clickable(open)
            .padding(horizontal = 14.dp, vertical = 10.dp),
    ) {
        Column(GlanceModifier.fillMaxSize()) {
            Text(
                headline,
                maxLines = 1,
                style = TextStyle(color = headColor, fontSize = if (size.width >= 180.dp) 17.sp else 14.sp, fontWeight = FontWeight.Bold),
            )
            if (size.height >= 80.dp && state != null) {
                Text(
                    (serverName ?: "") + " · " + Fmt.ago(state.updatedAt),
                    maxLines = 1,
                    style = TextStyle(color = GlanceTheme.colors.onSurfaceVariant, fontSize = 12.sp),
                )
                Spacer(GlanceModifier.height(6.dp))
                LazyColumn(GlanceModifier.fillMaxWidth().defaultWeight()) {
                    items(checks, itemId = { it.id }) { ck -> CheckRow(ck, wide = size.width >= 240.dp, onClick = open) }
                }
            }
        }
    }
}

@Composable
private fun CheckRow(ck: CheckStatus, wide: Boolean, onClick: androidx.glance.action.Action) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        modifier = GlanceModifier.fillMaxWidth().padding(vertical = 3.dp).clickable(onClick),
    ) {
        Box(GlanceModifier.size(8.dp).cornerRadius(4.dp).background(ck.color())) {}
        Spacer(GlanceModifier.width(8.dp))
        Text(
            ck.name,
            maxLines = 1,
            style = TextStyle(color = GlanceTheme.colors.onSurface, fontSize = 13.sp, fontWeight = FontWeight.Medium),
            modifier = GlanceModifier.defaultWeight(),
        )
        val last = ck.last
        val detail = when {
            ck.state == "down" -> "en panne"
            last == null -> "…"
            wide && ck.uptime24h >= 0 -> "${Fmt.ms(last.latencyMs)} · ${Fmt.uptimePct(ck.uptime24h)}"
            else -> Fmt.ms(last.latencyMs)
        }
        Text(
            detail,
            maxLines = 1,
            style = TextStyle(color = if (ck.state == "down") Crit else GlanceTheme.colors.onSurfaceVariant, fontSize = 12.sp),
        )
    }
}

class ChecksWidgetReceiver : GlanceAppWidgetReceiver() {
    override val glanceAppWidget: GlanceAppWidget = ChecksWidget()

    override fun onEnabled(context: Context) {
        super.onEnabled(context)
        appContainer.surfaces.refreshNow()
    }
}
