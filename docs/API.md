# API de l'agent Vigie (v1)

Base : `https://vigie.example.com`. Toutes les routes sauf `/healthz` et la page d'état publique (`/status/…`) exigent `Authorization: Bearer <token>`.
Dates en millisecondes Unix. Erreurs : `{"error": "message"}` avec le code HTTP adapté (401, 404, 409, 429, 502…).

## Système

| Méthode | Route | Réponse |
| --- | --- | --- |
| GET | `/healthz` | `{ok, version}` (sans authentification) |
| GET | `/api/v1/info` | nom, version, hôte (OS, noyau, CPU, arch), version Docker, seuils, `push_enabled` |
| GET | `/api/v1/overview` | `status` (`ok`/`warning`/`critical`), `issues[]`, `system`, compteurs conteneurs et checks |
| GET | `/api/v1/system` | instantané : `cpu`, `cores[]`, `mem`, `mem_used`, `mem_total`, `disk`, `disks[]`, `net_rx`/`net_tx` (o/s), `load1/5/15`, `uptime`, `temp` |
| GET | `/api/v1/system/history?range=1h\|6h\|24h\|7d` | `points[]` : `ts, cpu, mem, disk, rx, tx, load1, temp` (1 min, 2 min, 5 min ou 1 h par point) |

## Conteneurs

| Méthode | Route | Réponse |
| --- | --- | --- |
| GET | `/api/v1/containers` | liste : `id, name, image, state, status, health, cpu, mem, mem_used, mem_limit, net_rx, net_tx, started_at, exit_code, restart_count, problem, project, ports…` |
| GET | `/api/v1/containers/{id\|nom}` | `container` + `networks`, `mounts`, `health` (dernière sortie du healthcheck) |
| GET | `/api/v1/containers/{id}/history?range=…` | `points[]` : `ts, cpu, mem, mem_bytes` |
| GET | `/api/v1/containers/{id}/logs?tail=300` | `lines[]` : `ts, stream (stdout/stderr), line` |
| WS | `/api/v1/containers/{id}/logs/stream?tail=300` | messages `{"type":"lines","data":[…]}`, puis `{"type":"end"}` si le conteneur s'arrête |
| POST | `/api/v1/containers/{id}/start` · `/stop` · `/restart` | `{ok, state}` |
| POST | `/api/v1/containers/{id}/update` | `{pull_only?}` : télécharge l'image et recrée le conteneur (ou télécharge seulement). Sur l'agent lui-même : remplacement via un conteneur auxiliaire. Réponse 202 : l'opération (`job`) |
| GET | `/api/v1/containers/{id}/changelog` | notes de version GitHub de l'image : `source` (`github.com/owner/repo` ou `""`), `current` (version du conteneur ou `""`), `releases[]` (10 dernières : `tag, name, published_at, body` (Markdown, 6000 caractères au plus), `url, newer`), `error?`. Source : label `org.opencontainers.image.source`, sinon référence `ghcr.io/owner/repo`. Mis en cache 1 h par dépôt |
| GET | `/api/v1/containers/{id}/events?limit=80` | chronologie (30 jours) : `ts, action, detail, source` (`docker` ou `vigie`) |
| POST | `/api/v1/containers/{id}/mute` | `{muted}` : coupe ou réactive ses notifications, renvoie les réglages |

Chaque conteneur porte aussi `image_ref`, `update_available` (nouvelle image publiée sur le registre) et `muted`.

`problem` vaut `""`, `down` (arrêt anormal), `oom`, `restarting` ou `unhealthy`. CPU en % de la capacité totale de l'hôte.

## Retour à la version précédente (agent 0.6.0)

| Méthode | Route | Réponse |
| --- | --- | --- |
| GET | `/api/v1/containers/{id\|nom}/rollback` | point de retour du conteneur, 404 s'il n'en a pas |
| POST | `/api/v1/containers/{id\|nom}/rollback` | recrée le conteneur avec l'image d'avant sa dernière mise à jour (202 : `job`, `kind` et `action` `rollback`). 404 sans point de retour ; 409 si une opération est en cours, si le retour est déjà fait, si le conteneur a changé d'image depuis ou pour l'agent lui-même ; 403 si les mises à jour sont désactivées |
| DELETE | `/api/v1/containers/{id\|nom}/rollback/ignore` | autorise de nouveau la version écartée, renvoie le point |

Point de retour : `container`, `image_ref` (référence configurée, ex. `ghcr.io/x/y:latest`), `old_image_id`, `old_digests[]` (`dépôt@sha256:…`), `old_created` (construction de l'image, ms, 0 si inconnue), `old_version` (label `org.opencontainers.image.version`, `""` sinon), `new_image_id`, `new_digests[]`, `new_version`, `updated_at`, `rolled_back_at` (0 avant le retour), `ignored`, `ignored_digest` (digest publié de la version écartée, absent si inconnu), `state` (`ready` : retour possible, `rolled_back` : déjà revenu, `stale` : le conteneur a changé d'image depuis, par exemple redéployé depuis Dockhand), `old_label` / `new_label` (version, sinon début du digest ou de l'ID), `image_present` (ancienne image encore sur le disque), `downloadable` (re-téléchargeable par son digest).

- Un point est noté avant chaque mise à jour faite par Vigie (bouton, stack, tout mettre à jour, nuit), un par conteneur, conservé en base ; jamais pour l'agent (il a son propre retour automatique). Si la mise à jour échoue, le point précédent est rétabli.
- Avec `prune_images`, l'image d'avant la mise à jour est gardée ; celle du point précédent est supprimée. Après un retour, la version écartée est supprimée.
- Le retour repose localement la référence `image_ref` sur l'ancienne image (comme `docker tag`), puis recrée le conteneur comme une mise à jour : `Config.Image` garde la référence (Dockhand, compose, détection des mises à jour), réseaux, volumes et conteneurs liés suivent, et si l'ancienne version ne démarre pas, la version actuelle est rétablie avec sa référence. Ancienne image supprimée : téléchargement par digest, sinon échec explicite sans rien changer.
- La version écartée est ignorée : les mises à jour de stack, « tout mettre à jour » et de nuit ne la réinstallent pas (résultat « version ignorée »), `GET /api/v1/images` l'indique (`ignored: true`, `update_available: false`) comme chaque conteneur (`update_ignored`). Une version plus récente est proposée et installée normalement ; `POST /api/v1/containers/{id}/update` installe la version ignorée si on le demande.
- Chronologie : événements `update` (`detail` : « 1.0 → 2.0 ») et `rollback` (« 2.0 → 1.0 ») ; journal : `rollback` et `rollback-unignore` ; notification de fin comme les autres opérations.
- `GET /api/v1/cleanup` ajoute `rollback_images` (`count, size`) : images gardées pour revenir en arrière, comprises dans `images`. Le nettoyage les supprime ; le retour les re-télécharge alors par leur digest.
- Alerte de surconsommation (`regression`) : quand un retour est possible, la notification push porte `action: "rollback"` (bouton « Revenir à la version précédente ») et son texte le propose.

## Checks HTTP

| Méthode | Route | Corps / réponse |
| --- | --- | --- |
| GET | `/api/v1/checks` | état de chaque check : `state` (`up`/`down`/`pending`/`disabled`), `last`, `recent[]` (40 derniers), `uptime_24h`, `uptime_7d`, `latency_24h`, `consecutive_failures`, `cert_expires` (fin de validité du certificat HTTPS, ms) |
| POST | `/api/v1/checks` | `{name, url, expected_status, keyword, interval_sec, timeout_ms, enabled, kind, stremio_id}` |
| PUT | `/api/v1/checks/{id}` | idem (`kind` et `stremio_id` absents : valeurs actuelles conservées) |
| DELETE | `/api/v1/checks/{id}` | |
| POST | `/api/v1/checks/{id}/run` | exécute immédiatement, renvoie l'état |
| GET | `/api/v1/checks/{id}/history?range=24h\|7d\|30d\|90d&buckets=48` | `buckets[]` (`ts, total, ok, latency`), `uptime`, `latency` |

`kind` vaut `http` (défaut) ou `stremio`. En mode `stremio`, `url` est le manifest de l'addon (`…/manifest.json`, celui installé dans Stremio, configuration comprise). L'agent teste ce que l'addon fournit : les liens de `stremio_id` (IMDb, `tt0111161` par défaut ; `tt0903747:1:1` pour un épisode) s'il déclare `stream` pour ce type et ce préfixe, sinon la fiche (`meta`), sinon le premier catalogue consultable sans paramètre, sinon le manifest seul. `expected_status` et `keyword` sont ignorés ; intervalle minimum 300 s, délai minimum 30 s. Chaque résultat (`last`, `recent[]`) porte `probe` (`stream`, `meta`, `catalog` ou `manifest`) et `links`, le nombre de liens lisibles trouvés. Erreurs explicites : page HTML au lieu du manifest, addon non configuré (`configurationRequired`), aucun lien (avec le message de l'addon s'il en renvoie un), fiche absente, catalogue vide.

## Notifications, alertes, journal

| Méthode | Route | |
| --- | --- | --- |
| POST | `/api/v1/devices` | `{token, name}` : enregistre le jeton FCM de l'appareil |
| DELETE | `/api/v1/devices` | `{token}` |
| POST | `/api/v1/devices/test` | `{token?}` : notification de test |
| GET | `/api/v1/alerts?limit=50` | historique des alertes (30 jours) |
| GET | `/api/v1/logwatch` | alertes sur les logs (`log_rules` des réglages) : `[{rule_id, container, active, matches_24h, last_match, last_line}]` ; `active` : logs du conteneur en cours de lecture |
| GET | `/api/v1/audit?limit=100` | journal des actions (90 jours) |
| GET | `/api/v1/autoheal` | réparations automatiques des dernières 24 h (50 au plus, la plus récente en premier) : `[{ts, container, reason, action, ok, error?}]` ; `reason` : `unhealthy`, `down` ou `oom` ; `action` : `restart` ou `start` ; `ok` : conteneur revenu (en marche, et en bonne santé s'il a un healthcheck) |
| POST | `/api/v1/stacks/{projet}/{action}` | `action` : `update`, `pull`, `restart`, `start`, `stop` sur tous les conteneurs du projet compose (202 : `job`) |
| POST | `/api/v1/updates/apply` | met à jour tous les conteneurs qui ont une nouvelle image (202 : `job`) |
| POST | `/api/v1/agent/update` | met à jour l'agent (202 : `job`) |
| GET | `/api/v1/jobs` | opération en cours puis les 20 dernières : `id, kind, action, target, phase, progress, message, done, ok, error, results[]` |
| GET / PUT | `/api/v1/settings` | `{cpu, ram, disk, duration_min, quiet_enabled, quiet_start, quiet_end, muted[], image_updates, oracle_watch, cert_days, prune_images, auto_update, auto_update_time, auto_update_all, auto_update_list[], auto_update_agent, net_quota_tb, net_quota_alert, maintenance_until, log_rules[], weekly_report}` (champs absents : valeur actuelle conservée) |
| POST | `/api/v1/maintenance` | `{minutes}` (0 à 1440, 0 : fin) : aucune notification jusqu'à `maintenance_until`, puis un bilan des alertes survenues ; renvoie les réglages |
| GET | `/api/v1/cleanup` | espace récupérable : `images, volumes, build_cache` (`count, size`), `volumes_supported` (Docker ≥ 23) |
| POST | `/api/v1/cleanup` | `{images, volumes, build_cache}` : supprime les images sans conteneur, les volumes anonymes orphelins (jamais les volumes nommés), le cache de construction (202 : `job`) |
| GET | `/api/v1/oracle` | risque de récupération Oracle Always Free : `applicable, risk (low/medium/high/unknown), days, cpu, mem, net, net_mbps, summary` |
| GET | `/api/v1/images` | dernier résultat de la vérification des images, par conteneur |
| POST | `/api/v1/images/check` | interroge les registres maintenant |
| GET | `/api/v1/traffic` | trafic de l'hôte du mois en cours (heure locale) : `month`, `tx`, `rx`, `quota` (octets sortants autorisés, 0 sans quota), `pct`, `projected_tx` (sortant prévu en fin de mois au rythme actuel), `days_left`, `history[]` (`month, tx, rx`, 12 derniers mois) |
| GET | `/api/v1/top?range=24h\|7d` | consommation des conteneurs présents sur la période : `name, cpu_avg, cpu_max` (%), `mem_avg` (octets), `rx_bytes, tx_bytes` |
| GET | `/api/v1/report?days=7` | rapport de la période (1 à 7 jours) : `services` (`uptime` moyen, `worst`, `list[]`), `incidents` (`total`, `by_kind[]` : `kind, label, count`, `recent[]`), `containers` (`crashes`, `restarts`, `list[]`), `updates[]`, `disk` (`pct, used, total, change`), `traffic` (`rx, tx, month_tx, quota, month_pct`, absent sans historique), `oracle` (instances Oracle seulement) |

Quota de trafic (`net_quota_tb`, `net_quota_alert`) : alerte `traffic` au seuil choisi (warning) puis à 100 % (critical), une fois par mois et par niveau. Rapport hebdomadaire (`weekly_report`) : notification `report` le dimanche à 19:00 (heure locale), une fois par semaine.

Réparation automatique (réglages `auto_heal`, `auto_heal_all`, `auto_heal_list[]`, `auto_heal_delay` en minutes (1 à 60, 3 par défaut), `auto_heal_max` (1 à 10, 3 par défaut)) : pour les conteneurs retenus, jamais l'agent ni un conteneur ignoré, arrêté depuis l'appli ou visé par une action depuis moins de 2 min, l'agent redémarre (`restart`) un conteneur dont le healthcheck échoue depuis `auto_heal_delay` minutes, et relance (`start`) un conteneur arrêté anormalement (code de sortie autre que 0, 130, 137 et 143, ou OOM) et toujours arrêté 20 s plus tard, quelle que soit sa politique de redémarrage. Au plus `auto_heal_max` réparations par heure glissante et par conteneur ; au-delà, alerte `autoheal` critique « Réparation automatique abandonnée », une seule, close quand le conteneur fonctionne de nouveau. Chaque réparation donne une notification `autoheal` (info), une entrée `autoheal` au journal (`ip` : `agent`, `ua` : `vigie`) et un événement `autoheal` dans la chronologie du conteneur (`detail` : la raison).

## Accès partagés (lecture seule, agent 0.5.0)

| Méthode | Route | |
| --- | --- | --- |
| GET | `/api/v1/tokens` | `[{id, label, created, last_used}]` |
| POST | `/api/v1/tokens` | `{label}` (40 caractères au plus, 20 accès au plus) → 201 `{id, label, token, link, qr_png}` : le jeton (`ro_…`) n'est renvoyé qu'ici ; `link` = `vigie://connect?name=…&url=…&token=…`, `qr_png` = PNG en base64 |
| DELETE | `/api/v1/tokens/{id}` | révoque l'accès |

Seule l'empreinte SHA-256 des jetons est conservée. `GET /api/v1/info` et le message WebSocket `hello` indiquent `role` : `admin` (token principal) ou `readonly`. Un accès `readonly` n'a droit qu'aux `GET` (et à l'enregistrement de son téléphone, `/api/v1/devices`), sauf `/api/v1/tokens`, `/api/v1/audit`, `/api/v1/channels`, `/api/v1/debrid`, `/api/v1/backups` et les logs des conteneurs : 403 `accès en lecture seule`.

Alerte de surconsommation (réglage `regression_alert`, activé par défaut) : 6 h après une mise à jour (hors 15 premières minutes), la moyenne de chaque conteneur mis à jour est comparée à celle des 24 h précédant la mise à jour ; alerte si la mémoire dépasse ×1,5 et +100 Mio, ou le CPU ×2 et +5 points.

## Canaux d'alerte et comptes debrid

Les jetons et clés restent sur l'agent. Les réponses ne les renvoient que masqués (`••••` suivi des 4 derniers caractères) ; un `PUT` qui renvoie la valeur masquée ou vide conserve le secret enregistré. Ils n'apparaissent ni dans les logs ni dans le journal des actions, et uniquement sous `/api/v1/channels` et `/api/v1/debrid`.

| Méthode | Route | |
| --- | --- | --- |
| GET | `/api/v1/channels` | `{channels: [{type, enabled, min_level, …}]}` : `type` `telegram` (`bot_token`, `chat_id`), `discord` (`webhook_url`) ou `ntfy` (`server`, `topic`, `token` facultatif) ; `min_level` `all` (toutes les alertes) ou `critical` |
| PUT | `/api/v1/channels` | même format, configuration complète : un canal par type, un canal absent est supprimé avec ses secrets. Renvoie la configuration masquée |
| POST | `/api/v1/channels/test` | `{type?}` : message d'essai au canal demandé (même désactivé) ou à tous les canaux actifs. Réponse `{results: [{type, ok, error?}]}` |
| GET | `/api/v1/debrid` | `{accounts: [{id, provider, label, key, ok, premium, expires, days_left, username?, error?, refused, checked_at}], services: [{provider, name, state, detail?, latency_ms, since, checked_at, failures, uptime_24h, history: [{ts, s, ms}], incidents: [{start, end, cause}]}]}` (`services` : agent 0.6.0) |
| PUT | `/api/v1/debrid/accounts` | `[{id?, provider, label, key}]` : liste complète (`provider` : `realdebrid`, `alldebrid`, `torbox`, `premiumize` ; sans `id` connu, nouveau compte et clé obligatoire), puis vérification. Renvoie l'état |
| POST | `/api/v1/debrid/check` | vérifie tous les comptes maintenant, renvoie l'état |

Canaux : toute alerte qui donne lieu à une notification push (hors heures calmes et maintenance, résumés compris) part aussi vers les canaux actifs, même sans Firebase. Envoi en arrière-plan, 10 s au plus par essai, 2 nouvelles tentatives en cas d'erreur passagère. Telegram : message HTML ; Discord : embed coloré selon la sévérité, sans mentions ; ntfy : publication JSON, priorité 4 (critique), 3 ou 2 (retour à la normale) et étiquette émoji.

Debrid : vérification une minute après le démarrage, puis toutes les 5 min (6 h avant la 0.6.0). `expires` : fin du premium (ms, 0 si inconnue), `days_left` : jours entiers restants, `refused` : clé refusée par le fournisseur, `checked_at` : 0 avant la première vérification. Alertes `debrid` : premium qui expire dans 7 jours (warning), dans 1 jour (critical), expiré ou clé refusée (critical), une fois par seuil et par échéance, même après un redémarrage. État des services (`services`, un par fournisseur ayant au moins un compte) : `up` si l'un des comptes a obtenu une réponse (même un refus de clé), `degraded` si la réponse est lente (plus de 5 s), limitée (429) ou inattendue, `down` si le service est injoignable (réseau, délai dépassé, code 5xx) ; après un premier échec, nouvelle vérification une minute plus tard. `failures` : vérifications en panne d'affilée ; `history` : 24 h ; `incidents` : 30 jours (`end` à 0 : en cours). Alerte `debrid` critique quand la panne est confirmée (2 vérifications d'affilée), puis résolution (réglage `debrid_down_alert`, activé par défaut).

## Sauvegardes (agent 0.5.0)

Toutes les routes sont sous `/api/v1/backups`.

| Méthode | Route | Corps / réponse |
| --- | --- | --- |
| GET | `/api/v1/backups/config` | `{config, next_run}` : `config` = `{schedule, time, retention, endpoint, region, bucket, prefix, access_key, secret_key, items[]}`, `next_run` = prochaine sauvegarde planifiée (ms, 0 si `schedule` est faux) |
| PUT | `/api/v1/backups/config` | `config` (champs absents : valeur actuelle conservée), renvoie `{config, next_run}` |
| GET | `/api/v1/backups/items` | éléments sauvegardables : `id, kind (vigie/volume/bind), name, key (dossier dans le stockage), path (point de montage), containers[], size` (octets, estimée : taille du volume selon Docker, sinon celle de la dernière copie ; 0 si inconnue) |
| POST | `/api/v1/backups/run` | lance une sauvegarde (202 : `job`, `kind: "backup"`) |
| POST | `/api/v1/backups/test` | `config` facultative, pas enregistrée : écrit, liste puis supprime `<prefix>/.vigie-test`. Réponse 200 `{ok, message}` |
| GET | `/api/v1/backups/history` | 30 dernières sauvegardes, la plus récente en premier : `ts, duration (ms), size (octets envoyés), ok, error?, trigger (manual/schedule), items[]` (`id, name, key?, size, raw, ok, error?`) |
| GET | `/api/v1/backups/objects` | copies présentes dans le stockage : `key, item, size, ts` (5000 au plus). 409 si le stockage n'est pas configuré, 502 s'il répond en erreur |

- `items[]` de la configuration : `{id, stop}`. `id` vaut `vigie` (base SQLite et fichiers de l'agent), `volume:<nom>` (volume nommé) ou `bind:<chemin sur l'hôte>` (dossier monté dans un conteneur). `stop` : les conteneurs en marche qui l'utilisent sont arrêtés pendant la copie, puis toujours redémarrés.
- `time` : HH:MM selon `TZ` ; `retention` : copies gardées par élément (1 à 365, 7 par défaut) ; `prefix` : dossier dans le bucket (`vigie` par défaut, peut être vide) ; `endpoint` : `https://hôte` sans chemin (adressage « path-style »).
- `access_key` et `secret_key` ne sont jamais renvoyées : `"••••••••"` si elles sont enregistrées, `""` sinon. Envoyer cette valeur masquée (ou vide) conserve la clé. Changer `endpoint` exige d'envoyer à nouveau `secret_key`.
- Copies : `<prefix>/<key>/<AAAA-MM-JJ_HHMM>.tar.gz` (heure locale). La rétention ne supprime que les fichiers nommés ainsi.
- Planification (`schedule`) : chaque jour à `time`, une fois par jour au plus ; si une opération est en cours, 3 nouveaux essais à 10 min d'intervalle.
- Alertes : un échec envoie l'alerte `backup` (critical) même appli ouverte ; la réussite suivante la résout. Une réussite sans échec préalable n'est notifiée (`job`) que si l'appli est fermée.

## Stockage (agent 0.6.0)

| Méthode | Route | Réponse |
| --- | --- | --- |
| GET | `/api/v1/storage` | prévision du disque et dernier calcul des volumes (permis en lecture seule) |
| POST | `/api/v1/storage/refresh` | relance le calcul des volumes en arrière-plan. 202 : même réponse que `GET`, avec `refreshing: true` (un calcul déjà en cours n'est pas relancé) ; 429 (`Retry-After`) si le dernier a commencé il y a moins d'une minute |

Réponse :

- `disk` : `mount`, `total`, `used`, `free` (octets), `status` (`ok` : croissance nette, `stable` : pas de croissance significative ou baisse, `insufficient` : moins de 48 h de mesures), `trend_per_day` (octets par jour, `null` si `insufficient`), `days_left` (jours avant d'être plein au rythme actuel, `null` sauf `ok`), `history_hours` (recul couvert), `cleanups` (nettoyages écartés), `points[]` (`ts, used` : un point par heure sur 7 jours, puis l'instant présent).
- `volumes[]` (volumes nommés, du plus gros au plus petit) : `name`, `project` (label compose, absent sinon), `size`, `containers[]` (conteneurs qui le montent, même arrêtés), `growth_day` et `growth_week` (croissance ramenée à 24 h et à 7 jours, `null` sans mesure assez ancienne : 18 à 30 h, 5 à 8 jours), `fast` (`growth_day` ≥ `fast_growth`), `history[]` (`ts, size`, 7 jours, une mesure par heure au plus).
- `anonymous`, `images`, `build_cache` : `{count, size}` (images : place réelle des couches, cache : hors éléments partagés). `fast_growth` : seuil de croissance quotidienne (`volume_growth_gb`, 5 Go si l'alerte est désactivée). `volumes_at` : dernier calcul des volumes (ms, 0 avant le premier), `refreshing` : calcul en cours, `error` : échec du dernier calcul (les tailles précédentes restent), `computed_at`.

Prévision : à partir de l'historique système (occupation en %, tranches de 15 min, 7 derniers jours) convertie avec la taille actuelle du disque, réduite à un point par heure (médiane), pente de Theil-Sen (médiane des pentes entre tous les couples de points). Une baisse franche (≥ 512 Mio ou 0,5 % du disque en moins d'une heure, durable, qui ne défait pas une hausse brutale des 48 h précédentes) est un nettoyage : la suite de la série est relevée d'autant ; au-delà de 2 sur la période, les baisses font partie du rythme normal et rien n'est écarté. Croissance retenue si elle dépasse sur la période 256 Mio, 0,2 % du disque et deux fois le bruit (écart absolu médian autour de la droite) ; plus de 10 ans avant d'être plein : `stable`.

Volumes : `GET /system/df` (avec `type=image&type=volume&type=build-cache` à partir de Docker 23, pour ne pas mesurer les conteneurs) peu après le démarrage si le dernier calcul a plus de 6 h, puis toutes les 6 h, jamais deux à la fois, 10 min au plus. Volumes anonymes (label `com.docker.volume.anonymous` ou nom de 64 caractères hexadécimaux) comptés à part, volumes de taille inconnue (pilote distant) ignorés. Historique `volume_sizes` en base, 30 jours.

Alertes `storage` (réglages `disk_forecast_days`, 14 par défaut, 0 à 90, 0 : aucune ; `volume_growth_gb`, 5 par défaut, 0 à 1000, 0 : aucune) :

- disque plein dans moins de `disk_forecast_days` jours (évalué toutes les heures) : « Disque plein dans ~N jours » (warning, critical à 3 jours ou moins), « Au rythme actuel (+X Go/jour), le disque sera plein dans ~N jours. » avec le volume qui grossit le plus s'il compte pour au moins un quart de la croissance ; une fois, puis rappel tous les 3 jours au plus tant que la prévision reste sous le préavis ; résolue (« Disque : prévision rassurante ») quand elle repasse au-dessus d'une fois et demie le préavis (et d'au moins 2 jours de plus) ou que la croissance s'arrête. Mémorisée en base (redémarrages).
- volume qui grossit de plus de `volume_growth_gb` Go par jour (`growth_day`) : « Le volume X grossit vite », « +Y Go en 24 h, Z au total. Utilisé par … », une fois par jour au plus et par volume (23 h d'écart minimum), même après un redémarrage.

## Noms de domaine (agent 0.6.0)

| Méthode | Route | |
| --- | --- | --- |
| GET | `/api/v1/domains` | domaines suivis et dernier résultat connu, sans interroger les registres : `[{domain, source, services[], expires, days_left, registered, last_changed, registrar, status[], checked_at, error?, note?}]` (ordre alphabétique). Autorisé en lecture seule |
| POST | `/api/v1/domains/check` | interroge les registres maintenant (2 min au plus ; un domaine vérifié il y a moins d'une minute n'est pas réinterrogé), renvoie la liste |

- Domaines suivis : le domaine enregistrable (liste des suffixes publics) de l'hôte de chaque check HTTP ou Stremio et de `VIGIE_PUBLIC_URL` (`source` `auto`, `services` : noms des checks, `Vigie` pour l'adresse publique), plus le réglage `domains` (`source` `manual`, réduit lui aussi au domaine enregistrable : `www.exemple.co.uk` → `exemple.co.uk`). Jamais d'adresse IP, de `localhost`, de nom interne (`.local`, `.lan`, `.internal`…) ni de sous-domaine d'un suffixe privé (`duckdns.org`, `github.io`…) ; un tel domaine ajouté à la main apparaît avec `error`. 60 domaines au plus.
- RDAP : serveur du TLD d'après l'amorçage IANA (`https://data.iana.org/rdap/dns.json`, HTTPS seulement, gardé 7 jours en base), requête `<serveur>domain/<nom>` ; TLD absent de l'amorçage : `https://rdap.org/domain/<nom>`. 15 s et 1 Mio au plus par réponse, 4 requêtes en parallèle, 5 redirections au plus. Événements lus : `expiration` → `expires`, `registration` → `registered`, `last changed` → `last_changed` ; `registrar` : nom (`fn`, sinon `org`) de l'entité de rôle `registrar` ; `status` : statuts RDAP en minuscules (12 au plus). Dates acceptées : RFC 3339, sans fuseau (UTC), fuseau sans deux-points, espace au lieu de `T`, jour seul.
- `expires` : 0 si inconnue ; `days_left` : jours entiers restants (0 le dernier jour, négatif une fois expiré), `null` si la date est inconnue. Registre sans date d'expiration : `days_left` `null`, pas d'`error`, et `note` l'explique. `checked_at` : 0 avant la première vérification. `error` (en français) : registre injoignable, domaine introuvable, trop de requêtes, réponse illisible ou trop volumineuse… ; une erreur garde les dernières données valables.
- Vérification 2 min après le démarrage puis chaque heure : seuls les domaines vérifiés il y a plus d'un jour (6 h après une erreur) sont interrogés. Résultats conservés en base.
- Alertes `domain` (`target` : le domaine) à chaque passage : warning quand il reste au plus `domain_days` jours (réglage, 30 par défaut, 1 à 180), critical à 7 jours puis à l'expiration ; une seule fois par seuil et par date d'expiration, même après un redémarrage. Quand la date d'expiration recule (renouvellement), l'alerte est close (info, résolue) et les seuils sont réarmés.

## Page d'état publique (agent 0.6.0)

Configuration (token principal uniquement : 403 en lecture seule) :

| Méthode | Route | |
| --- | --- | --- |
| GET | `/api/v1/statuspage` | `{enabled, slug, title, description, items[], show_uptime, url}` |
| PUT | `/api/v1/statuspage` | même format (champs absents : valeur actuelle ; `slug` et `url` ignorés), renvoie la configuration |
| POST | `/api/v1/statuspage/regenerate` | nouvel identifiant secret (l'ancien lien cesse aussitôt de fonctionner), renvoie la configuration |

- `enabled` : faux par défaut. `slug` : identifiant secret de l'adresse, 22 caractères URL-safe (128 bits) tirés au hasard. `url` : `VIGIE_PUBLIC_URL` + `/status/<slug>` (vide si `VIGIE_PUBLIC_URL` n'est pas défini). `title` (80 caractères au plus, `État des services` si vide), `description` (300 au plus), `show_uptime` (barres et pourcentages, vrai par défaut).
- `items[]` (30 au plus, sans doublon) : `{kind, ref, label}` ; `kind` `check` (`ref` : identifiant du check, en texte) ou `container` (`ref` : nom du conteneur) ; `label` : nom public, obligatoire (60 caractères au plus). Configuration conservée en base (clé `status_page`), modifications inscrites au journal.

Pages publiques, **sans token** (seules exceptions avec `/healthz`) :

| Méthode | Route | |
| --- | --- | --- |
| GET | `/status/{slug}` | page HTML |
| GET | `/status/{slug}.json` | `{title, description, status, status_text, updated_at, items[], incidents[]}` |

- Page désactivée ou identifiant faux (comparé à temps constant) : même réponse `404` `Page introuvable`. La limitation de débit par IP s'applique ; une erreur d'identifiant ne compte pas comme un échec d'authentification. Toute autre méthode ou tout autre chemin sous `/status/` exige le token.
- `status` : `operational` (« Tous les services fonctionnent »), `degraded` (« Perturbations » : un élément n'est pas en ligne) ou `outage` (« Panne » : plus de la moitié des éléments hors ligne). `items[]` : `{name, status, uptime?, days?}` ; `status` `online`, `degraded`, `offline` ou `unknown`. Check : en ligne si le dernier résultat est bon, hors ligne après `VIGIE_CHECK_FAILURES` échecs consécutifs, dégradé avant, inconnu s'il est désactivé ou pas encore exécuté ; un check supprimé disparaît de la page. Conteneur : en ligne s'il tourne, dégradé si son healthcheck échoue ou démarre, hors ligne sinon (absent compris).
- Avec `show_uptime` : `uptime` (% sur les jours avec données) et `days[]` (90 jours, du plus ancien à aujourd'hui : `{date: "AAAA-MM-JJ" (UTC), uptime: % ou null}`). Checks : historique quotidien des checks (90 jours). Conteneurs : part des quarts d'heure mesurés par l'agent où le conteneur tournait (8 jours d'historique, à partir de sa première mesure).
- `incidents[]` (7 derniers jours, en cours d'abord, 20 au plus) : `{name, status (offline/degraded), start, end}` (`end` `null` : en cours). Checks : au moins `VIGIE_CHECK_FAILURES` échecs consécutifs. Conteneurs : arrêt (`die` ou pause → `start`) de 2 min ou plus, healthcheck en échec (`unhealthy` → `healthy`) de 2 min ou plus.
- Rien d'autre ne sort : ni adresse, ni IP, ni port, ni image, ni message d'erreur, ni nom de check ou de conteneur, ni nom du serveur ; seuls les noms publics choisis. Historique recalculé au plus une fois par minute.
- HTML généré par l'agent, sans JavaScript, CSS intégré, clair ou sombre selon le système, actualisation automatique toutes les 60 s, icône d'onglet de la couleur de l'état. En-têtes : `Content-Security-Policy: default-src 'none'; style-src 'sha256-…'; img-src data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `X-Frame-Options: DENY`, `X-Robots-Tag: noindex, nofollow`, `Cache-Control: max-age=30` (`no-store` pour la 404).

## Temps réel

`WS /api/v1/ws` envoie à la connexion `hello`, `overview`, `system`, `containers`, `checks`, `settings` (et l'opération en cours éventuelle), puis :

| `type` | Fréquence |
| --- | --- |
| `system` | toutes les 2 s |
| `containers` | toutes les 4 s (et immédiatement après un événement Docker) |
| `checks` | après chaque exécution |
| `overview` | quand l'état global change |
| `event` | événement Docker (`start`, `die`, `health_status…`) |
| `alert` | nouvelle alerte ou résolution |
| `settings` | réglages modifiés |
| `job` | avancement d'une mise à jour ou d'une action de stack (au plus 4 par seconde) |

Une seule opération à la fois (409 sinon). `GET /api/v1/info` indique `updates_enabled` et `last_self_update` (`ok`, `error`, `from`, `ts`).

Mises à jour de la nuit (réglages `auto_update`, `auto_update_time` en HH:MM selon `TZ`, `auto_update_all`, `auto_update_list[]`, `auto_update_agent`) : chaque jour à l'heure choisie, l'agent revérifie les images puis lance l'opération `kind: "auto"`, `target: "nightly"` sur les conteneurs retenus qui ont une nouvelle image (l'agent en dernier). Une fois par jour au plus, même après un redémarrage ; si une opération est déjà en cours, 3 nouveaux essais à 10 min d'intervalle.

Format : `{"type": "...", "data": ...}`. Un ping WebSocket est envoyé toutes les 25 s.

Message push FCM (données) : `type, alert_id, kind, severity, target, title, body, resolved, server, url, ts`, plus `container_id` ou `check_id`.
