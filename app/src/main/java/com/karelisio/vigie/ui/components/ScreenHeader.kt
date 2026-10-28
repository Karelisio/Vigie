package com.karelisio.vigie.ui.components

import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.navigation.NavHostController

/** En-tête d'un écran secondaire : retour et titre (premier élément d'une LazyColumn). */
@Composable
fun ScreenHeader(title: String, nav: NavHostController, modifier: Modifier = Modifier, actions: @Composable () -> Unit = {}) {
    Row(modifier.statusBarsPadding().padding(top = 4.dp), verticalAlignment = Alignment.CenterVertically) {
        IconButton(onClick = { nav.popBackStack() }) { Icon(Icons.AutoMirrored.Rounded.ArrowBack, "Retour") }
        Text(title, style = MaterialTheme.typography.headlineSmallEmphasized, modifier = Modifier.weight(1f))
        actions()
    }
}

/** Écran provisoire, remplacé par la fonctionnalité correspondante. */
@Composable
internal fun PlaceholderScreen(title: String, nav: NavHostController) {
    androidx.compose.foundation.lazy.LazyColumn(
        Modifier.padding(horizontal = 16.dp),
    ) {
        item { ScreenHeader(title, nav) }
        item { Text("Bientôt disponible.", Modifier.padding(16.dp), color = MaterialTheme.colorScheme.onSurfaceVariant) }
    }
}
