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
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"sheepdog/internal/config"
	"sheepdog/internal/store"
)

const (
	aliceToken  = "ghp_aliceToken0000"
	aliceToken2 = "ghp_aliceSecondToken"
	// newAliceToken belongs to another GitHub user, who took the login alice after a rename.
	newAliceToken = "ghp_newAliceToken000"
	bobToken      = "ghp_bobToken0000"
	carolToken    = "ghp_carolNoOrgScope"
	eveToken      = "ghp_eveNotAllowed"
	daveToken     = "ghp_daveNotAllowedNoOrgScope"
	aliceKey      = "sk-or-v1-aliceKey"
	bobWrongKey   = "sk-or-v1-bobWrongKey"
	aliceRepo     = "getsentry/alice-repo"
	bobRepo       = "getsentry/bob-repo"
)

type identity struct {
	id            int64
	login, scopes string
	number        int
}

var identities = map[string]identity{
	aliceToken:    {1, "alice", "read:org, repo", 101},
	aliceToken2:   {1, "alice", "repo, read:org", 101},
	newAliceToken: {6, "alice", "repo, read:org", 101},
	bobToken:      {2, "bob", "repo, read:org", 202},
	carolToken:    {3, "carol", "repo", 303},
	eveToken:      {4, "eve", "repo, read:org", 404},
	daveToken:     {5, "dave", "repo", 505},
}

// stalledPull is a PR number whose detail request waits until the request ends.
const stalledPull = 999

// fakeGitHub answers each token with the data of its own user.
type fakeGitHub struct {
	stalled   chan struct{}
	mu        sync.Mutex
	polls     map[string]int
	repoLists map[string]int
	revoked   map[string]bool
	renamed   map[string]string
	// pull holds the lane facts of the PR in the list of each user.
	pull fakePull
	// rules maps "repo@ref" to the content of .github/sheepdog.yml. The default branch of every repository is main.
	rules map[string]string
	// defaultReads counts the reads of the rules file on the default branch for each "login repo".
	defaultReads map[string]int
	// branches maps "repo@branch" to the head commit of the branches other than main.
	branches map[string]string
	// setupPulls maps a repository to its open PR from sheepdog/setup.
	setupPulls    map[string]fakeSetupPull
	failCreateRef string
	writes        []string
	mergeReads    int
	failApprove   string
	// ciQueries counts the GraphQL queries that read check suites, and searches keeps the search queries.
	ciQueries int
	searches  []string
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

// rename gives the GitHub user of the token a new login, as a rename on GitHub does.
func (f *fakeGitHub) rename(token, login string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.renamed[token] = login
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
	if login, ok := f.renamed[token]; ok {
		id.login = login
	}
	f.mu.Unlock()
	if !known || revoked {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"message":"Bad credentials"}`)
		return
	}
	if r.URL.Path == "/user" {
		w.Header().Set("X-OAuth-Scopes", id.scopes)
		fmt.Fprintf(w, `{"login":%q,"id":%d}`, id.login, id.id)
		return
	}
	if r.URL.Path != "/graphql" && r.Method != http.MethodGet {
		f.serveWrite(w, r, id.login)
		return
	}
	if r.Method == http.MethodGet {
		switch {
		case strings.HasSuffix(r.URL.Path, "/contents/.github/sheepdog.yml"):
			f.serveRules(w, r, id.login)
		case strings.Count(r.URL.Path, "/") == 3, strings.Contains(r.URL.Path, "/git/ref/heads/"), strings.HasSuffix(r.URL.Path, "/pulls"):
			f.serveSetupRead(w, r)
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
	case strings.Contains(req.Query, "checkSuites"):
		f.serveCI(w, id, req.Query)
	case strings.Contains(req.Query, "search("):
		f.mu.Lock()
		f.polls[token]++
		f.searches = append(f.searches, req.Query)
		f.mu.Unlock()
		nodes := ""
		if q, _ := req.Variables["q"].(string); strings.Contains(q, "repo:getsentry/"+id.login+"-repo ") {
			nodes = f.pullNode(id, "CLEAN")
		}
		fmt.Fprintf(w, `{"data":{"search":{"nodes":[%s]},"repositoryOwner":{"teams":{"nodes":[]}}}}`, nodes)
	case strings.Contains(req.Query, "autoMergeAllowed"):
		f.serveMergePR(w, id, req.Variables)
	case strings.Contains(req.Query, "enablePullRequestAutoMerge"):
		variables, _ := json.Marshal(req.Variables)
		f.record(id.login, "automerge "+string(variables))
		io.WriteString(w, `{"data":{"enablePullRequestAutoMerge":{"clientMutationId":null}}}`)
	case strings.Contains(req.Query, "repositories(first"):
		f.mu.Lock()
		f.repoLists[token]++
		f.mu.Unlock()
		fmt.Fprintf(w, `{"data":{"viewer":{"repositories":{"nodes":[{"nameWithOwner":"%s/one"}],"pageInfo":{"hasNextPage":false}}}}}`, id.login)
	case strings.Contains(req.Query, "pullRequest(number") && req.Variables["number"] == float64(stalledPull):
		f.stalled <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
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
		reply := `{"risk":"yellow","summary":"Looks fine.","findings":[{"severity":"low","file":"a.go","title":"t","detail":"d"}]}`
		if bytes.Contains(body, []byte(`"name":"classification"`)) {
			reply = `{"score":4,"reason":"Medium risk."}`
		}
		content, _ := json.Marshal(reply)
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
	// shutdown ends the server context, as a signal does, and keeps the connections open.
	shutdown func()
}

// newHarness runs the API with fake GitHub and OpenRouter servers. setup can add rows and change the server before it starts.
func newHarness(t *testing.T, setup func(*Server)) *harness {
	return newHarnessAt(t, filepath.Join(t.TempDir(), "test.db"), defaultAllowed, setup)
}

const defaultAllowed = "Alice, bob,carol"

// newHarnessAt runs the API on the database at dbPath. close stops it, also before the test ends.
func newHarnessAt(t *testing.T, dbPath, allowed string, setup func(*Server)) *harness {
	t.Helper()
	gh := &fakeGitHub{
		stalled: make(chan struct{}, 1), polls: map[string]int{}, repoLists: map[string]int{}, revoked: map[string]bool{}, renamed: map[string]string{},
		pull: defaultPull(), rules: map[string]string{}, defaultReads: map[string]int{}, branches: map[string]string{}, setupPulls: map[string]fakeSetupPull{},
	}
	for _, repo := range []string{aliceRepo, bobRepo, "getsentry/sentry-cocoa"} {
		gh.rules[repo+"@main"] = "version: 1\n"
	}
	ghServer := httptest.NewServer(gh)
	openRouter := &fakeOpenRouter{started: make(chan struct{}, 4), cancelled: make(chan struct{}, 4)}
	orServer := httptest.NewServer(openRouter)

	cfg, err := config.Load(func(name string) string {
		return map[string]string{
			"SHEEPDOG_SECRET_KEY":     "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
			"SHEEPDOG_ALLOWED_LOGINS": allowed,
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
	return &harness{t: t, url: api.URL, dbPath: dbPath, server: s, github: gh, openrouter: openRouter, close: closeOnce, shutdown: cancel}
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
	for _, secret := range []string{aliceToken, aliceToken2, newAliceToken, bobToken, carolToken, eveToken, daveToken, aliceKey, bobWrongKey} {
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
		{"POST", "/api/repos/getsentry/alice-repo/pulls/1/approve-and-merge"},
		{"PUT", "/api/repos/getsentry/alice-repo/star"},
		{"DELETE", "/api/repos/getsentry/alice-repo/star"},
		{"GET", "/api/repos/getsentry/alice-repo/setup/template"},
		{"POST", "/api/repos/getsentry/alice-repo/setup"},
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
	if !reflect.DeepEqual(settings, settingsResponse{Repo: aliceRepo, Model: "m/alice", HasOpenRouterKey: true, Starred: []string{}}) {
		t.Errorf("alice settings %+v", settings)
	}
	h.json("GET", "/api/settings", bob, nil, http.StatusOK, &settings)
	if !reflect.DeepEqual(settings, settingsResponse{Starred: []string{}}) {
		t.Errorf("bob settings before PUT %+v", settings)
	}
	h.json("PUT", "/api/settings", bob, map[string]string{"repo": bobRepo, "openRouterKey": bobWrongKey}, http.StatusOK, &settings)
	h.json("GET", "/api/settings", alice, nil, http.StatusOK, &settings)
	if !reflect.DeepEqual(settings, settingsResponse{Repo: aliceRepo, Model: "m/alice", HasOpenRouterKey: true, Starred: []string{}}) {
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
		id, err := db.UpsertUser(context.Background(), 1, "alice", aliceToken)
		if err != nil {
			t.Fatal(err)
		}
		repo := aliceRepo
		db.UpdateSettings(context.Background(), id, store.Settings{Repo: &repo})
		db.UpsertUser(context.Background(), 2, "bob", bobToken)
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

func TestStreamWithFullQueueEndsAndKeepsReviews(t *testing.T) {
	h := newHub()
	sub, unsubscribe, _ := h.subscribe(1, "session")
	defer unsubscribe()
	for i := range 3 * maxQueued {
		h.publish(1, "requests", i)
		h.publish(1, "tasks", -i)
	}
	if queue := h.take(sub); len(queue) != 2 || queue[0].name != "requests" || string(queue[0].data) != fmt.Sprint(3*maxQueued-1) ||
		queue[1].name != "tasks" || string(queue[1].data) != fmt.Sprint(1-3*maxQueued) {
		t.Fatalf("queue of state events %+v, want only the newest requests and tasks", queue)
	}

	h.publish(1, "tasks", "running")
	for i := range maxQueued - 1 {
		h.publish(1, "review", i)
	}
	select {
	case <-sub.done:
		t.Fatal("the stream ended before its queue was full")
	default:
	}
	h.publish(1, "review", "one more")
	select {
	case <-sub.done:
	default:
		t.Fatal("a review event was dropped and the stream stays open")
	}
	if n := h.count(1); n != 0 {
		t.Errorf("%d streams left after the end", n)
	}
	h.publish(1, "review", "after the end")
	if queue := h.take(sub); len(queue) != maxQueued {
		t.Errorf("queue has %d events, want the %d events before the end", len(queue), maxQueued)
	}
}

func TestShutdownAnswersRunningRequests(t *testing.T) {
	h := newHarness(t, nil)
	alice := h.signIn(aliceToken)
	go func() {
		<-h.github.stalled
		h.shutdown()
	}()
	h.wantError("GET", fmt.Sprintf("/api/repos/getsentry/sentry-cocoa/pulls/%d", stalledPull), alice, nil, http.StatusServiceUnavailable, "The server is shutting down")
}
