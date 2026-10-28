package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/karelisio/vigie/agent/internal/backup"
	"github.com/karelisio/vigie/agent/internal/config"
	"github.com/karelisio/vigie/agent/internal/store"
)

func TestBackupConfigMasksSecrets(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := &Server{cfg: &config.Config{}, st: st, log: log, Backups: backup.New(st, nil, nil, log, backup.Options{})}

	call := func(method, body string) (int, string) {
		r := httptest.NewRequest(method, "/api/v1/backups/config", strings.NewReader(body))
		w := httptest.NewRecorder()
		if method == http.MethodPut {
			s.putBackupConfig(w, r)
		} else {
			s.getBackupConfig(w, r)
		}
		return w.Code, w.Body.String()
	}
	code, out := call(http.MethodPut, `{"endpoint":"https://ns.compat.objectstorage.eu-paris-1.oraclecloud.com","region":"eu-paris-1",
		"bucket":"vigie","access_key":"AKID-visible-nulle-part","secret_key":"secret-tres-confidentiel","items":[{"id":"vigie","stop":false}]}`)
	if code != http.StatusOK || strings.Contains(out, "AKID-visible") || strings.Contains(out, "confidentiel") {
		t.Fatalf("PUT : %d %s", code, out)
	}
	// Champs absents : valeurs conservées ; secret masqué renvoyé tel quel : conservé.
	if code, out = call(http.MethodPut, `{"retention":9,"secret_key":"`+backup.Mask+`"}`); code != http.StatusOK {
		t.Fatalf("PUT partiel : %d %s", code, out)
	}
	code, out = call(http.MethodGet, "")
	var got struct {
		Config  backup.Config `json:"config"`
		NextRun int64         `json:"next_run"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || code != http.StatusOK {
		t.Fatalf("GET : %d %s", code, out)
	}
	c := got.Config
	if c.AccessKey != backup.Mask || c.SecretKey != backup.Mask || c.Retention != 9 || c.Bucket != "vigie" || len(c.Items) != 1 || got.NextRun != 0 {
		t.Fatalf("configuration : %+v", got)
	}
	if code, _ = call(http.MethodPut, `{"inconnu":true}`); code != http.StatusBadRequest {
		t.Errorf("champ inconnu accepté : %d", code)
	}
	audit, _ := st.AuditLog(context.Background(), 10)
	for _, a := range audit {
		if strings.Contains(a.Target+a.Result, "confidentiel") {
			t.Fatalf("secret dans le journal : %+v", a)
		}
	}
}
