package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"reviewqueue/internal/store"
)

type event struct {
	name string
	data []byte
}

// hub sends events to the open streams of one user.
type hub struct {
	mu      sync.Mutex
	streams map[int64]map[chan event]struct{}
}

func newHub() *hub {
	return &hub{streams: map[int64]map[chan event]struct{}{}}
}

func (h *hub) subscribe(userID int64) (chan event, func()) {
	ch := make(chan event, 16)
	h.mu.Lock()
	if h.streams[userID] == nil {
		h.streams[userID] = map[chan event]struct{}{}
	}
	h.streams[userID][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.streams[userID], ch)
		if len(h.streams[userID]) == 0 {
			delete(h.streams, userID)
		}
		h.mu.Unlock()
	}
}

// publish drops the event for a stream that does not read. Every event holds the full state, so the next one repairs it.
func (h *hub) publish(userID int64, name string, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		log.Printf("encode %s event: %v", name, err)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.streams[userID] {
		select {
		case ch <- event{name: name, data: data}:
		default:
		}
	}
}

func (s *Server) events(w http.ResponseWriter, r *http.Request, user store.User) {
	events, unsubscribe := s.hub.subscribe(user.ID)
	defer unsubscribe()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	stream := http.NewResponseController(w)

	current, _ := json.Marshal(s.requestsFor(user))
	writeEvent(w, event{name: "requests", data: current})
	running, _ := json.Marshal(s.tasks.List(user.ID))
	writeEvent(w, event{name: "tasks", data: running})
	if stream.Flush() != nil {
		return
	}

	keepAlive := time.NewTicker(s.keepAlive)
	defer keepAlive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case e := <-events:
			writeEvent(w, e)
		case <-keepAlive.C:
			io.WriteString(w, ": keep-alive\n\n")
		}
		if stream.Flush() != nil {
			return
		}
	}
}

func writeEvent(w io.Writer, e event) {
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.name, e.data)
}
