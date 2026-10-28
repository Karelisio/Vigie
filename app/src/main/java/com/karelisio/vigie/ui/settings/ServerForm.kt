package com.karelisio.vigie.ui.settings

import android.content.ClipboardManager
import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.SizeTransform
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.scaleIn
import androidx.compose.animation.togetherWith
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.ContentPaste
import androidx.compose.material.icons.rounded.QrCodeScanner
import androidx.compose.material.icons.rounded.Visibility
import androidx.compose.material.icons.rounded.VisibilityOff
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialShapes
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.toShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.karelisio.vigie.appContainer
import com.karelisio.vigie.data.Server
import com.karelisio.vigie.data.ServerInfo
import com.karelisio.vigie.ui.components.AnimatedMark
import com.karelisio.vigie.ui.components.MiniRadar
import com.karelisio.vigie.ui.components.Springs
import com.karelisio.vigie.ui.components.rememberHaptics
import com.karelisio.vigie.ui.components.shake
import com.karelisio.vigie.ui.components.ShakeController
import com.karelisio.vigie.ui.theme.Vigie
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

private sealed interface TestState {
    data object Idle : TestState
    data object Testing : TestState
    data class Ok(val info: ServerInfo) : TestState
    data class Failed(val message: String) : TestState
}

/**
 * Configuration d'un serveur : QR code, lien collé ou saisie manuelle, puis test de connexion
 * (radar animé) avant l'enregistrement.
 */
@Composable
fun ServerForm(
    initial: Server?,
    link: String?,
    onScan: () -> Unit,
    onSaved: (Server) -> Unit,
    modifier: Modifier = Modifier,
    saveLabel: String = "Enregistrer",
) {
    val context = LocalContext.current
    val haptics = rememberHaptics()
    val scope = rememberCoroutineScope()
    val shaker = remember { ShakeController() }
    val fromLink = remember(link) { link?.let { Server.fromLink(it) } }
    var name by rememberSaveable(fromLink) { mutableStateOf(fromLink?.name ?: initial?.name.orEmpty()) }
    var url by rememberSaveable(fromLink) { mutableStateOf(fromLink?.url ?: initial?.url ?: "https://") }
    var token by rememberSaveable(fromLink) { mutableStateOf(fromLink?.token ?: initial?.token.orEmpty()) }
    var showToken by remember { mutableStateOf(false) }
    var test by remember { mutableStateOf<TestState>(TestState.Idle) }
    var hint by remember { mutableStateOf<String?>(null) }

    fun current() = Server(
        id = initial?.id ?: java.util.UUID.randomUUID().toString(),
        name = name.trim().ifEmpty { android.net.Uri.parse(Server.normalizeUrl(url)).host ?: "Serveur" },
        url = Server.normalizeUrl(url),
        token = token.trim(),
    )

    fun runTest() {
        haptics.tick()
        test = TestState.Testing
        scope.launch {
            val started = System.currentTimeMillis()
            val r = runCatching { appContainer.repository.testConnection(current()) }
            val wait = 1400 - (System.currentTimeMillis() - started)
            if (wait > 0) delay(wait)
            test = r.fold(
                onSuccess = { info ->
                    haptics.confirm()
                    if (name.isBlank()) name = info.name
                    TestState.Ok(info)
                },
                onFailure = {
                    haptics.reject()
                    launch { shaker.shake() }
                    TestState.Failed(it.message ?: "Connexion impossible")
                },
            )
        }
    }

    // Lien reçu (QR, lien profond) : test automatique.
    LaunchedEffect(fromLink) { if (fromLink != null) runTest() }

    Column(modifier, verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            FilledTonalButton(onClick = { haptics.tick(); onScan() }, modifier = Modifier.weight(1f).height(52.dp), shapes = ButtonDefaults.shapes()) {
                Icon(Icons.Rounded.QrCodeScanner, null)
                Spacer(Modifier.width(8.dp))
                Text("Scanner")
            }
            OutlinedButton(
                onClick = {
                    val clip = context.getSystemService(ClipboardManager::class.java).primaryClip
                    val text = clip?.getItemAt(0)?.coerceToText(context)?.toString().orEmpty()
                    val s = Server.fromLink(text)
                    if (s != null) {
                        name = s.name; url = s.url; token = s.token
                        hint = null
                        runTest()
                    } else {
                        haptics.reject()
                        hint = "Le presse-papiers ne contient pas de lien vigie://"
                    }
                },
                modifier = Modifier.weight(1f).height(52.dp),
            ) {
                Icon(Icons.Rounded.ContentPaste, null)
                Spacer(Modifier.width(8.dp))
                Text("Coller le lien")
            }
        }
        hint?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error) }
        OutlinedTextField(name, { name = it }, label = { Text("Nom (facultatif)") }, singleLine = true, modifier = Modifier.fillMaxWidth())
        OutlinedTextField(
            url, { url = it.trim(); test = TestState.Idle },
            label = { Text("Adresse de l'agent") },
            placeholder = { Text("https://vigie.mondomaine.fr") },
            singleLine = true,
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri),
            modifier = Modifier.fillMaxWidth(),
        )
        OutlinedTextField(
            token, { token = it.trim(); test = TestState.Idle },
            label = { Text("Token") },
            singleLine = true,
            visualTransformation = if (showToken) VisualTransformation.None else PasswordVisualTransformation(),
            textStyle = MaterialTheme.typography.bodyLarge.copy(fontFamily = FontFamily.Monospace),
            trailingIcon = {
                IconButton(onClick = { showToken = !showToken }) {
                    Icon(if (showToken) Icons.Rounded.VisibilityOff else Icons.Rounded.Visibility, "Afficher")
                }
            },
            supportingText = { Text("Affiché avec le QR dans les logs de l'agent (Dockhand)") },
            isError = token.isNotEmpty() && token.length < 32,
            modifier = Modifier.fillMaxWidth(),
        )

        AnimatedContent(
            targetState = test,
            transitionSpec = { (fadeIn() + scaleIn(Springs.bouncy(), initialScale = 0.85f)) togetherWith fadeOut() using SizeTransform(clip = false) },
            contentAlignment = Alignment.Center,
            modifier = Modifier.fillMaxWidth(),
            label = "test",
        ) { t ->
            when (t) {
                TestState.Idle -> Spacer(Modifier.height(1.dp))
                TestState.Testing -> Column(Modifier.fillMaxWidth(), horizontalAlignment = Alignment.CenterHorizontally) {
                    MiniRadar(Modifier.size(120.dp), speed = 260f)
                    Text("Recherche de l'agent…", style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
                is TestState.Ok -> ResultCard(ok = true, title = "Connecté à ${t.info.name}") {
                    Text(
                        "Agent ${t.info.version} · ${t.info.host.os.ifEmpty { t.info.host.hostname }}" +
                            (t.info.docker?.let { "\nDocker ${it.version}" } ?: "") +
                            if (t.info.pushEnabled) "\nNotifications push prêtes" else "\nPush non configuré sur l'agent (facultatif)",
                        style = MaterialTheme.typography.bodySmall,
                        textAlign = TextAlign.Center,
                    )
                }
                is TestState.Failed -> Box(Modifier.shake(shaker)) {
                    ResultCard(ok = false, title = "Connexion impossible") {
                        Text(t.message, style = MaterialTheme.typography.bodySmall, textAlign = TextAlign.Center)
                    }
                }
            }
        }

        val canSave = test is TestState.Ok
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            OutlinedButton(
                onClick = { runTest() },
                enabled = token.length >= 32 && url.length > 10 && test != TestState.Testing,
                modifier = Modifier.weight(1f).height(56.dp),
            ) { Text("Tester") }
            Button(
                onClick = {
                    val s = current()
                    appContainer.servers.upsert(s)
                    scope.launch { appContainer.push.register(s) }
                    haptics.confirm()
                    onSaved(s)
                },
                enabled = canSave,
                shapes = ButtonDefaults.shapes(),
                modifier = Modifier.weight(1f).height(56.dp),
            ) { Text(saveLabel) }
        }
    }
}

@Composable
private fun ResultCard(ok: Boolean, title: String, body: @Composable () -> Unit) {
    val status = Vigie.status
    Surface(
        shape = RoundedCornerShape(28.dp),
        color = if (ok) status.okContainer else status.criticalContainer,
        contentColor = if (ok) status.onOkContainer else status.onCriticalContainer,
        modifier = Modifier.fillMaxWidth(),
    ) {
        Column(Modifier.padding(20.dp), horizontalAlignment = Alignment.CenterHorizontally) {
            Box(contentAlignment = Alignment.Center) {
                Surface(
                    shape = (if (ok) MaterialShapes.Cookie9Sided else MaterialShapes.Cookie4Sided).toShape(),
                    color = if (ok) status.ok else status.critical,
                    modifier = Modifier.size(64.dp),
                ) {}
                AnimatedMark(ok = ok, color = Color.White, modifier = Modifier.size(28.dp))
            }
            Spacer(Modifier.height(10.dp))
            Text(title, style = MaterialTheme.typography.titleMedium)
            body()
        }
    }
}
