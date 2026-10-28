package com.karelisio.vigie.shortcuts

import android.content.Context
import android.content.Intent
import android.net.Uri
import androidx.core.content.pm.ShortcutInfoCompat
import androidx.core.content.pm.ShortcutManagerCompat
import androidx.core.graphics.drawable.IconCompat
import com.karelisio.vigie.MainActivity
import com.karelisio.vigie.R

/** Raccourcis dynamiques de l'icône : logs des conteneurs épinglés (2 au plus, après Conteneurs et Services). */
object Shortcuts {
    private const val PREFIX = "logs_"

    fun logsUri(name: String): Uri = Uri.parse("vigie://logs").buildUpon()
        .appendQueryParameter("id", name)
        .appendQueryParameter("name", name)
        .build()

    fun updateFavorites(context: Context, favorites: Collection<String>) {
        val wanted = favorites.sorted().take(2)
        val list = wanted.mapIndexed { i, name ->
            ShortcutInfoCompat.Builder(context, PREFIX + name)
                .setShortLabel("Logs $name".take(25))
                .setLongLabel("Logs de $name")
                .setIcon(IconCompat.createWithResource(context, R.drawable.ic_sc_logs))
                .setIntent(Intent(Intent.ACTION_VIEW, logsUri(name), context, MainActivity::class.java))
                .setRank(10 + i)
                .build()
        }
        runCatching {
            val stale = ShortcutManagerCompat.getDynamicShortcuts(context)
                .map { it.id }
                .filter { it.startsWith(PREFIX) && it.removePrefix(PREFIX) !in wanted }
            if (stale.isNotEmpty()) ShortcutManagerCompat.removeDynamicShortcuts(context, stale)
            list.forEach { ShortcutManagerCompat.pushDynamicShortcut(context, it) }
        }
    }

    /** Signale l'usage d'un raccourci au lanceur (meilleur classement). */
    fun reportUsed(context: Context, name: String) {
        runCatching { ShortcutManagerCompat.reportShortcutUsed(context, PREFIX + name) }
    }
}
