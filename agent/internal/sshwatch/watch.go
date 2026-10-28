// Package sshwatch lit le journal d'authentification de l'hôte (/var/log/auth.log ou /var/log/secure,
// et /var/log/fail2ban.log s'il existe) : tentatives de connexion SSH, connexions réussies et
// bannissements, conservés 30 jours, avec le pays des adresses et une alerte à chaque connexion réussie.
package sshwatch

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/karelisio/vigie/agent/internal/settings"
	"github.com/karelisio/vigie/agent/internal/store"
)

const (
	pollEvery    = 15 * time.Second
	geoEvery     = time.Minute
	pruneEvery   = time.Hour
	backfillDays = 7             // historique repris à la première lecture
	alertWindow  = 6 * time.Hour // connexion plus ancienne (agent arrêté longtemps) : pas d'alerte
	maxAlerts    = 5             // alertes de connexion au plus par passage
	flushEvery   = 5000          // événements enregistrés par lot
	retention    = 30 * 24 * time.Hour
	cursorKey    = "ssh_cursor"
	f2bCursorKey = "ssh_f2b_cursor"
)

// États de la lecture du journal.
const (
	StateOK         = "ok"
	StatePermission = "permission" // fichier présent mais illisible (droits du conteneur)
	StateMissing    = "missing"    // aucun journal trouvé (ou système de fichiers de l'hôte non monté)
	StateJournald   = "journald"   // système en journald seul, sans fichier texte
	StateDisabled   = "disabled"   // surveillance désactivée dans les réglages
	StateError      = "error"
)

// Options de la surveillance.
type Options struct {
	Root   string // racine du système de fichiers de l'hôte (/host/root)
	GeoURL string // service des pays (défaut : GeoJS) ; « off » : pas de pays
	Now    func() time.Time
}

// Login est une connexion SSH réussie, signalée par une alerte.
type Login struct {
	TS          int64
	User        string
	IP          string
	Method      string
	Country     string // code ISO, "" si inconnu
	CountryName string // nom français, « Réseau privé », ou ""
	NewIP       bool   // jamais vue lors d'une connexion réussie
	Failures    int    // échecs de cette adresse sur 7 jours
	Others      int    // connexions suivantes du même passage, non détaillées
}

// Status décrit la lecture du journal.
type Status struct {
	State    string `json:"state"`
	Source   string `json:"source"`        // fichier lu, chemin sur l'hôte
	Message  string `json:"message"`       // explication et marche à suivre
	Fix      string `json:"fix,omitempty"` // extrait à ajouter au service dans le compose
	Fail2ban string `json:"fail2ban"`      // ok, absent ou permission
	ReadAt   int64  `json:"read_at"`       // dernière lecture réussie (ms)
}

type Watcher struct {
	root   string
	st     *store.Store
	set    func() settings.Settings
	notify func(Login)
	log    *slog.Logger
	now    func() time.Time
	geo    *geo
	parser *parser
	open   func(string) (*os.File, error) // os.Open (remplacé dans les tests)

	kick   chan struct{}
	pollMu sync.Mutex
	mu     sync.Mutex
	status Status
}

func New(st *store.Store, set func() settings.Settings, notify func(Login), log *slog.Logger, opt Options) *Watcher {
	if opt.Now == nil {
		opt.Now = time.Now
	}
	w := &Watcher{
		root: opt.Root, st: st, set: set, notify: notify, log: log, now: opt.Now,
		geo:    newGeo(opt.GeoURL, st, opt.Now),
		parser: newParser(hostLocation(opt.Root), opt.Now),
		kick:   make(chan struct{}, 1),
		open:   os.Open,
		status: Status{State: StateMissing, Message: "Lecture du journal pas encore faite.", Fail2ban: "absent"},
	}
	return w
}

// Kick demande une lecture prochaine (réglages modifiés).
func (w *Watcher) Kick() {
	select {
	case w.kick <- struct{}{}:
	default:
	}
}

// Run lit le journal toutes les 15 s, cherche les pays des adresses chaque minute et purge chaque heure.
func (w *Watcher) Run(ctx context.Context) {
	w.Poll(ctx)
	_ = w.st.PruneSSH(ctx, w.now())
	poll := time.NewTicker(pollEvery)
	geoT := time.NewTicker(geoEvery)
	prune := time.NewTicker(pruneEvery)
	defer poll.Stop()
	defer geoT.Stop()
	defer prune.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-poll.C:
			w.Poll(ctx)
		case <-w.kick:
			w.Poll(ctx)
		case <-geoT.C:
			w.resolveCountries(ctx)
		case <-prune.C:
			if err := w.st.PruneSSH(ctx, w.now()); err != nil {
				w.log.Warn("SSH : purge", "erreur", err)
			}
		}
	}
}

// State renvoie l'état de la lecture.
func (w *Watcher) State() Status {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status
}

func (w *Watcher) setStatus(s Status) {
	w.mu.Lock()
	prev := w.status
	w.status = s
	w.mu.Unlock()
	if prev.State != s.State {
		switch s.State {
		case StateOK:
			w.log.Info("SSH : lecture du journal", "fichier", s.Source)
		case StateDisabled:
		default:
			w.log.Warn("SSH : journal illisible", "état", s.State, "fichier", s.Source)
		}
	}
}

// locate trouve le journal d'authentification de l'hôte et vérifie qu'il est lisible.
func (w *Watcher) locate() (string, Status) {
	logDir := filepath.Join(w.root, "var", "log")
	if _, err := os.Stat(logDir); err != nil {
		return "", Status{State: StateMissing, Message: "Le système de fichiers de l'hôte n'est pas visible par l'agent : " +
			"ajoute « - /:/host/root:ro » à ses volumes, puis redéploie."}
	}
	for _, name := range []string{"auth.log", "secure"} {
		hostPath := "/var/log/" + name
		full := filepath.Join(logDir, name)
		fi, err := os.Stat(full)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err == nil {
			f, oerr := w.open(full)
			if oerr == nil {
				f.Close()
				return full, Status{State: StateOK, Source: hostPath}
			}
			err = oerr
		}
		if errors.Is(err, fs.ErrPermission) {
			return full, w.permission(hostPath, fi)
		}
		return full, Status{State: StateError, Source: hostPath, Message: "Lecture de " + hostPath + " impossible : " + errText(err)}
	}
	for _, d := range []string{"var/log/journal", "run/log/journal"} {
		if _, err := os.Stat(filepath.Join(w.root, d)); err == nil {
			return "", Status{State: StateJournald, Message: "Ce système n'écrit le journal SSH que dans journald : il n'y a pas de fichier " +
				"/var/log/auth.log à lire. Installe rsyslog pour l'obtenir (sudo apt install rsyslog, ou sudo dnf install rsyslog " +
				"sur Oracle Linux), puis sudo systemctl enable --now rsyslog."}
		}
	}
	return "", Status{State: StateMissing, Message: "Aucun journal d'authentification sur l'hôte (/var/log/auth.log ou /var/log/secure)."}
}

// permission explique le correctif exact selon les droits du fichier : groupe à ajouter au conteneur
// s'il est lisible par son groupe (syslog:adm 640 sur Debian/Ubuntu), sinon capacité DAC_READ_SEARCH.
func (w *Watcher) permission(hostPath string, fi os.FileInfo) Status {
	s := Status{State: StatePermission, Source: hostPath}
	gid, mode := -1, fs.FileMode(0)
	if fi != nil {
		mode = fi.Mode().Perm()
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			gid = int(st.Gid)
		}
	}
	if gid > 0 && mode&0o040 != 0 {
		group := groupName(w.root, gid)
		label := strconv.Itoa(gid)
		if group != "" {
			label = group + ", " + label
		}
		s.Message = fmt.Sprintf("L'agent n'a pas le droit de lire %s (groupe %s, droits %o) : il tourne sans aucune capacité. "+
			"Ajoute ce groupe au conteneur dans le compose, puis redéploie.", hostPath, label, mode)
		comment := "groupe de " + hostPath
		if group != "" {
			comment = "groupe " + group + " de l'hôte (getent group " + group + ")"
		}
		s.Fix = fmt.Sprintf("    group_add:\n      - \"%d\"   # %s", gid, comment)
		return s
	}
	s.Message = fmt.Sprintf("L'agent n'a pas le droit de lire %s (droits %o, lisible par son seul propriétaire). "+
		"Autorise-le à lire les fichiers de l'hôte en ajoutant cette capacité au conteneur, puis redéploie.", hostPath, mode)
	s.Fix = "    cap_add:\n      - DAC_READ_SEARCH   # lecture seule des fichiers de l'hôte"
	return s
}

// groupName cherche le nom d'un groupe dans /etc/group de l'hôte.
func groupName(root string, gid int) string {
	f, err := os.Open(filepath.Join(root, "etc", "group"))
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	id := strconv.Itoa(gid)
	for sc.Scan() {
		parts := strings.SplitN(sc.Text(), ":", 4)
		if len(parts) >= 3 && parts[2] == id {
			return parts[0]
		}
	}
	return ""
}

func errText(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}

// fail2ban renvoie le chemin du journal de fail2ban et son état (ok, absent, permission).
func (w *Watcher) fail2ban() (string, string) {
	full := filepath.Join(w.root, "var", "log", "fail2ban.log")
	f, err := w.open(full)
	switch {
	case err == nil:
		f.Close()
		return full, "ok"
	case errors.Is(err, fs.ErrPermission):
		return "", "permission"
	}
	return "", "absent"
}

// Poll lit les nouvelles lignes du journal (et de fail2ban), les enregistre et signale les connexions.
func (w *Watcher) Poll(ctx context.Context) {
	w.pollMu.Lock()
	defer w.pollMu.Unlock()
	if !w.set().SSHWatch {
		w.setStatus(Status{State: StateDisabled, Message: "Surveillance des connexions SSH désactivée.", Fail2ban: w.State().Fail2ban})
		return
	}
	path, status := w.locate()
	f2bPath, f2b := w.fail2ban()
	status.Fail2ban = f2b
	if status.State != StateOK {
		w.setStatus(status)
		return
	}
	now := w.now()
	logins, err := w.read(ctx, path, cursorKey, w.parser.parseAuth, now)
	if err == nil && f2bPath != "" {
		_, ferr := w.read(ctx, f2bPath, f2bCursorKey, w.parser.parseBan, now)
		if ferr != nil {
			w.log.Warn("SSH : lecture de fail2ban", "erreur", ferr)
		}
	}
	if err != nil {
		status = Status{State: StateError, Source: status.Source, Fail2ban: f2b, Message: "Lecture de " + status.Source + " impossible : " + errText(err)}
		if errors.Is(err, fs.ErrPermission) {
			fi, _ := os.Stat(path)
			status = w.permission(status.Source, fi)
			status.Fail2ban = f2b
		}
		w.setStatus(status)
		return
	}
	status.ReadAt = now.UnixMilli()
	w.setStatus(status)
	w.alert(ctx, logins, now)
}

// read lit un fichier depuis la position mémorisée (rotation et troncature comprises), enregistre les
// événements avec la nouvelle position, et renvoie les connexions réussies lues hors première lecture.
// La première lecture reprend aussi le fichier tourné (.1, ou .1.gz) : 7 jours d'historique.
func (w *Watcher) read(ctx context.Context, path, key string, parse func(string) []store.SSHEvent, now time.Time) ([]store.SSHEvent, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	ino := inodeOf(fi)
	raw := w.st.Get(ctx, key)
	first := raw == ""
	var cur cursor
	if !first {
		_ = json.Unmarshal([]byte(raw), &cur)
	}
	from := now.Add(-retention)
	if first {
		from = now.Add(-backfillDays * 24 * time.Hour)
	}
	b := &batch{w: w, ctx: ctx, from: from, keepLogins: !first}
	add := func(line string) { b.add(parse(line)) }

	if first {
		if key == cursorKey {
			// Lecture interrompue avant d'avoir enregistré sa position : on repart de zéro.
			if err := w.st.ClearSSHEvents(ctx); err != nil {
				return nil, err
			}
		}
		if _, err := os.Stat(path + ".1"); err == nil {
			if _, err := readTail(path+".1", maxBackfill, add); err != nil {
				w.log.Warn("SSH : fichier tourné illisible", "erreur", errText(err))
			}
		} else if _, err := os.Stat(path + ".1.gz"); err == nil {
			if err := readGzip(path+".1.gz", add); err != nil {
				w.log.Warn("SSH : fichier tourné illisible", "erreur", errText(err))
			}
		}
		if b.err != nil {
			return nil, b.err
		}
		size := fi.Size()
		start := max(0, size-maxBackfill)
		off, err := readLines(path, start, size-start, start > 0, add)
		if err != nil {
			return nil, err
		}
		cur = cursor{Inode: ino, Offset: off}
	} else {
		if cur.Inode != ino {
			// Fichier tourné : on finit l'ancien (devenu .1) avant de lire le nouveau depuis le début.
			if rfi, err := os.Stat(path + ".1"); err == nil && inodeOf(rfi) == cur.Inode && rfi.Size() > cur.Offset {
				if _, err := readLines(path+".1", cur.Offset, maxPerPoll, false, add); err != nil {
					w.log.Warn("SSH : fin du fichier tourné illisible", "erreur", errText(err))
				}
			}
			cur = cursor{Inode: ino}
		} else if fi.Size() < cur.Offset || !sameHead(path, cur.Head) {
			cur.Offset = 0 // tronqué (copytruncate), même si de nouvelles lignes l'ont déjà rallongé
		}
		off, err := readLines(path, cur.Offset, maxPerPoll, false, add)
		if err != nil {
			return nil, err
		}
		cur.Offset = off
	}
	cur.Head = headOf(path, cur.Offset)
	pos, _ := json.Marshal(cur)
	if err := b.flush(map[string]string{key: string(pos)}); err != nil {
		return nil, err
	}
	return b.logins, nil
}

// batch accumule les événements lus et les enregistre par lots (mémoire bornée à la première lecture).
type batch struct {
	w          *Watcher
	ctx        context.Context
	from       time.Time
	pending    []store.SSHEvent
	seen       map[string]bool // adresses de connexions réussies déjà vues dans cette lecture
	keepLogins bool
	logins     []store.SSHEvent
	err        error
}

func (b *batch) add(evs []store.SSHEvent) {
	for _, e := range evs {
		if e.TS >= b.from.UnixMilli() {
			b.pending = append(b.pending, e)
		}
	}
	if len(b.pending) >= flushEvery && b.err == nil {
		b.err = b.flush(nil)
	}
}

// flush enregistre les événements en attente (et, s'il y en a, les positions de lecture).
func (b *batch) flush(kv map[string]string) error {
	if b.err != nil {
		return b.err
	}
	evs := merge(b.pending)
	b.pending = b.pending[:0]
	if b.seen == nil {
		b.seen = map[string]bool{}
	}
	for i := range evs {
		e := &evs[i]
		if e.Kind != store.SSHAccepted || e.IP == "" {
			continue
		}
		known := b.seen[e.IP]
		if !known {
			k, err := b.w.st.SSHKnownIP(b.ctx, e.IP)
			if err != nil {
				return err
			}
			known = k
		}
		e.NewIP = !known
		b.seen[e.IP] = true
		if b.keepLogins {
			b.logins = append(b.logins, *e)
		}
	}
	if len(evs) == 0 && len(kv) == 0 {
		return nil
	}
	return b.w.st.AddSSHEvents(b.ctx, evs, kv)
}

// alert signale les connexions réussies récentes (au plus 5 par passage, puis un récapitulatif).
func (w *Watcher) alert(ctx context.Context, logins []store.SSHEvent, now time.Time) {
	if w.notify == nil || len(logins) == 0 || !w.set().SSHLoginAlert {
		return
	}
	var recent []store.SSHEvent
	for _, l := range logins {
		if now.Sub(time.UnixMilli(l.TS)) <= alertWindow {
			recent = append(recent, l)
		}
	}
	others := max(0, len(recent)-maxAlerts)
	recent = recent[:len(recent)-others]
	for i, l := range recent {
		lctx, cancel := context.WithTimeout(ctx, geoTimeout)
		country := w.geo.lookup(lctx, []string{l.IP}, true)[l.IP]
		failures, _ := w.st.SSHFailuresFrom(lctx, l.IP, now.Add(-7*24*time.Hour))
		cancel()
		login := Login{
			TS: l.TS, User: l.User, IP: l.IP, Method: l.Method, Country: country, CountryName: CountryName(country, l.IP),
			NewIP: l.NewIP, Failures: failures,
		}
		if i == len(recent)-1 {
			login.Others = others
		}
		w.notify(login)
	}
}

// resolveCountries cherche en tâche de fond les pays des adresses les plus actives et des connexions.
func (w *Watcher) resolveCountries(ctx context.Context) {
	if !w.geo.enabled() || !w.set().SSHWatch {
		return
	}
	now := w.now()
	var ips []string
	if top, err := w.st.SSHTopIPs(ctx, now.Add(-7*24*time.Hour), 30); err == nil {
		for _, t := range top {
			ips = append(ips, t.IP)
		}
	}
	if logins, err := w.st.SSHList(ctx, now.Add(-retention), 30, store.SSHAccepted); err == nil {
		for _, l := range logins {
			ips = append(ips, l.IP)
		}
	}
	if len(ips) > 0 {
		w.geo.lookup(ctx, ips, false)
	}
}

// ---- Résumé (GET /api/v1/ssh) ----

type IPStat struct {
	store.SSHIPCount
	Country     string `json:"country"`
	CountryName string `json:"country_name"`
}

type LoginRow struct {
	TS          int64  `json:"ts"`
	User        string `json:"user"`
	IP          string `json:"ip"`
	Method      string `json:"method"`
	Country     string `json:"country"`
	CountryName string `json:"country_name"`
	NewIP       bool   `json:"new_ip"`
}

type EventRow struct {
	store.SSHEvent
	Country     string `json:"country"`
	CountryName string `json:"country_name"`
}

type Point struct {
	TS    int64 `json:"ts"` // début de la tranche (ms)
	Count int   `json:"count"`
}

type Summary struct {
	Status
	Geo      bool                 `json:"geo"` // pays des adresses activés
	Stats    store.SSHStats       `json:"stats"`
	TopIPs   []IPStat             `json:"top_ips"`   // 7 jours
	TopUsers []store.SSHUserCount `json:"top_users"` // 7 jours
	Logins   []LoginRow           `json:"logins"`    // connexions réussies, 30 jours
	Hourly   []Point              `json:"hourly"`    // échecs par heure, 24 h
	Daily    []Point              `json:"daily"`     // échecs par jour, 30 jours
}

// Summary rassemble l'état, les statistiques et les listes affichées par l'appli.
func (w *Watcher) Summary(ctx context.Context) (Summary, error) {
	now := w.now()
	out := Summary{Status: w.State(), Geo: w.geo.enabled(), TopIPs: []IPStat{}, Logins: []LoginRow{}}
	var err error
	if out.Stats, err = w.st.SSHStats(ctx, now); err != nil {
		return out, err
	}
	week := now.Add(-7 * 24 * time.Hour)
	top, err := w.st.SSHTopIPs(ctx, week, 20)
	if err != nil {
		return out, err
	}
	if out.TopUsers, err = w.st.SSHTopUsers(ctx, week, 20); err != nil {
		return out, err
	}
	logins, err := w.st.SSHList(ctx, now.Add(-retention), 50, store.SSHAccepted)
	if err != nil {
		return out, err
	}
	var ips []string
	for _, t := range top {
		ips = append(ips, t.IP)
	}
	for _, l := range logins {
		ips = append(ips, l.IP)
	}
	countries := w.geo.cached(ctx, ips)
	for _, t := range top {
		c := countries[t.IP]
		out.TopIPs = append(out.TopIPs, IPStat{SSHIPCount: t, Country: c, CountryName: CountryName(c, t.IP)})
	}
	for _, l := range logins {
		c := countries[l.IP]
		out.Logins = append(out.Logins, LoginRow{
			TS: l.TS, User: l.User, IP: l.IP, Method: l.Method, Country: c, CountryName: CountryName(c, l.IP), NewIP: l.NewIP,
		})
	}
	hourEnd := now.Truncate(time.Hour).Add(time.Hour)
	if out.Hourly, err = w.series(ctx, hourEnd, time.Hour, 24); err != nil {
		return out, err
	}
	y, m, d := now.Date()
	dayEnd := time.Date(y, m, d+1, 0, 0, 0, 0, now.Location())
	if out.Daily, err = w.series(ctx, dayEnd, 24*time.Hour, 30); err != nil {
		return out, err
	}
	return out, nil
}

func (w *Watcher) series(ctx context.Context, end time.Time, bucket time.Duration, n int) ([]Point, error) {
	counts, err := w.st.SSHFailSeries(ctx, end, bucket, n)
	if err != nil {
		return nil, err
	}
	start := end.Add(-time.Duration(n) * bucket)
	out := make([]Point, n)
	for i, c := range counts {
		out[i] = Point{TS: start.Add(time.Duration(i) * bucket).UnixMilli(), Count: c}
	}
	return out, nil
}

// Events renvoie les derniers événements (GET /api/v1/ssh/events), du plus récent au plus ancien.
func (w *Watcher) Events(ctx context.Context, limit int) ([]EventRow, error) {
	list, err := w.st.SSHList(ctx, w.now().Add(-retention), limit)
	if err != nil {
		return nil, err
	}
	ips := make([]string, 0, len(list))
	for _, e := range list {
		ips = append(ips, e.IP)
	}
	countries := w.geo.cached(ctx, ips)
	out := make([]EventRow, 0, len(list))
	for _, e := range list {
		c := countries[e.IP]
		out = append(out, EventRow{SSHEvent: e, Country: c, CountryName: CountryName(c, e.IP)})
	}
	return out, nil
}
