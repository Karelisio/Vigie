package secret

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestMask(t *testing.T) {
	cases := map[string]string{
		"":                       "",
		"court":                  "••••",
		"123456789:AAHdqTcvCH1v": "••••CH1v",
	}
	for in, want := range cases {
		if got := Mask(in); got != want {
			t.Errorf("Mask(%q) = %q, attendu %q", in, got, want)
		}
	}
}

func TestMerge(t *testing.T) {
	if got := Merge("", "ancien"); got != "ancien" {
		t.Errorf("vide : %q", got)
	}
	if got := Merge(Mask("une-valeur-assez-longue"), "une-valeur-assez-longue"); got != "une-valeur-assez-longue" {
		t.Errorf("masqué : %q", got)
	}
	if got := Merge("  nouveau  ", "ancien"); got != "nouveau" {
		t.Errorf("nouveau : %q", got)
	}
}

// L'adresse de la requête (qui contient le jeton) ne doit jamais apparaître dans l'erreur.
func TestDescribeHidesURL(t *testing.T) {
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://127.0.0.1:1/botSECRET123/sendMessage", nil)
	_, err := http.DefaultClient.Do(req)
	if err == nil {
		t.Fatal("erreur attendue")
	}
	if msg := Describe(err); strings.Contains(msg, "SECRET") {
		t.Fatalf("jeton divulgué : %q", msg)
	}
	if msg := Describe(context.DeadlineExceeded); msg != "délai dépassé" {
		t.Errorf("délai : %q", msg)
	}
	if msg := Describe(errors.New("autre")); msg != "autre" {
		t.Errorf("autre : %q", msg)
	}
}
