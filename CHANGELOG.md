# Changelog

Le workflow de release publie la section correspondant au tag (`## [X.Y.Z]`).

## [0.5.0]

- Surveillance depuis le téléphone : l'appli vérifie toutes les 15 min que chaque serveur répond et prévient s'il ne répond plus (VPS ou agent tombé), avec la cause, puis quand il répond de nouveau.
- Réparation automatique : redémarrage d'un conteneur en mauvaise santé depuis quelques minutes, relance après un plantage, 3 fois par heure au plus puis alerte ; suspendue en mode maintenance.
- Canaux d'alerte : Telegram, Discord ou ntfy, en plus des notifications (toutes les alertes ou seulement les critiques).
- Comptes debrid (Real-Debrid, AllDebrid, TorBox, Premiumize) : jours de premium restants sur l'accueil, alerte 7 jours puis 1 jour avant la fin.
- Services Stremio : test adapté au type d'addon (liens, fiche ou catalogue) et messages clairs (mauvaise URL, addon non configuré, message de l'addon) ; préréglages corrigés.
- Sauvegardes chaque jour vers un stockage compatible S3 (Oracle, B2, R2, MinIO…) : volumes, dossiers montés et données de Vigie, avec rétention.
- Logs d'une stack entière en direct, et sélection de lignes à copier ou partager.
- Alerte de surconsommation 6 h après une mise à jour (mémoire ou CPU nettement au-dessus de la veille).
- Accès partagés en lecture seule par QR code ou lien, révocables.
- Agent : à mettre à jour depuis l'appli (Réglages → Mettre à jour l'agent).

## [0.4.0]

- Mises à jour automatiques la nuit, à l'heure choisie, pour tous les conteneurs ou une sélection (agent compris en option) ; retour arrière si un conteneur ne démarre pas, résumé au réveil.
- Nouveautés avant une mise à jour : notes de version GitHub de l'image, les versions plus récentes mises en avant.
- Mode maintenance : notifications coupées 30 min à 4 h (accueil ou tuile de réglages rapides), bilan des alertes à la fin.
- Nettoyage du disque : images inutilisées, cache de construction, volumes anonymes orphelins, avec estimation avant confirmation.
- Trafic du mois : quota sortant (10 To pour Oracle Always Free), projection de fin de mois, historique, alerte à 80 % puis à 100 %.
- Top consommateurs : CPU, mémoire et réseau des conteneurs sur 24 h ou 7 jours.
- Rapport hebdomadaire le dimanche à 19 h : disponibilité, incidents, plantages, mises à jour, disque, trafic.
- Alertes sur les logs : texte ou expression régulière, par conteneur, 15 min entre deux alertes avec le nombre d'occurrences.
- Services : mode « Addon Stremio » qui vérifie la présence de liens pour un film témoin (préréglages AIOStreams et Comet).
- Agent : à mettre à jour depuis l'appli (Réglages → Mettre à jour l'agent) pour profiter de ces nouveautés.

## [0.3.0]

- Mises à jour depuis l'appli, sans passer par Dockhand : bouton « Mettre à jour » dans la fiche d'un conteneur, téléchargement seul de l'image, « Tout mettre à jour » dans le filtre Mises à jour.
- Vue « par stack » dans Conteneurs : mettre à jour, télécharger, redémarrer, démarrer ou arrêter toute une stack compose.
- Le conteneur est recréé à l'identique (nom, réseaux, volumes, variables, labels compose) ; s'il ne démarre pas, l'ancienne version est rétablie automatiquement. Les conteneurs qui partagent son réseau (ex. traefik dans gerbil) sont rattachés au nouveau.
- Mise à jour de l'agent lui-même (Réglages → Mettre à jour l'agent), avec retour arrière automatique.
- Progression en direct (bandeau animé), résultat détaillé par conteneur, notification à la fin si l'appli est fermée.
- Notification « nouvelle image » : bouton « Mettre à jour » / « Tout mettre à jour ».
- Option : supprimer l'ancienne image après une mise à jour réussie (activée par défaut).
- Agent : dernière mise à jour manuelle à faire dans Dockhand (Pull + Redeploy) ; les suivantes se feront depuis l'appli.

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
