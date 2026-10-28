package com.karelisio.vigie.ui.settings

import android.Manifest
import android.annotation.SuppressLint
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.PowerManager
import android.provider.Settings
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.BatteryChargingFull
import androidx.compose.material.icons.rounded.InstallMobile
import androidx.compose.material.icons.rounded.NotificationsActive
import androidx.compose.material.icons.rounded.QrCodeScanner
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat
import androidx.lifecycle.compose.LifecycleResumeEffect

enum class Perm(val title: String, val why: String, val icon: ImageVector) {
    Notifications(
        "Notifications",
        "Pour être prévenu immédiatement quand un conteneur tombe, redémarre en boucle, ou qu'un service ne répond plus.",
        Icons.Rounded.NotificationsActive,
    ),
    Camera(
        "Caméra",
        "Uniquement pour scanner le QR code affiché par l'agent dans les logs de Dockhand. Aucune image n'est conservée.",
        Icons.Rounded.QrCodeScanner,
    ),
    Battery(
        "Activité en arrière-plan",
        "Pour que les alertes arrivent sans délai, même quand le téléphone est en veille depuis longtemps.",
        Icons.Rounded.BatteryChargingFull,
    ),
    Install(
        "Installation des mises à jour",
        "Pour installer les nouvelles versions de Vigie depuis GitHub, directement dans l'appli.",
        Icons.Rounded.InstallMobile,
    ),
}

fun Perm.granted(context: Context): Boolean = when (this) {
    Perm.Notifications ->
        if (Build.VERSION.SDK_INT >= 33) ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED
        else NotificationManagerCompat.from(context).areNotificationsEnabled()
    Perm.Camera -> ContextCompat.checkSelfPermission(context, Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED
    Perm.Battery -> context.getSystemService(PowerManager::class.java).isIgnoringBatteryOptimizations(context.packageName)
    Perm.Install -> context.packageManager.canRequestPackageInstalls()
}

/** État des autorisations, relu à chaque retour au premier plan. */
class PermissionState(val granted: Map<Perm, Boolean>, val request: (Perm) -> Unit)

@SuppressLint("BatteryLife")
@Composable
fun rememberPermissionState(): PermissionState {
    val context = LocalContext.current
    var tick by remember { mutableIntStateOf(0) }
    LifecycleResumeEffect(Unit) {
        tick++
        onPauseOrDispose { }
    }
    val runtime = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { tick++ }
    val settings = rememberLauncherForActivityResult(ActivityResultContracts.StartActivityForResult()) { tick++ }
    val granted = remember(tick) { Perm.entries.associateWith { it.granted(context) } }
    return PermissionState(granted) { p ->
        val pkg = Uri.parse("package:${context.packageName}")
        when (p) {
            Perm.Notifications ->
                if (Build.VERSION.SDK_INT >= 33) runtime.launch(Manifest.permission.POST_NOTIFICATIONS)
                else settings.launch(Intent(Settings.ACTION_APP_NOTIFICATION_SETTINGS).putExtra(Settings.EXTRA_APP_PACKAGE, context.packageName))
            Perm.Camera -> runtime.launch(Manifest.permission.CAMERA)
            Perm.Battery -> runCatching {
                settings.launch(Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS, pkg))
            }.onFailure { settings.launch(Intent(Settings.ACTION_IGNORE_BATTERY_OPTIMIZATION_SETTINGS)) }
            Perm.Install -> settings.launch(Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, pkg))
        }
    }
}
