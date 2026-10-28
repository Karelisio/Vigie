package sshwatch

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/karelisio/vigie/agent/internal/settings"
	"github.com/karelisio/vigie/agent/internal/store"
)

// env : faux système de fichiers de l'hôte, base et surveillance.
type env struct {
	t      *testing.T
	root   string
	st     *store.Store
	w      *Watcher
	mu     sync.Mutex
	set    settings.Settings
	logins []Login
}

func newEnv(t *testing.T, geoURL string) *env {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "var", "log"), 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e := &env{t: t, root: root, st: st, set: settings.Settings{SSHWatch: true, SSHLoginAlert: true}}
	if geoURL == "" {
		geoURL = "off"
	}
	e.w = New(st, e.settings, func(l Login) {
		e.mu.Lock()
		e.logins = append(e.logins, l)
		e.mu.Unlock()
	}, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Root: root, GeoURL: geoURL})
	return e
}

func (e *env) settings() settings.Settings {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.set
}

func (e *env) path(name string) string { return filepath.Join(e.root, "var", "log", name) }

func (e *env) write(name string, lines ...string) {
	e.t.Helper()
	if err := os.WriteFile(e.path(name), []byte(strings.Join(lines, "")), 0o640); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) appendLines(name string, lines ...string) {
	e.t.Helper()
	f, err := os.OpenFile(e.path(name), os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o640)
	if err != nil {
		e.t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(strings.Join(lines, "")); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) poll() Status {
	e.w.Poll(context.Background())
	return e.w.State()
}

func (e *env) stats() store.SSHStats {
	e.t.Helper()
	s, err := e.st.SSHStats(context.Background(), time.Now())
	if err != nil {
		e.t.Fatal(err)
	}
	return s
}

func (e *env) takeLogins() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for _, l := range e.logins {
		out = append(out, fmt.Sprintf("%s@%s new=%v fails=%d %s", l.User, l.IP, l.NewIP, l.Failures, l.CountryName))
	}
	e.logins = nil
	return out
}

// iso écrit une ligne au format de rsyslog récent, datée de « ago » avant maintenant.
func iso(ago time.Duration, pid int, msg string) string {
	return fmt.Sprintf("%s vps sshd[%d]: %s\n", time.Now().Add(-ago).Format("2006-01-02T15:04:05.000000-07:00"), pid, msg)
}

func fail(ago time.Duration, user, ip string) string {
	return iso(ago, int(ago.Seconds())%30000+1, "Failed password for "+user+" from "+ip+" port 22 ssh2")
}

func accepted(ago time.Duration, user, ip string) string {
	return iso(ago, 40000+int(ago.Seconds())%1000, "Accepted publickey for "+user+" from "+ip+" port 50000 ssh2: ED25519 SHA256:x")
}

func TestFollowRotationAndTruncation(t *testing.T) {
	e := newEnv(t, "")
	h := time.Hour
	// Première lecture : fichier tourné compressé (.1.gz) et fichier courant ; plus de 7 jours : ignoré.
	gz, err := os.Create(e.path("auth.log.1.gz"))
	if err != nil {
		t.Fatal(err)
	}
	zw := gzip.NewWriter(gz)
	fmt.Fprint(zw, fail(9*24*h, "root", "192.0.2.1"), fail(6*24*h, "root", "192.0.2.1"), fail(5*24*h, "admin", "192.0.2.2"))
	zw.Close()
	gz.Close()
	e.write("auth.log", fail(3*h, "root", "192.0.2.3"), accepted(2*h, "ubuntu", "203.0.113.7"), "ligne inachevée sans fin")
	if s := e.poll(); s.State != StateOK || s.Source != "/var/log/auth.log" || s.ReadAt == 0 {
		t.Fatalf("état : %+v", s)
	}
	if s := e.stats(); s.Failed7d != 3 || s.Accepted7d != 1 || s.Failed24h != 1 {
		t.Fatalf("première lecture : %+v", s)
	}
	if got := e.takeLogins(); len(got) != 0 {
		t.Fatalf("pas d'alerte pour l'historique : %q", got)
	}

	// La ligne inachevée est lue une fois terminée ; seules les nouvelles lignes comptent.
	e.appendLines("auth.log", "\n", fail(30*time.Minute, "root", "192.0.2.3"))
	e.poll()
	if s := e.stats(); s.Failed7d != 4 {
		t.Fatalf("lecture incrémentale : %+v", s)
	}

	// Rotation : une ligne écrite dans l'ancien fichier juste avant, puis un nouveau fichier.
	e.appendLines("auth.log", fail(20*time.Minute, "oracle", "192.0.2.4"))
	if err := os.Rename(e.path("auth.log"), e.path("auth.log.1")); err != nil {
		t.Fatal(err)
	}
	e.write("auth.log", fail(10*time.Minute, "pi", "192.0.2.5"))
	e.poll()
	if s := e.stats(); s.Failed7d != 6 {
		t.Fatalf("après rotation : %+v", s)
	}

	// Troncature (copytruncate) : relu depuis le début.
	if err := os.Truncate(e.path("auth.log"), 0); err != nil {
		t.Fatal(err)
	}
	e.appendLines("auth.log", fail(time.Minute, "test", "192.0.2.6"))
	e.poll()
	e.poll()
	if s := e.stats(); s.Failed7d != 7 || s.IPs7d != 6 {
		t.Fatalf("après troncature : %+v", s)
	}

	// Redémarrage de l'agent : reprise à la position mémorisée, sans doublon.
	again := New(e.st, e.settings, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Root: e.root, GeoURL: "off"})
	again.Poll(context.Background())
	if s := e.stats(); s.Failed7d != 7 {
		t.Fatalf("après redémarrage : %+v", s)
	}
	var cur cursor
	if err := json.Unmarshal([]byte(e.st.Get(context.Background(), cursorKey)), &cur); err != nil || cur.Offset == 0 || cur.Inode == 0 {
		t.Fatalf("position : %+v %v", cur, err)
	}
}

func TestLoginAlertsAndNewIP(t *testing.T) {
	e := newEnv(t, "")
	e.write("auth.log", accepted(48*time.Hour, "ubuntu", "203.0.113.7"))
	e.poll()

	e.appendLines("auth.log",
		accepted(5*time.Minute, "ubuntu", "203.0.113.7"), // adresse déjà vue
		fail(4*time.Minute, "root", "198.51.100.9"),
		fail(3*time.Minute, "ubuntu", "198.51.100.9"),
		accepted(2*time.Minute, "ubuntu", "198.51.100.9"), // nouvelle adresse, après 2 échecs
		accepted(time.Minute, "ubuntu", "198.51.100.9"),   // vue juste avant
		accepted(10*time.Minute, "admin", "10.0.0.20"),    // réseau privé
	)
	e.poll()
	got := e.takeLogins()
	want := []string{
		"ubuntu@203.0.113.7 new=false fails=0 ",
		"ubuntu@198.51.100.9 new=true fails=2 ",
		"ubuntu@198.51.100.9 new=false fails=2 ",
		"admin@10.0.0.20 new=true fails=0 Réseau privé",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("alertes :\n%s\nattendu :\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// Connexion ancienne (agent arrêté longtemps) ou alerte désactivée : rien.
	e.appendLines("auth.log", accepted(7*time.Hour, "ubuntu", "192.0.2.80"))
	e.poll()
	e.mu.Lock()
	e.set.SSHLoginAlert = false
	e.mu.Unlock()
	e.appendLines("auth.log", accepted(time.Minute, "ubuntu", "192.0.2.81"))
	e.poll()
	if got := e.takeLogins(); len(got) != 0 {
		t.Fatalf("aucune alerte attendue : %q", got)
	}

	// Au-delà de 5 connexions d'un coup : les suivantes sont résumées.
	e.mu.Lock()
	e.set.SSHLoginAlert = true
	e.mu.Unlock()
	var lines []string
	for i := 0; i < 8; i++ {
		lines = append(lines, accepted(time.Duration(10-i)*time.Second, "ci", "203.0.113.7"))
	}
	e.appendLines("auth.log", lines...)
	e.poll()
	e.mu.Lock()
	n, others := len(e.logins), e.logins[len(e.logins)-1].Others
	e.logins = nil
	e.mu.Unlock()
	if n != maxAlerts || others != 3 {
		t.Fatalf("%d alertes, %d autres", n, others)
	}

	// Liste des connexions réussies : la plus récente d'abord, nouvelle adresse marquée.
	sum, err := e.w.Summary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.Logins) < 4 || sum.Logins[0].User != "ci" {
		t.Fatalf("connexions : %+v", sum.Logins)
	}
	newIPs := 0
	for _, l := range sum.Logins {
		if l.NewIP {
			newIPs++
		}
	}
	// 203.0.113.7 (première fois, historique), 198.51.100.9, 10.0.0.20, 192.0.2.80, 192.0.2.81.
	if newIPs != 5 {
		t.Fatalf("nouvelles adresses : %d", newIPs)
	}
}

func TestPermissionState(t *testing.T) {
	e := newEnv(t, "")
	e.write("auth.log", fail(time.Minute, "root", "192.0.2.1"))
	if err := os.Chown(e.path("auth.log"), 0, 4); err != nil {
		t.Skip("changement de groupe impossible :", err)
	}
	if err := os.MkdirAll(filepath.Join(e.root, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.root, "etc", "group"), []byte("root:x:0:\nadm:x:4:syslog,ubuntu\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Sans capacité, root n'est ni propriétaire ni membre du groupe : EACCES.
	e.w.open = func(p string) (*os.File, error) {
		if strings.HasSuffix(p, "auth.log") {
			return nil, &fs.PathError{Op: "open", Path: p, Err: syscall.EACCES}
		}
		return os.Open(p)
	}
	s := e.poll()
	if s.State != StatePermission || s.Source != "/var/log/auth.log" {
		t.Fatalf("état : %+v", s)
	}
	if !strings.Contains(s.Fix, "group_add:") || !strings.Contains(s.Fix, `- "4"`) || !strings.Contains(s.Fix, "getent group adm") {
		t.Fatalf("correctif : %q", s.Fix)
	}
	if !strings.Contains(s.Message, "groupe adm, 4") || !strings.Contains(s.Message, "640") {
		t.Fatalf("message : %q", s.Message)
	}
	if st := e.stats(); st.Failed7d != 0 {
		t.Fatalf("rien ne doit être lu : %+v", st)
	}

	// Fichier lisible par son seul propriétaire (/var/log/secure en 600) : capacité DAC_READ_SEARCH.
	if err := os.Chmod(e.path("auth.log"), 0o600); err != nil {
		t.Fatal(err)
	}
	if s := e.poll(); s.State != StatePermission || !strings.Contains(s.Fix, "DAC_READ_SEARCH") {
		t.Fatalf("état : %+v", s)
	}

	// Droits corrigés : lecture normale.
	e.w.open = os.Open
	if s := e.poll(); s.State != StateOK {
		t.Fatalf("état : %+v", s)
	}
	if st := e.stats(); st.Failed7d != 1 {
		t.Fatalf("lecture : %+v", st)
	}
}

func TestMissingJournaldDisabledAndSecure(t *testing.T) {
	e := newEnv(t, "")
	if s := e.poll(); s.State != StateMissing {
		t.Fatalf("sans fichier : %+v", s)
	}
	if err := os.MkdirAll(filepath.Join(e.root, "var", "log", "journal"), 0o755); err != nil {
		t.Fatal(err)
	}
	if s := e.poll(); s.State != StateJournald || !strings.Contains(s.Message, "rsyslog") {
		t.Fatalf("journald : %+v", s)
	}
	// RHEL / Oracle Linux : /var/log/secure, syslog classique.
	e.write("secure", "Oct 28 09:00:00 vps sshd[1]: Invalid user oracle from 192.0.2.9 port 1\n")
	if s := e.poll(); s.State != StateOK || s.Source != "/var/log/secure" {
		t.Fatalf("secure : %+v", s)
	}
	// fail2ban présent.
	e.write("fail2ban.log", time.Now().Add(-time.Minute).Format("2006-01-02 15:04:05")+",123 fail2ban.actions [9]: NOTICE  [sshd] Ban 192.0.2.9\n")
	if s := e.poll(); s.Fail2ban != "ok" {
		t.Fatalf("fail2ban : %+v", s)
	}
	if st := e.stats(); st.Bans7d != 1 {
		t.Fatalf("bannissements : %+v", st)
	}
	e.mu.Lock()
	e.set.SSHWatch = false
	e.mu.Unlock()
	if s := e.poll(); s.State != StateDisabled {
		t.Fatalf("désactivé : %+v", s)
	}

	none := newEnv(t, "")
	if err := os.RemoveAll(filepath.Join(none.root, "var")); err != nil {
		t.Fatal(err)
	}
	if s := none.poll(); s.State != StateMissing || !strings.Contains(s.Message, "/:/host/root:ro") {
		t.Fatalf("hôte non monté : %+v", s)
	}
}

func TestAggregates(t *testing.T) {
	e := newEnv(t, "")
	ctx := context.Background()
	now := time.Now()
	ms := func(d time.Duration) int64 { return now.Add(-d).UnixMilli() }
	evs := []store.SSHEvent{
		{TS: ms(time.Hour), Kind: store.SSHInvalid, User: "admin", IP: "192.0.2.1", Count: 10},
		{TS: ms(2 * time.Hour), Kind: store.SSHFail, User: "root", IP: "192.0.2.1", Method: "password", Count: 5},
		{TS: ms(30 * time.Hour), Kind: store.SSHFail, User: "root", IP: "192.0.2.2", Count: 4},
		{TS: ms(3 * 24 * time.Hour), Kind: store.SSHInvalid, User: "test", IP: "192.0.2.3", Count: 1},
		{TS: ms(10 * 24 * time.Hour), Kind: store.SSHFail, User: "root", IP: "192.0.2.9", Count: 100}, // hors des 7 jours
		{TS: ms(time.Hour), Kind: store.SSHBan, IP: "192.0.2.1", Method: "sshd", Count: 1},
		{TS: ms(20 * time.Minute), Kind: store.SSHAccepted, User: "ubuntu", IP: "203.0.113.7", Method: "publickey", Count: 1, NewIP: true},
		{TS: ms(10 * time.Minute), Kind: store.SSHFail, User: "ubuntu", IP: "203.0.113.7", Count: 1},
	}
	if err := e.st.AddSSHEvents(ctx, evs, nil); err != nil {
		t.Fatal(err)
	}
	sum, err := e.w.Summary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s := sum.Stats
	if s.Failed24h != 16 || s.Failed7d != 21 || s.Accepted7d != 1 || s.Bans7d != 1 || s.IPs7d != 4 || s.Users7d != 4 || s.Since != ms(10*24*time.Hour) {
		t.Fatalf("statistiques : %+v", s)
	}
	top := sum.TopIPs
	if len(top) != 4 || top[0].IP != "192.0.2.1" || top[0].Count != 15 || !top[0].Banned || top[0].Last != ms(time.Hour) {
		t.Fatalf("top IP : %+v", top)
	}
	// À égalité, la plus récente d'abord ; une connexion a aussi réussi depuis 203.0.113.7.
	if top[2].IP != "203.0.113.7" || !top[2].Accepted || top[3].IP != "192.0.2.3" || top[3].Accepted {
		t.Fatalf("top IP : %+v", top)
	}
	users := sum.TopUsers
	if len(users) != 4 || users[0].User != "admin" || !users[0].Invalid || users[1].User != "root" || users[1].Count != 9 || users[1].Invalid {
		t.Fatalf("top utilisateurs : %+v", users)
	}
	if len(sum.Logins) != 1 || !sum.Logins[0].NewIP || sum.Logins[0].Method != "publickey" {
		t.Fatalf("connexions : %+v", sum.Logins)
	}
	total := 0
	for _, p := range sum.Hourly {
		total += p.Count
	}
	if len(sum.Hourly) != 24 || total != 16 || sum.Hourly[23].TS > now.UnixMilli() {
		t.Fatalf("série horaire : %d points, %d échecs", len(sum.Hourly), total)
	}
	total = 0
	for _, p := range sum.Daily {
		total += p.Count
	}
	if len(sum.Daily) != 30 || total != 121 {
		t.Fatalf("série journalière : %d points, %d échecs", len(sum.Daily), total)
	}
	list, err := e.w.Events(ctx, 3)
	if err != nil || len(list) != 3 || list[0].Kind != store.SSHFail || list[0].IP != "203.0.113.7" {
		t.Fatalf("événements : %+v %v", list, err)
	}
	week, err := e.st.SSHWeek(ctx, now.Add(-7*24*time.Hour))
	if err != nil || week.Failed != 21 || week.Accepted != 1 || week.NewIPs != 1 || week.Bans != 1 {
		t.Fatalf("semaine : %+v %v", week, err)
	}
	if err := e.st.PruneSSH(ctx, now.Add(25*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if s, _ := e.st.SSHStats(ctx, now.Add(25*24*time.Hour)); s.Since == 0 || s.Since < ms(6*24*time.Hour) {
		t.Fatalf("purge : %+v", s)
	}
}

// fakeGeo simule GeoJS : tableau pour plusieurs adresses, objet pour une seule.
type fakeGeo struct {
	mu       sync.Mutex
	requests []string
	fail     bool
}

func (f *fakeGeo) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ips := r.URL.Query().Get("ip")
	f.requests = append(f.requests, ips)
	if f.fail {
		http.Error(w, "indisponible", http.StatusServiceUnavailable)
		return
	}
	countries := map[string]string{"198.51.100.9": "FR", "203.0.113.7": "de", "192.0.2.1": "nil"}
	type entry struct {
		IP      string `json:"ip"`
		Country string `json:"country"`
		Name    string `json:"name"`
	}
	var list []entry
	for _, ip := range strings.Split(ips, ",") {
		list = append(list, entry{IP: ip, Country: countries[ip], Name: "x"})
	}
	if len(list) == 1 {
		_ = json.NewEncoder(w).Encode(list[0])
		return
	}
	_ = json.NewEncoder(w).Encode(list)
}

func (f *fakeGeo) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.requests...)
}

func TestGeoCacheBatchAndRateLimit(t *testing.T) {
	fg := &fakeGeo{}
	srv := httptest.NewServer(http.HandlerFunc(fg.handler))
	defer srv.Close()
	e := newEnv(t, srv.URL)
	now := time.Now()
	e.w.geo.now = func() time.Time { return now }
	ctx := context.Background()

	got := e.w.geo.lookup(ctx, []string{"198.51.100.9", "203.0.113.7", "10.0.0.5", "192.0.2.1", "198.51.100.9"}, false)
	if got["198.51.100.9"] != "FR" || got["203.0.113.7"] != "DE" || got["192.0.2.1"] != "" {
		t.Fatalf("pays : %v", got)
	}
	if _, ok := got["10.0.0.5"]; ok {
		t.Fatal("adresse privée envoyée au service")
	}
	if c := fg.calls(); len(c) != 1 || c[0] != "198.51.100.9,203.0.113.7,192.0.2.1" {
		t.Fatalf("requêtes : %q", c)
	}
	// Cache : plus de requête, même pour le pays inconnu (redemandé après 24 h seulement).
	e.w.geo.lookup(ctx, []string{"198.51.100.9", "192.0.2.1"}, false)
	if c := fg.calls(); len(c) != 1 {
		t.Fatalf("requêtes : %q", c)
	}
	// Limite de débit : une nouvelle adresse attend 10 s, sauf pour une alerte de connexion.
	if got := e.w.geo.lookup(ctx, []string{"198.51.100.20"}, false); got["198.51.100.20"] != "" || len(fg.calls()) != 1 {
		t.Fatalf("limite non respectée : %v %q", got, fg.calls())
	}
	e.w.geo.lookup(ctx, []string{"198.51.100.20"}, true)
	if c := fg.calls(); len(c) != 2 || c[1] != "198.51.100.20" {
		t.Fatalf("requête urgente : %q", c)
	}
	// Service en panne : pays inconnu, rien n'est mis en cache, nouvel essai plus tard.
	fg.mu.Lock()
	fg.fail = true
	fg.mu.Unlock()
	now = now.Add(time.Minute)
	if got := e.w.geo.lookup(ctx, []string{"198.51.100.30"}, false); got["198.51.100.30"] != "" {
		t.Fatalf("panne : %v", got)
	}
	fg.mu.Lock()
	fg.fail = false
	fg.mu.Unlock()
	now = now.Add(time.Minute)
	e.w.geo.lookup(ctx, []string{"198.51.100.30"}, false)
	if c := fg.calls(); len(c) != 4 {
		t.Fatalf("nouvel essai : %q", c)
	}
	// Au bout de 30 jours, le pays est redemandé.
	now = now.Add(31 * 24 * time.Hour)
	e.w.geo.lookup(ctx, []string{"198.51.100.9"}, false)
	if c := fg.calls(); len(c) != 5 {
		t.Fatalf("expiration : %q", c)
	}

	// Nom du pays dans l'alerte de connexion.
	e.write("auth.log", accepted(48*time.Hour, "x", "192.0.2.200"))
	e.poll()
	e.appendLines("auth.log", accepted(time.Minute, "ubuntu", "203.0.113.7"))
	e.poll()
	if got := e.takeLogins(); len(got) != 1 || !strings.HasSuffix(got[0], "Allemagne") {
		t.Fatalf("alerte : %q", got)
	}
	if CountryName("FR", "198.51.100.9") != "France" || CountryName("", "192.168.1.2") != "Réseau privé" || CountryName("", "198.51.100.1") != "" {
		t.Fatal("noms de pays")
	}
}

func TestGeoDisabled(t *testing.T) {
	e := newEnv(t, "off")
	if e.w.geo.enabled() {
		t.Fatal("pays activés")
	}
	if got := e.w.geo.lookup(context.Background(), []string{"198.51.100.9"}, true); len(got) != 0 {
		t.Fatalf("pays : %v", got)
	}
}
