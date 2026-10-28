package com.karelisio.vigie.data

/** Vrai si le conteneur est mis à jour automatiquement la nuit (agent 0.4.0 et plus). */
fun AgentSettings.autoUpdates(c: Container): Boolean =
    autoUpdate && if (c.self) autoUpdateAgent else autoUpdateAll || c.name in autoUpdateList

/**
 * Ajoute le conteneur aux mises à jour de la nuit (en les activant) ou l'en retire. Retirer un
 * conteneur quand tous sont concernés revient à choisir explicitement les autres conteneurs de [all].
 */
fun AgentSettings.withAutoUpdate(c: Container, on: Boolean, all: List<Container>): AgentSettings = when {
    c.self -> copy(autoUpdate = autoUpdate || on, autoUpdateAgent = on)
    on -> copy(autoUpdate = true, autoUpdateList = (autoUpdateList + c.name).distinct().sorted())
    autoUpdateAll -> copy(autoUpdateAll = false, autoUpdateList = all.filter { !it.self && it.name != c.name }.map { it.name }.sorted())
    else -> copy(autoUpdateList = autoUpdateList - c.name)
}
