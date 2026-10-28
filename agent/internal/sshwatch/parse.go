package sshwatch

import (
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/karelisio/vigie/agent/internal/store"
)

const (
	maxUser    = 64   // caractères d'un nom d'utilisateur conservés (choisi par l'attaquant)
	maxPids    = 4096 // connexions suivies en même temps (dédoublonnage des lignes d'une même connexion)
	mergeEvery = time.Minute
)

var (
	reAccepted = regexp.MustCompile(`^Accepted (\S+) for (.*?) from (\S+) port \d+`)
	reFailed   = regexp.MustCompile(`^Failed (\S+) for (invalid user )?(.*?) from (\S+) port \d+`)
	reInvalid  = regexp.MustCompile(`^Invalid user (.*?) from (\S+)(?: port \d+)?\s*$`)
	reClosed   = regexp.MustCompile(`^(?:Connection closed by|Disconnected from) authenticating user (.*?) (\S+) port \d+ \[preauth\]`)
	reRepeated = regexp.MustCompile(`^message repeated (\d+) times: \[ ?(.*?) ?\]$`)
	reBan      = regexp.MustCompile(`\[([^\]]+)\]\s+Ban\s+(\S+)\s*$`)
)

// pidState suit une connexion sshd (un processus par connexion) pour ne pas compter deux fois
// la même tentative (« Invalid user » puis « Failed password for invalid user », fermeture
// « authenticating user » après un échec déjà compté).
type pidState struct {
	invalidPending bool // « Invalid user » compté, pas encore d'échec de mot de passe
	counted        bool // au moins une tentative comptée
}

// parser transforme les lignes du journal en événements SSH.
type parser struct {
	loc  *time.Location // fuseau de l'hôte (horodatage syslog classique, sans fuseau)
	now  func() time.Time
	pids map[string]*pidState
}

func newParser(loc *time.Location, now func() time.Time) *parser {
	if loc == nil {
		loc = time.Local
	}
	return &parser{loc: loc, now: now, pids: map[string]*pidState{}}
}

// syslogLine découpe « <horodatage> <hôte> <programme>[<pid>]: <message> ».
type syslogLine struct {
	ts   time.Time
	prog string
	pid  string
	msg  string
}

// splitSyslog reconnaît les deux formats d'horodatage : ISO 8601 de rsyslog récent
// (« 2026-10-28T10:15:01.123456+01:00 ») et syslog classique sans année (« Oct 28 10:15:01 »).
func (p *parser) splitSyslog(line string) (syslogLine, bool) {
	var out syslogLine
	var rest string
	if len(line) > 20 && line[4] == '-' && line[10] == 'T' {
		ts, r, ok := strings.Cut(line, " ")
		if !ok {
			return out, false
		}
		t, err := time.Parse(time.RFC3339Nano, ts)
		if err != nil {
			return out, false
		}
		out.ts, rest = t, r
	} else {
		if len(line) < 16 || line[15] != ' ' {
			return out, false
		}
		t, err := time.ParseInLocation("Jan _2 15:04:05", line[:15], p.loc)
		if err != nil {
			return out, false
		}
		now := p.now().In(p.loc)
		t = time.Date(now.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, p.loc)
		if t.After(now.Add(24 * time.Hour)) {
			t = t.AddDate(-1, 0, 0) // décembre lu en janvier
		}
		out.ts, rest = t, line[16:]
	}
	// Nom d'hôte, puis « programme[pid]: message ».
	_, rest, ok := strings.Cut(rest, " ")
	if !ok {
		return out, false
	}
	tag, msg, ok := strings.Cut(rest, ": ")
	if !ok {
		return out, false
	}
	if i := strings.IndexByte(tag, '['); i >= 0 && strings.HasSuffix(tag, "]") {
		out.prog, out.pid = tag[:i], tag[i+1:len(tag)-1]
	} else {
		out.prog = tag
	}
	out.msg = strings.TrimSpace(msg)
	return out, true
}

// parseAuth analyse une ligne du journal d'authentification ; seules les lignes de sshd comptent.
func (p *parser) parseAuth(line string) []store.SSHEvent {
	l, ok := p.splitSyslog(line)
	if !ok || (l.prog != "sshd" && !strings.HasPrefix(l.prog, "sshd-")) {
		return nil
	}
	msg, n := l.msg, 1
	if m := reRepeated.FindStringSubmatch(msg); m != nil {
		// Réduction des messages répétés de rsyslog : « message repeated 5 times: [ Failed password … ] ».
		n, _ = strconv.Atoi(m[1])
		n = max(1, min(n, 10_000))
		msg = m[2]
	}
	ts := l.ts.UnixMilli()
	key := l.pid
	switch {
	case strings.HasPrefix(msg, "Accepted "):
		m := reAccepted.FindStringSubmatch(msg)
		if m == nil {
			return nil
		}
		p.forget(key)
		return []store.SSHEvent{{TS: ts, Kind: store.SSHAccepted, User: cleanUser(m[2]), IP: cleanIP(m[3]), Method: cleanMethod(m[1]), Count: 1}}

	case strings.HasPrefix(msg, "Failed "):
		m := reFailed.FindStringSubmatch(msg)
		if m == nil {
			return nil
		}
		kind := store.SSHFail
		if m[2] != "" {
			kind = store.SSHInvalid
		}
		st := p.state(key)
		if st != nil && st.invalidPending && kind == store.SSHInvalid {
			// Déjà compté par « Invalid user » sur la même connexion.
			st.invalidPending = false
			if n--; n == 0 {
				return nil
			}
		}
		if st != nil {
			st.counted = true
		}
		return []store.SSHEvent{{TS: ts, Kind: kind, User: cleanUser(m[3]), IP: cleanIP(m[4]), Method: cleanMethod(m[1]), Count: n}}

	case strings.HasPrefix(msg, "Invalid user "):
		m := reInvalid.FindStringSubmatch(msg)
		if m == nil {
			return nil
		}
		if st := p.state(key); st != nil {
			st.invalidPending, st.counted = true, true
		}
		return []store.SSHEvent{{TS: ts, Kind: store.SSHInvalid, User: cleanUser(m[1]), IP: cleanIP(m[2]), Count: n}}

	case strings.Contains(msg, "authenticating user"):
		// Utilisateur existant, connexion fermée avant toute authentification réussie (clé refusée,
		// mot de passe désactivé) : une tentative, si rien n'a déjà été compté pour cette connexion.
		m := reClosed.FindStringSubmatch(msg)
		if m == nil {
			return nil
		}
		st := p.state(key)
		counted := st != nil && st.counted
		p.forget(key)
		if counted {
			return nil
		}
		return []store.SSHEvent{{TS: ts, Kind: store.SSHFail, User: cleanUser(m[1]), IP: cleanIP(m[2]), Count: n}}

	case strings.HasPrefix(msg, "Disconnected from") || strings.HasPrefix(msg, "Connection closed by"):
		p.forget(key) // fin de connexion
	}
	return nil
}

// state renvoie le suivi de la connexion (nil sans pid).
func (p *parser) state(pid string) *pidState {
	if pid == "" {
		return nil
	}
	st, ok := p.pids[pid]
	if !ok {
		if len(p.pids) >= maxPids {
			p.pids = map[string]*pidState{}
		}
		st = &pidState{}
		p.pids[pid] = st
	}
	return st
}

func (p *parser) forget(pid string) { delete(p.pids, pid) }

// parseBan analyse une ligne de /var/log/fail2ban.log (« 2026-10-28 10:15:01,234 fail2ban.actions [123]:
// NOTICE [sshd] Ban 1.2.3.4 ») ; seules les prisons SSH (et « recidive ») comptent.
func (p *parser) parseBan(line string) []store.SSHEvent {
	if len(line) < 24 {
		return nil
	}
	m := reBan.FindStringSubmatch(line)
	if m == nil || strings.Contains(line, "Restore Ban") {
		return nil
	}
	jail := strings.ToLower(m[1])
	if !strings.Contains(jail, "ssh") && jail != "recidive" {
		return nil
	}
	t, err := time.ParseInLocation("2006-01-02 15:04:05", line[:19], p.loc)
	if err != nil {
		return nil
	}
	return []store.SSHEvent{{TS: t.UnixMilli(), Kind: store.SSHBan, IP: cleanIP(m[2]), Method: cleanUser(m[1]), Count: 1}}
}

// cleanUser borne et assainit un nom d'utilisateur (caractères de contrôle retirés).
func cleanUser(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == utf8.RuneError || unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	if utf8.RuneCountInString(s) > maxUser {
		s = string([]rune(s)[:maxUser])
	}
	return s
}

// cleanIP normalise une adresse IP ; un nom d'hôte (UseDNS) est gardé, borné.
func cleanIP(s string) string {
	if a, err := netip.ParseAddr(s); err == nil {
		return a.Unmap().String()
	}
	return cleanUser(s)
}

func cleanMethod(s string) string {
	switch {
	case s == "keyboard-interactive/pam":
		return "keyboard-interactive"
	case len(s) > 32:
		return s[:32]
	}
	return s
}

// merge regroupe les tentatives identiques (type, utilisateur, adresse) rapprochées d'une minute au plus.
func merge(events []store.SSHEvent) []store.SSHEvent {
	out := make([]store.SSHEvent, 0, len(events))
	last := map[string]int{} // clé → index dans out
	for _, e := range events {
		if e.Kind != store.SSHFail && e.Kind != store.SSHInvalid {
			out = append(out, e)
			continue
		}
		k := e.Kind + "\x00" + e.User + "\x00" + e.IP + "\x00" + e.Method
		if i, ok := last[k]; ok && e.TS-out[i].TS < mergeEvery.Milliseconds() && e.TS >= out[i].TS {
			out[i].Count += e.Count
			continue
		}
		last[k] = len(out)
		out = append(out, e)
	}
	return out
}
