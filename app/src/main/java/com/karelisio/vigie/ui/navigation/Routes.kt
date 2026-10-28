package com.karelisio.vigie.ui.navigation

import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Inventory2
import androidx.compose.material.icons.outlined.MonitorHeart
import androidx.compose.material.icons.outlined.Radar
import androidx.compose.material.icons.outlined.Settings
import androidx.compose.material.icons.rounded.Inventory2
import androidx.compose.material.icons.rounded.MonitorHeart
import androidx.compose.material.icons.rounded.Radar
import androidx.compose.material.icons.rounded.Settings
import androidx.compose.ui.graphics.vector.ImageVector
import kotlinx.serialization.Serializable

@Serializable data object HomeRoute
@Serializable data object ContainersRoute
@Serializable data object ChecksRoute
@Serializable data object SettingsRoute
@Serializable data class ContainerRoute(val id: String, val name: String)
@Serializable data class LogsRoute(val id: String, val name: String)
@Serializable data class StackLogsRoute(val project: String)
@Serializable data class ServerEditRoute(val id: String? = null, val link: String? = null)
@Serializable data object ScanRoute
@Serializable data object PermissionsRoute
@Serializable data object AlertsRoute
@Serializable data object AlertSettingsRoute
@Serializable data object CleanupRoute
@Serializable data object SharedAccessRoute
@Serializable data object AutoUpdateRoute
@Serializable data object AutoHealRoute
@Serializable data object AuditRoute
@Serializable data object AboutRoute
@Serializable data object ReportRoute
@Serializable data object OnboardingRoute

data class TopLevel(val route: Any, val label: String, val icon: ImageVector, val selectedIcon: ImageVector)

val TopLevels = listOf(
    TopLevel(HomeRoute, "Accueil", Icons.Outlined.Radar, Icons.Rounded.Radar),
    TopLevel(ContainersRoute, "Conteneurs", Icons.Outlined.Inventory2, Icons.Rounded.Inventory2),
    TopLevel(ChecksRoute, "Services", Icons.Outlined.MonitorHeart, Icons.Rounded.MonitorHeart),
    TopLevel(SettingsRoute, "Réglages", Icons.Outlined.Settings, Icons.Rounded.Settings),
)
