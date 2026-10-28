package api

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/karelisio/vigie/agent/internal/docker"
	"github.com/karelisio/vigie/agent/internal/monitor"
)

const (
	pingInterval = 25 * time.Second
	writeTimeout = 10 * time.Second
)

func accept(w http.ResponseWriter, r *http.Request) (*websocket.Conn, error) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// L'appli native n'envoie pas d'en-tête Origin ; l'accès est protégé par le token.
		InsecureSkipVerify: true,
		CompressionMode:    websocket.CompressionContextTakeover,
	})
	if err != nil {
		return nil, err
	}
	c.SetReadLimit(64 << 10)
	return c, nil
}

// pinger garde la connexion ouverte à travers les proxys et détecte les clients morts.
func pinger(ctx context.Context, c *websocket.Conn, cancel context.CancelFunc) {
	t := time.NewTicker(pingInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pctx, pc := context.WithTimeout(ctx, writeTimeout)
			err := c.Ping(pctx)
			pc()
			if err != nil {
				cancel()
				return
			}
		}
	}
}

func write(ctx context.Context, c *websocket.Conn, b []byte) error {
	wctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return c.Write(wctx, websocket.MessageText, b)
}

// live diffuse l'état temps réel : système, conteneurs, checks, vue d'ensemble, événements et alertes.
func (s *Server) live(w http.ResponseWriter, r *http.Request) {
	c, err := accept(w, r)
	if err != nil {
		return
	}
	defer c.CloseNow()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	ctx = c.CloseRead(ctx)
	go pinger(ctx, c, cancel)

	sub := s.mon.Subscribe()
	defer s.mon.Unsubscribe(sub)
	s.mon.Refresh()

	initial := [][]byte{
		monitor.Encode("hello", map[string]any{
			"name": s.cfg.ServerName, "version": s.version, "thresholds": s.mon.Thresholds(),
			"updates_enabled": s.upd.Enabled(), "stacks": true,
		}),
		monitor.Encode("overview", s.mon.Overview()),
		monitor.Encode("system", s.mon.Snapshot()),
		monitor.Encode("containers", s.mon.Containers()),
		monitor.Encode("checks", s.mon.Checks()),
	}
	// Opération en cours (reconnexion pendant une mise à jour).
	for _, j := range s.upd.Jobs() {
		if !j.Done {
			initial = append(initial, monitor.Encode("job", j))
		}
	}
	for _, b := range initial {
		if err := write(ctx, c, b); err != nil {
			return
		}
	}
	for {
		select {
		case <-ctx.Done():
			c.Close(websocket.StatusNormalClosure, "")
			return
		case b := <-sub.C:
			if sub.Dropped() {
				c.Close(websocket.StatusTryAgainLater, "client trop lent")
				return
			}
			if err := write(ctx, c, b); err != nil {
				return
			}
		}
	}
}

// logsStream envoie les dernières lignes puis le flux en direct, groupé par lots de 150 ms.
func (s *Server) logsStream(w http.ResponseWriter, r *http.Request) {
	ctr, ok := s.find(w, r)
	if !ok {
		return
	}
	tail := queryInt(r, "tail", 300, 0, 5000)
	tty := false
	if in, err := s.dk.Inspect(r.Context(), ctr.ID); err == nil {
		tty = in.Config.Tty
	}
	c, err := accept(w, r)
	if err != nil {
		return
	}
	defer c.CloseNow()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	ctx = c.CloseRead(ctx)
	go pinger(ctx, c, cancel)

	var mu sync.Mutex
	batch := make([]docker.LogLine, 0, 256)
	flush := func() error {
		mu.Lock()
		if len(batch) == 0 {
			mu.Unlock()
			return nil
		}
		b := monitor.Encode("lines", batch)
		batch = make([]docker.LogLine, 0, 256)
		mu.Unlock()
		return write(ctx, c, b)
	}

	done := make(chan error, 1)
	go func() {
		done <- s.dk.Logs(ctx, ctr.ID, tail, true, tty, func(l docker.LogLine) {
			mu.Lock()
			if len(batch) < 2000 {
				batch = append(batch, l)
			}
			mu.Unlock()
		})
	}()

	t := time.NewTicker(150 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case err := <-done:
			_ = flush()
			reason := "fin du flux"
			if err != nil && ctx.Err() == nil {
				reason = err.Error()
			}
			_ = write(ctx, c, monitor.Encode("end", map[string]string{"reason": reason}))
			c.Close(websocket.StatusNormalClosure, "")
			return
		case <-t.C:
			if err := flush(); err != nil {
				return
			}
		}
	}
}
