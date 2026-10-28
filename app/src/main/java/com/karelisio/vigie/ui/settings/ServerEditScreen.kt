package com.karelisio.vigie.ui.settings

import android.Manifest
import android.content.pm.PackageManager
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
import androidx.compose.material.icons.rounded.Delete
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavHostController
import com.karelisio.vigie.appContainer
import com.karelisio.vigie.ui.navigation.ScanRoute
import com.karelisio.vigie.ui.navigation.ServerEditRoute

const val SCAN_RESULT = "scan_result"

/** Ouvre le scanner après avoir demandé la caméra si nécessaire. */
@Composable
fun rememberScanLauncher(nav: NavHostController): () -> Unit {
    val context = LocalContext.current
    val launcher = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { ok ->
        if (ok) nav.navigate(ScanRoute)
    }
    return {
        if (ContextCompat.checkSelfPermission(context, Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED) nav.navigate(ScanRoute)
        else launcher.launch(Manifest.permission.CAMERA)
    }
}

@Composable
fun ServerEditScreen(nav: NavHostController, route: ServerEditRoute) {
    val existing = route.id?.let { appContainer.servers.get(it) }
    val scanned by (nav.currentBackStackEntry?.savedStateHandle?.getStateFlow<String?>(SCAN_RESULT, null)
        ?: kotlinx.coroutines.flow.MutableStateFlow(null)).collectAsStateWithLifecycle()
    val scan = rememberScanLauncher(nav)
    var confirmDelete by remember { mutableStateOf(false) }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(if (existing == null) "Ajouter un serveur" else existing.name) },
                navigationIcon = { IconButton(onClick = { nav.popBackStack() }) { Icon(Icons.AutoMirrored.Rounded.ArrowBack, "Retour") } },
                actions = {
                    if (existing != null) IconButton(onClick = { confirmDelete = true }) { Icon(Icons.Rounded.Delete, "Supprimer") }
                },
            )
        },
    ) { padding ->
        ServerForm(
            initial = existing,
            link = scanned ?: route.link,
            onScan = scan,
            onSaved = { nav.popBackStack() },
            modifier = Modifier
                .fillMaxSize()
                .padding(padding)
                .imePadding()
                .verticalScroll(rememberScrollState())
                .padding(16.dp),
        )
    }
    if (confirmDelete && existing != null) {
        AlertDialog(
            onDismissRequest = { confirmDelete = false },
            title = { Text("Supprimer ${existing.name} ?") },
            text = { Text("Le token et le cache de ce serveur seront effacés du téléphone. L'agent continue de tourner.") },
            confirmButton = {
                TextButton(onClick = {
                    confirmDelete = false
                    appContainer.repository.forget(existing)
                    nav.popBackStack()
                }) { Text("Supprimer") }
            },
            dismissButton = { TextButton(onClick = { confirmDelete = false }) { Text("Annuler") } },
        )
    }
}
