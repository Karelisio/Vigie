package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ---- Adresse du client ----

func (s *Server) clientIP(r *http.Request) string {
	if s.cfg.TrustProxy {
		if ip := strings.TrimSpace(r.Header.Get("X-Real-Ip")); ip != "" {
			return ip
		}
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ---- Limitation de débit (seau à jetons par IP) + blocage après échecs d'authentification ----

type bucket struct {
	tokens float64
	last   time.Time
}

type limiter struct {
	rate  float64
	burst float64

	mu       sync.Mutex
	buckets  map[string]*bucket
	failures map[string][]time.Time
	banned   map[string]time.Time
}

const (
	maxAuthFailures = 10
	failureWindow   = 10 * time.Minute
	banDuration     = 15 * time.Minute
)

func newLimiter(rate float64, burst int) *limiter {
	l := &limiter{
		rate: rate, burst: float64(burst),
		buckets: map[string]*bucket{}, failures: map[string][]time.Time{}, banned: map[string]time.Time{},
	}
	go l.gc()
	return l
}

func (l *limiter) allow(ip string) (ok bool, banned bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if until, b := l.banned[ip]; b {
		if now.Before(until) {
			return false, true
		}
		delete(l.banned, ip)
	}
	b := l.buckets[ip]
	if b == nil {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[ip] = b
	}
	b.tokens = min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now
	if b.tokens < 1 {
		return false, false
	}
	b.tokens--
	return true, false
}

func (l *limiter) authFailed(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	list := append(l.failures[ip], now)
	cut := 0
	for cut < len(list) && now.Sub(list[cut]) > failureWindow {
		cut++
	}
	list = list[cut:]
	l.failures[ip] = list
	if len(list) >= maxAuthFailures {
		l.banned[ip] = now.Add(banDuration)
		delete(l.failures, ip)
	}
}

func (l *limiter) gc() {
	for range time.Tick(5 * time.Minute) {
		l.mu.Lock()
		now := time.Now()
		for ip, b := range l.buckets {
			if now.Sub(b.last) > 10*time.Minute {
				delete(l.buckets, ip)
			}
		}
		for ip, until := range l.banned {
			if now.After(until) {
				delete(l.banned, ip)
			}
		}
		for ip, list := range l.failures {
			if len(list) == 0 || now.Sub(list[len(list)-1]) > failureWindow {
				delete(l.failures, ip)
			}
		}
		l.mu.Unlock()
	}
}

// ---- Chaîne de middlewares ----

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Unwrap permet à http.ResponseController (WebSocket, flush) d'accéder à l'original.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (s *Server) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		ip := s.clientIP(r)
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Error("panique", "chemin", r.URL.Path, "erreur", rec)
				http.Error(sw, "erreur interne", http.StatusInternalServerError)
			}
			if sw.status >= 400 || time.Since(start) > 2*time.Second {
				s.log.Log(r.Context(), levelFor(sw.status), "requête", "méthode", r.Method, "chemin", r.URL.Path,
					"code", sw.status, "ip", ip, "durée", time.Since(start).Round(time.Millisecond))
			}
		}()

		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")

		if r.URL.Path == "/healthz" {
			next.ServeHTTP(sw, r)
			return
		}
		ok, banned := s.limiter.allow(ip)
		if !ok {
			if banned {
				writeErr(sw, http.StatusTooManyRequests, "trop d'échecs d'authentification, réessaie plus tard")
			} else {
				writeErr(sw, http.StatusTooManyRequests, "trop de requêtes")
			}
			return
		}
		if isPublicStatus(r) {
			// Page d'état publique : l'identifiant secret de l'adresse tient lieu de token.
			next.ServeHTTP(sw, r)
			return
		}
		role := s.authorized(r)
		if role == "" {
			s.limiter.authFailed(ip)
			w.Header().Set("WWW-Authenticate", `Bearer realm="vigie"`)
			writeErr(sw, http.StatusUnauthorized, "token invalide ou absent")
			return
		}
		if role == roleReader && !readOnlyAllowed(r) {
			writeErr(sw, http.StatusForbidden, "accès en lecture seule")
			return
		}
		next.ServeHTTP(sw, r.WithContext(context.WithValue(r.Context(), roleKey, role)))
	})
}

func levelFor(code int) slog.Level {
	if code >= 500 {
		return slog.LevelError
	}
	if code == 401 || code == 429 {
		return slog.LevelWarn
	}
	return slog.LevelInfo
}

// authorized renvoie le rôle du porteur du token (« » s'il est refusé).
func (s *Server) authorized(r *http.Request) string {
	h := r.Header.Get("Authorization")
	tok, ok := strings.CutPrefix(h, "Bearer ")
	if !ok {
		return ""
	}
	// Comparaison à temps constant sur des empreintes de même longueur.
	a := sha256.Sum256([]byte(strings.TrimSpace(tok)))
	if subtle.ConstantTimeCompare(a[:], s.tokenHash[:]) == 1 {
		return roleAdmin
	}
	if s.shared != nil && s.shared.match(a) {
		return roleReader
	}
	return ""
}
