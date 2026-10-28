package alerts

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/karelisio/vigie/agent/internal/ports"
	"github.com/karelisio/vigie/agent/internal/sshwatch"
)

func TestPortAlerts(t *testing.T) {
	e, st := newEngine(t)
	e.NotifyPort(ports.Change{Open: true, Port: ports.Port{Port: 8080, Proto: "tcp", Scope: "public", Address: "0.0.0.0", Container: "comet"}})
	e.NotifyPort(ports.Change{Open: true, Port: ports.Port{Port: 9000, Proto: "tcp", Scope: "public", Address: "0.0.0.0", Container: "portainer", HostNetwork: true}})
	e.NotifyPort(ports.Change{Open: true, Port: ports.Port{Port: 40000, Proto: "udp", Scope: "public", Address: "10.0.0.5"}})
	e.NotifyPort(ports.Change{Open: false, Port: ports.Port{Port: 8080, Proto: "tcp", Scope: "public", Container: "comet"}})
	want := []string{
		"warning Nouveau port ouvert sur Internet : 8080/tcp (comet)",
		"warning Nouveau port ouvert sur Internet : 9000/tcp (portainer)",
		"warning Nouveau port ouvert sur Internet : 40000/udp (processus de l'hôte)",
		"info Port fermé : 8080/tcp (comet)",
	}
	if got := alertTitles(t, st); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("alertes :\n%s", strings.Join(got, "\n"))
	}
	list, _ := st.Alerts(context.Background(), 10)
	if !list[0].Resolved || list[0].Kind != "port" || !strings.Contains(list[3].Body, "sans passer par Pangolin") ||
		!strings.Contains(list[1].Body, "10.0.0.5") {
		t.Fatalf("détail : %+v", list)
	}
}

func TestSSHLoginAlerts(t *testing.T) {
	e, st := newEngine(t)
	ts := time.Date(2026, 10, 28, 9, 15, 0, 0, time.Local).UnixMilli()
	e.NotifySSHLogin(sshwatch.Login{TS: ts, User: "ubuntu", IP: "203.0.113.7", Method: "publickey", Country: "FR", CountryName: "France"})
	e.NotifySSHLogin(sshwatch.Login{TS: ts, User: "root", IP: "198.51.100.9", Method: "password", NewIP: true, Failures: 12, Others: 2})
	s := e.set.Get()
	s.SSHLoginAlert = false
	if _, err := e.set.Update(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	e.NotifySSHLogin(sshwatch.Login{TS: ts, User: "ubuntu", IP: "203.0.113.7"})
	want := []string{
		"info Connexion SSH : ubuntu depuis 203.0.113.7 (France)",
		"warning Connexion SSH : root depuis 198.51.100.9",
	}
	if got := alertTitles(t, st); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("alertes :\n%s", strings.Join(got, "\n"))
	}
	list, _ := st.Alerts(context.Background(), 10)
	if b := list[1].Body; b != "Le 28/10 à 09:15, par clé SSH." {
		t.Fatalf("détail : %q", b)
	}
	if b := list[0].Body; !strings.Contains(b, "mot de passe") || !strings.Contains(b, "Première connexion depuis cette adresse") ||
		!strings.Contains(b, "échoué 12 fois") || !strings.Contains(b, "2 autre(s)") {
		t.Fatalf("détail : %q", b)
	}
}
