package debrid

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/karelisio/vigie/agent/internal/store"
)

// Clés de test : le préfixe choisit la réponse du faux fournisseur.
const (
	keyGood    = "good-0123456789abcdef"
	keyExpired = "expired-0123456789abcdef"
	keyBad     = "bad-0123456789abcdef"
	keyBlocked = "blocked-0123456789abcdef"
	keyDown    = "down-0123456789abcdef"
)

// fakeProviders simule les quatre API ; seen enregistre la clé reçue par chaque appel.
type fakeProviders struct {
	*httptest.Server
	mu   sync.Mutex
	seen []string
}

func newFakeProviders(t *testing.T) *fakeProviders {
	in10d := time.Now().Add(10*24*time.Hour + time.Hour)
	past := time.Now().Add(-48 * time.Hour)
	f := &fakeProviders{}
	reply := func(w http.ResponseWriter, code int, body string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = io.WriteString(w, body)
	}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		key := bearer
		if q := r.URL.Query().Get("apikey"); q != "" {
			key = q
		}
		f.mu.Lock()
		f.seen = append(f.seen, r.URL.Path+" "+key)
		f.mu.Unlock()
		if strings.HasPrefix(key, "down") {
			reply(w, http.StatusBadGateway, "<html>502</html>")
			return
		}
		switch r.URL.Path {
		case "/rd/user":
			switch {
			case strings.HasPrefix(key, "good"):
				reply(w, 200, fmt.Sprintf(`{"id":1,"username":"bob","type":"premium","premium":%d,"expiration":%q}`,
					int(time.Until(in10d).Seconds()), in10d.UTC().Format("2006-01-02T15:04:05.000Z")))
			case strings.HasPrefix(key, "expired"):
				reply(w, 200, fmt.Sprintf(`{"username":"bob","type":"free","premium":0,"expiration":%q}`, past.UTC().Format(time.RFC3339)))
			default:
				reply(w, 401, `{"error":"bad_token","error_code":8}`)
			}
		case "/ad/user":
			if r.URL.Query().Get("agent") != "vigie" || bearer != key {
				reply(w, 400, `{"status":"error","error":{"code":"AUTH_MISSING_AGENT","message":"agent manquant"}}`)
				return
			}
			switch {
			case strings.HasPrefix(key, "good"):
				reply(w, 200, fmt.Sprintf(`{"status":"success","data":{"user":{"username":"alice","isPremium":true,"isSubscribed":false,"premiumUntil":%d}}}`, in10d.Unix()))
			case strings.HasPrefix(key, "blocked"):
				reply(w, 200, `{"status":"error","error":{"code":"AUTH_BLOCKED","message":"This apikey is geo-blocked"}}`)
			default:
				reply(w, 200, `{"status":"error","error":{"code":"AUTH_BAD_APIKEY","message":"The auth apikey is invalid"}}`)
			}
		case "/tb/user/me":
			switch {
			case strings.HasPrefix(key, "good"):
				reply(w, 200, fmt.Sprintf(`{"success":true,"error":null,"detail":"ok","data":{"email":"c@example.org","plan":2,"is_subscribed":true,"premium_expires_at":%q}}`,
					in10d.UTC().Format("2006-01-02T15:04:05.000000+00:00")))
			default:
				reply(w, 403, `{"success":false,"error":"BAD_TOKEN","detail":"Invalid token.","data":null}`)
			}
		case "/pm/account/info":
			switch {
			case strings.HasPrefix(key, "good"):
				reply(w, 200, fmt.Sprintf(`{"status":"success","customer_id":123,"premium_until":%d,"limit_used":0.1}`, in10d.Unix()))
			case strings.HasPrefix(key, "expired"):
				reply(w, 200, `{"status":"success","customer_id":"123","premium_until":false}`)
			default:
				reply(w, 200, `{"status":"error","message":"Not logged in."}`)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeProviders) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.seen...)
}

func newManager(t *testing.T, st *store.Store, f *fakeProviders) *Manager {
	t.Helper()
	if st == nil {
		var err error
		if st, err = store.Open(t.TempDir()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.Close() })
	}
	m := New(context.Background(), st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	m.bases = map[string]string{RealDebrid: f.URL + "/rd", AllDebrid: f.URL + "/ad", TorBox: f.URL + "/tb", Premiumize: f.URL + "/pm"}
	return m
}

func TestProviders(t *testing.T) {
	f := newFakeProviders(t)
	m := newManager(t, nil, f)
	cases := []struct {
		provider, key     string
		ok, premium       bool
		refused           bool
		username, problem string
	}{
		{RealDebrid, keyGood, true, true, false, "bob", ""},
		{RealDebrid, keyExpired, true, false, false, "bob", ""},
		{RealDebrid, keyBad, false, false, true, "", "clé API refusée (jeton invalide ou expiré)"},
		{AllDebrid, keyGood, true, true, false, "alice", ""},
		{AllDebrid, keyBad, false, false, true, "", "clé API refusée"},
		{AllDebrid, keyBlocked, false, false, true, "", "clé bloquée par AllDebrid : confirme la connexion depuis l'e-mail reçu"},
		{TorBox, keyGood, true, true, false, "c@example.org", ""},
		{TorBox, keyBad, false, false, true, "", "clé API refusée"},
		{Premiumize, keyGood, true, true, false, "", ""},
		{Premiumize, keyExpired, true, false, false, "", ""},
		{Premiumize, keyBad, false, false, true, "", "clé API refusée"},
		{Premiumize, keyDown, false, false, false, "", "service indisponible (code 502)"},
		{TorBox, keyDown, false, false, false, "", "service indisponible (code 502)"},
	}
	for _, c := range cases {
		s := m.check(context.Background(), Account{ID: "x", Provider: c.provider, Key: c.key})
		if s.OK != c.ok || s.Premium != c.premium || s.Refused != c.refused || s.Username != c.username || s.Error != c.problem || s.CheckedAt == 0 {
			t.Errorf("%s %s : %+v", c.provider, c.key, s)
		}
		if c.premium {
			if left := time.Until(time.UnixMilli(s.Expires)); left < 10*24*time.Hour || left > 11*24*time.Hour {
				t.Errorf("%s : échéance %v", c.provider, time.UnixMilli(s.Expires))
			}
		}
	}
	// Real-Debrid expiré : la date passée est connue, le premium inactif.
	if s := m.check(context.Background(), Account{Provider: RealDebrid, Key: keyExpired}); s.Expires == 0 || time.UnixMilli(s.Expires).After(time.Now()) {
		t.Errorf("échéance passée attendue : %+v", s)
	}
}

func TestParseTime(t *testing.T) {
	want := time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC)
	for _, raw := range []string{`1796119200`, `"1796119200"`, `1796119200000`, `"2026-12-01T10:00:00.000Z"`, `"2026-12-01T11:00:00+01:00"`, `"2026-12-01T10:00:00.000000"`, `"2026-12-01 10:00:00"`} {
		if got := parseTime(json.RawMessage(raw)); !got.Equal(want) {
			t.Errorf("%s : %v", raw, got)
		}
	}
	for _, raw := range []string{`false`, `null`, `0`, `""`, `"bientôt"`, ``} {
		if got := parseTime(json.RawMessage(raw)); !got.IsZero() {
			t.Errorf("%s : %v, zéro attendu", raw, got)
		}
	}
}

// Clés masquées dans l'état, conservées quand l'appli renvoie la valeur masquée ou rien, relues après redémarrage.
func TestAccountsMaskedAndKept(t *testing.T) {
	f := newFakeProviders(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m := newManager(t, st, f)
	var checked [][]Status
	m.OnCheck = func(list []Status) { checked = append(checked, list) }
	ctx := context.Background()
	state, err := m.Update(ctx, []Account{{Provider: "realdebrid", Label: " Perso ", Key: keyGood}, {Provider: "torbox", Key: keyGood}})
	if err != nil {
		t.Fatal(err)
	}
	a, b := state.Accounts[0], state.Accounts[1]
	if a.Key != "••••cdef" || a.Label != "Perso" || b.Label != "TorBox" || a.ID == "" || a.ID == b.ID {
		t.Fatalf("état inattendu : %+v", state.Accounts)
	}
	if !a.OK || !a.Premium || a.DaysLeft != 10 || len(checked) != 1 {
		t.Fatalf("vérification après enregistrement : %+v (%d)", a, len(checked))
	}
	raw, _ := json.Marshal(m.State())
	if strings.Contains(string(raw), keyGood) {
		t.Fatalf("clé divulguée : %s", raw)
	}
	// L'appli renvoie l'état reçu (clé masquée) en renommant un compte et en supprimant l'autre.
	f.mu.Lock()
	f.seen = nil
	f.mu.Unlock()
	if _, err := m.Update(ctx, []Account{{ID: a.ID, Provider: a.Provider, Label: "Famille", Key: a.Key}}); err != nil {
		t.Fatal(err)
	}
	if seen := f.calls(); len(seen) != 1 || seen[0] != "/rd/user "+keyGood {
		t.Fatalf("clé d'origine attendue : %v", seen)
	}
	again := newManager(t, st, f)
	if s := again.State().Accounts; len(s) != 1 || s[0].ID != a.ID || s[0].Label != "Famille" || again.accounts[0].Key != keyGood {
		t.Fatalf("comptes relus : %+v", s)
	}
	// Nouvelle clé : remplace l'ancienne.
	if st, err := m.Update(ctx, []Account{{ID: a.ID, Provider: a.Provider, Key: keyBad}}); err != nil || !st.Accounts[0].Refused {
		t.Fatalf("nouvelle clé : %+v %v", st, err)
	}
}

func TestAccountsValidation(t *testing.T) {
	f := newFakeProviders(t)
	m := newManager(t, nil, f)
	ctx := context.Background()
	state, err := m.Update(ctx, []Account{{Provider: "alldebrid", Key: keyGood}})
	if err != nil {
		t.Fatal(err)
	}
	id := state.Accounts[0].ID
	cases := []struct {
		in   []Account
		want string
	}{
		{[]Account{{Provider: "megadebrid", Key: keyGood}}, `fournisseur inconnu "megadebrid" (realdebrid, alldebrid, torbox, premiumize)`},
		{[]Account{{Provider: "torbox", Label: "Nouveau"}}, "clé API manquante pour Nouveau"},
		{[]Account{{Provider: "torbox", Key: "••••cdef"}}, "clé API manquante pour TorBox"},
		{[]Account{{Provider: "torbox", Key: "deux mots"}}, "clé API invalide pour TorBox"},
		{[]Account{{Provider: "torbox", Label: strings.Repeat("x", 41), Key: keyGood}}, "nom de compte trop long (40 caractères au plus)"},
		// Fournisseur changé : la clé de l'ancien compte n'est pas reprise.
		{[]Account{{ID: id, Provider: "premiumize", Key: ""}}, "clé API manquante pour Premiumize"},
		{make([]Account, 11), "10 comptes au plus"},
	}
	for _, c := range cases {
		if _, err := m.Update(ctx, c.in); err == nil || err.Error() != c.want {
			t.Errorf("%+v : %v, attendu %q", c.in, err, c.want)
		}
	}
	if s := m.State().Accounts; len(s) != 1 || s[0].ID != id {
		t.Fatalf("une erreur ne doit rien modifier : %+v", s)
	}
}

// Une erreur réseau ne doit pas divulguer la clé (passée dans l'URL pour AllDebrid et Premiumize).
func TestNetworkErrorHidesKey(t *testing.T) {
	f := newFakeProviders(t)
	m := newManager(t, nil, f)
	f.Close()
	for _, p := range []string{AllDebrid, Premiumize} {
		s := m.check(context.Background(), Account{Provider: p, Key: keyGood})
		if s.OK || s.Refused || s.Error == "" || strings.Contains(s.Error, keyGood) {
			t.Errorf("%s : %+v", p, s)
		}
	}
}
