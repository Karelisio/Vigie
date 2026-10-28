# Vigie

Surveille et pilote ton VPS depuis ton téléphone.

- **Agent** (`/agent`) : petit service Go (~12 Mo, ~10 Mo de RAM) qui lit les métriques de l'hôte, suit les conteneurs Docker, exécute des checks HTTP, garde 7 jours d'historique (SQLite) et envoie des alertes push (FCM).
- **Appli** (`/app`) : appli Android native Material You (Kotlin, Jetpack Compose).
- **Montre** (`/wear`) : appli Wear OS (tuile, complication, écran d'état), configurée depuis le téléphone.

Tout se déploie **sans SSH** : un `docker-compose` collé dans Dockhand, une ressource Pangolin, un QR code à scanner.

<p align="center">
  <img src="docs/screenshots/accueil.png" width="200" alt="Accueil radar" />
  <img src="docs/screenshots/accueil-sombre.png" width="200" alt="Accueil en mode sombre, avec incidents" />
  <img src="docs/screenshots/conteneurs.png" width="200" alt="Conteneurs" />
  <img src="docs/screenshots/services.png" width="200" alt="Services surveillés" />
</p>

---

## 1. L'image de l'agent

GitHub Actions construit l'image `ghcr.io/karelisio/vigie-agent` (amd64 + arm64) à chaque push :

| Tag | Quand |
| --- | --- |
| `latest` | branche `main` et chaque version `vX.Y.Z` |
| `X.Y.Z` | chaque version |
| `edge` | autres branches (tests) |

**Une seule fois, après le premier build** : GitHub › ton profil › *Packages* › `vigie-agent` › *Package settings* › *Change visibility* › **Public**. Sinon Dockhand ne pourra pas télécharger l'image sans identifiants.

## 2. Déployer l'agent dans Dockhand

1. Dockhand › **Stacks** › **Créer**, nom : `vigie`.
2. Colle le contenu de [`deploy/docker-compose.yml`](deploy/docker-compose.yml).
3. Vérifie ces lignes :
   - `VIGIE_PUBLIC_URL` : `https://vigie.15122000.xyz` (l'adresse que tu exposeras via Pangolin) ;
   - `VIGIE_CHECKS` : les URLs de tes addons Stremio (modifiables ensuite dans l'appli) ;
   - `VIGIE_TOKEN` : laisse vide, un token aléatoire est généré.
4. **Déployer**.

Les réseaux `arr_backend` (tes services, pour les checks internes) et `pangolin_frontend` (celui de gerbil/traefik, pour que Pangolin joigne l'agent) doivent déjà exister. L'agent n'expose aucun port sur l'hôte : seul Pangolin y accède, via ce réseau.

Ce que monte le compose, et pourquoi :

| Montage | Usage |
| --- | --- |
| `/var/run/docker.sock` | liste, stats, logs, start/stop/restart des conteneurs (aucune autre commande Docker n'est exposée par l'API) |
| `/proc` et `/sys` en lecture seule | CPU, RAM, réseau, charge, uptime, température |
| `/` en lecture seule | occupation du disque |
| volume `vigie-data` | base SQLite, token |

Le conteneur tourne en lecture seule, sans aucune capability Linux, limité à 192 Mo de RAM et 0,5 CPU.

## 3. Exposer l'agent avec Pangolin

1. DNS : enregistrement `A` `vigie` → IP du VPS, **DNS only** (déjà fait).
2. Pangolin › **Resources** › **Add Resource** :
   - Type : **HTTP**, sous-domaine `vigie`, domaine `15122000.xyz` ;
   - Site : celui de ton VPS ;
   - Target : méthode `http`, hôte **`vigie-agent`**, port **`8080`**.
3. Onglet **Authentication** : l'appli ne peut pas passer par la page de connexion SSO. Au choix :
   - **simple** : désactive *Use Platform SSO* pour cette ressource (l'API reste protégée par le token) ;
   - **ou ciblé** : garde le SSO et ajoute dans **Rules** une règle *Path* `/api/*` → **Always Allow**, et une autre `/healthz` → **Always Allow**.
4. Test : `https://vigie.15122000.xyz/healthz` doit afficher `{"ok":true,…}`.

Les WebSockets (temps réel, logs en direct) passent par Pangolin sans réglage particulier.

## 4. Installer et connecter l'appli

Télécharge l'APK de la dernière version dans [Releases](https://github.com/Karelisio/Vigie/releases/latest) et installe-le. Les versions suivantes s'installent ensuite depuis l'appli elle-même.


1. Dockhand › conteneur **vigie-agent** › **Logs** : un QR code s'affiche au premier démarrage.
2. Appli Vigie › **Ajouter un serveur** › **Scanner le QR code**. L'URL et le token sont remplis automatiquement.
3. Pas de QR lisible ? Copie le **Lien** `vigie://connect?…` affiché sous le QR, ou saisis l'URL et le token à la main.

Le QR n'est affiché qu'au premier démarrage. Pour le revoir : ajoute `VIGIE_SHOW_QR: "true"` dans le compose, redéploie, puis retire-le. Si la visionneuse de logs a un fond clair : `VIGIE_QR_INVERT: "true"`.

## 5. Notifications push (Firebase)

Vigie réutilise ton projet Firebase existant.

**Côté appli** (pour la compilation) :
1. Console Firebase › ton projet › **Ajouter une appli** › Android › package `com.karelisio.vigie`.
2. Télécharge `google-services.json` et ajoute-le en secret GitHub (étape 6).

**Côté agent** (pour l'envoi) :
1. Console Firebase › ⚙ **Paramètres du projet** › **Comptes de service** › **Générer une nouvelle clé privée** → un fichier `.json`.
2. Encode-le en base64, **sur ton ordinateur** (n'utilise pas de site web : cette clé donne accès à ton projet) :
   - Windows (PowerShell) : `[Convert]::ToBase64String([IO.File]::ReadAllBytes("cle.json")) | Set-Clipboard`
   - macOS : `base64 -i cle.json | pbcopy`
   - Linux : `base64 -w0 cle.json`
3. Colle le résultat dans `FCM_SERVICE_ACCOUNT_B64` du compose dans Dockhand, puis redéploie.
4. Appli › Réglages › **Envoyer une notification de test**.

Alertes envoyées : conteneur tombé, redémarrages en boucle (3 en 10 min), healthcheck en échec, manque de mémoire (OOM), CPU/RAM/disque au-dessus du seuil pendant 5 min, check HTTP en échec 2 fois de suite, et retour à la normale. Une même alerte n'est pas renvoyée avant 30 min.

## 6. Secrets GitHub (compilation de l'APK)

Dépôt › **Settings** › **Secrets and variables** › **Actions** › **New repository secret** :

| Secret | Contenu |
| --- | --- |
| `KEYSTORE_BASE64` | ton keystore `.jks` encodé en base64 (même commande qu'à l'étape 5) |
| `KEYSTORE_PASSWORD` | mot de passe du keystore |
| `KEY_ALIAS` | alias de la clé |
| `KEY_PASSWORD` | mot de passe de la clé |
| `GOOGLE_SERVICES_JSON_BASE64` | `google-services.json` encodé en base64 |

Utilise **toujours le même keystore** : sinon Android refusera la mise à jour par-dessus l'appli installée.

Pas encore de keystore ? Sur un ordinateur avec Android Studio (ou un JDK) :

```
keytool -genkeypair -v -keystore vigie.jks -alias vigie -keyalg RSA -keysize 4096 -validity 36500
```

## 7. Publier une version

Au choix :
- modifie le fichier [`VERSION`](VERSION) (ex. `1.2.0`) et pousse : le tag `v1.2.0` est créé automatiquement ;
- GitHub › **Releases** › **Draft a new release** › *Choose a tag* › `v1.2.0` › **Publish** ;
- ou GitHub › **Actions** › **Release** › **Run workflow** › version `1.2.0` (le tag est créé automatiquement).

GitHub Actions :
- construit l'image de l'agent `X.Y.Z` + `latest` ;
- compile les APK signés du téléphone et de la montre, avec la même clé (`versionCode` = numéro d'exécution du workflow) ;
- publie la release avec `vigie-X.Y.Z.apk`, `vigie-wear-X.Y.Z.apk` (et leurs `.sha256`) et le changelog (section `## [X.Y.Z]` de [`CHANGELOG.md`](CHANGELOG.md), sinon la liste des commits).

L'appli vérifie la dernière release au lancement (au plus toutes les 6 h) et propose la mise à jour.

## L'appli en bref

- **Accueil** : radar animé (vert et lent quand tout va bien, rouge et plus rapide en cas de problème, écho pulsé sur le point en défaut), jauges CPU/RAM/disque, réseau en temps réel, historique 24 h / 7 jours.
- **Conteneurs** : recherche, filtres, glisser vers la droite pour redémarrer et vers la gauche pour arrêter ou démarrer (l'arrêt est confirmé), fiche détaillée avec graphiques, logs en direct avec recherche et niveaux colorés.
- **Services** : checks HTTP (préréglages AIOStreams, Comet, AIOMetadata), latence, barres de disponibilité sur 24 h, 7 ou 90 jours.
- **Conteneurs épinglés** (appui long) : en tête de liste, dans le grand format du widget et en raccourcis « Logs de … » sur l'icône de l'appli.
- **Chronologie** par conteneur (plantages, redémarrages, recréations, healthchecks) et badge **MAJ** quand une nouvelle image est publiée.
- **Notifications** avec bouton « Redémarrer », **heures calmes**, sourdine par conteneur, seuils réglables depuis l'appli (Réglages → Alertes et seuils).
- **Certificats HTTPS** des services : jours restants, alerte avant expiration (14 jours par défaut).
- **Oracle Always Free** : carte de risque de récupération pour inactivité (CPU, réseau, mémoire sous 20 % sur 7 jours) et alerte.
- **Verrouillage** par empreinte, à l'ouverture ou seulement pour les actions (Réglages → Verrouillage).
- **Mises à jour depuis l'appli** : bouton « Mettre à jour » sur un conteneur, une stack entière (vue « par stack ») ou toutes les images d'un coup, téléchargement seul, et mise à jour de l'agent lui-même (Réglages). Le conteneur est recréé à l'identique (nom, réseaux, volumes, labels compose : Dockhand le voit toujours dans sa stack), avec retour automatique à l'ancienne version s'il ne démarre pas. Progression en direct, notification à la fin si l'appli est fermée.
- **Progression en direct dans les notifications** : une opération lancée depuis l'appli (mise à jour d'un conteneur ou d'une stack, tout mettre à jour, nettoyage, sauvegarde, mise à jour de l'agent…), ou encore en cours quand tu quittes l'appli, est suivie dans une notification qui reste à jour appli fermée, puis laisse place au bilan (réussite ou échec, détail par conteneur). Sur Android 16 : barre découpée par étape ou par conteneur, échecs marqués, et mise en avant « Live Update » (pourcentage dans la barre d'état).
- **Mises à jour automatiques la nuit** pour les conteneurs choisis (Réglages → Mises à jour automatiques ou interrupteur dans la fiche), avec retour arrière et résumé au réveil ; **nouveautés** (notes de version GitHub) affichées avant de mettre à jour.
- **Réparation automatique** des conteneurs choisis (Réglages → Réparation automatique ou interrupteur dans la fiche) : redémarrage après quelques minutes de healthcheck en échec, relance après un plantage, 3 fois par heure au plus, puis alerte.
- **Mode maintenance** : notifications coupées 30 min à 4 h depuis l'accueil (icône outils) ou la tuile « Maintenance », bilan à la fin.
- **Nettoyage du disque** : images inutilisées, cache de construction et volumes anonymes orphelins, avec l'espace récupérable avant de confirmer (Réglages, ou appui sur la jauge Disque).
- **Stockage** (Réglages › Stockage) : prévision du disque (tendance sur 7 jours, nettoyages ponctuels écartés, « plein dans ~N jours » sous la jauge de l'accueil quand c'est moins de 60 jours), taille et croissance des volumes Docker toutes les 6 h (ou à la demande), images et cache de construction ; alertes quand le disque sera plein dans moins de 14 jours et quand un volume grossit de plus de 5 Go par jour (réglables).
- **Trafic du mois** avec quota (10 To sortants pour Oracle Always Free) et projection, **top consommateurs** (CPU, mémoire, réseau sur 24 h ou 7 jours), **rapport hebdomadaire** le dimanche à 19 h.
- **Alertes sur les logs** : être prévenu quand un texte ou une regex apparaît dans les logs d'un conteneur (Réglages → Alertes et seuils, ou depuis l'écran des logs).
- **Test réel des addons Stremio** : un service de type « Addon Stremio » interroge l'addon comme Stremio (liens d'un film témoin, sinon sa fiche, sinon un catalogue selon ce que l'addon fournit) et explique clairement un échec (mauvaise URL, addon non configuré, message de l'addon).
- **Surveillance depuis le téléphone** : toutes les 15 min, l'appli vérifie que chaque serveur répond ; s'il ne répond plus (agent ou VPS tombé), une notification le signale, avec la cause.
- **Logs d'une stack** : tous les conteneurs d'un projet compose en direct, fusionnés, et sélection de lignes à copier ou partager.
- **Canaux d'alerte** en plus des notifications : Telegram, Discord ou ntfy, toutes les alertes ou seulement les critiques (Réglages → Canaux d'alerte).
- **Comptes debrid** (Real-Debrid, AllDebrid, TorBox, Premiumize) : état du service vérifié toutes les 5 min (en ligne, ralenti, en panne, disponibilité sur 24 h et historique des pannes) avec alerte quand il ne répond plus, jours de premium restants sur l'accueil, alerte 7 jours puis 1 jour avant la fin ou si la clé est refusée.
- **Alerte de surconsommation après une mise à jour** : 6 h après, la mémoire ou le CPU d'un conteneur nettement au-dessus de la veille est signalé.
- **Retour à la version précédente** : après une mise à jour par Vigie (bouton, stack, nuit), « Revenir à cette version » dans la fiche du conteneur, sa chronologie ou la notification de surconsommation. Le conteneur est recréé à l'identique avec l'image d'avant (re-téléchargée si le nettoyage l'a supprimée), et la version écartée est ignorée par les mises à jour automatiques jusqu'à la suivante (« Ne plus ignorer » pour la réautoriser).
- **Accès partagés en lecture seule** (Réglages → Accès partagés) : un QR code ou un lien pour qu'un proche consulte l'état du serveur, sans pouvoir agir ni voir les logs et les secrets ; révocable à tout moment.
- **Sauvegardes** chaque jour (ou à la demande) vers un stockage compatible S3 : volumes, dossiers montés et données de Vigie, compressés, avec rétention et arrêt facultatif du conteneur pendant la copie, et **restauration en un geste** depuis l'appli, avec copie de sécurité avant et retour en arrière possible (Réglages → Sauvegardes, voir [plus bas](#sauvegardes)).
- **Noms de domaine** (Réglages → Noms de domaine) : date d'expiration lue chaque jour chez le registre (RDAP) pour les domaines de tes services et ceux que tu ajoutes, jours restants en couleur, bureau d'enregistrement ; alerte 30 jours avant (réglable), puis 7 jours avant et à l'expiration, close au renouvellement.
- **Page d'état publique** (Réglages → Page d'état publique) : une page web à partager, sans compte ni appli, avec l'état des services et conteneurs choisis sous le nom que tu leur donnes, la disponibilité sur 90 jours et les incidents de la semaine ; adresse secrète, que tu peux changer à tout moment (voir [plus bas](#page-détat-publique)).
- **Failles connues dans les images** (Réglages → Sécurité du serveur) : chaque nuit, après une mise à jour d'image ou à la demande, l'agent relève les paquets installés (Debian, Ubuntu, Alpine, images distroless, dépendances npm) et les compare à la base publique [OSV](https://osv.dev) ; seuls des noms et versions de paquets lui sont envoyés. Gravité, version corrigée, lien vers la fiche, et alerte pour les failles critiques corrigées dans une version plus récente.
- **Ports ouverts** (Réglages → Sécurité du serveur) : ce qui écoute sur Internet ou seulement sur le serveur, rattaché au conteneur qui le publie ; badges Pangolin, autorisé et « contourne Pangolin » ; alerte quand un nouveau port s'ouvre sur Internet ; test depuis le téléphone pour voir ce qui est vraiment joignable malgré le pare-feu d'Oracle.
- **Connexions SSH** (Réglages → Sécurité du serveur) : échecs sur 24 h et 7 jours, adresses les plus insistantes avec leur pays, utilisateurs essayés, bannissements fail2ban et connexions réussies, avec une alerte à chaque connexion (avertissement depuis une nouvelle adresse). Demande un groupe en plus dans le compose (voir [plus bas](#connexions-ssh)).
- **Deux widgets** (serveur, services) redimensionnables aux couleurs du système, **tuile** de réglages rapides, **raccourcis** Conteneurs/Services, **mode hors ligne** avec l'heure de la dernière mise à jour.
- **Montre Wear OS** : tuile, complication et petite appli avec l'état global, CPU/RAM/disque et les problèmes en cours ; accès dédié en lecture seule envoyé depuis le téléphone (Réglages → Montre, voir [plus bas](#montre-wear-os)).
- Plusieurs serveurs possibles (nom, URL, token), tokens chiffrés sur le téléphone (EncryptedSharedPreferences).
- Respecte le réglage système « Supprimer les animations ».

Captures d'écran : `./gradlew :app:recordRoborazziDebug` (rendu dans `app/build/outputs/roborazzi/`).

## Montre (Wear OS)

Pour les montres Wear OS 3 et plus : un écran d'état (serveur, état global coloré, CPU/RAM/disque, conteneurs et services en défaut, heure de mise à jour, bouton « Actualiser »), une **tuile** (état, métriques, nombre de problèmes ; un appui ouvre l'appli) et une **complication** pour le cadran (« OK » ou le nombre de problèmes). La montre interroge l'agent elle-même (`/api/v1/overview` et `/api/v1/system`), en Wi-Fi, en LTE ou par le Bluetooth du téléphone : la tuile se rafraîchit environ toutes les 15 min, et à chaque ouverture de l'appli.

### Installer l'appli sur la montre

L'APK `vigie-wear-X.Y.Z.apk` est joint à chaque [release](https://github.com/Karelisio/Vigie/releases/latest). Il est signé avec la même clé que l'appli du téléphone, ce qui est obligatoire pour qu'elles se parlent : prends toujours la **même version** que celle du téléphone. Il s'installe depuis un ordinateur avec `adb` ([Android SDK Platform-Tools](https://developer.android.com/tools/releases/platform-tools)) :

1. Sur la montre : **Paramètres › Système › À propos › Versions**, touche 7 fois **Numéro de build** pour activer les options pour les développeurs.
2. **Paramètres › Options pour les développeurs** : active **Débogage ADB**, puis **Déboguer via Wi-Fi** (montre et ordinateur sur le même réseau Wi-Fi). L'adresse `IP:port` de la montre s'affiche dessous.
3. **Déboguer via Wi-Fi › Associer un nouvel appareil** : la montre affiche un code à 6 chiffres et une adresse d'association. Sur l'ordinateur :
   ```
   adb pair 192.168.1.42:37415      # adresse d'association, puis le code affiché
   adb connect 192.168.1.42:41235   # adresse affichée sous « Déboguer via Wi-Fi »
   adb devices                      # la montre doit apparaître
   ```
4. Installe l'APK :
   ```
   adb install vigie-wear-0.6.0.apk
   ```
   Pour une nouvelle version, même commande avec `-r` (`adb install -r vigie-wear-X.Y.Z.apk`) : l'appli de la montre ne se met pas à jour toute seule.
5. Désactive ensuite **Débogage ADB** sur la montre (autonomie).

### Configurer depuis le téléphone

1. Vigie › **Réglages › Montre** : l'écran indique si une montre est connectée et si Vigie y est installée.
2. Laisse **Accès dédié en lecture seule** activé (recommandé, agent 0.5 ou plus) : l'appli crée un accès partagé « Montre » et c'est lui que reçoit la montre, pas ton token principal. Il apparaît dans **Réglages › Accès partagés**, où tu peux le révoquer ; un nouvel envoi remplace (et révoque) le précédent.
3. Touche **Envoyer à la montre**. Le nom, l'URL et le token du serveur actif passent par la Data Layer de Wear OS (seulement entre applis de même identifiant et de même signature, sans être conservés par les services Google), puis sont chiffrés sur la montre avec une clé de l'Android Keystore.
4. Sur la montre : ajoute la tuile **Vigie** (balaye le cadran, puis « + ») ou la complication **État du serveur** sur ton cadran.

En cas de souci, la montre l'affiche clairement : « Montre hors connexion », « Serveur injoignable » ou « Accès refusé » (accès révoqué : renvoie-le depuis le téléphone). La montre suit le serveur actif au moment de l'envoi ; pour en changer, choisis l'autre serveur dans l'appli, puis renvoie l'accès. « Retirer l'accès » efface l'accès de la montre et révoque son accès dédié.

## Sauvegardes

L'agent lit les volumes et dossiers choisis via l'API Docker (depuis un conteneur qui les monte, même arrêté), les compresse et les envoie en flux vers un stockage compatible S3 : Oracle Object Storage, Backblaze B2, Cloudflare R2, MinIO… Rien à installer ni à monter en plus dans le compose.

### Oracle Object Storage (20 Go gratuits en Always Free)

1. Console Oracle › **Stockage** › **Buckets** › **Créer un bucket** (ex. `vigie-sauvegardes`, niveau Standard, privé), dans la même région que ton VPS.
2. **Namespace** : menu du profil (en haut à droite) › **Location** (tenancy) › *Object storage namespace*.
3. **Région** : son identifiant, par exemple `eu-paris-1` ou `eu-marseille-1`.
4. **Clés** : menu du profil › **Mon profil** › **Customer Secret Keys** › **Générer une clé secrète**. Copie la clé secrète tout de suite (elle n'est affichée qu'une fois) ; la clé d'accès apparaît ensuite dans la liste.
5. Appli › Réglages › **Sauvegardes** :
   - adresse `https://<namespace>.compat.objectstorage.<région>.oraclecloud.com`, région, bucket, clés ;
   - **Tester la connexion** (écrit, liste puis supprime un petit fichier) ;
   - coche les éléments, choisis l'heure et le nombre de copies à garder, **Enregistrer**, puis **Sauvegarder maintenant** pour vérifier.

Autres stockages : Backblaze B2 `https://s3.<région>.backblazeb2.com` (région ex. `eu-central-003`), Cloudflare R2 `https://<id-du-compte>.r2.cloudflarestorage.com` (région `auto`), MinIO `https://minio.example.com` (région `us-east-1` par défaut).

### Ce qui est sauvegardé

- Chaque élément devient une archive `<préfixe>/<élément>/<AAAA-MM-JJ_HHMM>.tar.gz` (heure locale) : `<élément>` est le nom du volume, le chemin du dossier sur l'hôte avec des `_` (`/srv/app/data` → `srv_app_data`), ou `vigie`.
- L'archive contient le dossier tel que le voit le conteneur : son premier niveau est le nom du point de montage (`config/…` pour un volume monté sur `/config`).
- **Arrêter pendant la copie** (conseillé pour les applications à base SQLite : Sonarr, Radarr, Prowlarr…) : les conteneurs qui utilisent l'élément sont arrêtés, copiés, puis toujours relancés, même si la copie échoue.
- **Données de Vigie** : copie cohérente de la base (historique, services, réglages, alertes, journal) et des fichiers de l'agent, **sans** les clés du stockage ni le token d'accès, qui ne quittent jamais l'agent.
- Rétention : après chaque copie réussie, seules les N plus récentes de l'élément sont gardées (les autres fichiers du bucket ne sont jamais touchés).
- Un échec envoie une alerte, même appli ouverte ; la réussite n'est notifiée que si l'appli est fermée. Les 30 dernières sauvegardes sont listées dans l'appli.
- Limites : une opération à la fois (sauvegarde, restauration, mise à jour, nettoyage), 45 min au plus par sauvegarde ou restauration, 156 Gio au plus par archive. La mémoire reste bornée (une partie de 16 Mio envoyée à la fois), quelle que soit la taille des données.

### Restaurer depuis l'appli (agent 0.6)

Réglages › **Sauvegardes** › **Copies dans le stockage** › **Afficher les copies**, puis **Restaurer** sur la copie voulue (volume ou dossier monté). Après confirmation (empreinte ou code si le verrouillage est activé), l'agent :

1. télécharge la copie et la dépose dans un conteneur auxiliaire, pendant que tes conteneurs tournent encore ;
2. arrête les conteneurs qui utilisent l'élément ;
3. envoie une **copie de sécurité** de l'état actuel au stockage (`<préfixe>/<élément>/avant-restauration_<AAAA-MM-JJ_HHMM>.tar.gz`) ;
4. vérifie toute l'archive, vide l'élément puis y extrait la copie, propriétaires, droits et dates compris ;
5. relance les conteneurs arrêtés (toujours, même en cas d'erreur) et vérifie qu'ils tournent.

Si l'extraction échoue après avoir vidé l'élément, la copie de sécurité est remise automatiquement. Une archive invalide ou suspecte (chemin absolu, `..`, passage par un lien symbolique) est refusée avant toute modification. La progression s'affiche dans l'appli ; un échec envoie une alerte critique, même appli ouverte, et apparaît dans la chronologie des conteneurs concernés.

**Annuler la dernière restauration** : la copie de sécurité apparaît à part, au-dessus des copies de l'élément ; la restaurer remet l'état d'avant (et garde à son tour l'état actuel en copie de sécurité). Une seule copie de sécurité est gardée par élément, jamais supprimée par la rétention.

Bon à savoir :
- L'auxiliaire (`vigie-restauration-…`) est créé à partir de l'image de l'agent, sans réseau ni accès à Docker, avec les seules capacités utiles (`CHOWN`, `DAC_OVERRIDE`, `FOWNER`, `FSETID`) ; il est ignoré par les alertes et supprimé à la fin.
- La copie est déposée dans l'auxiliaire : il faut, le temps de l'opération, de la place libre pour la copie compressée en plus des données restaurées.
- Les fichiers spéciaux (périphériques, tubes) ne sont pas recréés ; les attributs étendus et ACL ne sont pas restaurés.
- Désactivée avec `VIGIE_ALLOW_UPDATES=false`. Derrière un socket-proxy, il faut `POST=1`.
- Les **données de Vigie** ne se restaurent pas depuis l'appli : l'agent les utilise en permanence. Voir ci-dessous.

### Restaurer à la main

Pour les données de Vigie, ou si l'agent ne répond plus. Sur le VPS (terminal), dans un dossier de travail :

1. **Télécharge la copie** depuis la console de ton stockage (Oracle : Buckets › bucket › objet › ⋮ › **Télécharger**) puis copie-la sur le serveur, ou directement :
   ```
   docker run --rm -v "$PWD":/backup -w /backup \
     -e AWS_ACCESS_KEY_ID=<clé d'accès> -e AWS_SECRET_ACCESS_KEY=<clé secrète> -e AWS_DEFAULT_REGION=eu-paris-1 \
     -e AWS_REQUEST_CHECKSUM_CALCULATION=when_required -e AWS_RESPONSE_CHECKSUM_VALIDATION=when_required \
     amazon/aws-cli --endpoint-url https://<namespace>.compat.objectstorage.eu-paris-1.oraclecloud.com \
     s3 cp s3://vigie-sauvegardes/vigie/sonarr_config/2026-10-28_0330.tar.gz .
   ```
2. **Arrête le conteneur** qui utilise les données (Dockhand › conteneur › Stop) ; pour les données de Vigie, c'est `vigie-agent` lui-même.
3. **Restaure** :
   - un volume (son contenu actuel est remplacé) :
     ```
     docker run --rm -v sonarr_config:/restore -v "$PWD":/backup alpine sh -c \
       'find /restore -mindepth 1 -delete && tar xzf /backup/2026-10-28_0330.tar.gz -C /restore --strip-components=1'
     ```
   - un dossier de l'hôte : `sudo tar xzf 2026-10-28_0330.tar.gz -C /srv/app/data --strip-components=1` (sans `--strip-components` si l'élément est un fichier seul) ;
   - les données de Vigie (volume `vigie_vigie-data` si la stack s'appelle `vigie`) :
     ```
     docker run --rm -v vigie_vigie-data:/data -v "$PWD":/backup alpine sh -c \
       'rm -f /data/vigie.db-wal /data/vigie.db-shm && tar xzf /backup/2026-10-28_0330.tar.gz -C /data --strip-components=1'
     ```
     Le token en place est conservé (sinon un nouveau est généré, avec son QR code) ; ressaisis ensuite les clés du stockage dans l'appli.
4. **Redémarre le conteneur**.

## Page d'état publique

Une page web, sans compte ni appli, pour montrer à tes proches si tes services fonctionnent : état global (« Tous les services fonctionnent », « Perturbations » ou « Panne »), état de chaque service et conteneur choisi, barres de disponibilité sur 90 jours et incidents des 7 derniers jours. Elle s'actualise toute seule chaque minute, suit le thème clair ou sombre du téléphone et fonctionne sans JavaScript.

1. Appli › Réglages › **Page d'état publique** : coche les services et conteneurs à montrer et donne à chacun un **nom public** (« Recherche de liens » plutôt que `aiostreams`), choisis un titre, active **Page publique**, puis **Enregistrer**.
2. **Partager** ou **Copier** le lien : `https://vigie.15122000.xyz/status/<identifiant>`. L'identifiant (22 caractères tirés au hasard) tient lieu de mot de passe : seules les personnes qui ont le lien voient la page, et elle n'est pas indexée par les moteurs de recherche.
3. **Pangolin** : la ressource `vigie` ne doit pas exiger de connexion Pangolin pour `/status/`. C'est déjà le cas si l'appli se connecte avec son token (SSO désactivé sur la ressource) ; si tu as gardé le SSO avec des règles ciblées, ajoute une règle *Path* `/status/*` → **Always Allow**. Le reste de l'API exige toujours le token.

La page n'affiche que les noms choisis : ni adresse, ni IP, ni image, ni message d'erreur, ni nom de conteneur ou de serveur. **Changer le lien** crée un nouvel identifiant : l'ancien lien affiche aussitôt « Page introuvable », comme une page désactivée. Une version JSON est servie à la même adresse suivie de `.json` (détail dans [`docs/API.md`](docs/API.md#page-détat-publique-agent-060)).

## Connexions SSH

L'agent lit le journal d'authentification de l'hôte par le montage `/:/host/root:ro` déjà présent : `/var/log/auth.log` (Debian, Ubuntu) ou `/var/log/secure` (RHEL, Oracle Linux), et `/var/log/fail2ban.log` s'il existe. Rien à installer sur le serveur.

### Donner le droit de lire le journal

L'agent tourne en root mais sans aucune capacité (`cap_drop: ALL`) : il ne lit que les fichiers dont root est propriétaire, ou ceux de ses groupes. Sur Ubuntu, `auth.log` appartient à `syslog`, groupe `adm`, droits 640. Ajoute ce groupe au conteneur :

1. Sur le VPS : `getent group adm` répond `adm:x:4:syslog,ubuntu` → le numéro du groupe est `4`.
2. Dans Dockhand, modifie la stack et ajoute au service `vigie-agent`, au même niveau que `cap_drop` (les lignes sont déjà là, en commentaire) :
   ```yaml
       group_add:
         - "4"
   ```
3. Redéploie. Tant que ce n'est pas fait, l'écran **Connexions SSH** explique le problème et donne l'extrait exact à copier, calculé d'après le groupe et les droits réels du fichier.

`/var/log/secure` (RHEL, Oracle Linux : 600, propriétaire root) se lit sans rien changer. Si l'appli signale un fichier lisible par son seul propriétaire (autre que root), ajoute plutôt `cap_add: [DAC_READ_SEARCH]` : lecture seule des fichiers, aucun autre droit.

**Pas de fichier ?** Un système qui n'écrit qu'avec journald n'a pas de `auth.log` : `sudo apt install rsyslog && sudo systemctl enable --now rsyslog` (ou `sudo dnf install rsyslog`).

### Ce que fait l'agent

- **Compte** les échecs (mot de passe, clé, utilisateur inexistant, connexion fermée avant authentification sur un serveur à clé seule), les connexions réussies et les bannissements fail2ban des prisons SSH ; les lignes d'une même connexion ne sont comptées qu'une fois. 30 jours d'historique ; au premier démarrage, la semaine passée est reprise (fichier tourné compris), sans alerte.
- **Alerte** à chaque connexion réussie : « Connexion SSH : ubuntu depuis 1.2.3.4 (France) », en avertissement si l'adresse n'a jamais servi à une connexion réussie. Jamais pour les simples tentatives (le bruit permanent d'Internet). Une ligne SSH s'ajoute au rapport du dimanche.
- **Pays des adresses** : seules les adresses sont envoyées, par lots, au service gratuit [GeoJS](https://www.geojs.io/) (HTTPS, sans clé ni compte), une requête toutes les 10 s au plus, réponses gardées 30 jours. `VIGIE_GEOIP: "off"` désactive l'envoi (pays affiché comme inconnu).

---

## Configuration de l'agent

| Variable | Défaut | Rôle |
| --- | --- | --- |
| `VIGIE_TOKEN` | généré | token d'accès (32 caractères minimum) |
| `VIGIE_PUBLIC_URL` | — | URL publique, intégrée au QR code |
| `VIGIE_SERVER_NAME` | nom d'hôte | nom affiché dans l'appli |
| `VIGIE_CHECKS` | — | checks HTTP ajoutés au démarrage : `Nom \| URL \| code \| mot-clé` |
| `VIGIE_ALERT_CPU` / `_RAM` / `_DISK` | `90` | seuils d'alerte en % (valeurs initiales, modifiables ensuite dans l'appli) |
| `VIGIE_ALERT_DURATION` | `5m` | durée de dépassement avant alerte CPU/RAM (idem) |
| `VIGIE_ALERT_COOLDOWN` | `30m` | délai minimum entre deux notifications identiques |
| `VIGIE_RESTART_LOOP_COUNT` / `_WINDOW` | `3` / `10m` | détection des redémarrages en boucle |
| `VIGIE_CHECK_FAILURES` | `2` | échecs consécutifs avant alerte |
| `VIGIE_IGNORE` | — | conteneurs à ne jamais signaler (aussi possible avec le label `vigie.ignore=true`) |
| `VIGIE_RATE_LIMIT` / `_BURST` | `10` / `60` | requêtes par seconde et rafale autorisées par IP |
| `VIGIE_SHOW_QR` / `VIGIE_QR_INVERT` | `false` | réafficher / inverser le QR code |
| `VIGIE_ALLOW_UPDATES` | `true` | autoriser les mises à jour d'images et les restaurations depuis l'appli (`false` : lecture et start/stop/restart seulement) |
| `TZ` | `Europe/Paris` (compose) | fuseau des heures calmes |
| `VIGIE_ORACLE` | auto | `true`/`false` pour forcer l'analyse Oracle Always Free (détectée via le noyau) |
| `VIGIE_NET_MBPS` | auto | débit réseau de l'instance, pour le critère réseau d'Oracle |
| `VIGIE_OSV_URL` | `https://api.osv.dev` | API OSV interrogée pour les failles connues (miroir, essais) |
| `FCM_SERVICE_ACCOUNT_B64` | — | compte de service Firebase (base64) |
| `VIGIE_GEOIP` | GeoJS | pays des adresses des connexions SSH ; `off` : rien n'est envoyé |
| `DOCKER_HOST` | `unix:///var/run/docker.sock` | accepte aussi `tcp://socket-proxy:2375` |

### Sécurité

- Toutes les routes (sauf `/healthz` et la [page d'état publique](#page-détat-publique), protégée par son adresse secrète) exigent `Authorization: Bearer <token>`, comparé à temps constant.
- Limitation de débit par IP ; 10 échecs d'authentification en 10 min → IP bloquée 15 min.
- Actions possibles : **start, stop, restart** et **mise à jour d'image**. Une mise à jour ne télécharge que l'image déjà configurée sur le conteneur et le recrée avec sa configuration actuelle : l'appli ne peut fournir ni image ni configuration. L'agent refuse de s'arrêter lui-même.
- Chaque action est enregistrée dans un journal (date, IP, action, cible, résultat), consultable dans l'appli.
- Les variables d'environnement des conteneurs ne sont jamais exposées.
- Accès partagés : seule l'empreinte SHA-256 du jeton est conservée ; ils ne peuvent que consulter (ni action, ni réglage, ni logs, ni journal, ni canaux, debrid ou sauvegardes).
- Canaux d'alerte et comptes debrid : jetons et clés restent sur l'agent, renvoyés masqués à l'appli (4 derniers caractères).
- Sauvegardes : les clés du stockage restent sur l'agent. Elles ne sont jamais renvoyées à l'appli, ni écrites dans les logs, le journal ou la copie de la base ; changer l'adresse du stockage oblige à ressaisir la clé secrète. Comme les sauvegardes lisent le contenu des volumes choisis, garde le token aussi précieusement qu'un accès au serveur.
- Restaurations : seulement une copie du stockage configuré, rangée dans le dossier de l'élément, sur un volume ou un dossier déjà monté par un conteneur (jamais un élément que l'agent modifie lui-même) ; extraction confinée à l'élément, dans un conteneur auxiliaire sans réseau, sans accès à Docker ni secret.

Pour aller plus loin, tu peux placer un [socket-proxy](https://github.com/linuxserver/docker-socket-proxy) entre l'agent et Docker (`CONTAINERS=1`, `EVENTS=1`, `INFO=1`, `IMAGES=1`, `ALLOW_START=1`, `ALLOW_STOP=1`, `ALLOW_RESTARTS=1`, `POST=0` avec `VIGIE_ALLOW_UPDATES=false` ; pour les mises à jour depuis l'appli, ajoute `POST=1` et `NETWORKS=1` ; pour les restaurations, `POST=1`) et pointer `DOCKER_HOST` dessus.

L'API est décrite dans [`docs/API.md`](docs/API.md).
