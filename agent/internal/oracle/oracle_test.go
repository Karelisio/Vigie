package oracle

import "testing"

func TestP95(t *testing.T) {
	v := make([]float64, 100)
	for i := range v {
		v[i] = float64(i + 1)
	}
	if got := p95(v); got != 95 {
		t.Fatalf("p95 = %v, attendu 95", got)
	}
	if p95(nil) != 0 {
		t.Fatal("p95 d'une liste vide")
	}
}

func TestApplicable(t *testing.T) {
	t.Setenv("VIGIE_ORACLE", "")
	if !Applicable("6.17.0-1020-oracle") || Applicable("6.8.0-45-generic") {
		t.Fatal("détection Oracle par le noyau")
	}
	t.Setenv("VIGIE_ORACLE", "true")
	if !Applicable("6.8.0-45-generic") {
		t.Fatal("VIGIE_ORACLE=true ignoré")
	}
}
