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
| POST | `/api/v1/checks` | `{name, url, expected_status, keyword, interval_sec, timeout_ms, enabled}` |
| PUT | `/api/v1/checks/{id}` | idem |
| DELETE | `/api/v1/checks/{id}` | |
| POST | `/api/v1/checks/{id}/run` | exécute immédiatement, renvoie l'état |
| GET | `/api/v1/checks/{id}/history?range=24h\|7d\|30d\|90d&buckets=48` | `buckets[]` (`ts, total, ok, latency`), `uptime`, `latency` |

## Notifications, alertes, journal

| Méthode | Route | |
| --- | --- | --- |
| POST | `/api/v1/devices` | `{token, name}` : enregistre le jeton FCM de l'appareil |
| DELETE | `/api/v1/devices` | `{token}` |
| POST | `/api/v1/devices/test` | `{token?}` : notification de test |
| GET | `/api/v1/alerts?limit=50` | historique des alertes (30 jours) |
| GET | `/api/v1/audit?limit=100` | journal des actions (90 jours) |
| POST | `/api/v1/stacks/{projet}/{action}` | `action` : `update`, `pull`, `restart`, `start`, `stop` sur tous les conteneurs du projet compose (202 : `job`) |
| POST | `/api/v1/updates/apply` | met à jour tous les conteneurs qui ont une nouvelle image (202 : `job`) |
| POST | `/api/v1/agent/update` | met à jour l'agent (202 : `job`) |
| GET | `/api/v1/jobs` | opération en cours puis les 20 dernières : `id, kind, action, target, phase, progress, message, done, ok, error, results[]` |
| GET / PUT | `/api/v1/settings` | `{cpu, ram, disk, duration_min, quiet_enabled, quiet_start, quiet_end, muted[], image_updates, oracle_watch, cert_days, prune_images}` (champs absents : valeur actuelle conservée) |
| GET | `/api/v1/oracle` | risque de récupération Oracle Always Free : `applicable, risk (low/medium/high/unknown), days, cpu, mem, net, net_mbps, summary` |
| GET | `/api/v1/images` | dernier résultat de la vérification des images, par conteneur |
| POST | `/api/v1/images/check` | interroge les registres maintenant |

## Temps réel

`WS /api/v1/ws` envoie à la connexion `hello`, `overview`, `system`, `containers`, `checks`, puis :

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
