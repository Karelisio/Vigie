# API de l'agent Vigie (v1)

Base : `https://vigie.example.com`. Toutes les routes sauf `/healthz` exigent `Authorization: Bearer <token>`.
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
| GET | `/api/v1/debrid` | `{accounts: [{id, provider, label, key, ok, premium, expires, days_left, username?, error?, refused, checked_at}]}` |
| PUT | `/api/v1/debrid/accounts` | `[{id?, provider, label, key}]` : liste complète (`provider` : `realdebrid`, `alldebrid`, `torbox`, `premiumize` ; sans `id` connu, nouveau compte et clé obligatoire), puis vérification. Renvoie l'état |
| POST | `/api/v1/debrid/check` | vérifie tous les comptes maintenant, renvoie l'état |

Canaux : toute alerte qui donne lieu à une notification push (hors heures calmes et maintenance, résumés compris) part aussi vers les canaux actifs, même sans Firebase. Envoi en arrière-plan, 10 s au plus par essai, 2 nouvelles tentatives en cas d'erreur passagère. Telegram : message HTML ; Discord : embed coloré selon la sévérité, sans mentions ; ntfy : publication JSON, priorité 4 (critique), 3 ou 2 (retour à la normale) et étiquette émoji.

Debrid : vérification une minute après le démarrage, puis toutes les 6 h. `expires` : fin du premium (ms, 0 si inconnue), `days_left` : jours entiers restants, `refused` : clé refusée par le fournisseur, `checked_at` : 0 avant la première vérification. Alertes `debrid` : premium qui expire dans 7 jours (warning), dans 1 jour (critical), expiré ou clé refusée (critical), une fois par seuil et par échéance, même après un redémarrage.

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
