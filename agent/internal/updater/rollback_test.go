package updater

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/karelisio/vigie/agent/internal/images"
	"github.com/karelisio/vigie/agent/internal/monitor"
	"github.com/karelisio/vigie/agent/internal/settings"
	"github.com/karelisio/vigie/agent/internal/store"
)

const cometRef = "g0ldyy/comet:latest"

// withStore branche une base SQLite au gestionnaire (points de retour, chronologie).
func withStore(t *testing.T, m *Manager) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	m.opt.Store = st
	m.points = newPoints(st, m.log)
	return st
}

// update met comet à jour et renvoie le conteneur obtenu.
func update(t *testing.T, f *fakeDocker, m *Manager) *fakeCtr {
	t.Helper()
	c := f.byName("comet")
	if _, err := m.UpdateContainer(Member{ID: c.ID, Name: "comet", Running: true}, false); err != nil {
		t.Fatal(err)
	}
	if j := waitJob(t, m); !j.OK || !j.Results[0].Updated {
		t.Fatalf("mise à jour : %+v", j)
	}
	return f.byName("comet")
}

// rollback revient à la version précédente de comet et renvoie l'opération terminée.
func rollback(t *testing.T, f *fakeDocker, m *Manager) Job {
	t.Helper()
	c := f.byName("comet")
	if _, err := m.Rollback(t.Context(), Member{ID: c.ID, Name: "comet", Running: true}); err != nil {
		t.Fatal(err)
	}
	return waitJob(t, m)
}

func group(t *testing.T, f *fakeDocker, m *Manager) Job {
	t.Helper()
	c := f.byName("comet")
	if _, err := m.Group("auto", "nightly", "update", []Member{{ID: c.ID, Name: "comet", Running: true}}); err != nil {
		t.Fatal(err)
	}
	return waitJob(t, m)
}

// publish publie une nouvelle version de comet sur le registre.
func publish(f *fakeDocker, id, digest, version string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.remote[cometRef] = id
	f.imageCfg[id] = map[string]any{"Labels": map[string]any{"org.opencontainers.image.version": version}}
	f.digests[id] = []string{"g0ldyy/comet@" + digest}
	f.registry["g0ldyy/comet@"+digest] = id
}

func TestUpdateRecordsRollbackPoint(t *testing.T) {
	f, srv := newFake(t)
	setupComet(f)
	m := newManager(t, srv.URL, true)
	st := withStore(t, m)
	c := update(t, f, m)

	p := m.points.get("comet")
	if p == nil || p.ImageRef != cometRef || p.OldImageID != "sha256:old" || p.NewImageID != "sha256:new" ||
		p.OldVersion != "1.0" || p.NewVersion != "2.0" || p.OldCreated == 0 || p.UpdatedAt == 0 ||
		len(p.OldDigests) != 1 || p.OldDigests[0] != "g0ldyy/comet@sha256:d1d1d1d1d1d1d1d1" || p.Ignored {
		t.Fatalf("point = %+v", p)
	}
	// Conservé en base : retrouvé après un redémarrage de l'agent.
	if again := newPoints(st, m.log).get("comet"); again == nil || again.OldImageID != "sha256:old" {
		t.Fatalf("point relu = %+v", again)
	}
	v, err := m.RollbackPoint(t.Context(), Member{ID: c.ID, Name: "comet"})
	if err != nil || v.State != "ready" || !v.ImagePresent || !v.Downloadable || v.OldLabel != "1.0" || v.NewLabel != "2.0" {
		t.Fatalf("vue = %+v, %v", v, err)
	}
	if !m.CanRollback("comet") {
		t.Error("retour arrière non proposé")
	}
	if got := m.RollbackImages(t.Context()); len(got) != 1 || got[0] != "sha256:old" {
		t.Errorf("images gardées = %v", got)
	}
	ev, _ := st.Events(t.Context(), "comet", 10)
	if len(ev) != 1 || ev[0].Action != "update" || ev[0].Detail != "1.0 → 2.0" {
		t.Errorf("chronologie = %+v", ev)
	}
	// L'image d'avant reste sur le disque malgré la suppression des anciennes images.
	if len(f.removedImg) != 0 || f.removed("sha256:old") {
		t.Errorf("images supprimées = %v", f.removedImg)
	}

	// Mise à jour suivante : l'image du point précédent part, celle d'avant celle-ci reste.
	publish(f, "sha256:v3", "sha256:d3d3d3d3d3d3d3d3", "3.0")
	update(t, f, m)
	if len(f.removedImg) != 1 || f.removedImg[0] != "sha256:old" {
		t.Errorf("images supprimées = %v", f.removedImg)
	}
	if p := m.points.get("comet"); p.OldImageID != "sha256:new" || p.NewImageID != "sha256:v3" {
		t.Errorf("nouveau point = %+v", p)
	}
}

func TestUpdateKeepsImagesWithoutPrune(t *testing.T) {
	f, srv := newFake(t)
	setupComet(f)
	m := newManager(t, srv.URL, false)
	update(t, f, m)
	publish(f, "sha256:v3", "sha256:d3d3d3d3d3d3d3d3", "3.0")
	update(t, f, m)
	if len(f.removedImg) != 0 {
		t.Errorf("images supprimées sans le réglage : %v", f.removedImg)
	}
}

func TestFailedUpdateRestoresPreviousPoint(t *testing.T) {
	f, srv := newFake(t)
	setupComet(f)
	m := newManager(t, srv.URL, true)
	update(t, f, m)
	publish(f, "sha256:v3", "sha256:d3d3d3d3d3d3d3d3", "3.0")
	f.crashing["sha256:v3"] = true
	c := f.byName("comet")
	if _, err := m.UpdateContainer(Member{ID: c.ID, Name: "comet", Running: true}, false); err != nil {
		t.Fatal(err)
	}
	if j := waitJob(t, m); j.OK {
		t.Fatalf("job = %+v", j)
	}
	if p := m.points.get("comet"); p == nil || p.OldImageID != "sha256:old" || p.NewImageID != "sha256:new" {
		t.Fatalf("point après échec = %+v", p)
	}
	if len(f.removedImg) != 0 {
		t.Errorf("images supprimées après un échec = %v", f.removedImg)
	}
}

func TestRollbackWithLocalImage(t *testing.T) {
	f, srv := newFake(t)
	setupComet(f)
	m := newManager(t, srv.URL, true)
	st := withStore(t, m)
	update(t, f, m)

	j := rollback(t, f, m)
	if !j.OK || j.Kind != "rollback" || j.Action != "rollback" || j.Target != "comet" || !strings.Contains(j.Message, "1.0") {
		t.Fatalf("job = %+v", j)
	}
	if j.Title() != "comet est revenu à sa version précédente" {
		t.Errorf("titre = %q", j.Title())
	}
	c := f.byName("comet")
	if c == nil || c.Image != "sha256:old" || !c.Running || c.Config["Image"] != cometRef {
		t.Fatalf("conteneur = %+v", c)
	}
	if lbl := c.Config["Labels"].(map[string]any); lbl["com.docker.compose.image"] != "sha256:old" {
		t.Errorf("labels = %v", lbl)
	}
	// La référence locale désigne de nouveau l'ancienne image (Dockhand, compose).
	if got := f.tagOf(cometRef); got != "sha256:old" {
		t.Errorf("référence %s → %s", cometRef, got)
	}
	if len(f.pulled) != 1 {
		t.Errorf("téléchargements = %v (l'image locale suffisait)", f.pulled)
	}
	// La version écartée est supprimée (réglage actif) et ignorée.
	if !f.removed("sha256:new") || f.removed("sha256:old") {
		t.Errorf("images supprimées = %v", f.removedImg)
	}
	p := m.points.get("comet")
	if p.RolledBackAt == 0 || !p.Ignored || p.IgnoredDigest != "sha256:d2d2d2d2d2d2d2d2" {
		t.Fatalf("point = %+v", p)
	}
	if m.IgnoredDigest("comet") != "sha256:d2d2d2d2d2d2d2d2" || m.CanRollback("comet") {
		t.Error("version écartée non ignorée")
	}
	if v, _ := m.RollbackPoint(t.Context(), Member{ID: c.ID, Name: "comet"}); v.State != "rolled_back" {
		t.Errorf("état = %q", v.State)
	}
	if got := m.RollbackImages(t.Context()); len(got) != 0 {
		t.Errorf("images gardées = %v", got)
	}
	ev, _ := st.Events(t.Context(), "comet", 10)
	if len(ev) != 2 || ev[0].Action != "rollback" || ev[0].Detail != "2.0 → 1.0" {
		t.Errorf("chronologie = %+v", ev)
	}
	if _, err := m.Rollback(t.Context(), Member{ID: c.ID, Name: "comet"}); !errors.Is(err, ErrRolledBack) {
		t.Errorf("second retour : %v", err)
	}
}

func TestRollbackDownloadsDeletedImage(t *testing.T) {
	f, srv := newFake(t)
	setupComet(f)
	m := newManager(t, srv.URL, false)
	c := update(t, f, m)
	f.mu.Lock()
	f.deleted["sha256:old"] = true // nettoyage du disque
	f.mu.Unlock()
	if v, _ := m.RollbackPoint(t.Context(), Member{ID: c.ID, Name: "comet"}); v.ImagePresent || !v.Downloadable {
		t.Fatalf("vue = %+v", v)
	}

	j := rollback(t, f, m)
	if !j.OK {
		t.Fatalf("job = %+v", j)
	}
	if last := f.pulled[len(f.pulled)-1]; last != "g0ldyy/comet@sha256:d1d1d1d1d1d1d1d1" {
		t.Errorf("téléchargements = %v", f.pulled)
	}
	if c := f.byName("comet"); c.Image != "sha256:old" || c.Config["Image"] != cometRef || f.tagOf(cometRef) != "sha256:old" {
		t.Fatalf("conteneur = %+v", c)
	}
}

func TestRollbackFailsWithoutImage(t *testing.T) {
	f, srv := newFake(t)
	setupComet(f)
	m := newManager(t, srv.URL, false)
	c := update(t, f, m)
	f.mu.Lock()
	f.deleted["sha256:old"] = true
	delete(f.registry, "g0ldyy/comet@sha256:d1d1d1d1d1d1d1d1") // plus publiée non plus
	f.mu.Unlock()

	j := rollback(t, f, m)
	if j.OK || !strings.Contains(j.Error, "n'a pas pu être re-téléchargée") || !strings.Contains(j.Error, "manifest unknown") {
		t.Fatalf("job = %+v", j)
	}
	if now := f.byName("comet"); now.ID != c.ID || now.Image != "sha256:new" || !now.Running || f.tagOf(cometRef) != "sha256:new" {
		t.Fatalf("conteneur modifié : %+v", now)
	}
	if p := m.points.get("comet"); p.RolledBackAt != 0 || p.Ignored {
		t.Errorf("point = %+v", p)
	}

	// Image construite localement : aucun digest pour la retrouver.
	f2, srv2 := newFake(t)
	setupComet(f2)
	f2.digests["sha256:old"] = nil
	m2 := newManager(t, srv2.URL, false)
	update(t, f2, m2)
	f2.mu.Lock()
	f2.deleted["sha256:old"] = true
	f2.mu.Unlock()
	if j := rollback(t, f2, m2); j.OK || !strings.Contains(j.Error, "ne vient pas d'un registre") {
		t.Fatalf("job = %+v", j)
	}
}

func TestRollbackKeepsCurrentWhenOldVersionCrashes(t *testing.T) {
	f, srv := newFake(t)
	setupComet(f)
	m := newManager(t, srv.URL, true)
	c := update(t, f, m)
	f.crashing["sha256:old"] = true

	j := rollback(t, f, m)
	if j.OK || !strings.Contains(j.Error, "reste en version 2.0") || strings.Contains(j.Error, "ancienne version rétablie") {
		t.Fatalf("job = %+v", j)
	}
	if now := f.byName("comet"); now.ID != c.ID || now.Image != "sha256:new" || !now.Running {
		t.Fatalf("version actuelle non rétablie : %+v", now)
	}
	if f.tagOf(cometRef) != "sha256:new" {
		t.Errorf("référence non rétablie : %s", f.tagOf(cometRef))
	}
	if p := m.points.get("comet"); p.RolledBackAt != 0 || p.Ignored || f.removed("sha256:new") {
		t.Errorf("point = %+v, images supprimées = %v", p, f.removedImg)
	}
}

func TestIgnoredVersionSkippedByNightly(t *testing.T) {
	f, srv := newFake(t)
	setupComet(f)
	m := newManager(t, srv.URL, false)
	update(t, f, m)
	rollback(t, f, m)
	before := f.byName("comet")

	// L'état des images signale la version comme ignorée : la nuit ne la retient pas.
	s := settings.Settings{AutoUpdateAll: true}
	ctrs := []monitor.Container{{ID: before.ID, Name: "comet", State: "running"}}
	if got := autoMembers(s, ctrs, map[string]images.Status{"comet": {Ignored: true}}); len(got) != 0 {
		t.Fatalf("retenu : %+v", got)
	}
	// Même retenue (état pas encore revérifié), elle n'est pas réinstallée.
	j := group(t, f, m)
	if !j.OK || j.Results[0].Updated || !strings.Contains(j.Results[0].Message, "version ignorée") {
		t.Fatalf("job = %+v", j)
	}
	if now := f.byName("comet"); now.ID != before.ID || now.Image != "sha256:old" {
		t.Fatalf("version écartée réinstallée : %+v", now)
	}
	if f.tagOf(cometRef) != "sha256:old" {
		t.Errorf("référence laissée sur la version écartée : %s", f.tagOf(cometRef))
	}

	// Une version plus récente paraît : elle est installée.
	publish(f, "sha256:v3", "sha256:d3d3d3d3d3d3d3d3", "3.0")
	j = group(t, f, m)
	if !j.OK || !j.Results[0].Updated || f.byName("comet").Image != "sha256:v3" {
		t.Fatalf("job = %+v", j)
	}
	if p := m.points.get("comet"); p.Ignored || p.OldImageID != "sha256:old" || p.RolledBackAt != 0 {
		t.Errorf("point = %+v", p)
	}
}

func TestUnignoreAllowsUpdateAgain(t *testing.T) {
	f, srv := newFake(t)
	setupComet(f)
	m := newManager(t, srv.URL, false)
	update(t, f, m)
	rollback(t, f, m)
	c := f.byName("comet")

	v, err := m.Unignore(t.Context(), Member{ID: c.ID, Name: "comet"})
	if err != nil || v.Ignored || v.IgnoredDigest != "" || v.State != "rolled_back" {
		t.Fatalf("vue = %+v, %v", v, err)
	}
	if m.IgnoredDigest("comet") != "" {
		t.Error("toujours ignorée")
	}
	if j := group(t, f, m); !j.OK || !j.Results[0].Updated || f.byName("comet").Image != "sha256:new" {
		t.Fatalf("job = %+v", j)
	}
}

func TestExplicitUpdateInstallsIgnoredVersion(t *testing.T) {
	f, srv := newFake(t)
	setupComet(f)
	m := newManager(t, srv.URL, false)
	update(t, f, m)
	rollback(t, f, m)
	// Bouton « Mettre à jour » de la fiche : la mise à jour demandée explicitement passe.
	if c := update(t, f, m); c.Image != "sha256:new" {
		t.Fatalf("conteneur = %+v", c)
	}
	if p := m.points.get("comet"); p.Ignored || p.RolledBackAt != 0 || p.OldImageID != "sha256:old" {
		t.Errorf("point = %+v", p)
	}
	if !m.CanRollback("comet") {
		t.Error("nouveau retour arrière impossible")
	}
}

func TestRollbackRefused(t *testing.T) {
	f, srv := newFake(t)
	old := setupComet(f)
	m := newManager(t, srv.URL, false)
	ctx := context.Background()

	if _, err := m.Rollback(ctx, Member{ID: old.ID, Name: "comet"}); !errors.Is(err, ErrNoPoint) {
		t.Errorf("sans point : %v", err)
	}
	if _, err := m.RollbackPoint(ctx, Member{ID: old.ID, Name: "comet"}); !errors.Is(err, ErrNoPoint) {
		t.Errorf("vue sans point : %v", err)
	}
	if _, err := m.Rollback(ctx, Member{ID: "self", Name: "vigie-agent", Self: true}); !errors.Is(err, ErrRollbackSelf) {
		t.Errorf("agent : %v", err)
	}
	c := update(t, f, m)

	m.current = &Job{}
	if _, err := m.Rollback(ctx, Member{ID: c.ID, Name: "comet"}); !errors.Is(err, ErrBusy) {
		t.Errorf("opération en cours : %v", err)
	}
	m.current = nil
	m.opt.Enabled = false
	if _, err := m.Rollback(ctx, Member{ID: c.ID, Name: "comet"}); !errors.Is(err, ErrDisabled) {
		t.Errorf("mises à jour désactivées : %v", err)
	}
	m.opt.Enabled = true

	// Recréé depuis Dockhand avec une autre image : le point ne correspond plus.
	f.mu.Lock()
	f.imageCfg["sha256:other"] = map[string]any{}
	c.Image = "sha256:other"
	f.mu.Unlock()
	if _, err := m.Rollback(ctx, Member{ID: c.ID, Name: "comet"}); !errors.Is(err, ErrStalePoint) {
		t.Errorf("point périmé : %v", err)
	}
	if v, _ := m.RollbackPoint(ctx, Member{ID: c.ID, Name: "comet"}); v.State != "stale" {
		t.Errorf("état = %q", v.State)
	}
	if m.CanRollback("comet") || len(m.RollbackImages(ctx)) != 0 {
		t.Error("point périmé proposé")
	}
}

func TestPointLabels(t *testing.T) {
	p := Point{ImageRef: "ghcr.io/x/y:latest", OldImageID: "sha256:0123456789abcdef", NewImageID: "sha256:fedcba9876543210",
		OldDigests: []string{"ghcr.io/other/z@sha256:aaaaaaaaaaaaaaaa", "ghcr.io/x/y@sha256:bbbbbbbbbbbbbbbbbb"}}
	if got := p.OldLabel(); got != "bbbbbbbbbbbb" {
		t.Errorf("libellé par digest = %q", got)
	}
	if got := p.NewLabel(); got != "fedcba987654" {
		t.Errorf("libellé par ID = %q", got)
	}
	if refs := p.pullRefs(); len(refs) != 2 || refs[0] != "ghcr.io/x/y@sha256:bbbbbbbbbbbbbbbbbb" {
		t.Errorf("références = %v", refs)
	}
	p.OldVersion = "v1.4.2"
	if p.OldLabel() != "v1.4.2" {
		t.Errorf("libellé par version = %q", p.OldLabel())
	}
	if (Job{Kind: "rollback", Target: "comet"}).Title() != "Échec du retour de comet à sa version précédente" {
		t.Error("titre d'échec")
	}
}
