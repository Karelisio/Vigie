package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/karelisio/vigie/agent/internal/config"
	"github.com/karelisio/vigie/agent/internal/store"
	"github.com/karelisio/vigie/agent/internal/updater"
	"github.com/karelisio/vigie/agent/internal/vulnfix"
)

// Refus de la correction des failles : code HTTP adapté et entrée au journal.
func TestVulnFixErrors(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := &Server{st: st, cfg: &config.Config{}}
	for _, c := range []struct {
		err  error
		code int
	}{
		{vulnfix.ErrNotFound, http.StatusNotFound},
		{vulnfix.ErrRoute, http.StatusBadRequest},
		{&vulnfix.NotFixableError{Reason: "Image sans shell"}, http.StatusConflict},
		{vulnfix.ErrNothing, http.StatusConflict},
		{fmt.Errorf("x : %w", updater.ErrSelf), http.StatusConflict},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/api/v1/vulns/fix", nil)
		if !s.vulnFixErr(w, r, "vulnfix", "comet", c.err) || w.Code != c.code || !strings.Contains(w.Body.String(), c.err.Error()) {
			t.Errorf("%v : %d %s", c.err, w.Code, w.Body.String())
		}
	}
	// Opération en cours, mises à jour désactivées : laissés à jobStarted (409, 403).
	if s.vulnFixErr(httptest.NewRecorder(), httptest.NewRequest("POST", "/", nil), "vulnfix", "comet", updater.ErrBusy) {
		t.Error("ErrBusy doit passer par jobStarted")
	}
	w := httptest.NewRecorder()
	s.jobStarted(w, httptest.NewRequest("POST", "/api/v1/vulns/fix", nil), "vulnfix", "comet", updater.Job{}, updater.ErrBusy)
	if w.Code != http.StatusConflict {
		t.Errorf("occupé : %d", w.Code)
	}
	w = httptest.NewRecorder()
	s.jobStarted(w, httptest.NewRequest("POST", "/api/v1/vulns/fix", nil), "vulnfix", "comet", updater.Job{}, updater.ErrDisabled)
	if w.Code != http.StatusForbidden {
		t.Errorf("désactivé : %d", w.Code)
	}
	entries, _ := st.AuditLog(t.Context(), 20)
	if len(entries) < 5 {
		t.Fatalf("journal : %d entrées", len(entries))
	}

	// Service absent, corps invalide ou sans cible.
	w = httptest.NewRecorder()
	s.getVulnFixes(w, httptest.NewRequest("GET", "/api/v1/vulns/fixes", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("sans service : %d", w.Code)
	}
	if _, ok := readFixRequest(httptest.NewRecorder(), httptest.NewRequest("POST", "/", strings.NewReader(`{}`))); ok {
		t.Error("cible manquante acceptée")
	}
	if _, ok := readFixRequest(httptest.NewRecorder(), httptest.NewRequest("POST", "/", strings.NewReader(`{"container":"a","image":"b"}`))); ok {
		t.Error("champ inconnu accepté")
	}
	req, ok := readFixRequest(httptest.NewRecorder(), httptest.NewRequest("POST", "/", strings.NewReader(`{"container":"comet","route":"patch"}`)))
	if !ok || req.Container != "comet" || req.Route != "patch" || fixTarget(req) != "comet" {
		t.Errorf("%+v", req)
	}
}
