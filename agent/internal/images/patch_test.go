package images

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPatchedLabelsRoundTrip(t *testing.T) {
	p := &Patched{
		At: time.Date(2026, 10, 28, 3, 15, 0, 0, time.UTC).UnixMilli(), BaseImage: "sha256:base",
		BaseDigests: []string{"ghcr.io/x/y@sha256:aaa", "ghcr.io/x/y@sha256:bbb"}, Ref: "ghcr.io/x/y:latest",
		Packages: []PackageChange{{"libssl3", "3.0.11-1", "3.0.15-1"}, {"openssl", "3.0.11-1", "3.0.15-1"}},
	}
	labels := p.Labels()
	if labels[LabelFixed] != "2026-10-28T03:15:00Z" || labels[LabelBaseImage] != "sha256:base" {
		t.Fatalf("%v", labels)
	}
	got := PatchedFrom(labels)
	if !reflect.DeepEqual(got, p) {
		t.Fatalf("relu : %+v\nattendu %+v", got, p)
	}
	if PatchedFrom(map[string]string{"org.opencontainers.image.version": "1"}) != nil {
		t.Fatal("image ordinaire vue comme corrigée")
	}
	if PatchedFrom(map[string]string{LabelFixed: "x", LabelBaseImage: "pas-un-id"}) != nil {
		t.Fatal("label d'origine invalide accepté")
	}
	// Liste très longue : bornée.
	long := &Patched{BaseImage: "sha256:b"}
	for i := range maxLabelPackages + 20 {
		long.Packages = append(long.Packages, PackageChange{Name: strings.Repeat("p", i%5+1), From: "1", To: "2"})
	}
	if n := len(PatchedFrom(long.Labels()).Packages); n != maxLabelPackages {
		t.Fatalf("%d paquets dans le label", n)
	}
}

func TestLabelsOf(t *testing.T) {
	got := LabelsOf(map[string]any{"Labels": map[string]any{"a": "1", "b": 2}})
	if !reflect.DeepEqual(got, map[string]string{"a": "1"}) {
		t.Fatalf("%v", got)
	}
	if len(LabelsOf(nil)) != 0 {
		t.Fatal("configuration vide")
	}
}

// Une image corrigée n'a pas de digest de registre : la détection des mises à jour compare le digest
// publié à celui de son image d'origine. Même version publiée : pas de « MAJ disponible » ; nouvelle
// version : toujours signalée.
func TestPatchedImageUpdateDetection(t *testing.T) {
	ref := "ghcr.io/x/y:latest"
	p := &Patched{BaseImage: "sha256:base", BaseDigests: []string{"ghcr.io/x/y@sha256:aaa"}, Ref: ref}
	labels := p.Labels()

	// Sans les labels (agent 0.6) : état inconnu, la nouvelle version n'était jamais détectée.
	if _, known := UpdateAvailable(ref, "sha256:bbb", nil); known {
		t.Fatal("image sans digest : état inconnu attendu")
	}
	digests := EffectiveDigests(nil, labels)
	if upd, known := UpdateAvailable(ref, "sha256:aaa", digests); upd || !known {
		t.Fatal("même version d'origine publiée : pas de mise à jour attendue")
	}
	if upd, known := UpdateAvailable(ref, "sha256:ccc", digests); !upd || !known {
		t.Fatal("nouvelle version publiée : mise à jour attendue")
	}
	// Image ordinaire : RepoDigests inchangés.
	if got := EffectiveDigests([]string{"ghcr.io/x/y@sha256:aaa"}, map[string]string{}); len(got) != 1 {
		t.Fatalf("%v", got)
	}

	// Téléchargement qui ramène l'origine : par ID ou par digest publié.
	if !p.IsBase(ref, "sha256:base", nil) || !p.IsBase(ref, "sha256:autre-id", []string{"ghcr.io/x/y@sha256:aaa"}) {
		t.Fatal("image d'origine non reconnue")
	}
	if p.IsBase(ref, "sha256:new", []string{"ghcr.io/x/y@sha256:ccc"}) {
		t.Fatal("nouvelle version prise pour l'origine")
	}
	var none *Patched
	if none.IsBase(ref, "sha256:base", nil) {
		t.Fatal("image non corrigée")
	}
}
