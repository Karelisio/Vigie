package settings

import (
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
