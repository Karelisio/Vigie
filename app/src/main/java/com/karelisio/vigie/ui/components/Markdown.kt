package com.karelisio.vigie.ui.components

import androidx.compose.foundation.background
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp

/** Bloc d'un texte Markdown. */
internal sealed interface MdBlock {
    data class Heading(val level: Int, val text: String) : MdBlock
    /** Élément de liste : [marker] vaut « • » ou le numéro (« 1. »). */
    data class Item(val depth: Int, val marker: String, val text: String) : MdBlock
    data class Quote(val text: String) : MdBlock
    data class Code(val text: String) : MdBlock
    data class Paragraph(val text: String) : MdBlock
    data object Rule : MdBlock
}

private val headingRe = Regex("""^(#{1,6})\s+(.*?)(?:\s+#+)?\s*$""")
private val itemRe = Regex("""^(\s*)([-*+]|\d{1,3}[.)])\s+(.*)$""")
private val ruleRe = Regex("""^\s*([-*_])(\s*\1){2,}\s*$""")
private val htmlTag = Regex("""</?[A-Za-z][^>]*>""")

/** Découpe un texte Markdown en blocs : titres, listes, citations, code, séparateurs, paragraphes. */
internal fun parseMarkdown(src: String): List<MdBlock> {
    val out = mutableListOf<MdBlock>()
    val para = mutableListOf<String>()
    fun flush() {
        if (para.isNotEmpty()) out += MdBlock.Paragraph(para.joinToString("\n"))
        para.clear()
    }
    val lines = src.replace("\r\n", "\n").lines()
    var i = 0
    var previousBlank = true
    while (i < lines.size) {
        val line = lines[i]
        val trimmed = line.trim()
        val last = out.lastOrNull()
        val item = itemRe.matchEntire(line)
        val heading = headingRe.matchEntire(trimmed)
        when {
            trimmed.startsWith("```") || trimmed.startsWith("~~~") -> {
                flush()
                val fence = trimmed.take(3)
                val code = mutableListOf<String>()
                while (++i < lines.size && !lines[i].trim().startsWith(fence)) code += lines[i]
                out += MdBlock.Code(code.joinToString("\n").trimEnd())
            }
            // Ligne vide, ou faite seulement de balises HTML (<details>, <img …>) : fin de paragraphe.
            htmlTag.replace(trimmed, "").isBlank() -> flush()
            ruleRe.matches(line) -> {
                flush()
                out += MdBlock.Rule
            }
            heading != null -> {
                flush()
                out += MdBlock.Heading(heading.groupValues[1].length, heading.groupValues[2])
            }
            item != null -> {
                flush()
                val (indent, marker, text) = item.destructured
                val depth = (indent.replace("\t", "    ").length / 2).coerceAtMost(3)
                out += MdBlock.Item(depth, if (marker[0].isDigit()) marker else "•", text)
            }
            trimmed.startsWith(">") -> {
                flush()
                val text = trimmed.trimStart('>', ' ')
                if (last is MdBlock.Quote && !previousBlank) out[out.lastIndex] = MdBlock.Quote(last.text + "\n" + text)
                else out += MdBlock.Quote(text)
            }
            // Suite d'un élément de liste sur la ligne suivante, en retrait.
            last is MdBlock.Item && para.isEmpty() && !previousBlank && line[0].isWhitespace() ->
                out[out.lastIndex] = last.copy(text = last.text + " " + trimmed)
            else -> para += trimmed
        }
        previousBlank = trimmed.isEmpty()
        i++
    }
    flush()
    return out
}

private val autolink = Regex("""<(https?://[^>\s]+)>""")
private val image = Regex("""!\[([^\]]*)]\([^)]*\)""")
private val link = Regex("""\[([^\]]+)]\([^)]*\)""")
private val inlineToken = Regex("""`([^`]+)`|\*\*|__""")
private val emphasis = Regex("""(?<![\w*])\*(?![\s*])([^*]+?)(?<![\s*])\*(?![\w*])""")
private val strong = SpanStyle(fontWeight = FontWeight.SemiBold)

/**
 * Mise en forme d'une ligne : **gras**, *italique*, `code`. Les liens sont réduits à leur texte,
 * les images à leur texte alternatif, les balises HTML retirées.
 */
internal fun inlineMarkdown(text: String, code: SpanStyle = SpanStyle(fontFamily = FontFamily.Monospace)): AnnotatedString {
    val src = text.replace(autolink, "$1").replace(image, "$1").replace(link, "$1")
    val matches = inlineToken.findAll(src).toList()
    // Un marqueur de gras sans partenaire reste affiché tel quel.
    val markers = matches.filter { it.groupValues[1].isEmpty() }
    val orphan = if (markers.size % 2 == 1) markers.last() else null
    return buildAnnotatedString {
        var bold = false
        var pos = 0
        fun plain(until: Int) {
            appendEmphasis(decodeHtml(htmlTag.replace(src.substring(pos, until), "")), if (bold) strong else null)
        }
        for (m in matches) {
            if (m === orphan) continue
            plain(m.range.first)
            pos = m.range.last + 1
            val content = m.groupValues[1]
            if (content.isEmpty()) bold = !bold else withStyle(if (bold) code.merge(strong) else code) { append(content) }
        }
        plain(src.length)
    }
}

private fun AnnotatedString.Builder.appendEmphasis(text: String, base: SpanStyle?) {
    var pos = 0
    fun styled(s: String, style: SpanStyle?) {
        if (s.isEmpty()) return
        if (style == null) append(s) else withStyle(style) { append(s) }
    }
    for (m in emphasis.findAll(text)) {
        styled(text.substring(pos, m.range.first), base)
        styled(m.groupValues[1], (base ?: SpanStyle()).merge(SpanStyle(fontStyle = FontStyle.Italic)))
        pos = m.range.last + 1
    }
    styled(text.substring(pos), base)
}

private fun decodeHtml(s: String): String =
    s.replace("&nbsp;", " ").replace("&lt;", "<").replace("&gt;", ">").replace("&quot;", "\"").replace("&#39;", "'").replace("&amp;", "&")

/** Rendu d'un texte Markdown simple (notes de version) : titres, listes, citations, code, gras. */
@Composable
fun Markdown(text: String, modifier: Modifier = Modifier, style: TextStyle = MaterialTheme.typography.bodyMedium) {
    val scheme = MaterialTheme.colorScheme
    val typography = MaterialTheme.typography
    val code = SpanStyle(fontFamily = FontFamily.Monospace, background = scheme.surfaceContainerHighest)
    val blocks = remember(text) { parseMarkdown(text) }
    Column(modifier, verticalArrangement = Arrangement.spacedBy(6.dp)) {
        blocks.forEach { b ->
            when (b) {
                is MdBlock.Heading -> Text(
                    inlineMarkdown(b.text, code),
                    style = if (b.level <= 2) typography.titleSmall else typography.labelLarge,
                    modifier = Modifier.padding(top = 4.dp),
                )
                is MdBlock.Item -> Row(Modifier.padding(start = (b.depth * 16).dp)) {
                    Text(b.marker, style = style, color = scheme.primary, modifier = Modifier.widthIn(min = 18.dp))
                    Spacer(Modifier.width(4.dp))
                    Text(inlineMarkdown(b.text, code), style = style)
                }
                is MdBlock.Quote -> Row(Modifier.height(IntrinsicSize.Min)) {
                    Box(Modifier.width(3.dp).fillMaxHeight().background(scheme.outlineVariant, RoundedCornerShape(2.dp)))
                    Spacer(Modifier.width(10.dp))
                    Text(inlineMarkdown(b.text, code), style = style, color = scheme.onSurfaceVariant)
                }
                is MdBlock.Code -> Surface(color = scheme.surfaceContainerHighest, shape = RoundedCornerShape(12.dp), modifier = Modifier.fillMaxWidth()) {
                    Text(
                        b.text,
                        style = typography.bodySmall,
                        fontFamily = FontFamily.Monospace,
                        softWrap = false,
                        modifier = Modifier.horizontalScroll(rememberScrollState()).padding(12.dp),
                    )
                }
                MdBlock.Rule -> HorizontalDivider(Modifier.padding(vertical = 4.dp), color = scheme.outlineVariant)
                is MdBlock.Paragraph -> Text(inlineMarkdown(b.text, code), style = style)
            }
        }
    }
}
