package com.karelisio.vigie.widget

import android.content.Context
import android.os.Build
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.DpSize
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.glance.GlanceId
import androidx.glance.GlanceModifier
import androidx.glance.GlanceTheme
import androidx.glance.Image
import androidx.glance.ImageProvider
import androidx.glance.LocalSize
import androidx.glance.action.clickable
import androidx.glance.appwidget.GlanceAppWidget
import androidx.glance.appwidget.GlanceAppWidgetReceiver
import androidx.glance.appwidget.LinearProgressIndicator
import androidx.glance.appwidget.SizeMode
import androidx.glance.action.actionStartActivity
import androidx.glance.appwidget.appWidgetBackground
import androidx.glance.appwidget.cornerRadius
import androidx.glance.appwidget.provideContent
import androidx.glance.background
import androidx.glance.color.ColorProvider
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
import androidx.glance.unit.ColorProvider
import com.karelisio.vigie.MainActivity
import com.karelisio.vigie.R
import com.karelisio.vigie.appContainer
import com.karelisio.vigie.data.CachedState
import com.karelisio.vigie.data.Fmt
import kotlin.math.roundToInt

internal val Ok = ColorProvider(day = Color(0xFF1B873F), night = Color(0xFF6EDB8F))
internal val Warn = ColorProvider(day = Color(0xFFB25E00), night = Color(0xFFFFB86B))
internal val Crit = ColorProvider(day = Color(0xFFC62828), night = Color(0xFFFF8A80))

internal fun statusColor(status: String?): ColorProvider = when (status) {
    "ok" -> Ok
    "warning" -> Warn
    "critical" -> Crit
    else -> Warn
}

/** Widget : état global + CPU/RAM, couleurs dynamiques, redimensionnable. Un appui ouvre l'appli. */
class VigieWidget : GlanceAppWidget() {
    override val sizeMode = SizeMode.Responsive(setOf(SMALL, MEDIUM, LARGE, XLARGE))

    override suspend fun provideGlance(context: Context, id: GlanceId) {
        val c = appContainer
        val server = c.servers.active
        val state = server?.let { c.cache.read(it.id) }
        val favorites = server?.let { c.prefs.current().favorites[it.id] }.orEmpty()
        // Panne constatée par la surveillance depuis le téléphone, après les dernières données reçues.
        val unreachable = server != null && c.watch.outageSince(server.id, state?.updatedAt ?: 0) != null
        provideContent {
            GlanceTheme {
                WidgetContent(serverName = server?.name, state = state, favorites = favorites, unreachable = unreachable)
            }
        }
    }

    companion object {
        val SMALL = DpSize(110.dp, 48.dp)
        val MEDIUM = DpSize(180.dp, 110.dp)
        val LARGE = DpSize(260.dp, 170.dp)
        val XLARGE = DpSize(260.dp, 250.dp)
    }
}

@Composable
private fun WidgetContent(serverName: String?, state: CachedState?, favorites: Set<String>, unreachable: Boolean) {
    val size = LocalSize.current
    val ov = state?.overview
    val sys = ov?.system ?: state?.system
    val now = System.currentTimeMillis()
    val stale = state == null || now - state.updatedAt > 20 * 60_000
    val status = if (unreachable) "critical" else if (state == null) null else ov?.status
    val headline = when {
        serverName == null -> "Ajoute un serveur"
        unreachable -> "Injoignable"
        state == null -> "En attente…"
        status == "ok" -> "Tout va bien"
        status == "warning" -> "À surveiller"
        else -> {
            val n = ov?.issues?.size ?: 0
            if (n <= 1) "1 problème" else "$n problèmes"
        }
    }
    val radius = if (Build.VERSION.SDK_INT >= 31) GlanceModifier.cornerRadius(android.R.dimen.system_app_widget_background_radius)
    else GlanceModifier.cornerRadius(24.dp)

    Box(
        GlanceModifier
            .fillMaxSize()
            .appWidgetBackground()
            .then(radius)
            .background(GlanceTheme.colors.widgetBackground)
            .clickable(actionStartActivity<MainActivity>())
            .padding(horizontal = 14.dp, vertical = 10.dp),
    ) {
        Column(GlanceModifier.fillMaxSize(), verticalAlignment = Alignment.CenterVertically) {
            Row(verticalAlignment = Alignment.CenterVertically, modifier = GlanceModifier.fillMaxWidth()) {
                Image(
                    ImageProvider(if (status == "ok" || status == null) R.drawable.ic_widget_ok else R.drawable.ic_widget_alert),
                    contentDescription = null,
                    colorFilter = androidx.glance.ColorFilter.tint(if (status == null) GlanceTheme.colors.primary else statusColor(status)),
                    modifier = GlanceModifier.size(if (size.height < 80.dp) 20.dp else 24.dp),
                )
                Spacer(GlanceModifier.width(8.dp))
                Column(GlanceModifier.defaultWeight()) {
                    Text(
                        headline,
                        maxLines = 1,
                        style = TextStyle(
                            color = if (status == null) GlanceTheme.colors.onSurface else statusColor(status),
                            fontSize = if (size.width >= 180.dp) 18.sp else 15.sp,
                            fontWeight = FontWeight.Bold,
                        ),
                    )
                    if (size.height >= 80.dp && serverName != null) {
                        Text(
                            serverName + if (state != null) " · " + Fmt.ago(state.updatedAt, now) else "",
                            maxLines = 1,
                            style = TextStyle(
                                color = if (stale) Warn else GlanceTheme.colors.onSurfaceVariant,
                                fontSize = 12.sp,
                            ),
                        )
                    }
                }
            }
            if (sys != null && size.height >= 80.dp) {
                Spacer(GlanceModifier.height(10.dp))
                val th = state?.thresholds ?: com.karelisio.vigie.data.Thresholds()
                Metric("CPU", sys.cpu, th.cpu)
                Spacer(GlanceModifier.height(6.dp))
                Metric("RAM", sys.mem, th.ram)
                if (size.height >= 160.dp) {
                    Spacer(GlanceModifier.height(6.dp))
                    Metric("Disque", sys.disk, th.disk)
                    val counts = ov?.containers
                    if (counts != null) {
                        Spacer(GlanceModifier.height(8.dp))
                        Text(
                            "${counts.running}/${counts.total} conteneurs actifs" +
                                if ((ov.checks.total) > 0) " · ${ov.checks.up}/${ov.checks.total} services OK" else "",
                            maxLines = 1,
                            style = TextStyle(color = GlanceTheme.colors.onSurfaceVariant, fontSize = 12.sp),
                        )
                    }
                    // Conteneurs épinglés (grand format).
                    val pinned = state?.containers.orEmpty().filter { it.name in favorites }.sortedBy { it.name }
                    if (size.height >= 240.dp && pinned.isNotEmpty()) {
                        Spacer(GlanceModifier.height(8.dp))
                        pinned.take(4).forEach { ct -> Pinned(ct) }
                    }
                }
            } else if (sys != null) {
                Text(
                    "CPU ${sys.cpu.roundToInt()} % · RAM ${sys.mem.roundToInt()} %",
                    maxLines = 1,
                    style = TextStyle(color = GlanceTheme.colors.onSurfaceVariant, fontSize = 12.sp),
                )
            }
        }
    }
}

@Composable
private fun Pinned(ct: com.karelisio.vigie.data.Container) {
    val color = when {
        ct.hasProblem -> Crit
        !ct.running -> GlanceTheme.colors.outline
        else -> Ok
    }
    Row(verticalAlignment = Alignment.CenterVertically, modifier = GlanceModifier.fillMaxWidth().padding(vertical = 2.dp)) {
        Box(GlanceModifier.size(8.dp).cornerRadius(4.dp).background(color)) {}
        Spacer(GlanceModifier.width(8.dp))
        Text(
            ct.name,
            maxLines = 1,
            style = TextStyle(color = GlanceTheme.colors.onSurface, fontSize = 13.sp, fontWeight = FontWeight.Medium),
            modifier = GlanceModifier.defaultWeight(),
        )
        Text(
            if (ct.running) "${ct.cpu.roundToInt()} % · ${Fmt.bytes(ct.memUsed)}" else "arrêté",
            maxLines = 1,
            style = TextStyle(color = GlanceTheme.colors.onSurfaceVariant, fontSize = 12.sp),
        )
    }
}

@Composable
private fun Metric(label: String, value: Double, threshold: Double) {
    val color = when {
        value >= threshold -> Crit
        value >= threshold - 15 -> Warn
        else -> GlanceTheme.colors.primary
    }
    Row(verticalAlignment = Alignment.CenterVertically, modifier = GlanceModifier.fillMaxWidth()) {
        Text(label, style = TextStyle(color = GlanceTheme.colors.onSurfaceVariant, fontSize = 12.sp), modifier = GlanceModifier.width(46.dp))
        LinearProgressIndicator(
            progress = (value / 100).toFloat().coerceIn(0f, 1f),
            color = color,
            backgroundColor = GlanceTheme.colors.surfaceVariant,
            modifier = GlanceModifier.defaultWeight().height(8.dp).cornerRadius(4.dp),
        )
        Text(
            "${value.roundToInt()} %",
            style = TextStyle(color = GlanceTheme.colors.onSurface, fontSize = 12.sp, fontWeight = FontWeight.Medium),
            modifier = GlanceModifier.width(44.dp).padding(start = 6.dp),
        )
    }
}

class VigieWidgetReceiver : GlanceAppWidgetReceiver() {
    override val glanceAppWidget: GlanceAppWidget = VigieWidget()

    override fun onEnabled(context: Context) {
        super.onEnabled(context)
        appContainer.surfaces.refreshNow()
    }
}

