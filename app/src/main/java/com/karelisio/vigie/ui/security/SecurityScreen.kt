package com.karelisio.vigie.ui.security

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.asPaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.navigationBars
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.BugReport
import androidx.compose.material.icons.rounded.Lan
import androidx.compose.material.icons.rounded.Terminal
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavHostController
import com.karelisio.vigie.appContainer
import com.karelisio.vigie.data.vulnReport
import com.karelisio.vigie.data.vulns
import com.karelisio.vigie.ui.components.ScreenHeader
import com.karelisio.vigie.ui.navigation.PortsRoute
import com.karelisio.vigie.ui.navigation.SshRoute
import com.karelisio.vigie.ui.navigation.VulnsRoute
import com.karelisio.vigie.ui.settings.Row_

/** Sécurité du serveur : failles des images, ports ouverts, connexions SSH (agent 0.6.0). */
@Composable
fun SecurityScreen(nav: NavHostController) {
    val bottom = WindowInsets.navigationBars.asPaddingValues().calculateBottomPadding() + 32.dp
    LazyColumn(
        Modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = 16.dp, end = 16.dp, bottom = bottom),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        item { ScreenHeader("Sécurité", nav) }
        item {
            Text(
                "Ce qui pourrait exposer ton serveur : failles connues dans tes images, services joignables depuis Internet " +
                    "et tentatives de connexion SSH.",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(horizontal = 4.dp),
            )
        }
        item {
            // Résumé de la dernière analyse des failles, chargé à l'ouverture.
            val vulns by remember { appContainer.repository.vulnReport() }.collectAsStateWithLifecycle(null)
            LaunchedEffect(Unit) { runCatching { appContainer.repository.vulns() } }
            Row_(Icons.Rounded.BugReport, "Failles connues", vulns?.summary() ?: "Paquets vulnérables dans les images, version corrigée") {
                nav.navigate(VulnsRoute)
            }
        }
        item {
            Row_(Icons.Rounded.Lan, "Ports ouverts", "Ce qui écoute sur Internet, et ce qui contourne Pangolin") {
                nav.navigate(PortsRoute)
            }
        }
        item {
            Row_(Icons.Rounded.Terminal, "Connexions SSH", "Tentatives, adresses, pays, connexions réussies") {
                nav.navigate(SshRoute)
            }
        }
    }
}
