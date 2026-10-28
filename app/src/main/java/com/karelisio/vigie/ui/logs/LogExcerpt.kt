package com.karelisio.vigie.ui.logs

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import com.karelisio.vigie.data.Fmt

/** En-tête d'un extrait de logs : « stack · conteneur(s) · date » (la stack est omise hors compose). */
internal fun excerptHeader(stack: String, containers: List<String>, ts: Long): String =
    listOf(stack, containers.joinToString(", "), Fmt.dateTime(ts)).filter { it.isNotEmpty() }.joinToString(" · ")

/** Texte d'un extrait : l'en-tête, puis une ligne par entrée (heure, conteneur d'origine dans une stack, texte). */
internal fun excerptText(header: String, lines: List<UiLine>): String = buildString {
    append(header).append("\n\n")
    lines.forEach { l ->
        append(Fmt.timeSeconds(l.ts)).append("  ")
        if (l.source.isNotEmpty()) append('[').append(l.source).append("] ")
        append(l.text).append('\n')
    }
}.trimEnd()

internal fun copyExcerpt(context: Context, text: String) {
    context.getSystemService(ClipboardManager::class.java).setPrimaryClip(ClipData.newPlainText("logs", text))
}

internal fun shareExcerpt(context: Context, subject: String, text: String) {
    val send = Intent(Intent.ACTION_SEND).apply {
        type = "text/plain"
        putExtra(Intent.EXTRA_SUBJECT, subject)
        putExtra(Intent.EXTRA_TEXT, text)
    }
    context.startActivity(Intent.createChooser(send, "Partager les logs"))
}
