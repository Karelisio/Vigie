package com.karelisio.vigie.ui.settings

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.graphics.BitmapFactory
import android.util.Base64
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
import androidx.compose.material.icons.rounded.Add
import androidx.compose.material.icons.rounded.ContentCopy
import androidx.compose.material.icons.rounded.Delete
import androidx.compose.material.icons.rounded.Share
import androidx.compose.material.icons.rounded.Visibility
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LoadingIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.FilterQuality
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.NavHostController
import com.karelisio.vigie.appContainer
import com.karelisio.vigie.data.Fmt
import com.karelisio.vigie.data.SharedAccess
import com.karelisio.vigie.data.SharedAccessCreated
import com.karelisio.vigie.security.LocalActionGuard
import com.karelisio.vigie.ui.components.EmptyState
import com.karelisio.vigie.ui.components.cascade
import com.karelisio.vigie.ui.components.rememberHaptics
import com.karelisio.vigie.ui.navigation.LocalSnackbar
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

class SharedAccessViewModel : ViewModel() {
    private val repo = appContainer.repository
    private val _list = MutableStateFlow<List<SharedAccess>?>(null)
    val list = _list.asStateFlow()
    private val _error = MutableStateFlow<String?>(null)
    val error = _error.asStateFlow()

    init {
        load()
    }

    fun load() {
        viewModelScope.launch {
            runCatching { repo.sharedAccesses() }
                .onSuccess { _list.value = it; _error.value = null }
                .onFailure { _error.value = it.message }
        }
    }

    suspend fun create(label: String): Result<SharedAccessCreated> = runCatching { repo.createSharedAccess(label) }.also { load() }

    suspend fun revoke(id: String): Result<Unit> = runCatching { repo.revokeSharedAccess(id) }.also { load() }
}

/** Accès en lecture seule à partager (QR code ou lien), révocables à tout moment. */
@Composable
fun SharedAccessScreen(nav: NavHostController, vm: SharedAccessViewModel = viewModel()) {
    val list by vm.list.collectAsStateWithLifecycle()
    val error by vm.error.collectAsStateWithLifecycle()
    var creating by remember { mutableStateOf(false) }
    var created by remember { mutableStateOf<SharedAccessCreated?>(null) }
    var revoking by remember { mutableStateOf<SharedAccess?>(null) }
    val scope = rememberCoroutineScope()
    val snackbar = LocalSnackbar.current
    val guard = LocalActionGuard.current
    val haptics = rememberHaptics()

    LazyColumn(
        Modifier.fillMaxSize().navigationBarsPadding(),
        contentPadding = PaddingValues(start = 16.dp, end = 16.dp, bottom = 32.dp),
        verticalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        item {
            Row(Modifier.statusBarsPadding().padding(top = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                IconButton(onClick = { nav.popBackStack() }) { Icon(Icons.AutoMirrored.Rounded.ArrowBack, "Retour") }
                Text("Accès partagés", style = MaterialTheme.typography.headlineSmallEmphasized)
            }
        }
        item {
            Text(
                "Donne à quelqu'un un accès en lecture seule : il voit l'état du serveur et reçoit les alertes, " +
                    "sans pouvoir agir, ni lire les logs, ni voir tes réglages sensibles.",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(horizontal = 4.dp),
            )
        }
        item {
            Button(
                onClick = { haptics.tick(); creating = true },
                shapes = ButtonDefaults.shapes(),
                modifier = Modifier.fillMaxWidth().height(56.dp).cascade(0),
            ) {
                Icon(Icons.Rounded.Add, null)
                Spacer(Modifier.width(8.dp))
                Text("Créer un accès")
            }
        }
        val l = list
        when {
            error != null && l == null -> item { EmptyState(Icons.Rounded.Visibility, "Liste indisponible", error.orEmpty()) }
            l == null -> item { LoadingIndicator(Modifier.fillMaxWidth().padding(32.dp)) }
            l.isEmpty() -> item {
                EmptyState(Icons.Rounded.Visibility, "Aucun accès partagé", "Les accès que tu crées apparaissent ici ; tu peux les révoquer à tout moment.")
            }
            else -> items(l, key = { it.id }) { a ->
                Surface(
                    shape = RoundedCornerShape(24.dp),
                    color = MaterialTheme.colorScheme.surfaceContainer,
                    modifier = Modifier.fillMaxWidth().animateItem().cascade(l.indexOf(a) + 1),
                ) {
                    Row(Modifier.padding(start = 16.dp, end = 8.dp, top = 12.dp, bottom = 12.dp), verticalAlignment = Alignment.CenterVertically) {
                        Icon(Icons.Rounded.Visibility, null, tint = MaterialTheme.colorScheme.onSurfaceVariant)
                        Spacer(Modifier.width(16.dp))
                        Column(Modifier.weight(1f)) {
                            Text(a.label, style = MaterialTheme.typography.titleMedium)
                            Text(
                                "Créé le ${Fmt.day(a.created)} · " + if (a.lastUsed > 0) "utilisé ${Fmt.ago(a.lastUsed)}" else "jamais utilisé",
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                        IconButton(onClick = { haptics.tick(); revoking = a }) { Icon(Icons.Rounded.Delete, "Révoquer") }
                    }
                }
            }
        }
    }

    if (creating) {
        var label by remember { mutableStateOf("") }
        AlertDialog(
            onDismissRequest = { creating = false },
            icon = { Icon(Icons.Rounded.Visibility, null) },
            title = { Text("Nouvel accès en lecture seule") },
            text = {
                OutlinedTextField(
                    label, { label = it.take(40) },
                    label = { Text("Pour qui ?") },
                    placeholder = { Text("Par ex. Lucas") },
                    singleLine = true,
                )
            },
            confirmButton = {
                TextButton(enabled = label.isNotBlank(), onClick = {
                    creating = false
                    scope.launch {
                        if (!guard("Créer un accès partagé")) return@launch
                        vm.create(label.trim()).fold(
                            { haptics.confirm(); created = it },
                            { snackbar.showSnackbar("Échec : ${it.message}") },
                        )
                    }
                }) { Text("Créer") }
            },
            dismissButton = { TextButton(onClick = { creating = false }) { Text("Annuler") } },
        )
    }

    revoking?.let { a ->
        AlertDialog(
            onDismissRequest = { revoking = null },
            icon = { Icon(Icons.Rounded.Delete, null) },
            title = { Text("Révoquer l'accès de ${a.label} ?") },
            text = { Text("Son appli perdra immédiatement l'accès au serveur.") },
            confirmButton = {
                TextButton(onClick = {
                    revoking = null
                    scope.launch {
                        if (!guard("Révoquer un accès")) return@launch
                        vm.revoke(a.id).exceptionOrNull()?.let { snackbar.showSnackbar("Échec : ${it.message}") }
                    }
                }) { Text("Révoquer") }
            },
            dismissButton = { TextButton(onClick = { revoking = null }) { Text("Annuler") } },
        )
    }

    created?.let { CreatedSheet(it) { created = null } }
}

/** Le jeton n'est affiché qu'une fois : QR code à scanner dans Vigie, ou lien à envoyer. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun CreatedSheet(access: SharedAccessCreated, onDismiss: () -> Unit) {
    val context = LocalContext.current
    val qr = remember(access.qrPng) {
        runCatching {
            val bytes = Base64.decode(access.qrPng, Base64.DEFAULT)
            BitmapFactory.decodeByteArray(bytes, 0, bytes.size)?.asImageBitmap()
        }.getOrNull()
    }
    ModalBottomSheet(onDismissRequest = onDismiss) {
        Column(
            Modifier.fillMaxWidth().padding(horizontal = 24.dp).navigationBarsPadding().padding(bottom = 16.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Text("Accès de ${access.label}", style = MaterialTheme.typography.titleLarge)
            Spacer(Modifier.height(6.dp))
            Text(
                "À scanner dans l'appli Vigie de cette personne (Ajouter un serveur → Scanner). " +
                    "Ce code ne sera plus affiché : partage-le maintenant.",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.height(16.dp))
            if (qr != null) {
                // Fond blanc obligatoire pour que le QR code reste lisible en mode sombre.
                Image(
                    qr, "QR code de l'accès",
                    filterQuality = FilterQuality.None,
                    modifier = Modifier.size(240.dp).background(Color.White, RoundedCornerShape(16.dp)).padding(12.dp),
                )
                Spacer(Modifier.height(16.dp))
            }
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                FilledTonalButton(onClick = { share(context, access) }, shapes = ButtonDefaults.shapes()) {
                    Icon(Icons.Rounded.Share, null)
                    Spacer(Modifier.width(8.dp))
                    Text("Partager le lien")
                }
                OutlinedButton(onClick = { copy(context, access.link) }, shapes = ButtonDefaults.shapes()) {
                    Icon(Icons.Rounded.ContentCopy, "Copier le lien")
                }
            }
        }
    }
}

private fun share(context: Context, access: SharedAccessCreated) {
    val uri = android.net.Uri.parse(access.link)
    val text = "Accès en lecture seule à mon serveur dans Vigie : ouvre ce lien sur ton téléphone (appli Vigie installée).\n" +
        "${access.link}\n\nSi le lien ne s'ouvre pas : Vigie → Ajouter un serveur → saisie manuelle.\n" +
        "Adresse : ${uri.getQueryParameter("url").orEmpty()}\nToken : ${access.token}"
    val send = Intent(Intent.ACTION_SEND).setType("text/plain").putExtra(Intent.EXTRA_TEXT, text)
    context.startActivity(Intent.createChooser(send, "Partager l'accès").addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
}

private fun copy(context: Context, link: String) {
    context.getSystemService(ClipboardManager::class.java).setPrimaryClip(ClipData.newPlainText("Vigie", link))
}
