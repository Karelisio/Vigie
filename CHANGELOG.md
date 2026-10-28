# Changelog

Le workflow de release publie la section correspondant au tag (`## [X.Y.Z]`).

## [0.1.1]

- Services : quand le mot-clé attendu est absent, l'erreur indique ce qui a été reçu (page HTML et son titre, JSON, réponse vide…).
- Services : la courbe de latence est remplacée par un message quand aucun essai n'a réussi.
- Agent : plus de fausse notification « de nouveau en bonne santé » au démarrage.
- Agent : relié au réseau de Pangolin (`pangolin_frontend`) dans le compose fourni.

## [0.1.0]

Première version.

- Agent : métriques de l'hôte, conteneurs Docker (état, santé, CPU/RAM, logs en direct), start/stop/restart, checks HTTP, historique 7 jours, alertes push FCM, QR code de configuration.
- Appli : accueil radar, jauges temps réel, conteneurs, logs, checks, widget, tuile de réglages rapides, mise à jour intégrée.
