package api

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"reviewqueue/internal/config"
	"reviewqueue/internal/store"
)

const (
	aliceToken  = "ghp_aliceToken0000"
	aliceToken2 = "ghp_aliceSecondToken"
	bobToken    = "ghp_bobToken0000"
	carolToken  = "ghp_carolNoOrgScope"
	eveToken    = "ghp_eveNotAllowed"
	daveToken   = "ghp_daveNotAllowedNoOrgScope"
	aliceKey    = "sk-or-v1-aliceKey"
	bobWrongKey = "sk-or-v1-bobWrongKey"
	aliceRepo   = "getsentry/alice-repo"
	bobRepo     = "getsentry/bob-repo"
)

type identity struct {
	login, scopes string
	number        int
}

var identities = map[string]identity{
	aliceToken:  {"alice", "read:org, repo", 101},
	aliceToken2: {"alice", "repo, read:org", 101},
	bobToken:    {"bob", "repo, read:org", 202},
	carolToken:  {"carol", "repo", 303},
	eveToken:    {"eve", "repo, read:org", 404},
	daveToken:   {"dave", "repo", 505},
}

// fakeGitHub answers each token with the data of its own user.
type fakeGitHub struct {
	mu        sync.Mutex
	polls     map[string]int
	repoLists map[string]int
	revoked   map[string]bool
}

func (f *fakeGitHub) repoListCount(token string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.repoLists[token]
}

func (f *fakeGitHub) pollCount(token string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.polls[token]
}

func (f *fakeGitHub) revoke(token string, revoked bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked[token] = revoked
}

func (f *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	id, known := identities[token]
	f.mu.Lock()
	revoked := f.revoked[token]
	f.mu.Unlock()
	if !known || revoked {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"message":"Bad credentials"}`)
		return
	}
	if r.URL.Path == "/user" {
		w.Header().Set("X-OAuth-Scopes", id.scopes)
		fmt.Fprintf(w, `{"login":%q}`, id.login)
		return
	}
	if r.Method == http.MethodGet {
		switch {
		case strings.HasSuffix(r.URL.Path, "/contents/AGENTS.md"):
			fmt.Fprintf(w, "agents of %s at %s", id.login, r.URL.Query().Get("ref"))
		case strings.Contains(r.URL.Path, "/pulls/"):
			fmt.Fprintf(w, "diff of %s for %s", r.URL.Path, id.login)
		default:
			http.NotFound(w, r)
		}
		return
	}
	var req struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	switch {
	case strings.Contains(req.Query, "search("):
		f.mu.Lock()
		f.polls[token]++
		f.mu.Unlock()
		nodes := ""
		if q, _ := req.Variables["q"].(string); strings.Contains(q, "repo:getsentry/"+id.login+"-repo ") {
			nodes = fmt.Sprintf(`{"number":%d,"title":"PR for %s","url":"u","isDraft":false,"updatedAt":"2026-01-01T00:00:00Z",
				"additions":1,"deletions":2,"author":{"login":"x"},
				"reviewRequests":{"nodes":[{"requestedReviewer":{"__typename":"User","login":%q}}]}}`, id.number, id.login, id.login)
		}
		fmt.Fprintf(w, `{"data":{"search":{"nodes":[%s]},"repositoryOwner":{"teams":{"nodes":[]}}}}`, nodes)
	case strings.Contains(req.Query, "repositories(first"):
		f.mu.Lock()
		f.repoLists[token]++
		f.mu.Unlock()
		fmt.Fprintf(w, `{"data":{"viewer":{"repositories":{"nodes":[{"nameWithOwner":"%s/one"}],"pageInfo":{"hasNextPage":false}}}}}`, id.login)
	case strings.Contains(req.Query, "pullRequest(number"):
		fmt.Fprintf(w, `{"data":{"repository":{"pullRequest":{"title":"%s/%s#%v","body":"","headRefName":"h","baseRefName":"main","headRefOid":"sha-1",
			"changedFiles":1,"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z","reviewDecision":null,
			"labels":{"nodes":[]},"reviewRequests":{"nodes":[]},"latestReviews":{"nodes":[]},"commits":{"nodes":[]}}}}}`,
			id.login, req.Variables["name"], req.Variables["number"])
	default:
		http.NotFound(w, r)
	}
}

// fakeOpenRouter accepts only aliceKey. The model slow/model answers only after the request ends.
type fakeOpenRouter struct {
	mu        sync.Mutex
	bodies    []string
	started   chan struct{}
	cancelled chan struct{}
}

func (f *fakeOpenRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+aliceKey {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch r.URL.Path {
	case "/key":
		io.WriteString(w, `{"data":{}}`)
	case "/models/user":
		io.WriteString(w, `{"data":[{"id":"alice/model","supported_parameters":["structured_outputs"]},{"id":"plain/model","supported_parameters":[]}]}`)
	case "/chat/completions":
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.bodies = append(f.bodies, string(body))
		f.mu.Unlock()
		if bytes.Contains(body, []byte(`"model":"slow/model"`)) {
			f.started <- struct{}{}
			select {
			case <-r.Context().Done():
				f.cancelled <- struct{}{}
			case <-time.After(10 * time.Second):
			}
			return
		}
		content, _ := json.Marshal(`{"risk":"yellow","summary":"Looks fine.","findings":[{"severity":"low","file":"a.go","title":"t","detail":"d"}]}`)
		fmt.Fprintf(w, `{"choices":[{"message":{"content":%s}}]}`, content)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeOpenRouter) lastBody() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.bodies) == 0 {
		return ""
	}
	return f.bodies[len(f.bodies)-1]
}

type harness struct {
	t          *testing.T
	url        string
	dbPath     string
	server     *Server
	github     *fakeGitHub
	openrouter *fakeOpenRouter
	close      func()
}

// newHarness runs the API with fake GitHub and OpenRouter servers. setup can add rows and change the server before it starts.
func newHarness(t *testing.T, setup func(*Server)) *harness {
	return newHarnessAt(t, filepath.Join(t.TempDir(), "test.db"), defaultAllowed, setup)
}

const defaultAllowed = "Alice, bob,carol"

// newHarnessAt runs the API on the database at dbPath. close stops it, also before the test ends.
func newHarnessAt(t *testing.T, dbPath, allowed string, setup func(*Server)) *harness {
	t.Helper()
	gh := &fakeGitHub{polls: map[string]int{}, repoLists: map[string]int{}, revoked: map[string]bool{}}
	ghServer := httptest.NewServer(gh)
	openRouter := &fakeOpenRouter{started: make(chan struct{}, 4), cancelled: make(chan struct{}, 4)}
	orServer := httptest.NewServer(openRouter)

	cfg, err := config.Load(func(name string) string {
		return map[string]string{
			"REVIEWQ_SECRET_KEY":     "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
			"REVIEWQ_ALLOWED_LOGINS": allowed,
		}[name]
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg.GitHubURL, cfg.OpenRouterURL = ghServer.URL, orServer.URL

	db, err := store.Open(dbPath, cfg.SecretKey)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	s := New(ctx, cfg, db)
	s.pollInterval = 50 * time.Millisecond
	s.keepAlive = 100 * time.Millisecond
	s.writeTimeout = 500 * time.Millisecond
	if setup != nil {
		setup(s)
	}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	api := httptest.NewUnstartedServer(s.Handler())
	api.Config.BaseContext = func(net.Listener) context.Context { return ctx }
	api.Config.ReadTimeout = readTimeout
	api.Config.IdleTimeout = time.Minute
	api.Start()

	closeOnce := sync.OnceFunc(func() {
		cancel()
		api.Close()
		s.Wait()
		db.Close()
		ghServer.Close()
		orServer.Close()
	})
	t.Cleanup(closeOnce)
	return &harness{t: t, url: api.URL, dbPath: dbPath, server: s, github: gh, openrouter: openRouter, close: closeOnce}
}

// do sends a request and fails the test when the response contains a GitHub token or an OpenRouter key.
func (h *harness) do(method, path, session string, body any) (int, []byte) {
	h.t.Helper()
	var reader io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	}
	req, _ := http.NewRequest(method, h.url+path, reader)
	if session != "" {
		req.Header.Set("Authorization", "Bearer "+session)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	for _, secret := range []string{aliceToken, aliceToken2, bobToken, carolToken, eveToken, daveToken, aliceKey, bobWrongKey} {
		if bytes.Contains(data, []byte(secret)) {
			h.t.Fatalf("%s %s response contains a secret", method, path)
		}
	}
	return resp.StatusCode, data
}

func (h *harness) json(method, path, session string, body any, want int, out any) {
	h.t.Helper()
	status, data := h.do(method, path, session, body)
	if status != want {
		h.t.Fatalf("%s %s = %d %s, want %d", method, path, status, data, want)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			h.t.Fatalf("%s %s: %v in %s", method, path, err, data)
		}
	}
}

func (h *harness) signIn(token string) string {
	h.t.Helper()
	var resp struct{ SessionToken, Login string }
	h.json("POST", "/api/session", "", map[string]string{"githubToken": token}, http.StatusOK, &resp)
	if len(resp.SessionToken) != 43 {
		h.t.Fatalf("session token has %d characters", len(resp.SessionToken))
	}
	return resp.SessionToken
}

func (h *harness) wantError(method, path, session string, body any, status int, message string) {
	h.t.Helper()
	var resp struct{ Error string }
	h.json(method, path, session, body, status, &resp)
	if resp.Error != message {
		h.t.Errorf("%s %s error = %q, want %q", method, path, resp.Error, message)
	}
}

// waitRequests polls the API until check accepts the review requests.
func (h *harness) waitRequests(session string, check func(Requests) bool) Requests {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var got Requests
		h.json("GET", "/api/review-requests", session, nil, http.StatusOK, &got)
		if check(got) {
			return got
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("review requests did not change: %+v", got)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

type stream struct {
	t      *testing.T
	reader *bufio.Reader
}

func (h *harness) events(session string) *stream {
	h.t.Helper()
	status, events := h.openEvents(session)
	if status != http.StatusOK {
		h.t.Fatalf("events: %d", status)
	}
	return events
}

// openEvents returns the status, and a stream for status 200.
func (h *harness) openEvents(session string) (int, *stream) {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	req, _ := http.NewRequestWithContext(ctx, "GET", h.url+"/api/events", nil)
	req.Header.Set("Authorization", "Bearer "+session)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() {
		cancel()
		resp.Body.Close()
	})
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, nil
	}
	if resp.Header.Get("Content-Type") != "text/event-stream" {
		h.t.Fatalf("events content type %s", resp.Header.Get("Content-Type"))
	}
	return resp.StatusCode, &stream{t: h.t, reader: bufio.NewReader(resp.Body)}
}

// waitClosed reads the stream until the server ends it.
func (s *stream) waitClosed() {
	s.t.Helper()
	for {
		if _, err := s.reader.ReadString('\n'); err != nil {
			if !errors.Is(err, io.EOF) {
				s.t.Fatalf("stream did not end: %v", err)
			}
			return
		}
	}
}

// next returns the next event, or "comment" for a comment line.
func (s *stream) next() (name, data string) {
	s.t.Helper()
	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			s.t.Fatalf("read event: %v", err)
		}
		line = strings.TrimSuffix(line, "\n")
		switch {
		case strings.HasPrefix(line, ":"):
			s.reader.ReadString('\n')
			return "comment", line
		case strings.HasPrefix(line, "event: "):
			name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data = strings.TrimPrefix(line, "data: ")
		case line == "":
			return name, data
		}
	}
}

// nextEvent skips comments and returns the next event.
func (s *stream) nextEvent() (name, data string) {
	s.t.Helper()
	for {
		if name, data = s.next(); name != "comment" {
			return name, data
		}
	}
}

// nextRequests skips comments and tasks events and returns the next requests event.
func (s *stream) nextRequests() Requests {
	s.t.Helper()
	for {
		name, data := s.nextEvent()
		if name == "tasks" {
			continue
		}
		if name != "requests" {
			s.t.Fatalf("event %q", name)
		}
		var r Requests
		if err := json.Unmarshal([]byte(data), &r); err != nil {
			s.t.Fatal(err)
		}
		return r
	}
}

func numbers(r Requests) []int {
	var n []int
	for _, row := range r.Rows {
		n = append(n, row.Number)
	}
	return n
}

func TestSignIn(t *testing.T) {
	h := newHarness(t, nil)

	var resp struct{ SessionToken, Login string }
	h.json("POST", "/api/session", "", map[string]string{"githubToken": " " + aliceToken + "\n"}, http.StatusOK, &resp)
	if resp.Login != "alice" {
		t.Errorf("login %q", resp.Login)
	}
	var me struct{ Login string }
	h.json("GET", "/api/me", resp.SessionToken, nil, http.StatusOK, &me)
	if me.Login != "alice" {
		t.Errorf("me %q", me.Login)
	}

	h.wantError("POST", "/api/session", "", map[string]string{"githubToken": carolToken}, http.StatusBadRequest, "The token needs the scopes repo and read:org")
	h.wantError("POST", "/api/session", "", map[string]string{"githubToken": "ghp_unknown"}, http.StatusUnauthorized, "GitHub rejected the token")
	h.wantError("POST", "/api/session", "", map[string]string{"githubToken": eveToken}, http.StatusForbidden, "This GitHub account is not allowed on this server")
	h.wantError("POST", "/api/session", "", map[string]string{"githubToken": daveToken}, http.StatusForbidden, "This GitHub account is not allowed on this server")
	h.wantError("POST", "/api/session", "", map[string]string{}, http.StatusBadRequest, "githubToken is required")

	raw, err := sql.Open("sqlite", "file:"+h.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var logins []string
	rows, err := raw.Query(`SELECT login FROM users ORDER BY login`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var login string
		rows.Scan(&login)
		logins = append(logins, login)
	}
	rows.Close()
	if !slices.Equal(logins, []string{"alice"}) {
		t.Errorf("stored users %v, want only alice", logins)
	}
}

func TestSignInAcceptsBroaderOrgScopes(t *testing.T) {
	for _, scopes := range [][]string{{"repo", "read:org"}, {"admin:org", "repo"}, {"write:org", "repo"}} {
		if !hasScopes(scopes) {
			t.Errorf("%v rejected", scopes)
		}
	}
	for _, scopes := range [][]string{{"repo"}, {"read:org"}, {"public_repo", "read:org"}, nil} {
		if hasScopes(scopes) {
			t.Errorf("%v accepted", scopes)
		}
	}
}

func TestSignInIsRateLimited(t *testing.T) {
	h := newHarness(t, nil)
	for range 10 {
		h.wantError("POST", "/api/session", "", map[string]string{"githubToken": "ghp_unknown"}, http.StatusUnauthorized, "GitHub rejected the token")
	}
	h.wantError("POST", "/api/session", "", map[string]string{"githubToken": aliceToken}, http.StatusTooManyRequests, tooManySignIns)
}

func TestRoutesNeedSession(t *testing.T) {
	h := newHarness(t, nil)
	session := h.signIn(aliceToken)
	routes := [][2]string{
		{"DELETE", "/api/session"},
		{"DELETE", "/api/sessions"},
		{"GET", "/api/me"},
		{"GET", "/api/settings"},
		{"PUT", "/api/settings"},
		{"GET", "/api/repos"},
		{"GET", "/api/models"},
		{"GET", "/api/review-requests"},
		{"GET", "/api/repos/getsentry/alice-repo/pulls/1"},
		{"GET", "/api/repos/getsentry/alice-repo/pulls/1/review"},
		{"POST", "/api/repos/getsentry/alice-repo/pulls/1/review"},
		{"GET", "/api/tasks"},
		{"DELETE", "/api/tasks/x"},
		{"GET", "/api/events"},
	}
	for _, route := range routes {
		for _, token := range []string{"", "not-a-session", aliceToken} {
			if status, _ := h.do(route[0], route[1], token, nil); status != http.StatusUnauthorized {
				t.Errorf("%s %s with %q = %d, want 401", route[0], route[1], token, status)
			}
		}
	}

	h.json("DELETE", "/api/session", session, nil, http.StatusNoContent, nil)
	h.wantError("GET", "/api/me", session, nil, http.StatusUnauthorized, "The session is not valid. Sign in again")
}

func TestUsersSeeOnlyOwnData(t *testing.T) {
	h := newHarness(t, nil)
	alice, bob := h.signIn(aliceToken), h.signIn(bobToken)
	aliceEvents, bobEvents := h.events(alice), h.events(bob)
	if r := aliceEvents.nextRequests(); r.Repo != "" || len(r.Rows) != 0 {
		t.Errorf("first alice event %+v", r)
	}
	bobEvents.nextRequests()

	var settings settingsResponse
	h.json("PUT", "/api/settings", alice, map[string]string{"repo": aliceRepo, "model": "m/alice", "openRouterKey": aliceKey}, http.StatusOK, &settings)
	if settings != (settingsResponse{Repo: aliceRepo, Model: "m/alice", HasOpenRouterKey: true}) {
		t.Errorf("alice settings %+v", settings)
	}
	h.json("GET", "/api/settings", bob, nil, http.StatusOK, &settings)
	if settings != (settingsResponse{}) {
		t.Errorf("bob settings before PUT %+v", settings)
	}
	h.json("PUT", "/api/settings", bob, map[string]string{"repo": bobRepo, "openRouterKey": bobWrongKey}, http.StatusOK, &settings)
	h.json("GET", "/api/settings", alice, nil, http.StatusOK, &settings)
	if settings != (settingsResponse{Repo: aliceRepo, Model: "m/alice", HasOpenRouterKey: true}) {
		t.Errorf("alice settings after bob PUT %+v", settings)
	}

	var repos []string
	h.json("GET", "/api/repos", alice, nil, http.StatusOK, &repos)
	if !slices.Equal(repos, []string{"alice/one"}) {
		t.Errorf("alice repos %v", repos)
	}
	h.json("GET", "/api/repos", bob, nil, http.StatusOK, &repos)
	if !slices.Equal(repos, []string{"bob/one"}) {
		t.Errorf("bob repos %v", repos)
	}

	var models []string
	h.json("GET", "/api/models", alice, nil, http.StatusOK, &models)
	if !slices.Equal(models, []string{"alice/model"}) {
		t.Errorf("alice models %v", models)
	}
	h.wantError("GET", "/api/models", bob, nil, http.StatusBadRequest, "Invalid API key")

	aliceRequests := h.waitRequests(alice, func(r Requests) bool { return r.FetchedAt != nil })
	if aliceRequests.Repo != aliceRepo || !slices.Equal(numbers(aliceRequests), []int{101}) || !slices.Equal(aliceRequests.Rows[0].Reasons, []string{"you"}) {
		t.Errorf("alice requests %+v", aliceRequests)
	}
	bobRequests := h.waitRequests(bob, func(r Requests) bool { return r.FetchedAt != nil })
	if bobRequests.Repo != bobRepo || !slices.Equal(numbers(bobRequests), []int{202}) {
		t.Errorf("bob requests %+v", bobRequests)
	}

	var detail struct{ Title string }
	h.json("GET", "/api/repos/getsentry/sentry-cocoa/pulls/7", alice, nil, http.StatusOK, &detail)
	if detail.Title != "alice/sentry-cocoa#7" {
		t.Errorf("alice detail %q", detail.Title)
	}
	h.json("GET", "/api/repos/getsentry/sentry-cocoa/pulls/7", bob, nil, http.StatusOK, &detail)
	if detail.Title != "bob/sentry-cocoa#7" {
		t.Errorf("bob detail %q", detail.Title)
	}

	for range 3 {
		if r := aliceEvents.nextRequests(); r.Repo != aliceRepo || slices.Contains(numbers(r), 202) {
			t.Errorf("alice stream got %+v", r)
		}
		if r := bobEvents.nextRequests(); r.Repo != bobRepo || slices.Contains(numbers(r), 101) {
			t.Errorf("bob stream got %+v", r)
		}
	}
}

func TestBadRequests(t *testing.T) {
	h := newHarness(t, nil)
	alice := h.signIn(aliceToken)
	h.wantError("PUT", "/api/settings", alice, map[string]string{"repo": "getsentry/a b"}, http.StatusBadRequest, `invalid repository name "getsentry/a b"`)
	h.wantError("GET", "/api/repos/get%20sentry/x/pulls/1", alice, nil, http.StatusBadRequest, `invalid repository name "get sentry/x"`)
	h.wantError("GET", "/api/repos/getsentry/sentry-cocoa/pulls/x", alice, nil, http.StatusBadRequest, "The pull request number is not valid")
	h.wantError("GET", "/api/models", alice, nil, http.StatusBadRequest, "Set an OpenRouter API key first")

	var settings settingsResponse
	h.json("PUT", "/api/settings", alice, map[string]string{"openRouterKey": aliceKey}, http.StatusOK, &settings)
	h.json("PUT", "/api/settings", alice, map[string]string{"openRouterKey": ""}, http.StatusOK, &settings)
	if settings.HasOpenRouterKey {
		t.Error("an empty key did not delete the key")
	}
}

func TestPollerRepeatsAndStopsOnRejectedToken(t *testing.T) {
	h := newHarness(t, nil)
	alice := h.signIn(aliceToken)
	h.json("PUT", "/api/settings", alice, map[string]string{"repo": aliceRepo}, http.StatusOK, nil)
	h.waitRequests(alice, func(Requests) bool { return h.github.pollCount(aliceToken) >= 3 })

	h.github.revoke(aliceToken, true)
	got := h.waitRequests(alice, func(r Requests) bool { return r.Error != "" })
	if got.Error != rejectedAgain || !slices.Equal(numbers(got), []int{101}) {
		t.Errorf("after 401: %+v", got)
	}
	stopped := h.github.pollCount(aliceToken)
	time.Sleep(200 * time.Millisecond)
	if n := h.github.pollCount(aliceToken); n != stopped {
		t.Errorf("poller ran %d more times after a 401", n-stopped)
	}

	h.github.revoke(aliceToken, false)
	h.signIn(aliceToken)
	h.waitRequests(alice, func(r Requests) bool { return r.Error == "" && r.FetchedAt != nil })
}

func TestClearingRepoStopsPoller(t *testing.T) {
	h := newHarness(t, nil)
	alice := h.signIn(aliceToken)
	h.json("PUT", "/api/settings", alice, map[string]string{"repo": aliceRepo}, http.StatusOK, nil)
	h.waitRequests(alice, func(r Requests) bool { return r.FetchedAt != nil })
	events := h.events(alice)
	events.nextRequests()

	h.json("PUT", "/api/settings", alice, map[string]string{"repo": ""}, http.StatusOK, nil)
	if r := events.nextRequests(); r.Repo != "" || len(r.Rows) != 0 || r.FetchedAt != nil {
		t.Errorf("event after clearing the repository: %+v", r)
	}
	stopped := h.github.pollCount(aliceToken)
	time.Sleep(200 * time.Millisecond)
	if n := h.github.pollCount(aliceToken); n != stopped {
		t.Errorf("poller ran %d more times without a repository", n-stopped)
	}
}

func TestPollerStartsForStoredUsers(t *testing.T) {
	h := newHarness(t, func(s *Server) {
		db := s.store
		id, err := db.UpsertUser(context.Background(), "alice", aliceToken)
		if err != nil {
			t.Fatal(err)
		}
		repo := aliceRepo
		db.UpdateSettings(context.Background(), id, store.Settings{Repo: &repo})
		db.UpsertUser(context.Background(), "bob", bobToken)
	})
	deadline := time.Now().Add(5 * time.Second)
	for h.github.pollCount(aliceToken) < 2 {
		if time.Now().After(deadline) {
			t.Fatal("no poll without a client")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if n := h.github.pollCount(bobToken); n != 0 {
		t.Errorf("bob has no repository but was polled %d times", n)
	}
}

func TestEventsSendKeepAlive(t *testing.T) {
	h := newHarness(t, nil)
	events := h.events(h.signIn(aliceToken))
	if name, _ := events.next(); name != "requests" {
		t.Fatalf("first event %q", name)
	}
	if name, data := events.next(); name != "tasks" || data != "[]" {
		t.Fatalf("second event %q %s", name, data)
	}
	if name, line := events.next(); name != "comment" || line != ": keep-alive" {
		t.Errorf("got %q %q, want a keep-alive comment", name, line)
	}
}
