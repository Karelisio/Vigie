package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/karelisio/vigie/agent/internal/docker"
	"github.com/karelisio/vigie/agent/internal/store"
	"github.com/karelisio/vigie/agent/internal/updater"
)

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type env struct {
	svc   *Service
	st    *store.Store
	upd   *updater.Manager
	dk    *fakeDocker
	s3    *fakeS3
	s3URL string
	dir   string

	mu    sync.Mutex
	notes []string
}

const anonymous = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{dir: t.TempDir()}
	st, err := store.Open(e.dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	fd, dsrv := newFakeDocker(t)
	hostRoot := bindMount("/", "/host/root")
	hostRoot["RW"] = false
	fd.containers = []*fakeCtr{
		{ID: "self", Name: "vigie-agent", Running: true, Mounts: []map[string]any{
			volumeMount("vigie_vigie-data", "/data"), bindMount("/var/run/docker.sock", "/var/run/docker.sock"), hostRoot,
		}},
		{ID: "s1", Name: "sonarr", Running: true, StopTimeout: 45, Mounts: []map[string]any{
			volumeMount("sonarr_config", "/config"), bindMount("/etc/localtime", "/etc/localtime"), volumeMount(anonymous, "/cache"),
		}},
		{ID: "r1", Name: "radarr", Running: false, Mounts: []map[string]any{
			bindMount("/srv/radarr", "/data"), volumeMount("sonarr_config", "/sonarr"),
		}},
		{ID: "d1", Name: "dockhand", Running: true, Mounts: []map[string]any{bindMount("/var/run/docker.sock", "/var/run/docker.sock")}},
	}
	// Le volume partagé est lu depuis le premier conteneur par ordre alphabétique : radarr, arrêté.
	fd.files["/sonarr"] = map[string]string{"config.xml": "<Config><ApiKey>abc</ApiKey></Config>", "sonarr.db": string(randomBytes(20000))}
	fd.files["/data"] = map[string]string{"films.txt": "Le Fabuleux Destin"}
	fd.volumes["sonarr_config"] = 12345
	fs, ssrv := newFakeS3(t)
	dk, err := docker.New(dsrv.URL)
	if err != nil {
		t.Fatal(err)
	}
	upd := updater.New(dk, discard(), updater.Hooks{}, updater.Options{Enabled: true})
	e.st, e.upd, e.dk, e.s3, e.s3URL = st, upd, fd, fs, ssrv.URL
	e.svc = New(st, dk, upd, discard(), Options{DataDir: e.dir, SelfID: "self", NoteAction: func(name, action string) {
		e.mu.Lock()
		e.notes = append(e.notes, name+" "+action)
		e.mu.Unlock()
	}})
	e.svc.part = 4096
	_ = os.WriteFile(filepath.Join(e.dir, "token"), []byte("jeton-api-secret"), 0o600)
	_ = os.WriteFile(filepath.Join(e.dir, "self-update.json"), []byte(`{"ok":true}`), 0o600)
	return e
}

// configure enregistre le stockage simulé et les éléments choisis.
func (e *env) configure(t *testing.T, items ...Selection) Config {
	t.Helper()
	c, sec := e.s3.config(e.s3URL)
	c.AccessKey, c.SecretKey, c.Retention, c.Items = sec.AccessKey, sec.SecretKey, 2, items
	out, err := e.svc.Update(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func waitJob(t *testing.T, m *updater.Manager) updater.Job {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if jobs := m.Jobs(); len(jobs) > 0 && jobs[0].Done {
			return jobs[0]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("sauvegarde non terminée")
	return updater.Job{}
}

// untar décompresse une copie et renvoie ses fichiers.
func untar(t *testing.T, gz []byte) map[string][]byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out[h.Name], _ = io.ReadAll(tr)
	}
}

func TestItems(t *testing.T) {
	e := newEnv(t)
	items, err := e.svc.Items(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	// L'agent, les sockets, le fuseau horaire et les volumes anonymes ne sont pas proposés.
	if got := strings.Join(ids, ","); got != "vigie,volume:sonarr_config,bind:/srv/radarr" {
		t.Fatalf("éléments : %s", got)
	}
	vol := items[1]
	if vol.Size != 12345 || vol.Key != "sonarr_config" || vol.Path != "/sonarr" || strings.Join(vol.Containers, ",") != "radarr,sonarr" {
		t.Errorf("volume : %+v", vol)
	}
	if items[2].Key != "srv_radarr" || items[0].Size == 0 {
		t.Errorf("dossier %+v, données de Vigie %+v", items[2], items[0])
	}
}

func TestBackupRun(t *testing.T) {
	e := newEnv(t)
	e.configure(t, Selection{ID: "volume:sonarr_config", Stop: true}, Selection{ID: "bind:/srv/radarr"}, Selection{ID: vigieID})
	old := time.Date(2026, 1, 1, 3, 30, 0, 0, time.UTC)
	e.s3.put("vigie/sonarr_config/2026-01-01_0330.tar.gz", []byte("ancienne"), old)
	e.s3.put("vigie/sonarr_config/2026-01-02_0330.tar.gz", []byte("moins ancienne"), old)

	if _, err := e.svc.Start("manual"); err != nil {
		t.Fatal(err)
	}
	j := waitJob(t, e.upd)
	if !j.OK || j.Kind != "backup" || !strings.HasPrefix(j.Message, "3 éléments sauvegardés") {
		t.Fatalf("opération : %+v", j)
	}

	// Arrêt de sonarr (délai d'arrêt du conteneur), lecture, puis redémarrage ; radarr, déjà arrêté, n'est pas démarré.
	if got := e.dk.log(); got != "stop sonarr t=45 | archive radarr /sonarr | start sonarr | archive radarr /data" {
		t.Fatalf("Docker : %s", got)
	}
	if strings.Join(e.notes, ",") != "sonarr stop,sonarr start" {
		t.Errorf("actions signalées : %v", e.notes)
	}

	stamp := time.UnixMilli(j.StartedAt).Format(stampLayout)
	keys := strings.Join(e.s3.keys(), ",")
	for _, k := range []string{"vigie/sonarr_config/2026-01-02_0330.tar.gz", "vigie/sonarr_config/" + stamp + ".tar.gz", "vigie/srv_radarr/" + stamp + ".tar.gz", "vigie/vigie/" + stamp + ".tar.gz"} {
		if !strings.Contains(keys, k) {
			t.Errorf("objet %s absent : %s", k, keys)
		}
	}
	if strings.Contains(keys, "2026-01-01_0330") {
		t.Errorf("rétention (2 copies) non appliquée : %s", keys)
	}
	if e.s3.maxBody > e.svc.part {
		t.Errorf("corps de %d octets pour des parties de %d", e.s3.maxBody, e.svc.part)
	}

	files := untar(t, e.s3.object("vigie/sonarr_config/"+stamp+".tar.gz"))
	if string(files["sonarr/config.xml"]) != "<Config><ApiKey>abc</ApiKey></Config>" || len(files["sonarr/sonarr.db"]) != 20000 {
		t.Errorf("contenu du volume : %v", len(files))
	}

	h := e.svc.History(context.Background())
	if len(h) != 1 || !h[0].OK || h[0].Trigger != "manual" || len(h[0].Items) != 3 || h[0].Size == 0 || h[0].Items[0].Raw < 20000 {
		t.Fatalf("historique : %+v", h)
	}
}

// Les données de Vigie : copie cohérente de la base, sans les clés du stockage ni le token.
func TestVigieData(t *testing.T) {
	e := newEnv(t)
	e.configure(t, Selection{ID: vigieID})
	if _, err := e.svc.Start("manual"); err != nil {
		t.Fatal(err)
	}
	if j := waitJob(t, e.upd); !j.OK {
		t.Fatalf("opération : %+v", j)
	}
	keys := e.s3.keys()
	if len(keys) != 1 {
		t.Fatalf("objets : %v", keys)
	}
	files := untar(t, e.s3.object(keys[0]))
	if _, ok := files["data/token"]; ok || string(files["data/self-update.json"]) != `{"ok":true}` || len(files) != 2 {
		t.Fatalf("fichiers : %v", len(files))
	}
	db := files["data/vigie.db"]
	if bytes.Contains(db, []byte(e.s3.secret)) || bytes.Contains(db, []byte(e.s3.access)) {
		t.Fatal("les clés du stockage ne doivent pas quitter l'agent")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "vigie.db"), db, 0o600); err != nil {
		t.Fatal(err)
	}
	restored, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if restored.Get(context.Background(), configKey) == "" || restored.Get(context.Background(), secretKey) != "" {
		t.Error("la base restaurée garde la configuration, sans les secrets")
	}
	if _, err := os.Stat(filepath.Join(e.dir, snapshotName)); !os.IsNotExist(err) {
		t.Error("copie temporaire de la base non supprimée")
	}
}

// Le conteneur arrêté pour la copie est toujours relancé, que la lecture ou l'envoi échoue.
func TestRestartGuaranteed(t *testing.T) {
	t.Run("lecture en échec", func(t *testing.T) {
		e := newEnv(t)
		e.dk.failPath["/sonarr"] = true
		e.configure(t, Selection{ID: "volume:sonarr_config", Stop: true}, Selection{ID: "bind:/srv/radarr"})
		if _, err := e.svc.Start("manual"); err != nil {
			t.Fatal(err)
		}
		j := waitJob(t, e.upd)
		if j.OK || !strings.Contains(j.Error, "1 élément sur 2 en échec") || !strings.Contains(j.Error, "archive impossible") {
			t.Fatalf("échec attendu : %+v", j)
		}
		if !e.dk.running("sonarr") || !strings.Contains(e.dk.log(), "start sonarr") {
			t.Fatalf("sonarr doit être relancé : %s", e.dk.log())
		}
		h := e.svc.History(context.Background())
		if len(h) != 1 || h[0].OK || h[0].Items[0].OK || !h[0].Items[1].OK {
			t.Fatalf("historique : %+v", h)
		}
	})
	t.Run("stockage en échec", func(t *testing.T) {
		e := newEnv(t)
		e.configure(t, Selection{ID: "volume:sonarr_config", Stop: true})
		e.s3.bucket = "supprimé"
		if _, err := e.svc.Start("manual"); err != nil {
			t.Fatal(err)
		}
		if j := waitJob(t, e.upd); j.OK || !strings.Contains(j.Error, "bucket introuvable") {
			t.Fatalf("échec attendu : %+v", j)
		}
		if !e.dk.running("sonarr") {
			t.Fatalf("sonarr doit être relancé : %s", e.dk.log())
		}
	})
	t.Run("redémarrage en échec", func(t *testing.T) {
		// Copie réussie mais conteneur non relancé : l'élément est en échec (alerte).
		e := newEnv(t)
		e.dk.failStart = true
		e.configure(t, Selection{ID: "volume:sonarr_config", Stop: true})
		if _, err := e.svc.Start("manual"); err != nil {
			t.Fatal(err)
		}
		if j := waitJob(t, e.upd); j.OK || !strings.Contains(j.Error, "redémarrage impossible : sonarr") {
			t.Fatalf("échec attendu : %+v", j)
		}
		if h := e.svc.History(context.Background()); h[0].Items[0].OK || h[0].Size != 0 {
			t.Fatalf("historique : %+v", h)
		}
	})
	t.Run("élément disparu", func(t *testing.T) {
		e := newEnv(t)
		e.configure(t, Selection{ID: "volume:disparu", Stop: true})
		if _, err := e.svc.Start("manual"); err != nil {
			t.Fatal(err)
		}
		if j := waitJob(t, e.upd); j.OK || !strings.Contains(j.Error, "introuvable") {
			t.Fatalf("échec attendu : %+v", j)
		}
	})
}

func TestStartRefusesIncompleteConfig(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.Start("manual"); err == nil || !strings.Contains(err.Error(), "stockage incomplet") {
		t.Fatalf("stockage non configuré : %v", err)
	}
	e.configure(t)
	if _, err := e.svc.Start("manual"); err == nil || !strings.Contains(err.Error(), "aucun élément") {
		t.Fatalf("aucun élément : %v", err)
	}
}

func TestObjectsAndTest(t *testing.T) {
	e := newEnv(t)
	cfg := e.configure(t, Selection{ID: vigieID})
	at := time.Date(2026, 10, 1, 3, 30, 0, 0, time.UTC)
	e.s3.put("vigie/vigie/2026-10-01_0330.tar.gz", []byte("a"), at)
	e.s3.put("vigie/vigie/2026-10-02_0330.tar.gz", []byte("bb"), at.AddDate(0, 0, 1))
	e.s3.put("vigie/sonarr_config/2026-10-01_0330.tar.gz", []byte("c"), at)
	e.s3.put("vigie/notes.txt", []byte("d"), at)
	objs, err := e.svc.Objects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != 3 || objs[0].Item != "sonarr_config" || objs[1].Key != "vigie/vigie/2026-10-02_0330.tar.gz" || objs[1].Size != 2 || objs[1].TS != at.AddDate(0, 0, 1).UnixMilli() {
		t.Fatalf("copies : %+v", objs)
	}

	// Test avec la configuration enregistrée (secrets masqués) : écrit puis supprime son fichier.
	msg, err := e.svc.Test(context.Background(), cfg)
	if err != nil || !strings.HasPrefix(msg, "Connexion réussie") {
		t.Fatalf("test : %q %v", msg, err)
	}
	if strings.Contains(strings.Join(e.s3.keys(), ","), ".vigie-test") {
		t.Error("fichier de test non supprimé")
	}
	cfg.SecretKey = "mauvaise"
	if _, err := e.svc.Test(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "clé secrète incorrecte") {
		t.Fatalf("clé incorrecte : %v", err)
	}
}
