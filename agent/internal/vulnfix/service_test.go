package vulnfix

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/karelisio/vigie/agent/internal/images"
	"github.com/karelisio/vigie/agent/internal/settings"
	"github.com/karelisio/vigie/agent/internal/updater"
)

func ctx() context.Context { return context.Background() }

// planFor renvoie le plan de l'image du conteneur.
func planFor(t *testing.T, e *env, name string) Plan {
	t.Helper()
	for _, p := range e.s.Plans(ctx()).Images {
		for _, c := range p.Containers {
			if c.Name == name {
				return p
			}
		}
	}
	t.Fatalf("pas de plan pour %s", name)
	return Plan{}
}

// Correction complète : image dérivée fidèle, conteneurs recréés avec la même référence, point de
// retour vers l'image d'origine, bilan, puis retour à l'image d'origine.
func TestFixPatchesImageAndRecreatesContainers(t *testing.T) {
	e := newEnv(t)
	e.d.addContainer("comet", cometRef, true, map[string]any{"com.docker.compose.project": "media"})
	e.d.addContainer("comet-worker", cometRef, false, nil)

	p := planFor(t, e, "comet")
	if p.Route != RoutePatch || len(p.Containers) != 2 || p.Patch.Fixes.Critical != 1 {
		t.Fatalf("plan : %+v", p)
	}
	if _, err := e.s.Fix(ctx(), FixRequest{Container: "comet"}); err != nil {
		t.Fatal(err)
	}
	j := e.wait(t)
	if !j.OK || j.Kind != "vulnfix" || j.Target != "comet" || len(j.Results) != 2 {
		t.Fatalf("opération : %+v", j)
	}
	if want := "2 paquets mis à niveau — 1 faille corrigée (dont 1 critique), 1 restante (sans correctif disponible)"; j.Message != want {
		t.Fatalf("bilan : %q", j.Message)
	}
	for _, r := range j.Results {
		if !r.OK || !r.Updated || !strings.HasPrefix(r.Message, r.Name+" : 2 paquets mis à niveau") {
			t.Fatalf("résultat : %+v", r)
		}
	}

	// Conteneur temporaire : commande à la place de l'Entrypoint, en root, sans healthcheck, ni labels
	// ni réglages du conteneur d'origine ; supprimé à la fin.
	tmp := e.d.created[0]
	ep, _ := tmp["Entrypoint"].([]any)
	if tmp["Image"] != "sha256:base" || len(ep) != 3 || ep[0] != "/bin/sh" || tmp["Cmd"] != nil || tmp["User"] != "0" || tmp["Labels"] != nil {
		t.Fatalf("conteneur temporaire : %v", tmp)
	}
	script, _ := ep[2].(string)
	if !strings.Contains(script, "--only-upgrade") || !strings.Contains(script, "'libssl3' 'openssl'\n") || strings.Contains(script, "zlib1g") {
		t.Fatalf("script : %s", script)
	}
	hc, _ := tmp["HostConfig"].(map[string]any)
	if hc["NetworkMode"] != "bridge" || hc["Binds"] != nil || hc["Mounts"] != nil {
		t.Fatalf("HostConfig : %v", hc)
	}
	if names := e.d.names(); !reflect.DeepEqual(names, []string{"comet", "comet-worker"}) {
		t.Fatalf("conteneurs restants : %v", names)
	}
	for n, c := range e.mon.transient {
		if c != 0 {
			t.Fatalf("conteneur temporaire %s toujours masqué", n)
		}
	}

	// Image dérivée : configuration de l'image d'origine, plus les labels Vigie.
	c := e.d.byName("comet")
	patched := e.d.image(c.image)
	if c.cfg["Image"] != cometRef || patched == nil || patched.id == "sha256:base" || e.d.tagOf(cometRef) != patched.id {
		t.Fatalf("conteneur : image %s, référence %v, tag %s", c.image, c.cfg["Image"], e.d.tagOf(cometRef))
	}
	if w := e.d.byName("comet-worker"); w.image != patched.id || w.running {
		t.Fatalf("conteneur arrêté : %+v", w)
	}
	if field := sameConfig(commitConfig(copyJSON(baseConfig), nil), patched.cfg); field != "Labels" {
		t.Fatalf("configuration de l'image corrigée : %s", field)
	}
	pi := images.PatchedFrom(images.LabelsOf(patched.cfg))
	if pi == nil || pi.BaseImage != "sha256:base" || !reflect.DeepEqual(pi.BaseDigests, []string{"ghcr.io/x/comet@sha256:aaa"}) || pi.Ref != cometRef ||
		!reflect.DeepEqual(pi.Packages, []images.PackageChange{{Name: "libssl3", From: "3.0.11-1", To: "3.0.15-1"}, {Name: "openssl", From: "3.0.11-1", To: "3.0.15-1"}}) {
		t.Fatalf("labels : %+v", pi)
	}
	// Réglages du conteneur conservés (labels compose, variables propres).
	if l, _ := c.cfg["Labels"].(map[string]any); l["com.docker.compose.project"] != "media" {
		t.Fatalf("labels du conteneur : %v", l)
	}
	if env := toList(c.cfg["Env"]); len(env) == 0 || env[len(env)-1] != "SECRET=1" {
		t.Fatalf("variables : %v", env)
	}

	// Nouvelle analyse : inventaire de l'image corrigée enregistré, analyse relancée, bilan gardé.
	if _, ok := e.vs.StoredInventory(ctx(), patched.id); !ok || e.vs.rescans != 1 {
		t.Fatalf("inventaire %v, analyses %d", ok, e.vs.rescans)
	}
	p = planFor(t, e, "comet")
	if p.Last == nil || p.Last.Fixed.Critical != 1 || p.Last.Remaining.Medium != 1 || p.Patched == nil && p.Route != RouteNone {
		t.Fatalf("après : %+v", p)
	}

	// Point de retour vers l'image d'origine, puis retour.
	v, err := e.upd.RollbackPoint(ctx(), updater.Member{ID: c.id, Name: "comet"})
	if err != nil || v.Kind != updater.PointVulnfix || v.OldImageID != "sha256:base" || v.NewImageID != patched.id || v.State != "ready" || v.NewLabel != "1.4.2 corrigée" {
		t.Fatalf("point : %+v (%v)", v, err)
	}
	if _, err := e.upd.Rollback(ctx(), updater.Member{ID: c.id, Name: "comet", Running: true}); err != nil {
		t.Fatal(err)
	}
	if j := e.wait(t); !j.OK || j.Title() != "comet est revenu à son image d'origine" {
		t.Fatalf("retour : %+v", j)
	}
	if c := e.d.byName("comet"); c.image != "sha256:base" || c.cfg["Image"] != cometRef || e.d.tagOf(cometRef) != "sha256:base" {
		t.Fatalf("après le retour : %s", c.image)
	}
	// Correction écartée : la nuit ne la refait pas ; le bouton reste possible.
	if !e.upd.FixDeclined("comet", "sha256:base") || !planFor(t, e, "comet").Declined {
		t.Fatal("correction écartée non notée")
	}
}

// Un téléchargement qui ramène l'image d'origine ne défait pas la correction ; une vraie nouvelle
// version s'installe normalement (l'image corrigée devient l'ancienne version).
func TestPatchedImageAndUpdates(t *testing.T) {
	e := newEnv(t)
	e.d.addContainer("comet", cometRef, true, nil)
	if _, err := e.s.Fix(ctx(), FixRequest{Container: "comet"}); err != nil {
		t.Fatal(err)
	}
	e.wait(t)
	patched := e.d.byName("comet").image

	c := e.d.byName("comet")
	if _, err := e.upd.UpdateContainer(updater.Member{ID: c.id, Name: "comet", Running: true}, false); err != nil {
		t.Fatal(err)
	}
	j := e.wait(t)
	if !j.OK || j.Results[0].Updated || j.Results[0].Message != "comet : déjà à jour (image corrigée par Vigie)" {
		t.Fatalf("mise à jour sans nouvelle version : %+v", j)
	}
	if e.d.byName("comet").image != patched || e.d.tagOf(cometRef) != patched {
		t.Fatal("image d'origine réinstallée : la correction est perdue")
	}

	// Nouvelle version publiée.
	newFS := debianFS([]pkg{{"dpkg", "dpkg", "1.21.22"}, {"libssl3", "openssl", "3.0.16-1"}, {"openssl", "openssl", "3.0.16-1"}, {"libc6", "glibc", "2.36-9"}})
	e.d.addImage("sha256:v2", nil, []string{"ghcr.io/x/comet@sha256:bbb"}, baseConfig, newFS)
	e.d.publish(cometRef, "sha256:v2")
	c = e.d.byName("comet")
	if _, err := e.upd.UpdateContainer(updater.Member{ID: c.id, Name: "comet", Running: true}, false); err != nil {
		t.Fatal(err)
	}
	if j := e.wait(t); !j.OK || !j.Results[0].Updated {
		t.Fatalf("vraie mise à jour : %+v", j)
	}
	if c := e.d.byName("comet"); c.image != "sha256:v2" || e.d.tagOf(cometRef) != "sha256:v2" {
		t.Fatalf("après la mise à jour : %s", c.image)
	}
	v, err := e.upd.RollbackPoint(ctx(), updater.Member{ID: e.d.byName("comet").id, Name: "comet"})
	if err != nil || v.Kind != "" || v.OldImageID != patched || v.NewImageID != "sha256:v2" {
		t.Fatalf("point : %+v (%v)", v, err)
	}
	// L'image d'origine ne sert plus : supprimée ; l'image corrigée reste pour le retour arrière.
	if e.d.image("sha256:base") != nil || e.d.image(patched) == nil {
		t.Fatalf("images supprimées : %v", e.d.removedImg)
	}
}

func TestFixNothingChanged(t *testing.T) {
	e := newEnv(t)
	e.d.addContainer("comet", cometRef, true, nil)
	e.d.upgrade = func(*fakeCtr, string) (int, []string) {
		return 0, []string{"W: Failed to fetch http://deb.debian.org/debian/dists/bookworm/InRelease  Could not resolve 'deb.debian.org'", "vigie: ok"}
	}
	if _, err := e.s.Fix(ctx(), FixRequest{Container: "comet"}); err != nil {
		t.Fatal(err)
	}
	j := e.wait(t)
	if !j.OK || j.Results[0].Updated || !strings.HasPrefix(j.Message, "Rien n'a changé : les dépôts de paquets sont injoignables") ||
		!strings.Contains(j.Message, "Could not resolve") {
		t.Fatalf("%+v", j)
	}
	if j.Title() != "comet : rien n'a changé" || len(e.d.commits) != 0 || e.d.byName("comet").image != "sha256:base" {
		t.Fatalf("titre %q, %d commits", j.Title(), len(e.d.commits))
	}
	// Sans erreur de dépôt : les dépôts n'ont pas encore la version corrigée.
	e.d.upgrade = func(*fakeCtr, string) (int, []string) { return 0, nil }
	if _, err := e.s.Fix(ctx(), FixRequest{ImageID: "sha256:base"}); err != nil {
		t.Fatal(err)
	}
	if j := e.wait(t); j.Message != "Rien n'a changé : les dépôts de l'image ne proposent pas encore de version plus récente de ces paquets" {
		t.Fatalf("%q", j.Message)
	}
	if p := planFor(t, e, "comet"); p.Last == nil || !strings.HasPrefix(p.Last.Message, "Rien n'a changé") {
		t.Fatalf("dernier essai : %+v", p.Last)
	}
}

func TestFixUpgradeFailureLeavesContainerAlone(t *testing.T) {
	e := newEnv(t)
	e.d.addContainer("comet", cometRef, true, nil)
	e.d.upgrade = func(*fakeCtr, string) (int, []string) {
		return 100, []string{"Reading package lists...", "E: Sub-process /usr/bin/dpkg returned an error code (1)"}
	}
	if _, err := e.s.Fix(ctx(), FixRequest{Container: "comet"}); err != nil {
		t.Fatal(err)
	}
	j := e.wait(t)
	if j.OK || !strings.Contains(j.Error, "rien n'a été modifié (code 100) : E: Sub-process") || j.Title() != "Échec de la correction de comet" {
		t.Fatalf("%+v", j)
	}
	if e.d.byName("comet").image != "sha256:base" || len(e.d.commits) != 0 || len(e.d.names()) != 1 || e.d.tagOf(cometRef) != "sha256:base" {
		t.Fatalf("conteneur modifié : %v", e.d.names())
	}
}

// Une image dérivée dont la configuration diffère de l'origine est supprimée, rien n'est recréé.
func TestFixRejectsUnfaithfulImage(t *testing.T) {
	e := newEnv(t)
	e.d.addContainer("comet", cometRef, true, nil)
	e.d.commitMutate = func(cfg map[string]any) { cfg["Env"] = append(toList(cfg["Env"]), "LEAK=1") }
	if _, err := e.s.Fix(ctx(), FixRequest{Container: "comet"}); err != nil {
		t.Fatal(err)
	}
	j := e.wait(t)
	if j.OK || !strings.Contains(j.Error, "différente de l'origine (Env)") {
		t.Fatalf("%+v", j)
	}
	if len(e.d.removedImg) != 1 || e.d.image(e.d.removedImg[0]) != nil || e.d.byName("comet").image != "sha256:base" {
		t.Fatalf("image non fidèle gardée : %v", e.d.removedImg)
	}
}

// Une image déjà corrigée est refaite depuis l'image d'origine, avec les paquets déjà mis à niveau ;
// le point de retour mène toujours à l'origine.
func TestRepatchStartsFromOrigin(t *testing.T) {
	e := newEnv(t)
	e.d.addContainer("comet", cometRef, true, nil)
	if _, err := e.s.Fix(ctx(), FixRequest{Container: "comet"}); err != nil {
		t.Fatal(err)
	}
	e.wait(t)
	first := e.d.byName("comet").image

	// Même résultat que l'image en service : rien n'a changé, rien n'est recréé.
	e.vs.mu.Lock()
	e.vs.rules["openssl"] = append(e.vs.rules["openssl"], rule{"V-SSL2", "high", "3.0.99-1"})
	e.vs.mu.Unlock()
	if _, err := e.s.Fix(ctx(), FixRequest{Container: "comet"}); err != nil {
		t.Fatal(err)
	}
	if j := e.wait(t); !j.OK || j.Results[0].Updated || !strings.HasPrefix(j.Message, "Rien n'a changé") {
		t.Fatalf("%+v", j)
	}
	if last := e.d.created[len(e.d.created)-1]; last["Image"] != "sha256:base" {
		t.Fatalf("refait depuis %v", last["Image"])
	}

	// Nouvelle faille corrigeable (zlib) : image refaite depuis l'origine avec openssl et zlib.
	e.vs.mu.Lock()
	e.vs.rules["zlib"] = []rule{{"V-Z", "high", "1.3.1"}}
	e.vs.mu.Unlock()
	if _, err := e.s.Fix(ctx(), FixRequest{Container: "comet"}); err != nil {
		t.Fatal(err)
	}
	if j := e.wait(t); !j.OK || !j.Results[0].Updated {
		t.Fatalf("%+v", j)
	}
	tmp := e.d.created[len(e.d.created)-2] // avant la recréation du conteneur
	script, _ := toList(tmp["Entrypoint"])[2].(string)
	if tmp["Image"] != "sha256:base" || !strings.Contains(script, "'libssl3' 'openssl' 'zlib1g'") {
		t.Fatalf("conteneur temporaire : %v\n%s", tmp["Image"], script)
	}
	second := e.d.byName("comet").image
	pi := images.PatchedFrom(images.LabelsOf(e.d.image(second).cfg))
	if second == first || pi.BaseImage != "sha256:base" || len(pi.Packages) != 3 {
		t.Fatalf("image refaite : %+v", pi)
	}
	v, _ := e.upd.RollbackPoint(ctx(), updater.Member{ID: e.d.byName("comet").id, Name: "comet"})
	if v.OldImageID != "sha256:base" || v.NewImageID != second {
		t.Fatalf("point : %+v", v)
	}
	if e.d.image(first) != nil {
		t.Fatal("la première image corrigée devait être supprimée")
	}
}

// Image d'origine supprimée du disque : re-téléchargée par son digest ; introuvable : la correction part
// de l'image corrigée en service et cumule les paquets.
func TestRepatchWhenOriginDeleted(t *testing.T) {
	e := newEnv(t)
	e.d.addContainer("comet", cometRef, true, nil)
	if _, err := e.s.Fix(ctx(), FixRequest{Container: "comet"}); err != nil {
		t.Fatal(err)
	}
	e.wait(t)
	first := e.d.byName("comet").image
	e.d.mu.Lock()
	origin := e.d.images["sha256:base"]
	delete(e.d.images, "sha256:base")
	e.d.byDigest["ghcr.io/x/comet@sha256:aaa"] = origin
	e.d.mu.Unlock()
	e.vs.mu.Lock()
	e.vs.rules["zlib"] = []rule{{"V-Z", "high", "1.3.1"}}
	e.vs.mu.Unlock()

	if _, err := e.s.Fix(ctx(), FixRequest{Container: "comet"}); err != nil {
		t.Fatal(err)
	}
	if j := e.wait(t); !j.OK || !j.Results[0].Updated {
		t.Fatalf("%+v", j)
	}
	if tmp := e.d.created[len(e.d.created)-2]; tmp["Image"] != "sha256:base" || e.d.pulls != 1 {
		t.Fatalf("départ %v, %d téléchargement(s)", tmp["Image"], e.d.pulls)
	}

	// Origine introuvable : départ de l'image corrigée, paquets cumulés, retour toujours vers l'origine.
	second := e.d.byName("comet").image
	e.d.mu.Lock()
	delete(e.d.images, "sha256:base")
	delete(e.d.byDigest, "ghcr.io/x/comet@sha256:aaa")
	e.d.mu.Unlock()
	e.vs.mu.Lock()
	e.vs.rules["glibc"] = []rule{{"V-LIBC", "critical", "2.37-1"}}
	e.vs.mu.Unlock()
	e.d.upgrade = aptUpgrade(map[string]string{"libc6": "2.37-1"})
	if _, err := e.s.Fix(ctx(), FixRequest{Container: "comet"}); err != nil {
		t.Fatal(err)
	}
	if j := e.wait(t); !j.OK || !j.Results[0].Updated {
		t.Fatalf("%+v", j)
	}
	if tmp := e.d.created[len(e.d.created)-2]; tmp["Image"] != second {
		t.Fatalf("départ %v au lieu de l'image corrigée", tmp["Image"])
	}
	third := e.d.byName("comet").image
	pi := images.PatchedFrom(images.LabelsOf(e.d.image(third).cfg))
	var names []string
	for _, p := range pi.Packages {
		names = append(names, p.Name+" "+p.From+" → "+p.To)
	}
	if pi.BaseImage != "sha256:base" || strings.Join(names, ", ") != "libc6 2.36-9 → 2.37-1, libssl3 3.0.11-1 → 3.0.15-1, openssl 3.0.11-1 → 3.0.15-1, zlib1g 1.2.13 → 1.3.1" {
		t.Fatalf("labels : %+v / %v", pi, names)
	}
	v, _ := e.upd.RollbackPoint(ctx(), updater.Member{ID: e.d.byName("comet").id, Name: "comet"})
	if v.OldImageID != "sha256:base" || v.ImagePresent || !v.Downloadable || first == third {
		t.Fatalf("point : %+v", v)
	}
}

func TestFixRefusals(t *testing.T) {
	e := newEnv(t)
	e.d.addContainer("comet", cometRef, true, nil)
	if _, err := e.s.Fix(ctx(), FixRequest{Container: "inconnu"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("inconnu : %v", err)
	}
	if _, err := e.s.Fix(ctx(), FixRequest{Container: "comet", Route: "magie"}); !errors.Is(err, ErrRoute) {
		t.Fatalf("voie : %v", err)
	}
	var nf *NotFixableError
	if _, err := e.s.Fix(ctx(), FixRequest{Container: "comet", Route: RouteUpdate}); !errors.As(err, &nf) {
		t.Fatalf("pas de nouvelle version : %v", err)
	}
	if _, err := e.s.Analyze(ctx(), FixRequest{Container: "comet"}); !errors.As(err, &nf) {
		t.Fatalf("analyse sans nouvelle version : %v", err)
	}

	// Une opération en cours : refus.
	block := make(chan struct{})
	if _, err := e.upd.Operate("cleanup", "cleanup", "disque", func(context.Context, *updater.Op) error { <-block; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Fix(ctx(), FixRequest{Container: "comet"}); !errors.Is(err, updater.ErrBusy) {
		t.Fatalf("occupé : %v", err)
	}
	if _, err := e.s.FixAll(ctx()); !errors.Is(err, updater.ErrBusy) {
		t.Fatalf("occupé (tout) : %v", err)
	}
	close(block)
	e.wait(t)

	// L'agent lui-même : jamais corrigé.
	e.d.mu.Lock()
	c := e.d.find("comet")
	delete(e.d.containers, c.id)
	c.id = "self-id"
	e.d.containers["self-id"] = c
	e.d.mu.Unlock()
	if _, err := e.s.Fix(ctx(), FixRequest{Container: "comet"}); !errors.Is(err, updater.ErrSelf) {
		t.Fatalf("agent : %v", err)
	}
	if p := planFor(t, e, "comet"); p.Route != RouteNone || !p.Self {
		t.Fatalf("plan de l'agent : %+v", p)
	}
	if _, err := e.s.FixAll(ctx()); !errors.Is(err, ErrNothing) {
		t.Fatalf("tout corriger sans l'agent : %v", err)
	}
}

func TestFixDisabledAgent(t *testing.T) {
	e := newEnv(t)
	e.d.addContainer("comet", cometRef, true, nil)
	e.upd = updater.New(e.dk, e.s.log, updater.Hooks{}, updater.Options{Enabled: false})
	e.s.upd = e.upd
	var nf *NotFixableError
	if _, err := e.s.Fix(ctx(), FixRequest{Container: "comet"}); !errors.As(err, &nf) || !strings.Contains(err.Error(), "VIGIE_ALLOW_UPDATES") {
		t.Fatalf("%v", err)
	}
	if p := e.s.Plans(ctx()); p.Enabled || p.Images[0].Route != RouteNone {
		t.Fatalf("%+v", p)
	}
}

// Nouvelle version : analysée sans être installée, puis installée si elle corrige des failles ;
// si elle n'en corrige aucune, les paquets sont corrigés à la place.
func TestUpdateRouteWithCandidateAnalysis(t *testing.T) {
	e := newEnv(t)
	e.d.addContainer("comet", cometRef, true, nil)
	e.vs.updates["comet"] = true
	newFS := debianFS([]pkg{{"dpkg", "dpkg", "1.21.22"}, {"libssl3", "openssl", "3.0.16-1"}, {"openssl", "openssl", "3.0.16-1"}, {"libc6", "glibc", "2.36-9"}})
	e.d.addImage("sha256:v2", nil, []string{"ghcr.io/x/comet@sha256:bbb"}, map[string]any{"Cmd": []any{"/app"}, "Labels": map[string]any{"org.opencontainers.image.version": "1.5.0"}}, newFS)
	e.d.publish(cometRef, "sha256:v2")

	if p := planFor(t, e, "comet"); p.Route != RouteUpdate || p.Update.Candidate != nil {
		t.Fatalf("%+v", p)
	}
	if _, err := e.s.Analyze(ctx(), FixRequest{Container: "comet"}); err != nil {
		t.Fatal(err)
	}
	j := e.wait(t)
	if !j.OK || j.Action != "analyze" || !strings.HasPrefix(j.Message, "La nouvelle version corrige 1 faille (dont 1 critique).") {
		t.Fatalf("%+v", j)
	}
	// Rien n'est installé ; la référence n'a pas bougé ; le conteneur d'analyse est supprimé.
	if c := e.d.byName("comet"); c.image != "sha256:base" || e.d.tagOf(cometRef) != "sha256:base" || len(e.d.names()) != 1 {
		t.Fatalf("analyse : %s, tag %s, %v", c.image, e.d.tagOf(cometRef), e.d.names())
	}
	p := planFor(t, e, "comet")
	if p.Route != RouteUpdate || p.Update.Candidate == nil || p.Update.Candidate.Version != "1.5.0" || p.Update.Candidate.Fixed.Critical != 1 {
		t.Fatalf("%+v", p.Update)
	}
	if _, err := e.s.Fix(ctx(), FixRequest{Container: "comet"}); err != nil {
		t.Fatal(err)
	}
	j = e.wait(t)
	if !j.OK || j.Message != "Version 1.5.0 installée : 1 faille corrigée (dont 1 critique), 1 restante" {
		t.Fatalf("%+v", j)
	}
	if c := e.d.byName("comet"); c.image != "sha256:v2" || c.cfg["Image"] != cometRef {
		t.Fatalf("après : %s", c.image)
	}
	if e.vs.rescans != 0 || e.vs.delayed != 1 {
		t.Fatalf("analyses : %d immédiate(s), %d différée(s)", e.vs.rescans, e.vs.delayed)
	}

	// Nouvelle version qui ne corrige rien : correction des paquets à la place.
	e2 := newEnv(t)
	e2.d.addContainer("comet", cometRef, true, nil)
	e2.vs.updates["comet"] = true
	e2.d.addImage("sha256:v3", nil, []string{"ghcr.io/x/comet@sha256:ccc"}, baseConfig, debianFS(basePkgs))
	e2.d.publish(cometRef, "sha256:v3")
	if _, err := e2.s.Fix(ctx(), FixRequest{Container: "comet"}); err != nil {
		t.Fatal(err)
	}
	j = e2.wait(t)
	if !j.OK || !strings.HasPrefix(j.Message, "2 paquets mis à niveau") {
		t.Fatalf("%+v", j)
	}
	if pi := images.PatchedFrom(images.LabelsOf(e2.d.image(e2.d.byName("comet").image).cfg)); pi == nil || pi.BaseImage != "sha256:base" {
		t.Fatalf("correction attendue sur l'image en service : %+v", pi)
	}
	// Voie demandée explicitement : la nouvelle version est installée même sans correction.
	e3 := newEnv(t)
	e3.d.addContainer("comet", cometRef, true, nil)
	e3.vs.updates["comet"] = true
	e3.d.addImage("sha256:v3", nil, []string{"ghcr.io/x/comet@sha256:ccc"}, baseConfig, debianFS(basePkgs))
	e3.d.publish(cometRef, "sha256:v3")
	if _, err := e3.s.Fix(ctx(), FixRequest{Container: "comet", Route: RouteUpdate}); err != nil {
		t.Fatal(err)
	}
	if j := e3.wait(t); !j.OK || e3.d.byName("comet").image != "sha256:v3" {
		t.Fatalf("%+v", j)
	}
}

func TestFixAll(t *testing.T) {
	e := newEnv(t)
	e.d.addContainer("comet", cometRef, true, nil)
	e.d.addImage("sha256:clean", []string{"ghcr.io/x/clean:1"}, nil, baseConfig, debianFS([]pkg{{"dpkg", "dpkg", "1"}}))
	e.d.addContainer("propre", "ghcr.io/x/clean:1", true, nil)
	e.d.addImage("sha256:other", []string{"ghcr.io/x/other:1"}, nil, baseConfig, debianFS(basePkgs))
	e.d.addContainer("ignoré", "ghcr.io/x/other:1", true, nil)
	e.mon.ignored["ignoré"] = true
	if _, err := e.s.FixAll(ctx()); err != nil {
		t.Fatal(err)
	}
	j := e.wait(t)
	if !j.OK || j.Target != "all" || len(j.Results) != 1 || j.Results[0].Name != "comet" || j.Title() != "Correction des failles : 1 corrigé" {
		t.Fatalf("%+v (%s)", j, j.Title())
	}
	if !strings.HasPrefix(j.Message, "1 image corrigée · 1 faille corrigée") || !strings.HasPrefix(j.Body(), "Corrigés : comet.") {
		t.Fatalf("%q / %q", j.Message, j.Body())
	}
}

// La nuit : une passe sur les conteneurs choisis, sans l'agent, les conteneurs arrêtés par
// l'utilisateur, ignorés ou dont la correction a été écartée ; seulement au-dessus du niveau choisi.
func TestNightlyPass(t *testing.T) {
	e := newEnv(t)
	e.d.addContainer("comet", cometRef, true, nil)
	e.d.addImage("sha256:b", []string{"ghcr.io/x/b:1"}, nil, baseConfig, debianFS(basePkgs))
	e.d.addContainer("arrete", "ghcr.io/x/b:1", false, nil)
	e.mon.stopped["arrete"] = true
	e.d.addImage("sha256:c", []string{"ghcr.io/x/c:1"}, nil, baseConfig, debianFS(basePkgs))
	e.d.addContainer("hors-liste", "ghcr.io/x/c:1", true, nil)
	e.vs.mu.Lock()
	e.vs.rules["zlib"] = []rule{{"V-Z", "medium", "1.3.1"}}
	e.vs.mu.Unlock()

	// Désactivée par défaut : rien.
	e.s.Nightly()
	if len(e.upd.Jobs()) != 0 {
		t.Fatal("correction de nuit sans le réglage")
	}
	e.configure(func(s *settings.Settings) {
		s.VulnFixAuto, s.VulnFixList, s.VulnFixLevel = true, []string{"comet", "arrete"}, "critical"
	})
	// Maintenance : rien.
	e.configure(func(s *settings.Settings) { s.MaintenanceUntil = time.Now().Add(time.Hour).UnixMilli() })
	e.s.Nightly()
	if len(e.upd.Jobs()) != 0 || !strings.Contains(e.s.Plans(ctx()).Nightly.Message, "maintenance") {
		t.Fatal("correction pendant la maintenance")
	}
	e.configure(func(s *settings.Settings) { s.MaintenanceUntil = 0 })

	e.s.Nightly()
	j := e.wait(t)
	if !j.OK || j.Target != "nightly" || len(j.Results) != 1 || j.Results[0].Name != "comet" || j.Title() != "Correctifs de la nuit : 1 corrigé" {
		t.Fatalf("%+v", j)
	}
	// Niveau « critique » : seul openssl est mis à niveau, pas zlib (gravité moyenne).
	tmp := e.d.created[0]
	script, _ := toList(tmp["Entrypoint"])[2].(string)
	if !strings.Contains(script, "'libssl3' 'openssl'\n") || strings.Contains(script, "zlib1g") {
		t.Fatalf("script : %s", script)
	}
	if n := e.s.Plans(ctx()).Nightly; n.LastRun == 0 || n.Message != "1 conteneur corrigé" || !n.Enabled || n.Level != "critical" {
		t.Fatalf("état de nuit : %+v", n)
	}

	// Retour à l'image d'origine : la nuit suivante ne refait pas la correction.
	c := e.d.byName("comet")
	if _, err := e.upd.Rollback(ctx(), updater.Member{ID: c.id, Name: "comet", Running: true}); err != nil {
		t.Fatal(err)
	}
	e.wait(t)
	before := len(e.upd.Jobs())
	e.s.Nightly()
	if len(e.upd.Jobs()) != before || e.s.Plans(ctx()).Nightly.Message != "Rien à corriger" {
		t.Fatalf("correction écartée refaite : %+v", e.upd.Jobs()[0])
	}

	// Opération en cours : nouvel essai plus tard.
	e.configure(func(s *settings.Settings) { s.VulnFixAll = true })
	block := make(chan struct{})
	if _, err := e.upd.Operate("cleanup", "cleanup", "disque", func(context.Context, *updater.Op) error { <-block; return nil }); err != nil {
		t.Fatal(err)
	}
	e.s.Nightly()
	close(block)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if jobs := e.upd.Jobs(); len(jobs) > 0 && jobs[0].Target == "nightly" && jobs[0].Done {
			if r := jobs[0].Results; len(r) != 1 || r[0].Name != "hors-liste" {
				t.Fatalf("après report : %+v", r)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("passe de nuit non relancée après l'opération en cours")
}
