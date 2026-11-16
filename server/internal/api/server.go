package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"reviewqueue/internal/config"
	"reviewqueue/internal/github"
	"reviewqueue/internal/store"
)

const maxBodyBytes = 64 << 10

type Server struct {
	ctx   context.Context
	cfg   config.Config
	store *store.Store
	hub   *hub
	wg    sync.WaitGroup

	pollInterval time.Duration
	keepAlive    time.Duration
	signIns      *limiter

	mu        sync.Mutex
	polls     map[int64]*pollRun
	requests  map[int64]Requests
	repoCache map[int64]cachedRepos
}

type cachedRepos struct {
	at    time.Time
	repos []string
}

// New makes a server whose pollers and event streams stop when ctx ends.
func New(ctx context.Context, cfg config.Config, db *store.Store) *Server {
	return &Server{
		ctx:          ctx,
		cfg:          cfg,
		store:        db,
		hub:          newHub(),
		pollInterval: 30 * time.Second,
		keepAlive:    25 * time.Second,
		signIns:      newLimiter(10, time.Minute),
		polls:        map[int64]*pollRun{},
		requests:     map[int64]Requests{},
		repoCache:    map[int64]cachedRepos{},
	}
}

// Start begins polling for every stored user with a repository.
func (s *Server) Start() error {
	ids, err := s.store.UserIDsWithRepo(s.ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.restartPoll(id); err != nil {
			return err
		}
	}
	return nil
}

// Wait blocks until all pollers stop after the context ends.
func (s *Server) Wait() {
	s.wg.Wait()
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/session", s.signIn)
	mux.HandleFunc("DELETE /api/session", s.authed(s.signOut))
	mux.HandleFunc("GET /api/me", s.authed(s.me))
	mux.HandleFunc("GET /api/settings", s.authed(s.getSettings))
	mux.HandleFunc("PUT /api/settings", s.authed(s.putSettings))
	mux.HandleFunc("GET /api/repos", s.authed(s.repos))
	mux.HandleFunc("GET /api/models", s.authed(s.models))
	mux.HandleFunc("GET /api/review-requests", s.authed(s.reviewRequests))
	mux.HandleFunc("GET /api/repos/{owner}/{name}/pulls/{number}", s.authed(s.pullRequest))
	mux.HandleFunc("GET /api/events", s.authed(s.events))
	return mux
}

func (s *Server) authed(handler func(http.ResponseWriter, *http.Request, store.User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "Sign in first")
			return
		}
		id, err := s.store.SessionUserID(r.Context(), token)
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusUnauthorized, "The session is not valid. Sign in again")
			return
		}
		if err != nil {
			internalError(w, err)
			return
		}
		user, err := s.store.User(r.Context(), id)
		if err != nil {
			internalError(w, err)
			return
		}
		handler(w, r, user)
	}
}

func bearerToken(r *http.Request) (string, bool) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	token = strings.TrimSpace(token)
	return token, ok && token != ""
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "The request body is not valid JSON")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func internalError(w http.ResponseWriter, err error) {
	log.Printf("internal error: %v", err)
	writeError(w, http.StatusInternalServerError, "Internal server error")
}

const rejectedAgain = "GitHub rejected the token. Sign in again with a new token"

// GitHub messages reach the client, so that it can show SSO links and permission errors.
func githubError(w http.ResponseWriter, err error) {
	if errors.Is(err, github.ErrUnauthorized) {
		writeError(w, http.StatusUnauthorized, rejectedAgain)
		return
	}
	if errors.Is(err, context.Canceled) {
		return
	}
	writeError(w, http.StatusBadGateway, err.Error())
}

type limiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	hits   map[string]*window
}

type window struct {
	start time.Time
	count int
}

func newLimiter(limit int, period time.Duration) *limiter {
	return &limiter{limit: limit, window: period, hits: map[string]*window{}}
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	for k, w := range l.hits {
		if now.Sub(w.start) >= l.window {
			delete(l.hits, k)
		}
	}
	w := l.hits[key]
	if w == nil {
		w = &window{start: now}
		l.hits[key] = w
	}
	w.count++
	return w.count <= l.limit
}
