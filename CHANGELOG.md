# Changelog

Le workflow de release publie la section correspondant au tag (`## [X.Y.Z]`).

## [0.2.0]

- Notifications : bouton « Redémarrer » directement dans l'alerte d'un conteneur.
- Services : date d'expiration du certificat HTTPS, alerte 14 jours avant (réglable).
- Verrouillage par empreinte (ou code du téléphone) : à l'ouverture, ou seulement pour démarrer, arrêter, redémarrer.
- Raccourcis de l'icône : Conteneurs, Services et « Logs de … » pour les conteneurs épinglés.
- Conteneurs épinglés (appui long ou étoile) : en tête de liste et dans le grand format du widget.
- Oracle Always Free : carte de risque de récupération pour inactivité sur l'accueil, et alerte.
- Réglages → Alertes et seuils : seuils CPU/RAM/disque, durée, heures calmes (résumé au réveil), sourdine par conteneur.
- Badge « MAJ » quand une nouvelle version d'une image est publiée, filtre et vérification manuelle.
- Chronologie par conteneur : plantages, redémarrages, recréations, healthchecks.
- Nouveau widget « Services ».
- Agent : mettre à jour l'image (`latest`) pour profiter de ces nouveautés.

## [0.1.1]

- Services : quand le mot-clé attendu est absent, l'erreur indique ce qui a été reçu (page HTML et son titre, JSON, réponse vide…).
- Services : la courbe de latence est remplacée par un message quand aucun essai n'a réussi.
- Agent : plus de fausse notification « de nouveau en bonne santé » au démarrage.
- Agent : relié au réseau de Pangolin (`pangolin_frontend`) dans le compose fourni.

## [0.1.0]

Première version.

- Agent : métriques de l'hôte, conteneurs Docker (état, santé, CPU/RAM, logs en direct), start/stop/restart, checks HTTP, historique 7 jours, alertes push FCM, QR code de configuration.
- Appli : accueil radar, jauges temps réel, conteneurs, logs, checks, widget, tuile de réglages rapides, mise à jour intégrée.
