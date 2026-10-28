package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/karelisio/vigie/agent/internal/updater"
)

// restoreEnv : le volume sonarr_config est un vrai dossier, monté par radarr (arrêté) et sonarr.
type restoreEnv struct {
	*env
	vol string

	tmu       sync.Mutex
	transient []string
}

func newRestoreEnv(t *testing.T) *restoreEnv {
	t.Helper()
	e := &restoreEnv{env: newEnv(t), vol: t.TempDir()}
	e.dk.find("self").Entrypoint = []string{"/vigie-agent"}
	e.dk.dirs["sonarr_config"] = e.vol
	writeTree(t, e.vol, map[string]string{"config.xml": "<Config>v1</Config>", "db/sonarr.db": "base v1"})
	e.svc.poll, e.svc.settle = 5*time.Millisecond, 0
	e.svc.opt.SetTransient = func(name string, on bool) {
		e.tmu.Lock()
		defer e.tmu.Unlock()
		e.transient = append(e.transient, fmt.Sprintf("%s %t", strings.TrimRight(name, "0123456789"), on))
	}
	e.configure(t, Selection{ID: "volume:sonarr_config", Stop: true})
	return e
}

func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		mustWrite(t, filepath.Join(dir, name), content)
	}
}

// tree renvoie les fichiers d'un dossier : chemin relatif → contenu.
func tree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err != nil || !fi.Mode().IsRegular() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		out[rel] = readFile(t, p)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func sameTree(t *testing.T, got, want map[string]string) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("contenu %v, attendu %v", got, want)
	}
}

// backupNow fait une vraie sauvegarde et renvoie la clé de la copie de sonarr_config.
func (e *restoreEnv) backupNow(t *testing.T) string {
	t.Helper()
	if _, err := e.svc.Start("manual"); err != nil {
		t.Fatal(err)
	}
	j := waitJob(t, e.upd)
	if !j.OK {
		t.Fatalf("sauvegarde : %+v", j)
	}
	return "vigie/sonarr_config/" + time.UnixMilli(j.StartedAt).Format(stampLayout) + ".tar.gz"
}

func (e *restoreEnv) restore(t *testing.T, key string) updater.Job {
	t.Helper()
	e.dk.mu.Lock()
	e.dk.events = nil
	e.dk.mu.Unlock()
	if _, err := e.svc.Restore("volume:sonarr_config", key); err != nil {
		t.Fatal(err)
	}
	return waitJob(t, e.upd)
}

func (e *restoreEnv) safetyKeys() []string {
	var out []string
	for _, k := range e.s3.keys() {
		if strings.HasPrefix(k, "vigie/sonarr_config/"+safetyPrefix) {
			out = append(out, k)
		}
	}
	return out
}

// files décompresse une copie du stockage (sans le premier niveau, comme à la restauration).
func (e *restoreEnv) files(t *testing.T, key string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for name, b := range untar(t, e.s3.object(key)) {
		if _, rel, _ := strings.Cut(name, "/"); rel != "" && !strings.HasSuffix(rel, "/") {
			out[rel] = string(b)
		}
	}
	return out
}

func TestRestoreVolume(t *testing.T) {
	e := newRestoreEnv(t)
	v1 := tree(t, e.vol)
	copyKey := e.backupNow(t)

	// L'application continue : nouvelle configuration, fichier ajouté, base supprimée.
	writeTree(t, e.vol, map[string]string{"config.xml": "<Config>v2</Config>", "nouveau.txt": "ajouté après"})
	if err := os.RemoveAll(filepath.Join(e.vol, "db")); err != nil {
		t.Fatal(err)
	}
	v2 := tree(t, e.vol)

	j := e.restore(t, copyKey)
	if !j.OK || j.Kind != "restore" || j.Target != "sonarr_config" || !strings.HasPrefix(j.Message, "sonarr_config restauré : copie du ") ||
		!strings.Contains(j.Message, "relancé : sonarr") {
		t.Fatalf("opération : %+v", j)
	}
	sameTree(t, tree(t, e.vol), v1)

	// Copie déposée avant l'arrêt, copie de sécurité conteneurs arrêtés, puis remplacement et redémarrage.
	want := "create auxiliaire sonarr_config | dépôt auxiliaire sonarr_config | stop sonarr t=45 | archive radarr /sonarr | " +
		"exécution auxiliaire sonarr_config : code 0 | start sonarr | remove auxiliaire sonarr_config force=true v=true"
	if got := e.dk.log(); got != want {
		t.Fatalf("Docker :\n%s\nattendu :\n%s", got, want)
	}
	if !e.dk.running("sonarr") || e.dk.running("radarr") || e.dk.helpers() != 0 {
		t.Fatal("état des conteneurs")
	}
	if strings.Join(e.notes, ",") != "sonarr stop,sonarr start,sonarr stop,sonarr start" {
		t.Errorf("actions signalées : %v", e.notes)
	}
	if strings.Join(e.transient, ",") != "vigie-restauration- true,vigie-restauration- false" {
		t.Errorf("auxiliaire masqué : %v", e.transient)
	}

	// Copie de sécurité : l'état d'avant la restauration, à part, jamais touchée par la rétention.
	safety := e.safetyKeys()
	if len(safety) != 1 {
		t.Fatalf("copies de sécurité : %v", safety)
	}
	sameTree(t, e.files(t, safety[0]), v2)
	if err := e.svc.prune(context.Background(), newClient(t, e.s3, e.s3URL), "vigie/sonarr_config", 1); err != nil || len(e.safetyKeys()) != 1 {
		t.Fatalf("rétention : %v %v", err, e.safetyKeys())
	}
	objs, err := e.svc.Objects(context.Background())
	if err != nil || len(objs) != 2 || !objs[0].Safety || objs[0].Key != safety[0] || objs[1].Safety {
		t.Fatalf("copies listées : %+v %v", objs, err)
	}

	// Chronologie des conteneurs qui utilisent le volume.
	for _, name := range []string{"sonarr", "radarr"} {
		ev, _ := e.st.Events(context.Background(), name, 10)
		if len(ev) == 0 || ev[0].Action != "restore" || !strings.HasPrefix(ev[0].Detail, "copie du ") {
			t.Errorf("chronologie de %s : %+v", name, ev)
		}
	}

	// Auxiliaire : image de l'agent, sans réseau, sans accès à Docker ni secret.
	spec := e.dk.created[0]
	hc := spec["HostConfig"].(map[string]any)
	mount := hc["Mounts"].([]any)[0].(map[string]any)
	if spec["Image"] != "sha256:vigie" || spec["User"] != "0:0" || fmt.Sprint(spec["Env"]) != "[VIGIE_HELPER_RESTORE=/vigie-restore.tar.gz]" ||
		fmt.Sprint(spec["Entrypoint"]) != "[/vigie-agent]" || hc["NetworkMode"] != "none" || fmt.Sprint(hc["CapDrop"]) != "[ALL]" ||
		len(hc["Mounts"].([]any)) != 1 || mount["Type"] != "volume" || mount["Source"] != "sonarr_config" || mount["Target"] != "/target" ||
		spec["Labels"].(map[string]any)["vigie.ignore"] != "true" || hc["Binds"] != nil {
		t.Fatalf("auxiliaire : %v", spec)
	}
	raw, _ := json.Marshal(spec)
	if strings.Contains(string(raw), e.s3.secret) || strings.Contains(string(raw), e.s3.access) || strings.Contains(string(raw), "docker.sock") {
		t.Fatal("secret ou socket Docker transmis à l'auxiliaire")
	}

	// Annuler la dernière restauration : retour à l'état d'avant, une seule copie de sécurité gardée
	// (nouveau nom, même dans la même minute).
	j = e.restore(t, safety[0])
	if !j.OK || !strings.HasPrefix(j.Message, "Dernière restauration de sonarr_config annulée") {
		t.Fatalf("annulation : %+v", j)
	}
	sameTree(t, tree(t, e.vol), v2)
	if got := e.safetyKeys(); len(got) != 1 || got[0] == safety[0] {
		t.Fatalf("copies de sécurité après annulation : %v (avant : %s)", got, safety[0])
	} else {
		sameTree(t, e.files(t, got[0]), v1)
	}
	if ev, _ := e.st.Events(context.Background(), "sonarr", 1); ev[0].Detail == "" || !strings.HasPrefix(ev[0].Detail, "retour à la copie de sécurité du ") {
		t.Errorf("chronologie : %+v", ev)
	}
}

// Une archive qui échoue après le vidage : la copie de sécurité est remise.
func TestRestoreRollback(t *testing.T) {
	e := newRestoreEnv(t)
	before := tree(t, e.vol)
	key := "vigie/sonarr_config/2026-01-01_0330.tar.gz"
	e.s3.put(key, tarGz(t, dirEnt("sonarr/", 0o755), fileEnt("sonarr/a", "x"), hardEnt("sonarr/h", "sonarr/absent")), time.Now())

	j := e.restore(t, key)
	if j.OK || !strings.Contains(j.Error, "extraction") || !strings.Contains(j.Error, "état d'avant remis depuis la copie de sécurité") {
		t.Fatalf("échec attendu : %+v", j)
	}
	sameTree(t, tree(t, e.vol), before)
	log := e.dk.log()
	if !strings.Contains(log, "exécution auxiliaire sonarr_config : code 3") || !strings.Contains(log, "exécution auxiliaire sonarr_config : code 0") ||
		strings.Count(log, "remove auxiliaire") != 2 || !strings.HasSuffix(log, "start sonarr | remove auxiliaire sonarr_config force=true v=true") {
		t.Fatalf("Docker : %s", log)
	}
	if !e.dk.running("sonarr") || e.dk.helpers() != 0 {
		t.Fatal("sonarr doit être relancé et les auxiliaires supprimés")
	}
	if ev, _ := e.st.Events(context.Background(), "sonarr", 1); len(ev) == 0 || ev[0].Action != "restore_failed" {
		t.Errorf("chronologie : %+v", ev)
	}
}

// Une archive refusée avant tout changement : rien à remettre.
func TestRestoreRefusedArchive(t *testing.T) {
	e := newRestoreEnv(t)
	before := tree(t, e.vol)
	key := "vigie/sonarr_config/2026-01-01_0330.tar.gz"
	e.s3.put(key, tarGz(t, dirEnt("sonarr/", 0o755), linkEnt("sonarr/l", "/etc"), fileEnt("sonarr/l/cron", "x")), time.Now())
	j := e.restore(t, key)
	if j.OK || !strings.Contains(j.Error, "lien symbolique") || !strings.Contains(j.Error, "rien n'a été modifié") {
		t.Fatalf("échec attendu : %+v", j)
	}
	sameTree(t, tree(t, e.vol), before)
	if log := e.dk.log(); strings.Count(log, "exécution") != 1 || !e.dk.running("sonarr") {
		t.Fatalf("Docker : %s", log)
	}
}

// Les conteneurs arrêtés sont toujours relancés, quelle que soit l'étape en échec.
func TestRestoreRestartGuaranteed(t *testing.T) {
	cases := []struct {
		name     string
		setup    func(e *restoreEnv)
		want     string
		restored bool // contenu remplacé malgré l'erreur
		stopped  bool // conteneurs arrêtés (l'échec survient après l'arrêt)
	}{
		{"copie de sécurité refusée", func(e *restoreEnv) { e.s3.denyPut = "vigie/sonarr_config/" + safetyPrefix },
			"copie de sécurité impossible : stockage : 403 AccessDenied", false, true},
		{"lecture impossible", func(e *restoreEnv) { e.dk.failPath["/sonarr"] = true }, "archive impossible", false, true},
		{"redémarrage impossible", func(e *restoreEnv) { e.dk.failStart = true }, "contenu restauré, mais redémarrage impossible : sonarr", true, true},
		{"conteneur qui ne repart pas", func(e *restoreEnv) { e.dk.exitOnStart["sonarr"] = true }, "ne tourne pas : sonarr (arrêté, code 0)", true, true},
		{"auxiliaire impossible", func(e *restoreEnv) { e.dk.failCreate = true }, "conteneur auxiliaire : Docker 500", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newRestoreEnv(t)
			copyKey := e.backupNow(t)
			writeTree(t, e.vol, map[string]string{"config.xml": "<Config>v2</Config>"})
			v2 := tree(t, e.vol)
			c.setup(e)
			j := e.restore(t, copyKey)
			if j.OK || !strings.Contains(j.Error, c.want) {
				t.Fatalf("échec attendu (%s) : %+v", c.want, j)
			}
			if c.restored == (tree(t, e.vol)["config.xml"] == v2["config.xml"]) {
				t.Fatalf("contenu : %v", tree(t, e.vol))
			}
			if !c.restored && !strings.Contains(j.Error, "rien n'a été modifié") {
				t.Errorf("message : %s", j.Error)
			}
			log := e.dk.log()
			if c.stopped != strings.Contains(log, "stop sonarr") || (c.stopped && !strings.Contains(log, "start sonarr")) {
				t.Fatalf("arrêt / redémarrage : %s", log)
			}
			if e.dk.helpers() != 0 {
				t.Fatalf("auxiliaire non supprimé : %s", log)
			}
		})
	}
}

func TestRestoreRefusals(t *testing.T) {
	e := newRestoreEnv(t)
	for _, c := range []struct{ item, key, want string }{
		{"vigie", "vigie/vigie/2026-10-28_0330.tar.gz", "ne se restaurent pas depuis l'appli"},
		{"volume:sonarr_config", "vigie/radarr/2026-10-28_0330.tar.gz", "copie invalide"},
		{"volume:sonarr_config", "vigie/sonarr_config/../radarr/2026-10-28_0330.tar.gz", "copie invalide"},
		{"volume:sonarr_config", "vigie/sonarr_config/notes.txt", "copie invalide"},
		{"volume:sonarr_config", "vigie/sonarr_config/2026-10-28_0330.tar.gz/x", "copie invalide"},
		{"volume:../x", "vigie/x/2026-10-28_0330.tar.gz", "élément invalide"},
	} {
		if _, err := e.svc.Restore(c.item, c.key); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s %s : %v", c.item, c.key, err)
		}
	}
	if len(e.upd.Jobs()) != 0 {
		t.Fatal("aucune opération ne doit avoir été lancée")
	}

	// Une opération en cours : 409 côté API.
	release := make(chan struct{})
	if _, err := e.upd.Run("backup", "backup", "sauvegarde", func(context.Context, func(float64, string)) (string, error) {
		<-release
		return "", nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Restore("volume:sonarr_config", "vigie/sonarr_config/2026-10-28_0330.tar.gz"); !errors.Is(err, updater.ErrBusy) {
		t.Errorf("opération en cours : %v", err)
	}
	close(release)
	waitJob(t, e.upd)

	// VIGIE_ALLOW_UPDATES=false : lecture et start/stop/restart seulement.
	e.svc.upd = updater.New(nil, discard(), updater.Hooks{}, updater.Options{Enabled: false})
	if _, err := e.svc.Restore("volume:sonarr_config", "vigie/sonarr_config/2026-10-28_0330.tar.gz"); !errors.Is(err, updater.ErrDisabled) ||
		!strings.HasPrefix(err.Error(), "restauration impossible") {
		t.Errorf("restauration désactivée : %v", err)
	}

	unconfigured := newEnv(t)
	if _, err := unconfigured.svc.Restore("volume:sonarr_config", "vigie/sonarr_config/2026-10-28_0330.tar.gz"); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("stockage non configuré : %v", err)
	}
}

// Échecs constatés pendant l'opération, avant tout arrêt.
func TestRestoreJobRefusals(t *testing.T) {
	t.Run("copie absente", func(t *testing.T) {
		e := newRestoreEnv(t)
		j := e.restore(t, "vigie/sonarr_config/2020-01-01_0000.tar.gz")
		if j.OK || j.Error != "copie introuvable dans le stockage — rien n'a été modifié" || strings.Contains(e.dk.log(), "stop") {
			t.Fatalf("%+v %s", j, e.dk.log())
		}
	})
	t.Run("élément disparu", func(t *testing.T) {
		e := newRestoreEnv(t)
		if _, err := e.svc.Restore("volume:disparu", "vigie/disparu/2026-10-28_0330.tar.gz"); err != nil {
			t.Fatal(err)
		}
		if j := waitJob(t, e.upd); j.OK || !strings.Contains(j.Error, "plus aucun conteneur ne l'utilise") {
			t.Fatalf("%+v", j)
		}
	})
	t.Run("élément utilisé par l'agent", func(t *testing.T) {
		e := newRestoreEnv(t)
		e.dk.find("self").Mounts = append(e.dk.find("self").Mounts, bindMount("/srv/partage", "/partage"))
		e.dk.containers = append(e.dk.containers, &fakeCtr{ID: "p1", Name: "partage", Running: true, Mounts: []map[string]any{bindMount("/srv/partage/sous", "/data")}})
		e.s3.put("vigie/srv_partage_sous/2026-10-28_0330.tar.gz", tarGz(t, dirEnt("data/", 0o755)), time.Now())
		if _, err := e.svc.Restore("bind:/srv/partage/sous", "vigie/srv_partage_sous/2026-10-28_0330.tar.gz"); err != nil {
			t.Fatal(err)
		}
		if j := waitJob(t, e.upd); j.OK || !strings.Contains(j.Error, "l'agent Vigie utilise cet élément") || strings.Contains(e.dk.log(), "stop") {
			t.Fatalf("%+v %s", j, e.dk.log())
		}
	})
	t.Run("agent hors de Docker", func(t *testing.T) {
		e := newRestoreEnv(t)
		e.svc.opt.SelfID = ""
		j := e.restore(t, "vigie/sonarr_config/2026-10-28_0330.tar.gz")
		if j.OK || !strings.Contains(j.Error, "que l'agent tourne dans Docker") {
			t.Fatalf("%+v", j)
		}
	})
}

// Un dossier de l'hôte monté comme fichier : son contenu est remplacé, le fichier reste en place.
func TestRestoreSingleFile(t *testing.T) {
	e := newRestoreEnv(t)
	file := filepath.Join(t.TempDir(), "config.yml")
	mustWrite(t, file, "ancien: 1\n")
	e.dk.dirs["/srv/app/config.yml"] = file
	e.dk.containers = append(e.dk.containers, &fakeCtr{ID: "a1", Name: "app", Running: true, Mounts: []map[string]any{bindMount("/srv/app/config.yml", "/app/config.yml")}})
	key := "vigie/srv_app_config.yml/2026-10-28_0330.tar.gz"
	e.s3.put(key, tarGz(t, withOwner(fileEnt("config.yml", "nouveau: 2\n"), 0, 0, 0o600)), time.Now())
	if _, err := e.svc.Restore("bind:/srv/app/config.yml", key); err != nil {
		t.Fatal(err)
	}
	if j := waitJob(t, e.upd); !j.OK || !strings.HasPrefix(j.Message, "/srv/app/config.yml restauré") {
		t.Fatalf("%+v %s", j, e.dk.log())
	}
	if readFile(t, file) != "nouveau: 2\n" {
		t.Fatalf("fichier : %q", readFile(t, file))
	}
	var safety string
	for _, k := range e.s3.keys() {
		if strings.HasPrefix(k, "vigie/srv_app_config.yml/"+safetyPrefix) {
			safety = k
		}
	}
	if files := untar(t, e.s3.object(safety)); string(files["config.yml"]) != "ancien: 1\n" {
		t.Fatalf("copie de sécurité %s : %v", safety, files)
	}
}

func TestSafetyKeyAndLabel(t *testing.T) {
	now := time.Date(2026, 10, 28, 14, 5, 9, 0, time.Local)
	if k := safetyKey("vigie/x", "vigie/x/2026-10-28_1405.tar.gz", now); k != "vigie/x/avant-restauration_2026-10-28_1405.tar.gz" {
		t.Errorf("clé : %s", k)
	}
	// Annulation dans la même minute : jamais le nom de la copie restaurée.
	if k := safetyKey("vigie/x", "vigie/x/avant-restauration_2026-10-28_1405.tar.gz", now); k != "vigie/x/avant-restauration_2026-10-28_140509.tar.gz" {
		t.Errorf("clé : %s", k)
	}
	if k := safetyKey("x", "x/avant-restauration_2026-10-28_140509.tar.gz", now); k != "x/avant-restauration_2026-10-28_1405.tar.gz" {
		t.Errorf("clé : %s", k)
	}
	for name, want := range map[string]string{
		"2026-10-28_0330.tar.gz":                      "du 28/10/2026 à 03:30",
		"avant-restauration_2026-10-28_140509.tar.gz": "du 28/10/2026 à 14:05",
		"autre.tar.gz":                                "autre.tar.gz",
	} {
		if got := copyLabel(name); got != want {
			t.Errorf("%s : %q", name, got)
		}
	}
	if !safetyRe.MatchString("avant-restauration_2026-10-28_140509.tar.gz") || stampRe.MatchString("avant-restauration_2026-10-28_1405.tar.gz") {
		t.Error("expressions des noms de copies")
	}
}
