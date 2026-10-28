package com.karelisio.vigie.ui.home

import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.Animatable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowForward
import androidx.compose.material.icons.rounded.CheckCircle
import androidx.compose.material.icons.rounded.CloudOff
import androidx.compose.material.icons.rounded.EventBusy
import androidx.compose.material.icons.rounded.HourglassEmpty
import androidx.compose.material.icons.rounded.KeyOff
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.CircularWavyProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialShapes
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.toShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.karelisio.vigie.data.DebridAccount
import com.karelisio.vigie.data.DebridState
import com.karelisio.vigie.data.Fmt
import com.karelisio.vigie.ui.components.Springs
import com.karelisio.vigie.ui.components.SectionCard
import com.karelisio.vigie.ui.components.rememberNow
import com.karelisio.vigie.ui.theme.Vigie
import com.karelisio.vigie.ui.theme.numeric

private const val DAY = 86_400_000L

/** Urgence d'un compte : ok, warning (7 jours ou moins, vérification impossible), critical (1 jour, expiré, clé refusée). */
internal fun DebridAccount.tone(now: Long): String = when {
    refused || (ok && !premium) -> "critical"
    !ok -> if (checkedAt == 0L) "neutral" else "warning"
    expires == 0L -> "ok"
    expires - now <= DAY -> "critical"
    expires - now <= 7 * DAY -> "warning"
    else -> "ok"
}

/** État lisible d'un compte : échéance, expiration ou erreur. */
internal fun DebridAccount.summary(): String = when {
    refused -> error.replaceFirstChar { it.uppercase() }.ifEmpty { "Clé refusée" }
    !ok && checkedAt == 0L -> "Vérification en attente"
    !ok -> "Vérification impossible : $error"
    !premium && expires > 0 -> "Premium expiré le ${Fmt.day(expires)}"
    !premium -> "Pas de premium actif"
    expires == 0L -> "Premium actif"
    else -> "Premium jusqu'au ${Fmt.day(expires)}"
}

/** Comptes debrid : jours de premium restants (couleur selon l'urgence), échéance et erreur éventuelle. */
@Composable
fun DebridCard(state: DebridState, onOpen: () -> Unit, modifier: Modifier = Modifier) {
    val now = rememberNow(60_000)
    SectionCard(
        modifier,
        title = "Debrid",
        action = {
            TextButton(onClick = onOpen, shapes = ButtonDefaults.shapes()) {
                Text("Gérer")
                Spacer(Modifier.width(4.dp))
                Icon(Icons.AutoMirrored.Rounded.ArrowForward, null, Modifier.size(18.dp))
            }
        },
    ) {
        Column(verticalArrangement = Arrangement.spacedBy(14.dp)) {
            state.accounts.forEach { DebridRow(it, now) }
        }
    }
}

/** Ligne d'un compte : indicateur des jours restants, nom, échéance ; [trailing] : actions (gestion des comptes). */
@Composable
internal fun DebridRow(account: DebridAccount, now: Long, modifier: Modifier = Modifier, trailing: (@Composable () -> Unit)? = null) {
    val scheme = MaterialTheme.colorScheme
    val tone = account.tone(now)
    Row(modifier, verticalAlignment = Alignment.CenterVertically) {
        DaysIndicator(account, tone, now)
        Spacer(Modifier.width(14.dp))
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Text(account.title, style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
            Text(
                account.summary(),
                style = MaterialTheme.typography.bodyMedium,
                color = if (tone == "critical" || tone == "warning") Vigie.status.of(tone) else scheme.onSurface,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
            val details = listOfNotNull(
                account.username.takeIf { it.isNotEmpty() },
                account.checkedAt.takeIf { it > 0 }?.let { "vérifié ${Fmt.ago(it, now)}" },
            )
            if (details.isNotEmpty()) {
                Text(
                    details.joinToString(" · "),
                    style = MaterialTheme.typography.labelSmall,
                    color = scheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
        trailing?.invoke()
    }
}

/**
 * Jours restants au centre d'un anneau ondulé qui se remplit en ressort (un mois plein = tour complet) ;
 * sans échéance à afficher, une forme expressive porte l'icône de l'état.
 */
@Composable
private fun DaysIndicator(account: DebridAccount, tone: String, now: Long) {
    val status = Vigie.status
    val color by animateColorAsState(status.of(tone), label = "debrid-tone")
    val showDays = account.ok && account.premium && account.expires > 0
    val days = if (account.daysLeft <= 1) "jour" else "jours"
    val description = if (showDays) "${account.daysLeft} $days de premium restants" else account.summary()
    Box(Modifier.size(60.dp).clearAndSetSemantics { contentDescription = description }, contentAlignment = Alignment.Center) {
        if (showDays) {
            val reduced = Vigie.reducedMotion
            val target = ((account.expires - now).toFloat() / (30 * DAY)).coerceIn(0.02f, 1f)
            val progress = remember { Animatable(if (reduced) target else 0f) }
            LaunchedEffect(target) { if (reduced) progress.snapTo(target) else progress.animateTo(target, Springs.bouncy()) }
            val track = status.containerOf(tone)
            if (reduced) {
                CircularProgressIndicator(progress = { progress.value }, color = color, trackColor = track, modifier = Modifier.fillMaxSize())
            } else {
                CircularWavyProgressIndicator(progress = { progress.value }, color = color, trackColor = track, modifier = Modifier.fillMaxSize())
            }
            Column(horizontalAlignment = Alignment.CenterHorizontally) {
                Text("${account.daysLeft}", style = MaterialTheme.typography.titleLarge.numeric(), color = color)
                Text(days, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        } else {
            val (shape, icon) = when {
                account.refused -> MaterialShapes.Burst to Icons.Rounded.KeyOff
                account.ok && !account.premium -> MaterialShapes.Burst to Icons.Rounded.EventBusy
                account.ok -> MaterialShapes.Cookie9Sided to Icons.Rounded.CheckCircle
                account.checkedAt == 0L -> MaterialShapes.Cookie6Sided to Icons.Rounded.HourglassEmpty
                else -> MaterialShapes.SoftBurst to Icons.Rounded.CloudOff
            }
            Surface(
                shape = shape.toShape(),
                color = status.containerOf(tone),
                contentColor = status.onContainerOf(tone),
                modifier = Modifier.size(52.dp),
            ) {
                Icon(icon, null, Modifier.padding(14.dp))
            }
        }
    }
}
