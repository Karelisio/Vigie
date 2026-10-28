package monitor

import (
	"encoding/json"
	"sync"
	"sync/atomic"
)

// Message est le format des messages WebSocket envoyés à l'appli.
type Message struct {
	Type string `json:"type"`
	Data any    `json:"data,omitempty"`
}

// Sub est un abonné au flux temps réel.
type Sub struct {
	C       chan []byte
	dropped atomic.Bool
}

type hub struct {
	mu   sync.Mutex
	subs map[*Sub]struct{}
}

func (h *hub) subscribe() *Sub {
	s := &Sub{C: make(chan []byte, 64)}
	h.mu.Lock()
	if h.subs == nil {
		h.subs = map[*Sub]struct{}{}
	}
	h.subs[s] = struct{}{}
	h.mu.Unlock()
	return s
}

func (h *hub) unsubscribe(s *Sub) {
	h.mu.Lock()
	delete(h.subs, s)
	h.mu.Unlock()
}

func (h *hub) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

func (h *hub) broadcast(typ string, data any) {
	if h.count() == 0 {
		return
	}
	b, err := json.Marshal(Message{Type: typ, Data: data})
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		select {
		case s.C <- b:
		default:
			// Client trop lent : on le marque pour fermeture.
			s.dropped.Store(true)
		}
	}
}

// Dropped indique que l'abonné a perdu des messages et doit être déconnecté.
func (s *Sub) Dropped() bool { return s.dropped.Load() }

func Encode(typ string, data any) []byte {
	b, _ := json.Marshal(Message{Type: typ, Data: data})
	return b
}
