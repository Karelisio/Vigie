package updater

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/karelisio/vigie/agent/internal/images"
)

// patchedImage ajoute une image corrigée par Vigie dérivée de sha256:old.
func patchedImage(f *fakeDocker, id string) {
	p := &images.Patched{BaseImage: "sha256:old", BaseDigests: []string{"g0ldyy/comet@sha256:d1d1d1d1d1d1d1d1"}, Ref: "g0ldyy/comet:latest"}
	labels := map[string]any{"org.opencontainers.image.version": "1.0"}
	for k, v := range p.Labels() {
		labels[k] = v
	}
	f.mu.Lock()
	f.imageCfg[id] = map[string]any{"Env": []any{"PATH=/usr/bin", "APP_VERSION=1.0"}, "Cmd": []any{"python", "main.py"}, "Labels": labels}
	f.mu.Unlock()
}

func applyPatch(t *testing.T, m *Manager, f *fakeDocker, image string) (Job, Result, error) {
	t.Helper()
	var res Result
	var rerr error
	c := f.byName("comet")
	if _, err := m.Operate("vulnfix", "vulnfix", "comet", func(ctx context.Context, op *Op) error {
		res, rerr = m.ApplyPatch(ctx, op, Member{ID: c.ID, Name: "comet", Running: true}, Patch{Image: image, Base: "sha256:old", Detail: "openssl 1 → 2"}, 0, 1)
		return rerr
	}); err != nil {
		t.Fatal(err)
	}
	return waitJob(t, m), res, rerr
}

func TestApplyPatchKeepsReferenceAndRecordsPoint(t *testing.T) {
	f, srv := newFake(t)
	setupComet(f)
	patchedImage(f, "sha256:fixed")
	m := newManager(t, srv.URL, true)
	st := withStore(t, m)
	j, r, err := applyPatch(t, m, f, "sha256:fixed")
	if err != nil || !j.OK || !r.OK || !r.Updated {
		t.Fatalf("%+v %+v %v", j, r, err)
	}
	c := f.byName("comet")
	if c.Image != "sha256:fixed" || c.Config["Image"] != "g0ldyy/comet:latest" || f.tagOf("g0ldyy/comet:latest") != "sha256:fixed" {
		t.Fatalf("conteneur : %s, %v", c.Image, c.Config["Image"])
	}
	// Labels de l'image corrigée hérités, labels compose gardés.
	if l, _ := c.Config["Labels"].(map[string]any); l["com.docker.compose.service"] != "comet" {
		t.Fatalf("labels : %v", l)
	}
	p := m.points.get("comet")
	if p == nil || p.Kind != PointVulnfix || p.OldImageID != "sha256:old" || p.NewImageID != "sha256:fixed" || p.NewLabel() != "1.0 corrigée" {
		t.Fatalf("point : %+v", p)
	}
	evs, _ := st.Events(context.Background(), "comet", 10)
	if len(evs) == 0 || evs[0].Action != "vulnfix" || evs[0].Detail != "openssl 1 → 2" {
		t.Fatalf("chronologie : %+v", evs)
	}
	if !m.CanRollback("comet") || m.FixDeclined("comet", "sha256:old") {
		t.Fatal("retour possible, correction pas encore écartée")
	}
	// Retour à l'image d'origine : correction écartée pour la nuit, image corrigée supprimée.
	j = rollback(t, f, m)
	if !j.OK || j.Results[0].Message != "comet est revenu à son image d'origine (1.0, non corrigée)" || j.Title() != "comet est revenu à son image d'origine" {
		t.Fatalf("%+v", j)
	}
	if !m.FixDeclined("comet", "sha256:old") || !f.removed("sha256:fixed") || m.IgnoredDigest("comet") != "" {
		t.Fatal("correction écartée mal notée")
	}
}

// Le nouveau conteneur ne démarre pas : l'ancien revient, avec sa référence et son point de retour.
func TestApplyPatchFailureRestoresEverything(t *testing.T) {
	f, srv := newFake(t)
	setupComet(f)
	patchedImage(f, "sha256:fixed")
	f.crashing["sha256:fixed"] = true
	m := newManager(t, srv.URL, true)
	withStore(t, m)
	update(t, f, m) // point de mise à jour old → new
	before := m.points.get("comet")
	j, _, err := applyPatch(t, m, f, "sha256:fixed")
	if err == nil || j.OK || !strings.Contains(err.Error(), "reste sur son image actuelle") {
		t.Fatalf("%+v %v", j, err)
	}
	if c := f.byName("comet"); c.Image != "sha256:new" || !c.Running || f.tagOf("g0ldyy/comet:latest") != "sha256:new" {
		t.Fatalf("conteneur : %+v, tag %s", c, f.tagOf("g0ldyy/comet:latest"))
	}
	if p := m.points.get("comet"); p == nil || p.Kind != "" || p.NewImageID != before.NewImageID {
		t.Fatalf("point non rétabli : %+v", p)
	}
}

// Téléchargement qui ramène l'image d'origine d'une image corrigée : rien ne change ; une nouvelle
// version s'installe.
func TestUpdateKeepsPatchedImageWhenOriginIsPublished(t *testing.T) {
	f, srv := newFake(t)
	setupComet(f)
	patchedImage(f, "sha256:fixed")
	m := newManager(t, srv.URL, true)
	withStore(t, m)
	if _, _, err := applyPatch(t, m, f, "sha256:fixed"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.remote["g0ldyy/comet:latest"] = "sha256:old" // le registre publie toujours l'origine
	f.mu.Unlock()
	for _, explicit := range []bool{true, false} {
		var j Job
		if explicit {
			j = update2(t, f, m)
		} else {
			j = group(t, f, m)
		}
		if !j.OK || j.Results[0].Updated || !strings.Contains(j.Results[0].Message, "image corrigée par Vigie") {
			t.Fatalf("explicite %v : %+v", explicit, j)
		}
		if f.byName("comet").Image != "sha256:fixed" || f.tagOf("g0ldyy/comet:latest") != "sha256:fixed" {
			t.Fatal("image d'origine réinstallée")
		}
	}
	// Téléchargement seul : idem.
	c := f.byName("comet")
	if _, err := m.UpdateContainer(Member{ID: c.ID, Name: "comet", Running: true}, true); err != nil {
		t.Fatal(err)
	}
	if j := waitJob(t, m); !j.OK || j.Results[0].Updated || f.tagOf("g0ldyy/comet:latest") != "sha256:fixed" {
		t.Fatalf("téléchargement seul : %+v", j)
	}
	// Vraie nouvelle version.
	publish(f, "sha256:v3", "sha256:d3d3d3d3d3d3d3d3", "3.0")
	if j := update2(t, f, m); !j.OK || !j.Results[0].Updated || f.byName("comet").Image != "sha256:v3" {
		t.Fatalf("nouvelle version : %+v", j)
	}
	if p := m.points.get("comet"); p.Kind != "" || p.OldImageID != "sha256:fixed" {
		t.Fatalf("point : %+v", p)
	}
}

// update2 : mise à jour explicite sans vérifier qu'elle change l'image.
func update2(t *testing.T, f *fakeDocker, m *Manager) Job {
	t.Helper()
	c := f.byName("comet")
	if _, err := m.UpdateContainer(Member{ID: c.ID, Name: "comet", Running: true}, false); err != nil {
		t.Fatal(err)
	}
	return waitJob(t, m)
}

func TestPullCandidateRestoresReference(t *testing.T) {
	f, srv := newFake(t)
	setupComet(f)
	m := newManager(t, srv.URL, true)
	var cand *Candidate
	c := f.byName("comet")
	if _, err := m.Operate("vulnfix", "analyze", "comet", func(ctx context.Context, op *Op) error {
		var err error
		cand, err = m.PullCandidate(ctx, op, Member{ID: c.ID, Name: "comet"}, 0, 1)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if j := waitJob(t, m); !j.OK || cand == nil || cand.Image != "sha256:new" || cand.Current != "sha256:old" || cand.Details == nil {
		t.Fatalf("%+v %+v", j, cand)
	}
	if f.tagOf("g0ldyy/comet:latest") != "sha256:old" || f.byName("comet").Image != "sha256:old" {
		t.Fatal("la nouvelle version ne doit pas être appliquée")
	}
}

func TestVulnfixJobTitles(t *testing.T) {
	ok := Result{Name: "a", OK: true, Updated: true}
	same := Result{Name: "b", OK: true}
	ko := Result{Name: "c", Message: "boum"}
	for _, c := range []struct {
		j    Job
		want string
	}{
		{Job{Kind: "vulnfix", Action: "vulnfix", Target: "nightly", OK: false, Results: []Result{ok, same, ko}}, "Correctifs de la nuit : 1 corrigé, 1 inchangé, 1 échec"},
		{Job{Kind: "vulnfix", Action: "vulnfix", Target: "all", OK: true}, "Correction des failles : rien à corriger"},
		{Job{Kind: "vulnfix", Action: "vulnfix", Target: "comet", OK: true, Results: []Result{ok}}, "comet : failles corrigées"},
		{Job{Kind: "vulnfix", Action: "vulnfix", Target: "comet", OK: true, Results: []Result{same}}, "comet : rien n'a changé"},
		{Job{Kind: "vulnfix", Action: "vulnfix", Target: "comet", Results: []Result{ko}}, "Échec de la correction de comet"},
		{Job{Kind: "vulnfix", Action: "analyze", Target: "comet", OK: true}, "Nouvelle version de comet analysée"},
	} {
		if got := c.j.Title(); got != c.want {
			t.Errorf("%q, attendu %q", got, c.want)
		}
	}
	j := Job{Kind: "vulnfix", Target: "nightly", Message: "2 images corrigées", Results: []Result{ok, ko}}
	if b := j.Body(); b != "Corrigés : a. Échec : c. 2 images corrigées" {
		t.Errorf("%q", b)
	}
}

func TestVulnfixRefusedWhenDisabledOrSelf(t *testing.T) {
	f, srv := newFake(t)
	setupComet(f)
	m := newManager(t, srv.URL, true)
	m.opt.Enabled = false
	if _, err := m.Operate("vulnfix", "vulnfix", "comet", func(context.Context, *Op) error { return nil }); !errors.Is(err, ErrDisabled) {
		t.Fatalf("%v", err)
	}
	m.opt.Enabled = true
	c := f.byName("comet")
	m.opt.SelfID = c.ID
	var err error
	if _, oerr := m.Operate("vulnfix", "vulnfix", "comet", func(ctx context.Context, op *Op) error {
		_, err = m.ApplyPatch(ctx, op, Member{ID: c.ID, Name: "comet"}, Patch{Image: "sha256:x", Base: "sha256:old"}, 0, 1)
		return err
	}); oerr != nil {
		t.Fatal(oerr)
	}
	waitJob(t, m)
	if !errors.Is(err, ErrSelf) {
		t.Fatalf("%v", err)
	}
}
