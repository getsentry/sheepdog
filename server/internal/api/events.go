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

const maxStreams = 8

// hub sends events to the open streams of one user.
type hub struct {
	mu      sync.Mutex
	streams map[int64]map[*subscriber]struct{}
}

type subscriber struct {
	session string
	events  chan event
	done    chan struct{}
}

func newHub() *hub {
	return &hub{streams: map[int64]map[*subscriber]struct{}{}}
}

// subscribe returns false when the user already has maxStreams open streams.
func (h *hub) subscribe(userID int64, session string) (*subscriber, func(), bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.streams[userID]) >= maxStreams {
		return nil, nil, false
	}
	sub := &subscriber{session: session, events: make(chan event, 16), done: make(chan struct{})}
	if h.streams[userID] == nil {
		h.streams[userID] = map[*subscriber]struct{}{}
	}
	h.streams[userID][sub] = struct{}{}
	return sub, func() {
		h.mu.Lock()
		h.remove(userID, sub)
		h.mu.Unlock()
	}, true
}

// close ends the streams of one session, or of every session of the user when session is empty.
func (h *hub) close(userID int64, session string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.streams[userID] {
		if session == "" || sub.session == session {
			h.remove(userID, sub)
			close(sub.done)
		}
	}
}

func (h *hub) remove(userID int64, sub *subscriber) {
	delete(h.streams[userID], sub)
	if len(h.streams[userID]) == 0 {
		delete(h.streams, userID)
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
	for sub := range h.streams[userID] {
		select {
		case sub.events <- event{name: name, data: data}:
		default:
		}
	}
}

func (s *Server) events(w http.ResponseWriter, r *http.Request, user store.User) {
	session, _ := bearerToken(r)
	sub, unsubscribe, ok := s.hub.subscribe(user.ID, session)
	if !ok {
		writeError(w, http.StatusTooManyRequests, "Too many open event streams. Close another client and try again")
		return
	}
	defer unsubscribe()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	stream := http.NewResponseController(w)
	send := func(write func()) bool {
		if stream.SetWriteDeadline(time.Now().Add(s.writeTimeout)) != nil {
			return false
		}
		write()
		return stream.Flush() == nil
	}

	current, _ := json.Marshal(s.requestsFor(user))
	running, _ := json.Marshal(s.tasks.List(user.ID))
	if !send(func() {
		writeEvent(w, event{name: "requests", data: current})
		writeEvent(w, event{name: "tasks", data: running})
	}) {
		return
	}

	keepAlive := time.NewTicker(s.keepAlive)
	defer keepAlive.Stop()
	for {
		var write func()
		select {
		case <-r.Context().Done():
			return
		case <-sub.done:
			return
		case e := <-sub.events:
			write = func() { writeEvent(w, e) }
		case <-keepAlive.C:
			write = func() { io.WriteString(w, ": keep-alive\n\n") }
		}
		if !send(write) {
			return
		}
	}
}

func writeEvent(w io.Writer, e event) {
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.name, e.data)
}
