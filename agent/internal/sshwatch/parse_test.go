package sshwatch

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/karelisio/vigie/agent/internal/store"
)

func fixedParser(now time.Time) *parser {
	return newParser(time.UTC, func() time.Time { return now })
}

func describe(evs []store.SSHEvent) string {
	var out []string
	for _, e := range evs {
		parts := []string{e.Kind, fmt.Sprintf("%q", e.User), e.IP}
		if e.Method != "" {
			parts = append(parts, e.Method)
		}
		out = append(out, strings.Join(append(parts, fmt.Sprintf("×%d", e.Count)), " "))
	}
	return strings.Join(out, " | ")
}

func TestParseAuthFormats(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	p := fixedParser(now)
	cases := []struct{ line, want string }{
		// rsyslog récent (Ubuntu 24.04) : ISO 8601 avec fuseau.
		{"2026-10-28T10:15:01.123456+01:00 vps sshd[1234]: Accepted publickey for ubuntu from 203.0.113.7 port 51234 ssh2: ED25519 SHA256:abc",
			`accepted "ubuntu" 203.0.113.7 publickey ×1`},
		// Syslog classique, jour sur un chiffre.
		{"Oct  3 08:00:00 vps sshd[2000]: Failed password for root from 198.51.100.4 port 4444 ssh2",
			`fail "root" 198.51.100.4 password ×1`},
		// OpenSSH 9.8+ : sshd-session, IPv6, clavier interactif.
		{"Oct 28 09:00:00 vps sshd-session[3000]: Accepted keyboard-interactive/pam for alice from 2001:db8::5 port 1022 ssh2",
			`accepted "alice" 2001:db8::5 keyboard-interactive ×1`},
		{"Oct 28 09:00:01 vps sshd[3001]: Invalid user  from 192.0.2.10 port 40000", `invalid "" 192.0.2.10 ×1`},
		{"Oct 28 09:00:02 vps sshd[3002]: Connection closed by authenticating user root 192.0.2.11 port 5555 [preauth]",
			`fail "root" 192.0.2.11 ×1`},
		{"Oct 28 09:00:03 vps sshd[3003]: message repeated 3 times: [ Failed password for root from 192.0.2.12 port 22 ssh2]",
			`fail "root" 192.0.2.12 password ×3`},
		{"Oct 28 09:00:04 vps sshd[3004]: Failed publickey for git from 192.0.2.13 port 22 ssh2: RSA SHA256:x",
			`fail "git" 192.0.2.13 publickey ×1`},
		{"Oct 28 09:00:05 vps sshd[3005]: Invalid user bad\x1buser from ::ffff:192.0.2.14 port 2", `invalid "baduser" 192.0.2.14 ×1`},
		// Bruit ignoré.
		{"Oct 28 09:00:06 vps sudo:   ubuntu : TTY=pts/0 ; PWD=/home/ubuntu ; USER=root ; COMMAND=/bin/ls", ""},
		{"Oct 28 09:00:07 vps CRON[42]: pam_unix(cron:session): session opened for user root(uid=0)", ""},
		{"Oct 28 09:00:08 vps sshd[3006]: Connection closed by 192.0.2.15 port 1234 [preauth]", ""},
		{"Oct 28 09:00:09 vps sshd[3007]: Received disconnect from 192.0.2.16 port 1234:11: Bye Bye [preauth]", ""},
		{"pas une ligne de journal", ""},
		{"2026-13-45T99:99:99+00:00 vps sshd[1]: Accepted publickey for x from 1.2.3.4 port 1 ssh2", ""},
	}
	for _, c := range cases {
		if got := describe(p.parseAuth(c.line)); got != c.want {
			t.Errorf("%q\n  → %s\n  attendu %s", c.line, got, c.want)
		}
	}

	evs := p.parseAuth("2026-10-28T10:15:01.123456+01:00 vps sshd[1234]: Accepted publickey for ubuntu from 203.0.113.7 port 51234 ssh2")
	if want := time.Date(2026, 10, 28, 9, 15, 1, 123456000, time.UTC).UnixMilli(); evs[0].TS != want {
		t.Errorf("horodatage ISO : %d, attendu %d", evs[0].TS, want)
	}
	evs = p.parseAuth("Oct  3 08:00:00 vps sshd[2000]: Failed password for root from 198.51.100.4 port 4444 ssh2")
	if want := time.Date(2026, 10, 3, 8, 0, 0, 0, time.UTC).UnixMilli(); evs[0].TS != want {
		t.Errorf("horodatage classique : %v", time.UnixMilli(evs[0].TS).UTC())
	}

	long := strings.Repeat("é", 100)
	evs = p.parseAuth("Oct 28 09:00:00 vps sshd[9]: Invalid user " + long + " from 192.0.2.1 port 1")
	if len([]rune(evs[0].User)) != maxUser {
		t.Errorf("nom non borné : %d caractères", len([]rune(evs[0].User)))
	}
}

func TestParseYearRollover(t *testing.T) {
	p := fixedParser(time.Date(2027, 1, 2, 10, 0, 0, 0, time.UTC))
	evs := p.parseAuth("Dec 31 23:59:59 vps sshd[1]: Failed password for root from 192.0.2.1 port 1 ssh2")
	if got := time.UnixMilli(evs[0].TS).UTC(); got.Year() != 2026 || got.Month() != 12 {
		t.Fatalf("31 décembre lu le 2 janvier : %v", got)
	}
}

func TestParseDeduplicatesConnection(t *testing.T) {
	p := fixedParser(time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC))
	lines := []string{
		// Utilisateur inexistant, deux mots de passe : 2 tentatives (et non 3).
		"Oct 28 10:00:00 vps sshd[100]: Invalid user admin from 192.0.2.1 port 1000",
		"Oct 28 10:00:02 vps sshd[100]: Failed password for invalid user admin from 192.0.2.1 port 1000 ssh2",
		"Oct 28 10:00:04 vps sshd[100]: Failed password for invalid user admin from 192.0.2.1 port 1000 ssh2",
		"Oct 28 10:00:05 vps sshd[100]: Disconnected from invalid user admin 192.0.2.1 port 1000 [preauth]",
		// Utilisateur existant : échec puis fermeture « authenticating user » : 1 tentative.
		"Oct 28 10:01:00 vps sshd[200]: Failed password for root from 192.0.2.2 port 2000 ssh2",
		"Oct 28 10:01:01 vps sshd[200]: Disconnected from authenticating user root 192.0.2.2 port 2000 [preauth]",
		// Clé seulement (mots de passe désactivés) : fermeture sans échec : 1 tentative.
		"Oct 28 10:02:00 vps sshd[300]: Connection closed by authenticating user ubuntu 192.0.2.3 port 3000 [preauth]",
		// Le pid 100 est réutilisé plus tard par une autre connexion : rien n'est hérité.
		"Oct 28 11:00:00 vps sshd[100]: Failed password for invalid user test from 192.0.2.4 port 4000 ssh2",
	}
	var all []store.SSHEvent
	for _, l := range lines {
		all = append(all, p.parseAuth(l)...)
	}
	got := describe(all)
	want := `invalid "admin" 192.0.2.1 ×1 | invalid "admin" 192.0.2.1 password ×1 | fail "root" 192.0.2.2 password ×1 | ` +
		`fail "ubuntu" 192.0.2.3 ×1 | invalid "test" 192.0.2.4 password ×1`
	if got != want {
		t.Fatalf("%s\nattendu %s", got, want)
	}
}

func TestParseBan(t *testing.T) {
	p := fixedParser(time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC))
	for line, want := range map[string]string{
		"2026-10-28 10:15:01,234 fail2ban.actions        [1234]: NOTICE  [sshd] Ban 192.0.2.50":           `ban "" 192.0.2.50 sshd ×1`,
		"2026-10-28 10:15:01,234 fail2ban.actions        [1234]: NOTICE  [recidive] Ban 192.0.2.51":       `ban "" 192.0.2.51 recidive ×1`,
		"2026-10-28 10:15:01,234 fail2ban.actions        [1234]: NOTICE  [sshd] Restore Ban 192.0.2.52":   "",
		"2026-10-28 10:15:01,234 fail2ban.actions        [1234]: NOTICE  [sshd] Unban 192.0.2.53":         "",
		"2026-10-28 10:15:01,234 fail2ban.actions        [1234]: NOTICE  [nginx-http-auth] Ban 192.0.2.5": "",
		"2026-10-28 10:15:01,234 fail2ban.filter         [1234]: INFO    [sshd] Found 192.0.2.54":         "",
	} {
		if got := describe(p.parseBan(line)); got != want {
			t.Errorf("%q → %s, attendu %s", line, got, want)
		}
	}
}

func TestMerge(t *testing.T) {
	ev := func(sec int, kind, user, ip string) store.SSHEvent {
		return store.SSHEvent{TS: int64(sec) * 1000, Kind: kind, User: user, IP: ip, Count: 1}
	}
	got := merge([]store.SSHEvent{
		ev(0, store.SSHInvalid, "admin", "1.1.1.1"),
		ev(10, store.SSHInvalid, "admin", "1.1.1.1"),
		ev(20, store.SSHFail, "root", "1.1.1.1"),
		ev(30, store.SSHInvalid, "admin", "1.1.1.1"),
		ev(90, store.SSHInvalid, "admin", "1.1.1.1"), // plus d'une minute après le premier : nouvelle ligne
		ev(91, store.SSHAccepted, "ubuntu", "2.2.2.2"),
		ev(92, store.SSHAccepted, "ubuntu", "2.2.2.2"), // connexions jamais regroupées
	})
	if d := describe(got); d != `invalid "admin" 1.1.1.1 ×3 | fail "root" 1.1.1.1 ×1 | invalid "admin" 1.1.1.1 ×1 | accepted "ubuntu" 2.2.2.2 ×1 | accepted "ubuntu" 2.2.2.2 ×1` {
		t.Fatal(d)
	}
}
