# Vigie

Surveille et pilote ton VPS depuis ton téléphone.

- **Agent** (`/agent`) : petit service Go (~12 Mo, ~10 Mo de RAM) qui lit les métriques de l'hôte, suit les conteneurs Docker, exécute des checks HTTP, garde 7 jours d'historique (SQLite) et envoie des alertes push (FCM).
- **Appli** (`/app`) : appli Android native Material You (Kotlin, Jetpack Compose).

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
- compile l'APK signé (`versionCode` = numéro d'exécution du workflow) ;
- publie la release avec l'APK et le changelog (section `## [X.Y.Z]` de [`CHANGELOG.md`](CHANGELOG.md), sinon la liste des commits).

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
- **Mises à jour automatiques la nuit** pour les conteneurs choisis (Réglages → Mises à jour automatiques ou interrupteur dans la fiche), avec retour arrière et résumé au réveil ; **nouveautés** (notes de version GitHub) affichées avant de mettre à jour.
- **Mode maintenance** : notifications coupées 30 min à 4 h depuis l'accueil (icône outils) ou la tuile « Maintenance », bilan à la fin.
- **Nettoyage du disque** : images inutilisées, cache de construction et volumes anonymes orphelins, avec l'espace récupérable avant de confirmer (Réglages, ou appui sur la jauge Disque).
- **Trafic du mois** avec quota (10 To sortants pour Oracle Always Free) et projection, **top consommateurs** (CPU, mémoire, réseau sur 24 h ou 7 jours), **rapport hebdomadaire** le dimanche à 19 h.
- **Alertes sur les logs** : être prévenu quand un texte ou une regex apparaît dans les logs d'un conteneur (Réglages → Alertes et seuils, ou depuis l'écran des logs).
- **Test réel des addons Stremio** : un service de type « Addon Stremio » vérifie que l'addon renvoie bien des liens pour un film témoin, pas seulement que son manifest répond.
- **Deux widgets** (serveur, services) redimensionnables aux couleurs du système, **tuile** de réglages rapides, **raccourcis** Conteneurs/Services, **mode hors ligne** avec l'heure de la dernière mise à jour.
- Plusieurs serveurs possibles (nom, URL, token), tokens chiffrés sur le téléphone (EncryptedSharedPreferences).
- Respecte le réglage système « Supprimer les animations ».

Captures d'écran : `./gradlew :app:recordRoborazziDebug` (rendu dans `app/build/outputs/roborazzi/`).

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
| `VIGIE_ALLOW_UPDATES` | `true` | autoriser les mises à jour d'images depuis l'appli (`false` : lecture et start/stop/restart seulement) |
| `TZ` | `Europe/Paris` (compose) | fuseau des heures calmes |
| `VIGIE_ORACLE` | auto | `true`/`false` pour forcer l'analyse Oracle Always Free (détectée via le noyau) |
| `VIGIE_NET_MBPS` | auto | débit réseau de l'instance, pour le critère réseau d'Oracle |
| `FCM_SERVICE_ACCOUNT_B64` | — | compte de service Firebase (base64) |
| `DOCKER_HOST` | `unix:///var/run/docker.sock` | accepte aussi `tcp://socket-proxy:2375` |

### Sécurité

- Toutes les routes (sauf `/healthz`) exigent `Authorization: Bearer <token>`, comparé à temps constant.
- Limitation de débit par IP ; 10 échecs d'authentification en 10 min → IP bloquée 15 min.
- Actions possibles : **start, stop, restart** et **mise à jour d'image**. Une mise à jour ne télécharge que l'image déjà configurée sur le conteneur et le recrée avec sa configuration actuelle : l'appli ne peut fournir ni image ni configuration. L'agent refuse de s'arrêter lui-même.
- Chaque action est enregistrée dans un journal (date, IP, action, cible, résultat), consultable dans l'appli.
- Les variables d'environnement des conteneurs ne sont jamais exposées.

Pour aller plus loin, tu peux placer un [socket-proxy](https://github.com/linuxserver/docker-socket-proxy) entre l'agent et Docker (`CONTAINERS=1`, `EVENTS=1`, `INFO=1`, `IMAGES=1`, `ALLOW_START=1`, `ALLOW_STOP=1`, `ALLOW_RESTARTS=1`, `POST=0` avec `VIGIE_ALLOW_UPDATES=false` ; pour les mises à jour depuis l'appli, ajoute `POST=1` et `NETWORKS=1`) et pointer `DOCKER_HOST` dessus.

L'API est décrite dans [`docs/API.md`](docs/API.md).
