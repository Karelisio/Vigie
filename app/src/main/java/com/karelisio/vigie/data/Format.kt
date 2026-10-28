package com.karelisio.vigie.data

import java.text.DateFormat
import java.text.DecimalFormat
import java.text.DecimalFormatSymbols
import java.util.Date
import java.util.Locale
import kotlin.math.abs
import kotlin.math.roundToInt

object Fmt {
    private val locale: Locale get() = Locale.FRANCE

    fun bytes(b: Long): String = bytes(b.toDouble())

    fun bytes(b: Double): String {
        val units = arrayOf("o", "Ko", "Mo", "Go", "To")
        var v = b
        var i = 0
        while (abs(v) >= 1024 && i < units.lastIndex) {
            v /= 1024
            i++
        }
        return when {
            i == 0 -> "${v.roundToInt()} ${units[i]}"
            v >= 100 -> "${v.roundToInt()} ${units[i]}"
            v >= 10 -> String.format(locale, "%.1f %s", v, units[i])
            else -> String.format(locale, "%.2f %s", v, units[i])
        }
    }

    fun rate(bps: Double): String = bytes(bps) + "/s"

    /** Volume en unités décimales, comme les quotas des hébergeurs (1 To = 10¹² octets) : « 1,64 To ». */
    fun volume(b: Long): String {
        val units = arrayOf("o", "ko", "Mo", "Go", "To", "Po")
        var v = b.toDouble()
        var i = 0
        while (abs(v) >= 1000 && i < units.lastIndex) {
            v /= 1000
            i++
        }
        val pattern = when {
            i == 0 || v >= 100 -> "0"
            v >= 10 -> "0.#"
            else -> "0.##"
        }
        return DecimalFormat(pattern, DecimalFormatSymbols(locale)).format(v) + " " + units[i]
    }

    fun pct(v: Double, decimals: Int = 0): String =
        if (decimals == 0) "${v.roundToInt()} %" else String.format(locale, "%.${decimals}f %%", v)

    fun ms(v: Number): String = "${v.toLong()} ms"

    /** Durée compacte : « 12 j 4 h », « 3 h 12 min », « 45 s ». */
    fun duration(seconds: Long): String {
        val s = seconds.coerceAtLeast(0)
        val d = s / 86_400
        val h = (s % 86_400) / 3600
        val m = (s % 3600) / 60
        return when {
            d > 0 -> if (h > 0) "$d j $h h" else "$d j"
            h > 0 -> if (m > 0) "$h h $m min" else "$h h"
            m > 0 -> "$m min"
            else -> "$s s"
        }
    }

    fun since(ms: Long, now: Long = System.currentTimeMillis()): String =
        if (ms <= 0) "—" else duration((now - ms) / 1000)

    /** « à l'instant », « il y a 3 min », « il y a 2 h », « il y a 3 j ». */
    fun ago(ms: Long, now: Long = System.currentTimeMillis()): String {
        if (ms <= 0) return "jamais"
        val s = ((now - ms) / 1000).coerceAtLeast(0)
        return when {
            s < 10 -> "à l'instant"
            s < 60 -> "il y a $s s"
            s < 3600 -> "il y a ${s / 60} min"
            s < 86_400 -> "il y a ${s / 3600} h"
            else -> "il y a ${s / 86_400} j"
        }
    }

    fun time(ms: Long): String = DateFormat.getTimeInstance(DateFormat.SHORT, locale).format(Date(ms))

    fun timeSeconds(ms: Long): String = DateFormat.getTimeInstance(DateFormat.MEDIUM, locale).format(Date(ms))

    fun dateTime(ms: Long): String =
        DateFormat.getDateTimeInstance(DateFormat.MEDIUM, DateFormat.SHORT, locale).format(Date(ms))

    fun day(ms: Long): String = DateFormat.getDateInstance(DateFormat.MEDIUM, locale).format(Date(ms))

    fun uptimePct(v: Double): String = when {
        v < 0 -> "—"
        v >= 99.995 -> "100 %"
        else -> String.format(locale, "%.2f %%", v)
    }

    fun stateLabel(c: Container): String = when {
        c.problem == "restarting" || c.state == "restarting" -> "Redémarre en boucle"
        c.problem == "oom" -> "Tué (mémoire)"
        c.problem == "down" -> "Tombé (code ${c.exitCode})"
        c.problem == "unhealthy" -> "En mauvaise santé"
        c.state == "running" && c.health == "starting" -> "Démarrage…"
        c.state == "running" -> "En marche"
        c.state == "paused" -> "En pause"
        c.state == "created" -> "Créé"
        c.state == "exited" -> "Arrêté"
        c.state == "dead" -> "Mort"
        else -> c.state.replaceFirstChar { it.uppercase() }
    }
}
