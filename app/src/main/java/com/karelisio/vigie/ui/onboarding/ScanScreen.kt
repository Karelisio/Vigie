package com.karelisio.vigie.ui.onboarding

import androidx.camera.core.CameraSelector
import androidx.camera.core.ImageAnalysis
import androidx.camera.core.ImageProxy
import androidx.camera.core.Preview
import androidx.camera.lifecycle.ProcessCameraProvider
import androidx.camera.view.PreviewView
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Close
import androidx.compose.material3.FilledTonalIconButton
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.BlendMode
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.CompositingStrategy
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.ContextCompat
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.navigation.NavHostController
import com.google.mlkit.vision.barcode.BarcodeScannerOptions
import com.google.mlkit.vision.barcode.BarcodeScanning
import com.google.mlkit.vision.barcode.common.Barcode
import com.google.mlkit.vision.common.InputImage
import com.karelisio.vigie.ui.components.rememberHaptics
import com.karelisio.vigie.ui.settings.SCAN_RESULT
import com.karelisio.vigie.ui.theme.Vigie
import java.util.concurrent.Executors

/** Scanner du QR code affiché par l'agent (CameraX + ML Kit, hors ligne). */
@androidx.annotation.OptIn(androidx.camera.core.ExperimentalGetImage::class)
@Composable
fun ScanScreen(nav: NavHostController) {
    val context = LocalContext.current
    val lifecycle = LocalLifecycleOwner.current
    val haptics = rememberHaptics()
    var done by remember { mutableStateOf(false) }
    var lastText by remember { mutableStateOf<String?>(null) }
    val previewView = remember { PreviewView(context).apply { scaleType = PreviewView.ScaleType.FILL_CENTER } }

    DisposableEffect(Unit) {
        val executor = Executors.newSingleThreadExecutor()
        val scanner = BarcodeScanning.getClient(BarcodeScannerOptions.Builder().setBarcodeFormats(Barcode.FORMAT_QR_CODE).build())
        val future = ProcessCameraProvider.getInstance(context)
        var frame = 0L
        future.addListener({
            val provider = future.get()
            val preview = Preview.Builder().build().also { it.surfaceProvider = previewView.surfaceProvider }
            val analysis = ImageAnalysis.Builder().setBackpressureStrategy(ImageAnalysis.STRATEGY_KEEP_ONLY_LATEST).build()
            analysis.setAnalyzer(executor) { proxy ->
                if (done) {
                    proxy.close()
                    return@setAnalyzer
                }
                // Une image sur deux est inversée : le QR des logs est souvent clair sur fond sombre.
                val input = if (frame++ % 2 == 0L) proxy.image?.let { InputImage.fromMediaImage(it, proxy.imageInfo.rotationDegrees) }
                else invertedLuma(proxy)
                if (input == null) {
                    proxy.close()
                    return@setAnalyzer
                }
                scanner.process(input)
                    .addOnSuccessListener { codes ->
                        val raw = codes.firstNotNullOfOrNull { it.rawValue }
                        if (raw != null) lastText = raw
                        val link = codes.firstNotNullOfOrNull { c -> c.rawValue?.takeIf { it.startsWith("vigie://") } }
                        if (link != null && !done) {
                            done = true
                            haptics.confirm()
                            nav.previousBackStackEntry?.savedStateHandle?.set(SCAN_RESULT, link)
                            nav.popBackStack()
                        }
                    }
                    .addOnCompleteListener { proxy.close() }
            }
            runCatching {
                provider.unbindAll()
                provider.bindToLifecycle(lifecycle, CameraSelector.DEFAULT_BACK_CAMERA, preview, analysis)
            }
        }, ContextCompat.getMainExecutor(context))
        onDispose {
            runCatching { future.get().unbindAll() }
            scanner.close()
            executor.shutdown()
        }
    }

    Box(Modifier.fillMaxSize().background(Color.Black)) {
        AndroidView({ previewView }, Modifier.fillMaxSize())
        ScanOverlay()
        FilledTonalIconButton(
            onClick = { nav.popBackStack() },
            modifier = Modifier.statusBarsPadding().padding(16.dp).align(Alignment.TopStart),
        ) { Icon(Icons.Rounded.Close, "Fermer") }
        Column(
            Modifier.align(Alignment.BottomCenter).navigationBarsPadding().padding(32.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Text(
                "Vise le QR code affiché dans les logs du conteneur vigie-agent",
                color = Color.White,
                style = MaterialTheme.typography.titleMedium,
                textAlign = TextAlign.Center,
            )
            if (lastText != null && lastText?.startsWith("vigie://") == false) {
                Text(
                    "Ce QR code n'est pas un lien Vigie.",
                    color = Vigie.status.warn,
                    style = MaterialTheme.typography.bodyMedium,
                    modifier = Modifier.padding(top = 8.dp),
                )
            }
        }
    }
}

/** Image en niveaux de gris inversés (NV21) à partir du plan Y. */
private fun invertedLuma(proxy: ImageProxy): InputImage? {
    val plane = proxy.planes.firstOrNull() ?: return null
    val w = proxy.width
    val h = proxy.height
    val buf = plane.buffer
    val rowStride = plane.rowStride
    val out = ByteArray(w * h + w * h / 2)
    val row = ByteArray(rowStride)
    for (y in 0 until h) {
        buf.position(y * rowStride)
        val n = minOf(rowStride, buf.remaining())
        buf.get(row, 0, n)
        for (x in 0 until w) out[y * w + x] = (255 - (row[x].toInt() and 0xFF)).toByte()
    }
    java.util.Arrays.fill(out, w * h, out.size, 128.toByte())
    return InputImage.fromByteArray(out, w, h, proxy.imageInfo.rotationDegrees, InputImage.IMAGE_FORMAT_NV21)
}

@Composable
private fun ScanOverlay() {
    val reduced = Vigie.reducedMotion
    val primary = MaterialTheme.colorScheme.primary
    val t = rememberInfiniteTransition(label = "scan")
    val sweep by t.animateFloat(0f, 1f, infiniteRepeatable(tween(1800, easing = LinearEasing), RepeatMode.Reverse), label = "line")
    Canvas(Modifier.fillMaxSize().graphicsLayer { compositingStrategy = CompositingStrategy.Offscreen }) {
        val side = size.minDimension * 0.68f
        val tl = Offset((size.width - side) / 2, (size.height - side) / 2.4f)
        drawRect(Color.Black.copy(alpha = 0.55f))
        drawRoundRect(Color.Transparent, tl, Size(side, side), CornerRadius(36.dp.toPx()), blendMode = BlendMode.Clear)
        drawRoundRect(primary, tl, Size(side, side), CornerRadius(36.dp.toPx()), style = Stroke(4.dp.toPx()))
        if (!reduced) {
            val y = tl.y + side * sweep
            drawRect(
                Brush.verticalGradient(listOf(Color.Transparent, primary.copy(alpha = 0.45f), Color.Transparent), y - 40.dp.toPx(), y + 4.dp.toPx()),
                Offset(tl.x + 12.dp.toPx(), y - 40.dp.toPx()),
                Size(side - 24.dp.toPx(), 44.dp.toPx()),
            )
        }
    }
}
