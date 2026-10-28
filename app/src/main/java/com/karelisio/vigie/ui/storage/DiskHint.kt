package com.karelisio.vigie.ui.storage

import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowForward
import androidx.compose.material.icons.automirrored.rounded.TrendingUp
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.karelisio.vigie.appContainer
import com.karelisio.vigie.data.StorageFmt
import com.karelisio.vigie.data.storage
import com.karelisio.vigie.data.storageState
import com.karelisio.vigie.data.versionAtLeast
import com.karelisio.vigie.ui.theme.Vigie

/** Mention de l'accueil : « Disque plein dans ~N jours » et les jours restants (couleur). */
data class DiskForecastHint(val text: String, val daysLeft: Double)

/**
 * Mention « disque plein dans ~N jours » de l'accueil (agent 0.6.0 et plus), quand la prévision passe
 * sous 60 jours ; null sinon. La prévision est rechargée à chaque retour sur l'accueil.
 */
@Composable
fun rememberDiskHint(agentVersion: String, serverId: String?): DiskForecastHint? {
    val repo = appContainer.repository
    val supported = versionAtLeast(agentVersion, 0, 6)
    val report by remember(repo) { repo.storageState() }.collectAsStateWithLifecycle(null)
    LaunchedEffect(supported, serverId) { if (supported) runCatching { repo.storage() } }
    val disk = report?.disk?.takeIf { supported } ?: return null
    val text = StorageFmt.homeHint(disk) ?: return null
    return DiskForecastHint(text, disk.daysLeft ?: 0.0)
}

/** Ligne cliquable sous la jauge du disque : ouvre l'écran Stockage. */
@Composable
fun DiskHint(hint: DiskForecastHint, onClick: () -> Unit, modifier: Modifier = Modifier) {
    val (text, days) = hint
    val color = when {
        days < 7 -> Vigie.status.critical
        days < 30 -> Vigie.status.warn
        else -> MaterialTheme.colorScheme.tertiary
    }
    Surface(
        onClick = onClick,
        shape = RoundedCornerShape(16.dp),
        color = color.copy(alpha = 0.12f),
        contentColor = color,
        modifier = modifier.fillMaxWidth(),
    ) {
        Row(Modifier.padding(horizontal = 12.dp, vertical = 8.dp), verticalAlignment = Alignment.CenterVertically) {
            Icon(Icons.AutoMirrored.Rounded.TrendingUp, null, Modifier.size(18.dp))
            Spacer(Modifier.width(8.dp))
            Text(text, style = MaterialTheme.typography.labelLarge, modifier = Modifier.weight(1f))
            Icon(Icons.AutoMirrored.Rounded.ArrowForward, "Voir le stockage", Modifier.size(18.dp))
        }
    }
}
