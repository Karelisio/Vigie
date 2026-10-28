package com.karelisio.vigie.ui.containers

import androidx.compose.animation.animateContentSize
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.karelisio.vigie.data.Fmt
import com.karelisio.vigie.data.TimelineEvent
import com.karelisio.vigie.data.autoHealReason
import com.karelisio.vigie.ui.components.SectionCard
import com.karelisio.vigie.ui.theme.Vigie

/** Un épisode de la vie du conteneur : plusieurs événements Docker rapprochés, résumés en une phrase. */
data class TimelineGroup(val ts: Long, val title: String, val detail: String, val tone: String)

private const val WINDOW_MS = 45_000L

/**
 * Regroupe les événements (du plus récent au plus ancien) en épisodes lisibles :
 * « die → start » devient « Plantage puis relance automatique », etc.
 */
fun groupTimeline(events: List<TimelineEvent>): List<TimelineGroup> {
    val asc = events.sortedBy { it.ts }
    val clusters = mutableListOf<MutableList<TimelineEvent>>()
    for (e in asc) {
        val last = clusters.lastOrNull()
        // Les changements de santé restent des épisodes à part.
        val joinable = last != null && e.action != "health" && last.last().action != "health" &&
            e.ts - last.last().ts <= WINDOW_MS
        if (joinable) last.add(e) else clusters.add(mutableListOf(e))
    }
    return clusters.map(::summarize).reversed()
}

private fun summarize(c: List<TimelineEvent>): TimelineGroup {
    val actions = c.map { it.action }.toSet()
    val byVigie = c.firstOrNull { it.source == "vigie" }
    val fromVigie = if (byVigie != null) " depuis Vigie" else ""
    val exit = c.lastOrNull { it.action == "die" }?.detail?.toIntOrNull()
    val crashed = exit != null && exit != 0 && exit != 137 && exit != 143 && byVigie == null && "stop" !in actions
    val ts = c.first().ts
    val count = if (c.size > 1) "${c.size} événements" else ""
    fun g(title: String, tone: String, detail: String = count) = TimelineGroup(ts, title, detail, tone)

    if (byVigie != null && byVigie.detail != "ok" && byVigie.detail.isNotEmpty() && c.size == 1) {
        return g("Échec de « ${verb(byVigie.action)} »$fromVigie", "critical", byVigie.detail)
    }
    return when {
        // Réparation automatique par l'agent : elle explique les arrêts et démarrages qui l'entourent.
        "autoheal" in actions -> {
            val reason = c.first { it.action == "autoheal" }.detail
            g("Réparé automatiquement (${autoHealReason(reason)})", "warning", if (reason == "down" && exit != null) "code $exit" else count)
        }
        "oom" in actions -> g("Tué : mémoire saturée", "critical")
        "health" in actions -> when (c.first().detail) {
            "healthy" -> g("Healthcheck OK", "ok")
            "unhealthy" -> g("Healthcheck en échec", "critical")
            else -> g("Healthcheck : ${c.first().detail}", "warning")
        }
        "destroy" in actions && "create" in actions -> g("Recréé (mise à jour ou redéploiement)", "neutral")
        "destroy" in actions -> g("Supprimé", "neutral")
        "create" in actions && "start" in actions -> g("Créé et démarré", "ok")
        "restart" in actions || ("die" in actions && "start" in actions) ->
            if (crashed) g("Plantage (code $exit) puis relance automatique", "warning")
            else g("Redémarré$fromVigie", "ok")
        "die" in actions && crashed -> g("Planté (code $exit)", "critical")
        "stop" in actions || "die" in actions -> g("Arrêté$fromVigie", "neutral", if (exit != null && exit != 0) "code $exit" else count)
        "start" in actions -> g("Démarré$fromVigie", "ok")
        "pause" in actions -> g("Mis en pause", "warning")
        "unpause" in actions -> g("Repris", "ok")
        "create" in actions -> g("Créé", "neutral")
        else -> g(c.first().action, "neutral")
    }
}

private fun verb(action: String) = when (action) {
    "start" -> "démarrer"
    "stop" -> "arrêter"
    "restart" -> "redémarrer"
    else -> action
}

@Composable
fun TimelineCard(events: List<TimelineEvent>?, modifier: Modifier = Modifier) {
    val groups = remember(events) { events?.let(::groupTimeline) }
    var all by rememberSaveable { mutableStateOf(false) }
    SectionCard(
        modifier.animateContentSize(),
        title = "Chronologie",
        action = {
            if ((groups?.size ?: 0) > 6) TextButton(onClick = { all = !all }) { Text(if (all) "Moins" else "Tout afficher") }
        },
    ) {
        when {
            groups == null -> Text("Chargement…", color = MaterialTheme.colorScheme.onSurfaceVariant)
            groups.isEmpty() -> Text(
                "Aucun événement enregistré pour l'instant. Les démarrages, arrêts, plantages et healthchecks apparaîtront ici (30 jours).",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            else -> {
                val shown = if (all) groups else groups.take(6)
                shown.forEachIndexed { i, g -> TimelineRow(g, last = i == shown.lastIndex) }
            }
        }
    }
}

@Composable
private fun TimelineRow(g: TimelineGroup, last: Boolean) {
    val color = Vigie.status.of(g.tone)
    Row(Modifier.height(IntrinsicSize.Min)) {
        Column(Modifier.width(20.dp).fillMaxHeight(), horizontalAlignment = Alignment.CenterHorizontally) {
            Spacer(Modifier.height(5.dp))
            Box(Modifier.size(10.dp).background(color, CircleShape))
            if (!last) {
                Box(
                    Modifier
                        .padding(vertical = 3.dp)
                        .width(2.dp)
                        .weight(1f)
                        .background(MaterialTheme.colorScheme.outlineVariant),
                )
            }
        }
        Spacer(Modifier.width(10.dp))
        Column(Modifier.weight(1f).padding(bottom = if (last) 0.dp else 14.dp)) {
            Text(g.title, style = MaterialTheme.typography.bodyLarge)
            Text(
                Fmt.dateTime(g.ts) + if (g.detail.isNotEmpty()) " · ${g.detail}" else "",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}
