package server

import (
	"encoding/json"
	"sync"
)

// hub fans server events out to every connected client.
type hub struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
}

func newHub() *hub { return &hub{subs: map[chan []byte]struct{}{}} }

func (h *hub) subscribe() chan []byte {
	ch := make(chan []byte, 64)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *hub) unsubscribe(ch chan []byte) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
}

// publish sends an event of kind with data to every client. A client too
// far behind misses events rather than stalling everyone else; progress
// events are frequent and each one supersedes the last.
func (h *hub) publish(kind string, data any) {
	payload, err := json.Marshal(data)
	if err != nil {
		return
	}
	msg := []byte("event: " + kind + "\ndata: " + string(payload) + "\n\n")
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- msg:
		default:
		}
	}
}
