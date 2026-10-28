package api

import (
	"crypto/sha256"
	"net/http/httptest"
	"testing"
)

func TestAuthorized(t *testing.T) {
	s := &Server{tokenHash: sha256.Sum256([]byte("secret-token-0123456789-abcdefghijkl"))}
	r := httptest.NewRequest("GET", "/api/v1/info", nil)
	if s.authorized(r) != "" {
		t.Fatal("requête sans token acceptée")
	}
	r.Header.Set("Authorization", "Bearer mauvais")
	if s.authorized(r) != "" {
		t.Fatal("mauvais token accepté")
	}
	r.Header.Set("Authorization", "Bearer secret-token-0123456789-abcdefghijkl")
	if s.authorized(r) != roleAdmin {
		t.Fatal("bon token refusé")
	}
}

func TestLimiter(t *testing.T) {
	l := newLimiter(1, 3)
	for i := 0; i < 3; i++ {
		if ok, _ := l.allow("1.2.3.4"); !ok {
			t.Fatalf("requête %d refusée", i)
		}
	}
	if ok, _ := l.allow("1.2.3.4"); ok {
		t.Fatal("rafale non limitée")
	}
	for i := 0; i < maxAuthFailures; i++ {
		l.authFailed("5.6.7.8")
	}
	if _, banned := l.allow("5.6.7.8"); !banned {
		t.Fatal("IP non bannie après échecs répétés")
	}
}
