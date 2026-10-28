package settings

import (
	"slices"
	"testing"
	"time"
)

func TestQuietHours(t *testing.T) {
	s := Settings{QuietEnabled: true, QuietStart: "23:00", QuietEnd: "07:00"}
	at := func(h, m int) time.Time { return time.Date(2026, 1, 1, h, m, 0, 0, time.Local) }
	for _, c := range []struct {
		t    time.Time
		want bool
	}{{at(23, 30), true}, {at(2, 0), true}, {at(6, 59), true}, {at(7, 0), false}, {at(12, 0), false}, {at(22, 59), false}} {
		if got := s.InQuietHours(c.t); got != c.want {
			t.Errorf("%s : %v, attendu %v", c.t.Format("15:04"), got, c.want)
		}
	}
	day := Settings{QuietEnabled: true, QuietStart: "12:00", QuietEnd: "14:00"}
	if !day.InQuietHours(at(13, 0)) || day.InQuietHours(at(15, 0)) {
		t.Error("plage dans la journée mal gérée")
	}
}

func TestValidate(t *testing.T) {
	s := Settings{CPU: 90, RAM: 90, Disk: 90, DurationMin: 5, CertDays: 14, Muted: []string{" b", "a", "b", ""}}
	if err := s.validate(); err != nil {
		t.Fatal(err)
	}
	if len(s.Muted) != 2 || s.Muted[0] != "a" || s.Muted[1] != "b" {
		t.Fatalf("sourdine mal normalisée : %v", s.Muted)
	}
	bad := s
	bad.CPU = 10
	if bad.validate() == nil {
		t.Error("seuil CPU de 10 % accepté")
	}
}

func TestNewSettingsValidation(t *testing.T) {
	base := Settings{CPU: 90, RAM: 90, Disk: 90, DurationMin: 5, CertDays: 14, NetQuotaAlert: 80, NetQuotaTB: 10}
	s := base
	s.LogRules = []LogRule{{Container: "/comet", Pattern: "  rate limit "}}
	s.AutoUpdateList = []string{"comet", "/comet", "redis"}
	if err := s.validate(); err != nil {
		t.Fatal(err)
	}
	if s.LogRules[0].ID == "" || s.LogRules[0].Container != "comet" || s.LogRules[0].Pattern != "rate limit" {
		t.Errorf("règle = %+v", s.LogRules[0])
	}
	if len(s.AutoUpdateList) != 2 || s.AutoUpdateTime != "04:00" {
		t.Errorf("auto = %v %q", s.AutoUpdateList, s.AutoUpdateTime)
	}
	bad := base
	bad.LogRules = []LogRule{{Container: "comet", Pattern: "(", Regex: true}}
	if bad.validate() == nil {
		t.Error("regex invalide acceptée")
	}
	bad = base
	bad.NetQuotaAlert = 20
	if bad.validate() == nil {
		t.Error("seuil de quota invalide accepté")
	}
	m := base
	m.MaintenanceUntil = time.Now().Add(time.Hour).UnixMilli()
	if !m.InMaintenance(time.Now()) || m.InMaintenance(time.Now().Add(2*time.Hour)) {
		t.Error("InMaintenance")
	}
}

func TestSettings06Validation(t *testing.T) {
	base := Settings{CPU: 90, RAM: 90, Disk: 90, DurationMin: 5, CertDays: 14, NetQuotaAlert: 80, NetQuotaTB: 10}
	s := base
	s.PortAllow = []string{" 443 ", "51820/UDP", "443/tcp"}
	s.Domains = []string{"https://Comet.15122000.xyz/manifest.json", "15122000.xyz.", "comet.15122000.xyz"}
	if err := s.validate(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(s.PortAllow, []string{"443/tcp", "51820/udp"}) {
		t.Errorf("ports = %v", s.PortAllow)
	}
	if !slices.Equal(s.Domains, []string{"15122000.xyz", "comet.15122000.xyz"}) {
		t.Errorf("domaines = %v", s.Domains)
	}
	if s.VulnAlert != "critical" || s.DomainDays != 30 {
		t.Errorf("valeurs par défaut : %q %d", s.VulnAlert, s.DomainDays)
	}
	for _, mut := range []func(*Settings){
		func(s *Settings) { s.PortAllow = []string{"70000"} },
		func(s *Settings) { s.PortAllow = []string{"53/sctp"} },
		func(s *Settings) { s.Domains = []string{"pas un domaine"} },
		func(s *Settings) { s.VulnAlert = "medium" },
		func(s *Settings) { s.DiskForecastDays = 91 },
		func(s *Settings) { s.VolumeGrowthGB = -1 },
		func(s *Settings) { s.DomainDays = 200 },
	} {
		bad := base
		mut(&bad)
		if bad.validate() == nil {
			t.Errorf("accepté : %+v", bad)
		}
	}
}

// Correction automatique des failles (agent 0.7.0) : désactivée par défaut, niveau « élevée ».
func TestVulnFixSettings(t *testing.T) {
	base := Settings{CPU: 90, RAM: 90, Disk: 90, DurationMin: 5, CertDays: 14, NetQuotaAlert: 80, NetQuotaTB: 10}
	s := base
	s.VulnFixList = []string{"/comet", "redis", "comet", ""}
	if err := s.validate(); err != nil {
		t.Fatal(err)
	}
	if s.VulnFixAuto || s.VulnFixAll || s.VulnFixLevel != "high" || !slices.Equal(s.VulnFixList, []string{"comet", "redis"}) {
		t.Fatalf("%+v", s)
	}
	for _, level := range []string{"critical", "high", "medium", "low"} {
		s.VulnFixLevel = level
		if err := s.validate(); err != nil {
			t.Errorf("%s refusé : %v", level, err)
		}
	}
	s.VulnFixLevel = "unknown"
	if s.validate() == nil {
		t.Error("niveau inconnu accepté")
	}
}
