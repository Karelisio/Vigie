package com.karelisio.vigie.ui.security

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.animateContentSize
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.selection.toggleable
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.OpenInNew
import androidx.compose.material.icons.automirrored.rounded.Undo
import androidx.compose.material.icons.rounded.AutoFixHigh
import androidx.compose.material.icons.rounded.Block
import androidx.compose.material.icons.rounded.Inventory2
import androidx.compose.material.icons.rounded.NightsStay
import androidx.compose.material.icons.rounded.Search
import androidx.compose.material.icons.rounded.SystemUpdate
import androidx.compose.material.icons.rounded.Verified
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Checkbox
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.SegmentedButton
import androidx.compose.material3.SegmentedButtonDefaults
import androidx.compose.material3.SingleChoiceSegmentedButtonRow
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.karelisio.vigie.data.AgentJob
import com.karelisio.vigie.data.AgentSettings
import com.karelisio.vigie.data.Container
import com.karelisio.vigie.data.FixLevels
import com.karelisio.vigie.data.FixPlan
import com.karelisio.vigie.data.FixPlans
import com.karelisio.vigie.data.Fmt
import com.karelisio.vigie.data.NightlyFix
import com.karelisio.vigie.data.VulnCounts
import com.karelisio.vigie.data.summary
import com.karelisio.vigie.ui.components.Chip
import com.karelisio.vigie.ui.components.JobProgress
import com.karelisio.vigie.ui.components.SectionCard
import com.karelisio.vigie.ui.components.StatusDot
import com.karelisio.vigie.ui.components.rememberHaptics
import com.karelisio.vigie.ui.containers.RollbackController
import com.karelisio.vigie.ui.containers.RollbackDialog
import com.karelisio.vigie.ui.settings.ToggleRow
import com.karelisio.vigie.ui.theme.Vigie
import com.karelisio.vigie.ui.theme.numeric

// ---- Textes (sans Android, testés) ----

private fun plural(n: Int, one: String, many: String = one + "s") = if (n == 1) "1 $one" else "$n $many"

/** « 7 failles (dont 2 critiques) ». */
internal fun countsText(c: VulnCounts): String = buildString {
    append(plural(c.total, "faille"))
    when {
        c.critical > 0 -> append(" (dont ${plural(c.critical, "critique")})")
        c.high > 0 -> append(" (dont ${plural(c.high, "élevée")})")
    }
}

/** Ce que la correction va faire, expliqué avant confirmation. */
internal fun fixExplanation(plan: FixPlan, route: String): String {
    val who = plan.containers.map { it.name }.ifEmpty { listOf(plan.target) }
    val ctrs = if (who.size == 1) who[0] else who.dropLast(1).joinToString(", ") + " et " + who.last()
    val recreated = if (who.size == 1) "$ctrs sera recréé" else "$ctrs seront recréés"
    return if (route == "update") {
        buildString {
            append("Vigie télécharge la nouvelle version de l'image et l'analyse avant de l'installer. ")
            append("$recreated à l'identique (réglages, réseaux, volumes), avec quelques secondes de coupure, ")
            append("et tu pourras revenir à la version précédente.")
            if (plan.update.candidate == null) {
                append(" Si elle ne corrige aucune faille, elle n'est pas installée")
                append(if (plan.patch.possible) " et Vigie corrige les paquets système à la place." else ".")
            }
        }
    } else {
        val names = plan.patch.packages.take(4).joinToString(", ") { it.name } + if (plan.patch.packages.size > 4) "…" else ""
        buildString {
            append("Vigie met à niveau ${plural(plan.patch.packages.size, "paquet système", "paquets système")} ($names) ")
            append("dans une image dérivée de l'image actuelle, sans rien changer d'autre. ")
            append("$recreated à l'identique, avec quelques secondes de coupure ; s'il ne redémarre pas, l'ancien revient tout seul. ")
            append("Tu pourras revenir à l'image d'origine à tout moment.")
        }
    }
}

/** Confirmation de « Corriger tout ce qui peut l'être ». */
internal fun fixAllExplanation(p: FixPlans): String = buildString {
    append("Vigie corrige l'une après l'autre ${plural(p.totals.fixable, "image", "images")}")
    val parts = buildList {
        if (p.totals.patch > 0) add("${p.totals.patch} en mettant à niveau leurs paquets système")
        if (p.totals.update > 0) add("${p.totals.update} en installant leur nouvelle version")
    }
    if (parts.isNotEmpty()) append(" (" + parts.joinToString(", ") + ")")
    append(". Chaque conteneur concerné est recréé à l'identique, avec quelques secondes de coupure, et peut revenir en arrière ensuite.")
}

/** Heure approximative de la correction de nuit : une heure après les mises à jour automatiques. */
internal fun nightlyClock(autoUpdateTime: String): String {
    val parts = autoUpdateTime.split(':').mapNotNull { it.toIntOrNull() }
    if (parts.size != 2) return "05:00"
    return ((parts[0] + 1) % 24).toString().padStart(2, '0') + ":" + parts[1].toString().padStart(2, '0')
}

// ---- Section « Correction » d'une image ----

@OptIn(ExperimentalLayoutApi::class)
@Composable
internal fun FixSection(
    plan: FixPlan,
    job: AgentJob?,
    busy: Boolean,
    onFix: (FixPlan, String) -> Unit,
    onAnalyze: (FixPlan) -> Unit,
    onLink: (String) -> Unit,
) {
    val scheme = MaterialTheme.colorScheme
    val status = Vigie.status
    val haptics = rememberHaptics()
    Surface(shape = RoundedCornerShape(20.dp), color = scheme.surfaceContainerHigh, modifier = Modifier.fillMaxWidth()) {
        Column(Modifier.padding(14.dp).animateContentSize()) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                val (icon, tint) = when (plan.route) {
                    "update" -> Icons.Rounded.SystemUpdate to scheme.primary
                    "patch" -> Icons.Rounded.AutoFixHigh to status.ok
                    else -> Icons.Rounded.Block to scheme.outline
                }
                Icon(icon, null, Modifier.size(20.dp), tint = tint)
                Spacer(Modifier.width(10.dp))
                Text(plan.title, style = MaterialTheme.typography.titleSmall)
            }
            Spacer(Modifier.height(6.dp))
            Text(plan.explain, style = MaterialTheme.typography.bodySmall, color = scheme.onSurfaceVariant)
            if (plan.route == "patch" && plan.patch.packages.isNotEmpty()) {
                Spacer(Modifier.height(8.dp))
                plan.patch.packages.take(4).forEach { pk ->
                    Text(
                        "${pk.name} ${pk.version} → ${pk.fixed}",
                        style = MaterialTheme.typography.bodySmall.numeric(),
                        color = status.ok,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
                if (plan.patch.packages.size > 4) {
                    Text("et ${plan.patch.packages.size - 4} autres", style = MaterialTheme.typography.bodySmall, color = scheme.onSurfaceVariant)
                }
            }
            plan.update.candidate?.takeIf { plan.update.available }?.let { c ->
                Spacer(Modifier.height(8.dp))
                FlowRow(horizontalArrangement = Arrangement.spacedBy(6.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                    if (c.version.isNotEmpty()) Chip("Version ${c.version}", scheme.secondaryContainer, scheme.onSecondaryContainer)
                    Chip("Corrige ${countsText(c.fixed)}", status.okContainer, status.onOkContainer)
                    if (c.added.total > 0) Chip("En ajoute ${c.added.total}", status.warnContainer, status.onWarnContainer)
                }
                Text(
                    "Analysée ${Fmt.ago(c.analyzedAt)}",
                    style = MaterialTheme.typography.bodySmall,
                    color = scheme.onSurfaceVariant,
                    modifier = Modifier.padding(top = 4.dp),
                )
            }
            plan.last?.let { o ->
                Spacer(Modifier.height(8.dp))
                Text(
                    "Dernière correction ${Fmt.ago(o.at)} : ${o.message}",
                    style = MaterialTheme.typography.bodySmall,
                    color = scheme.onSurfaceVariant,
                )
            }
            if (plan.declined) {
                Spacer(Modifier.height(6.dp))
                Text(
                    "Revenu à l'image d'origine après une correction : la nuit ne la refait pas.",
                    style = MaterialTheme.typography.bodySmall,
                    color = scheme.onSurfaceVariant,
                )
            }
            if (job != null && !job.done) {
                Spacer(Modifier.height(10.dp))
                JobProgress(job, showResults = false)
            }
            when {
                plan.canFix -> {
                    Spacer(Modifier.height(12.dp))
                    Button(
                        onClick = { haptics.tick(); onFix(plan, plan.route) },
                        enabled = !busy,
                        shapes = ButtonDefaults.shapes(),
                        modifier = Modifier.fillMaxWidth(),
                    ) {
                        Icon(if (plan.route == "update") Icons.Rounded.SystemUpdate else Icons.Rounded.AutoFixHigh, null)
                        Spacer(Modifier.width(8.dp))
                        Text(if (plan.route == "update") "Installer la nouvelle version" else "Corriger")
                    }
                    if (plan.needsAnalysis) {
                        Spacer(Modifier.height(4.dp))
                        OutlinedButton(
                            onClick = { haptics.tick(); onAnalyze(plan) },
                            enabled = !busy,
                            shapes = ButtonDefaults.shapes(),
                            modifier = Modifier.fillMaxWidth(),
                        ) {
                            Icon(Icons.Rounded.Search, null)
                            Spacer(Modifier.width(8.dp))
                            Text("Analyser la nouvelle version d'abord")
                        }
                    }
                }
                plan.app.fixable > 0 && plan.reportUrl.isNotEmpty() -> {
                    Spacer(Modifier.height(8.dp))
                    OutlinedButton(
                        onClick = { onLink(plan.reportUrl) },
                        shapes = ButtonDefaults.shapes(),
                        modifier = Modifier.fillMaxWidth(),
                    ) {
                        Icon(Icons.AutoMirrored.Rounded.OpenInNew, null)
                        Spacer(Modifier.width(8.dp))
                        Text("Signaler au projet")
                    }
                }
            }
        }
    }
}

/** Badge « Corrigée par Vigie le … » et retour à l'image d'origine (point de retour du conteneur). */
@Composable
internal fun PatchedBadge(plan: FixPlan, busy: Boolean) {
    val patched = plan.patched ?: return
    val status = Vigie.status
    Row(verticalAlignment = Alignment.CenterVertically) {
        Icon(Icons.Rounded.Verified, null, Modifier.size(18.dp), tint = status.ok)
        Spacer(Modifier.width(8.dp))
        Text(patched.summary(Fmt::day), style = MaterialTheme.typography.bodySmall, color = status.ok)
    }
    OriginRollback(plan.target, patched.at, busy)
}

@Composable
private fun OriginRollback(container: String, patchedAt: Long, busy: Boolean) {
    if (container.isEmpty()) return
    val rb = remember(container) { RollbackController(container) }
    LaunchedEffect(rb, patchedAt) { rb.load() }
    val p = rb.point
    val haptics = rememberHaptics()
    if (p != null && p.vulnfix && p.ready) {
        TextButton(onClick = { haptics.tick(); rb.confirm = true }, enabled = !busy && p.reachable) {
            Icon(Icons.AutoMirrored.Rounded.Undo, null, Modifier.size(18.dp))
            Spacer(Modifier.width(6.dp))
            Text("Revenir à l'image d'origine")
        }
    }
    RollbackDialog(rb)
}

/** Confirmation d'une correction (voie expliquée). */
@Composable
internal fun FixDialog(plan: FixPlan, route: String, onConfirm: () -> Unit, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        icon = { Icon(if (route == "update") Icons.Rounded.SystemUpdate else Icons.Rounded.AutoFixHigh, null) },
        title = {
            Text(if (route == "update") "Installer la nouvelle version de ${plan.target} ?" else "Corriger les failles de ${plan.target} ?")
        },
        text = { Text(fixExplanation(plan, route)) },
        confirmButton = { TextButton(onClick = onConfirm) { Text(if (route == "update") "Installer" else "Corriger") } },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Annuler") } },
    )
}

// ---- Tout corriger ----

@Composable
internal fun FixAllCard(plans: FixPlans, busy: Boolean, onFixAll: () -> Unit, modifier: Modifier = Modifier) {
    val scheme = MaterialTheme.colorScheme
    val haptics = rememberHaptics()
    SectionCard(modifier, title = "Correction") {
        when {
            !plans.enabled -> Text(
                "Les corrections sont désactivées sur cet agent (VIGIE_ALLOW_UPDATES=false).",
                style = MaterialTheme.typography.bodyMedium,
                color = scheme.onSurfaceVariant,
            )
            plans.totals.fixable == 0 -> Text(
                "Rien à corriger pour l'instant : les failles restantes n'ont pas de correctif utilisable par Vigie.",
                style = MaterialTheme.typography.bodyMedium,
                color = scheme.onSurfaceVariant,
            )
            else -> {
                Text(
                    buildString {
                        append(if (plans.totals.fixable == 1) "1 image corrigeable" else "${plans.totals.fixable} images corrigeables")
                        val parts = buildList {
                            if (plans.totals.patch > 0) add("${plans.totals.patch} par Vigie")
                            if (plans.totals.update > 0) add("${plans.totals.update} par une nouvelle version")
                        }
                        append(" : " + parts.joinToString(", "))
                    },
                    style = MaterialTheme.typography.titleSmall,
                )
                Text(
                    "Paquets système mis à niveau dans une image dérivée, ou nouvelle version analysée avant d'être installée. Retour arrière possible.",
                    style = MaterialTheme.typography.bodySmall,
                    color = scheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(12.dp))
                Button(
                    onClick = { haptics.tick(); onFixAll() },
                    enabled = !busy,
                    shapes = ButtonDefaults.shapes(),
                    modifier = Modifier.fillMaxWidth().height(52.dp),
                ) {
                    Icon(Icons.Rounded.AutoFixHigh, null)
                    Spacer(Modifier.width(8.dp))
                    Text("Corriger tout ce qui peut l'être")
                }
            }
        }
    }
}

@Composable
internal fun FixAllDialog(plans: FixPlans, onConfirm: () -> Unit, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        icon = { Icon(Icons.Rounded.AutoFixHigh, null) },
        title = { Text("Corriger tout ce qui peut l'être ?") },
        text = { Text(fixAllExplanation(plans)) },
        confirmButton = { TextButton(onClick = onConfirm) { Text("Corriger") } },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Annuler") } },
    )
}

// ---- Correction automatique de nuit ----

@Composable
internal fun NightFixCard(
    s: AgentSettings,
    nightly: NightlyFix?,
    containers: List<Container>,
    onChange: (AgentSettings) -> Unit,
    modifier: Modifier = Modifier,
) {
    val scheme = MaterialTheme.colorScheme
    val haptics = rememberHaptics()
    SectionCard(modifier, title = "Correction automatique") {
        ToggleRow(
            Icons.Rounded.NightsStay, "Corriger la nuit",
            if (s.vulnScan) "Vers ${nightlyClock(s.autoUpdateTime)}, après l'analyse : paquets système des conteneurs choisis. Bilan au réveil."
            else "Active d'abord l'analyse quotidienne.",
            s.vulnFixAuto, enabled = s.vulnScan,
        ) { on -> haptics.toggle(on); onChange(s.copy(vulnFixAuto = on)) }
        AnimatedVisibility(s.vulnFixAuto) {
            Column(Modifier.padding(top = 8.dp)) {
                Text("Gravité minimale", style = MaterialTheme.typography.titleSmall)
                Spacer(Modifier.height(8.dp))
                SingleChoiceSegmentedButtonRow(Modifier.fillMaxWidth()) {
                    FixLevels.forEachIndexed { i, (key, label) ->
                        SegmentedButton(
                            selected = s.vulnFixLevel == key,
                            onClick = { if (s.vulnFixLevel != key) { haptics.tick(); onChange(s.copy(vulnFixLevel = key)) } },
                            shape = SegmentedButtonDefaults.itemShape(i, FixLevels.size),
                        ) { Text(label, maxLines = 1) }
                    }
                }
                Spacer(Modifier.height(12.dp))
                ToggleRow(
                    Icons.Rounded.Inventory2, "Tous les conteneurs", "Y compris ceux créés plus tard",
                    s.vulnFixAll,
                ) { on -> haptics.toggle(on); onChange(s.copy(vulnFixAll = on)) }
                AnimatedVisibility(!s.vulnFixAll) {
                    Column(Modifier.padding(top = 4.dp)) {
                        val current = containers.filter { !it.self }.associateBy { it.name }
                        val names = (current.keys + s.vulnFixList).sorted()
                        names.forEach { name ->
                            val checked = name in s.vulnFixList
                            Row(
                                Modifier
                                    .fillMaxWidth()
                                    .clip(RoundedCornerShape(16.dp))
                                    .toggleable(checked, role = Role.Checkbox) { on ->
                                        haptics.toggle(on)
                                        onChange(s.copy(vulnFixList = (if (on) s.vulnFixList + name else s.vulnFixList - name).sorted()))
                                    }
                                    .padding(vertical = 2.dp),
                                verticalAlignment = Alignment.CenterVertically,
                            ) {
                                Checkbox(checked = checked, onCheckedChange = null, modifier = Modifier.padding(12.dp))
                                Text(name, style = MaterialTheme.typography.bodyLarge, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
                                val ctr = current[name]
                                if (ctr == null) {
                                    Text("absent", style = MaterialTheme.typography.labelMedium, color = scheme.outline, modifier = Modifier.padding(end = 8.dp))
                                } else {
                                    StatusDot(if (ctr.state == "running") "ok" else "neutral", Modifier.padding(end = 8.dp), breathe = false)
                                }
                            }
                        }
                    }
                }
                Spacer(Modifier.height(8.dp))
                Text(
                    "Jamais l'agent, ni un conteneur que tu as arrêté ou ignoré, ni pendant la maintenance. " +
                        "Les nouvelles versions d'image restent l'affaire des mises à jour automatiques.",
                    style = MaterialTheme.typography.bodySmall,
                    color = scheme.onSurfaceVariant,
                )
                nightly?.takeIf { it.lastRun > 0 }?.let { n ->
                    Text(
                        "Dernière passe : ${Fmt.dateTime(n.lastRun)}" + if (n.message.isNotEmpty()) " · ${n.message}" else "",
                        style = MaterialTheme.typography.bodySmall,
                        color = scheme.onSurfaceVariant,
                        modifier = Modifier.padding(top = 4.dp),
                    )
                }
            }
        }
    }
}
