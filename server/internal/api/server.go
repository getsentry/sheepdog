package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"reviewqueue/internal/config"
	"reviewqueue/internal/github"
	"reviewqueue/internal/store"
	"reviewqueue/internal/tasks"
)

const maxBodyBytes = 64 << 10

type Server struct {
	ctx   context.Context
	cfg   config.Config
	store *store.Store
	hub   *hub
	tasks *tasks.Manager
	wg    sync.WaitGroup

	pollInterval   time.Duration
	keepAlive      time.Duration
	writeTimeout   time.Duration
	sessionCleanup time.Duration
	signIns        *limiter

	mu        sync.Mutex
	polls     map[int64]*pollRun
	restarts  map[int64]uint64
	requests  map[int64]Requests
	repoCache map[int64]cachedRepos
}

type cachedRepos struct {
	at    time.Time
	repos []string
}

// New makes a server whose pollers, tasks, and event streams stop when ctx ends.
func New(ctx context.Context, cfg config.Config, db *store.Store) *Server {
	h := newHub()
	return &Server{
		ctx:   ctx,
		cfg:   cfg,
		store: db,
		hub:   h,
		tasks: tasks.NewManager(ctx, func(userID int64, list []tasks.Task) {
			h.publish(userID, "tasks", list)
		}),
		pollInterval:   30 * time.Second,
		keepAlive:      25 * time.Second,
		writeTimeout:   10 * time.Second,
		sessionCleanup: time.Hour,
		signIns:        newLimiter(10, time.Minute),
		polls:          map[int64]*pollRun{},
		restarts:       map[int64]uint64{},
		requests:       map[int64]Requests{},
		repoCache:      map[int64]cachedRepos{},
	}
}

// Start deletes expired sessions, and begins polling for every stored user with a repository.
func (s *Server) Start() error {
	if err := s.store.DeleteExpiredSessions(s.ctx); err != nil {
		return err
	}
	s.wg.Go(s.cleanSessions)
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

func (s *Server) cleanSessions() {
	ticker := time.NewTicker(s.sessionCleanup)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			if err := s.store.DeleteExpiredSessions(s.ctx); err != nil && s.ctx.Err() == nil {
				log.Printf("delete expired sessions: %v", err)
			}
		}
	}
}

// Wait blocks until all pollers and tasks stop after the context ends.
func (s *Server) Wait() {
	s.wg.Wait()
	s.tasks.Wait()
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/session", s.signIn)
	mux.HandleFunc("DELETE /api/session", s.authed(s.signOut))
	mux.HandleFunc("DELETE /api/sessions", s.authed(s.signOutEverywhere))
	mux.HandleFunc("GET /api/me", s.authed(s.me))
	mux.HandleFunc("GET /api/settings", s.authed(s.getSettings))
	mux.HandleFunc("PUT /api/settings", s.authed(s.putSettings))
	mux.HandleFunc("GET /api/repos", s.authed(s.repos))
	mux.HandleFunc("GET /api/models", s.authed(s.models))
	mux.HandleFunc("GET /api/review-requests", s.authed(s.reviewRequests))
	mux.HandleFunc("GET /api/repos/{owner}/{name}/pulls/{number}", s.authed(s.pullRequest))
	mux.HandleFunc("GET /api/repos/{owner}/{name}/pulls/{number}/review", s.authed(s.getReview))
	mux.HandleFunc("POST /api/repos/{owner}/{name}/pulls/{number}/review", s.authed(s.startReview))
	mux.HandleFunc("GET /api/repos/{owner}/{name}/classifications", s.authed(s.listClassifications))
	mux.HandleFunc("GET /api/repos/{owner}/{name}/pulls/{number}/classification", s.authed(s.getClassification))
	mux.HandleFunc("POST /api/repos/{owner}/{name}/pulls/{number}/classification", s.authed(s.startClassification))
	mux.HandleFunc("GET /api/tasks", s.authed(s.listTasks))
	mux.HandleFunc("DELETE /api/tasks/{id}", s.authed(s.cancelTask))
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
			writeError(w, http.StatusUnauthorized, sessionNotValid)
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
		if !s.cfg.Allowed(user.Login) {
			writeError(w, http.StatusForbidden, notAllowed)
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

// clientIP trusts X-Forwarded-For only from a reverse proxy on the same host. Its last entry is the address that the proxy saw.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	forwarded := r.Header.Values("X-Forwarded-For")
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() || len(forwarded) == 0 {
		return host
	}
	entries := strings.Split(forwarded[len(forwarded)-1], ",")
	if last := strings.TrimSpace(entries[len(entries)-1]); last != "" {
		return last
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
	if cancelled(w, err) {
		return
	}
	log.Printf("internal error: %v", err)
	writeError(w, http.StatusInternalServerError, "Internal server error")
}

const notAllowed = "This GitHub account is not allowed on this server"

const sessionNotValid = "The session is not valid. Sign in again"

const rejectedAgain = "GitHub rejected the token. Sign in again with a new token"

// GitHub messages reach the client, so that it can show SSO links and permission errors.
func githubError(w http.ResponseWriter, err error) {
	if errors.Is(err, github.ErrUnauthorized) {
		writeError(w, http.StatusUnauthorized, rejectedAgain)
		return
	}
	if cancelled(w, err) {
		return
	}
	writeError(w, http.StatusBadGateway, err.Error())
}

// cancelled writes 503 for an error that comes from an ended request context, for example on shutdown.
func cancelled(w http.ResponseWriter, err error) bool {
	if !errors.Is(err, context.Canceled) {
		return false
	}
	writeError(w, http.StatusServiceUnavailable, "The server is shutting down")
	return true
}

// limiter counts failures for each key in a fixed window.
type limiter struct {
	mu       sync.Mutex
	limit    int
	window   time.Duration
	failures map[string]*window
	swept    time.Time
}

type window struct {
	start time.Time
	count int
}

func newLimiter(limit int, period time.Duration) *limiter {
	return &limiter{limit: limit, window: period, failures: map[string]*window{}}
}

func (l *limiter) blocked(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.current(key)
	return w != nil && w.count >= l.limit
}

func (l *limiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.current(key)
	if w == nil {
		w = &window{start: time.Now()}
		l.failures[key] = w
	}
	w.count++
}

// current returns the window of the key, or nil when it ended. It deletes all ended windows at most once in each window period.
func (l *limiter) current(key string) *window {
	now := time.Now()
	if now.Sub(l.swept) >= l.window {
		l.swept = now
		for k, w := range l.failures {
			if now.Sub(w.start) >= l.window {
				delete(l.failures, k)
			}
		}
	}
	w := l.failures[key]
	if w != nil && now.Sub(w.start) >= l.window {
		delete(l.failures, key)
		return nil
	}
	return w
}

// signInKey is the /64 prefix of an IPv6 address, because one client usually holds a full /64.
func signInKey(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	if addr = addr.Unmap(); addr.Is4() {
		return addr.String()
	}
	prefix, _ := addr.WithZone("").Prefix(64)
	return prefix.String()
}
