package com.karelisio.vigie

import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import com.karelisio.vigie.ui.components.MdBlock
import com.karelisio.vigie.ui.components.inlineMarkdown
import com.karelisio.vigie.ui.components.parseMarkdown
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class MarkdownTest {
    @Test
    fun blocks() {
        val md = """
            ## What's Changed ##
            * **New**: faster search
              on large libraries
            - Fixed `--port` flag
              1. nested step

            > Heads up
            > read this
            <p align="center"><img src="banner.png" /></p>

            ```yaml
            port: 8080
            ```
            ---
            Plain text
            on two lines
        """.trimIndent().replace("\n", "\r\n")
        assertEquals(
            listOf(
                MdBlock.Heading(2, "What's Changed"),
                MdBlock.Item(0, "•", "**New**: faster search on large libraries"),
                MdBlock.Item(0, "•", "Fixed `--port` flag"),
                MdBlock.Item(1, "1.", "nested step"),
                MdBlock.Quote("Heads up\nread this"),
                MdBlock.Code("port: 8080"),
                MdBlock.Rule,
                MdBlock.Paragraph("Plain text\non two lines"),
            ),
            parseMarkdown(md),
        )
    }

    @Test
    fun inlineStyles() {
        val s = inlineMarkdown("**Breaking:** use `--flag` (see [#12](https://github.com/x/y/pull/12)) <br> *now* &amp; ![](img.png)")
        assertEquals("Breaking: use --flag (see #12)  now & ", s.text)
        val bold = s.spanStyles.single { it.item.fontWeight == FontWeight.SemiBold }
        assertEquals("Breaking:", s.text.substring(bold.start, bold.end))
        val italic = s.spanStyles.single { it.item.fontStyle == FontStyle.Italic }
        assertEquals("now", s.text.substring(italic.start, italic.end))
        assertTrue(s.spanStyles.any { s.text.substring(it.start, it.end) == "--flag" && it.item.fontFamily != null })
    }

    @Test
    fun boldAroundCodeAndOrphanMarkers() {
        val s = inlineMarkdown("**`config.yml`** moved, 2 ** 3 stays")
        assertEquals("config.yml moved, 2 ** 3 stays", s.text)
        val code = s.spanStyles.single()
        assertEquals(FontWeight.SemiBold, code.item.fontWeight)
        assertEquals("config.yml", s.text.substring(code.start, code.end))
        // Code protégé : pas de balise retirée, pas de gras.
        assertEquals("List<String> **x**", inlineMarkdown("`List<String> **x**`").text)
    }
}
