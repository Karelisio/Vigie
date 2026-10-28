package alerts

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/karelisio/vigie/agent/internal/domains"
)

func domain(name string, expires time.Time) domains.Status {
	return domains.Status{Domain: name, Source: domains.SourceAuto, Expires: expires.UnixMilli(), Registrar: "NameCheap, Inc.", CheckedAt: time.Now().UnixMilli()}
}

func TestDomainAlertsOncePerThreshold(t *testing.T) {
	e, st := newEngine(t) // préavis par défaut : 30 jours
	end := time.Now().Add(60 * 24 * time.Hour)
	d := domain("15122000.xyz", end)
	at := func(before time.Duration) time.Time { return end.Add(-before) }

	e.evaluateDomains([]domains.Status{d}, at(31*24*time.Hour)) // au-delà du préavis : rien
	e.evaluateDomains([]domains.Status{d}, at(30*24*time.Hour-time.Hour))
	e.evaluateDomains([]domains.Status{d}, at(20*24*time.Hour)) // même seuil, même date : rien de plus
	e.evaluateDomains([]domains.Status{d}, at(5*24*time.Hour+time.Hour))
	// Vérification impossible (date inconnue) : ni alerte ni oubli des seuils.
	e.evaluateDomains([]domains.Status{{Domain: "15122000.xyz", Error: "registre injoignable"}}, at(4*24*time.Hour))
	e.evaluateDomains([]domains.Status{d}, at(3*24*time.Hour))

	// Redémarrage de l'agent : les seuils déjà signalés sont mémorisés en base.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	again := New(ctx, e.cfg, e.mon, e.dk, st, e.log)
	again.evaluateDomains([]domains.Status{d}, at(2*24*time.Hour))
	again.evaluateDomains([]domains.Status{d}, at(-time.Hour))
	again.evaluateDomains([]domains.Status{d}, at(-48*time.Hour))

	want := []string{
		"warning Domaine 15122000.xyz : 29 jours restants",
		"critical Domaine 15122000.xyz : 5 jours restants",
		"critical Domaine 15122000.xyz expiré",
	}
	got := alertTitles(t, st)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("alertes :\n%s\nattendu :\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	list, _ := st.Alerts(context.Background(), 10)
	if list[1].Body != "Il expire le "+end.Format("02/01/2006")+". Renouvelle-le chez NameCheap, Inc." || list[0].Kind != "domain" || list[0].Target != "15122000.xyz" {
		t.Errorf("détail : %+v", list[1])
	}
}

func TestDomainRenewalResolves(t *testing.T) {
	e, st := newEngine(t)
	now := time.Now()
	d := domain("15122000.xyz", now.Add(3*24*time.Hour+time.Hour))
	other := domain("example.net", now.Add(200*24*time.Hour))
	e.evaluateDomains([]domains.Status{d, other}, now)
	// Renouvelé : la date d'expiration recule d'un an, l'alerte est close ; plus rien ensuite.
	renewed := domain("15122000.xyz", now.Add(368*24*time.Hour+time.Hour))
	e.evaluateDomains([]domains.Status{renewed, other}, now)
	e.evaluateDomains([]domains.Status{renewed, other}, now.Add(time.Hour))
	// Un an plus tard, l'échéance revient : nouvelle alerte pour la nouvelle date.
	e.evaluateDomains([]domains.Status{renewed}, now.Add(340*24*time.Hour))

	list, _ := st.Alerts(context.Background(), 10)
	got := alertTitles(t, st)
	want := []string{
		"critical Domaine 15122000.xyz : 3 jours restants",
		"info Domaine 15122000.xyz renouvelé",
		"warning Domaine 15122000.xyz : 28 jours restants",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("alertes :\n%s", strings.Join(got, "\n"))
	}
	if !list[1].Resolved || list[1].Body != "Il expire maintenant le "+time.UnixMilli(renewed.Expires).Format("02/01/2006")+"." {
		t.Errorf("résolution : %+v", list[1])
	}

	// Domaine qui n'est plus suivi : sa mémoire disparaît.
	e.evaluateDomains(nil, now)
	var sent map[string]string
	_ = json.Unmarshal([]byte(st.Get(context.Background(), domainKey)), &sent)
	if len(sent) != 0 {
		t.Fatalf("mémoire non vidée : %v", sent)
	}
}

func TestDomainNoticeFollowsSettings(t *testing.T) {
	e, st := newEngine(t)
	s := e.set.Get()
	s.DomainDays = 60
	if _, err := e.set.Update(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	unknown := domains.Status{Domain: "example.co.uk", CheckedAt: now.UnixMilli(), Note: "pas de date"}
	e.evaluateDomains([]domains.Status{domain("15122000.xyz", now.Add(45*24*time.Hour+time.Hour)), unknown}, now)
	last := domain("example.net", now.Add(10*time.Hour))
	last.Registrar = ""
	e.evaluateDomains([]domains.Status{last}, now)
	got := alertTitles(t, st)
	if len(got) != 2 || got[0] != "warning Domaine 15122000.xyz : 45 jours restants" || got[1] != "critical Domaine example.net : dernier jour" {
		t.Fatalf("alertes : %q", got)
	}
	list, _ := st.Alerts(context.Background(), 1)
	if !strings.HasSuffix(list[0].Body, "Pense à le renouveler.") {
		t.Errorf("détail : %q", list[0].Body)
	}
}
