package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"slices"
	"sync"
	"time"

	"reviewqueue/internal/store"
)

type event struct {
	name string
	data []byte
}

const (
	maxStreams = 8
	maxQueued  = 16
)

// hub sends events to the open streams of one user.
type hub struct {
	mu      sync.Mutex
	streams map[int64]map[*subscriber]struct{}
}

type subscriber struct {
	session string
	// queue is guarded by the hub lock. wake tells the stream that the queue has events.
	queue []event
	wake  chan struct{}
	done  chan struct{}
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
	sub := &subscriber{session: session, wake: make(chan struct{}, 1), done: make(chan struct{})}
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
			h.end(userID, sub)
		}
	}
}

func (h *hub) end(userID int64, sub *subscriber) {
	h.remove(userID, sub)
	close(sub.done)
}

func (h *hub) remove(userID int64, sub *subscriber) {
	delete(h.streams[userID], sub)
	if len(h.streams[userID]) == 0 {
		delete(h.streams, userID)
	}
}

// publish queues the event on every stream of the user. A requests or tasks event holds the full state, so it replaces the queued event of the same name.
// A stream with a full queue is ended, so that its client connects again and loads the current state.
func (h *hub) publish(userID int64, name string, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		log.Printf("encode %s event: %v", name, err)
		return
	}
	state := name == "requests" || name == "tasks"
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.streams[userID] {
		if state {
			sub.queue = slices.DeleteFunc(sub.queue, func(e event) bool { return e.name == name })
		}
		if len(sub.queue) >= maxQueued {
			log.Printf("end a slow event stream of user %d", userID)
			h.end(userID, sub)
			continue
		}
		sub.queue = append(sub.queue, event{name: name, data: data})
		select {
		case sub.wake <- struct{}{}:
		default:
		}
	}
}

func (h *hub) take(sub *subscriber) []event {
	h.mu.Lock()
	defer h.mu.Unlock()
	queue := sub.queue
	sub.queue = nil
	return queue
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
		case <-sub.wake:
			queue := s.hub.take(sub)
			write = func() {
				for _, e := range queue {
					writeEvent(w, e)
				}
			}
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
