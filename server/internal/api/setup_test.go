package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"sheepdog/internal/github"
	"sheepdog/internal/rules"
)

type fakeSetupPull struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
}

const (
	noRulesRepo = "getsentry/no-rules"
	mainSHA     = "sha-main"
	noRulesText = "getsentry/no-rules has no .github/sheepdog.yml on main. Set it up first, or set global rules in Settings"
)

func repoOf(path string) string {
	parts := strings.Split(strings.TrimPrefix(path, "/repos/"), "/")
	return parts[0] + "/" + parts[1]
}

func (f *fakeGitHub) serveSetupRead(w http.ResponseWriter, r *http.Request) {
	repo := repoOf(r.URL.Path)
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case strings.HasSuffix(r.URL.Path, "/pulls"):
		pulls := []fakeSetupPull{}
		owner, _, _ := strings.Cut(repo, "/")
		if pr, ok := f.setupPulls[repo]; ok && r.URL.Query().Get("head") == owner+":"+setupBranch && r.URL.Query().Get("base") == "main" && r.URL.Query().Get("state") == "open" {
			pulls = append(pulls, pr)
		}
		json.NewEncoder(w).Encode(pulls)
	case strings.Contains(r.URL.Path, "/git/ref/heads/"):
		branch := r.URL.Path[strings.Index(r.URL.Path, "/git/ref/heads/")+len("/git/ref/heads/"):]
		sha, ok := f.branches[repo+"@"+branch]
		if branch == "main" {
			sha, ok = mainSHA, true
		}
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"message":"Not Found"}`)
			return
		}
		fmt.Fprintf(w, `{"ref":"refs/heads/%s","object":{"type":"commit","sha":%q}}`, branch, sha)
	default:
		fmt.Fprintf(w, `{"full_name":%q,"default_branch":"main"}`, repo)
	}
}

// serveSetupWrite answers the writes of a setup. Like a ruleset, it rejects every commit and ref change on main.
func (f *fakeGitHub) serveSetupWrite(w http.ResponseWriter, r *http.Request, body []byte) bool {
	repo := repoOf(r.URL.Path)
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/git/refs"):
		var ref struct{ Ref, SHA string }
		json.Unmarshal(body, &ref)
		branch := strings.TrimPrefix(ref.Ref, "refs/heads/")
		switch _, exists := f.branches[repo+"@"+branch]; {
		case f.failCreateRef != "":
			w.WriteHeader(http.StatusUnprocessableEntity)
			fmt.Fprintf(w, `{"message":%q,"status":"422"}`, f.failCreateRef)
		case exists || branch == "main":
			w.WriteHeader(http.StatusUnprocessableEntity)
			io.WriteString(w, `{"message":"Reference already exists","status":"422"}`)
		default:
			f.branches[repo+"@"+branch] = ref.SHA
			io.WriteString(w, `{}`)
		}
	case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/contents/"):
		var file struct{ Branch string }
		json.Unmarshal(body, &file)
		if file.Branch == "" || file.Branch == "main" {
			w.WriteHeader(http.StatusConflict)
			io.WriteString(w, `{"message":"Repository rule violations found\n\nChanges must be made through a pull request.\n\n","status":"409"}`)
			return true
		}
		io.WriteString(w, `{"content":{}}`)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/pulls"):
		pr := fakeSetupPull{Number: 900, HTMLURL: "https://github.com/" + repo + "/pull/900"}
		f.setupPulls[repo] = pr
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(pr)
	default:
		return false
	}
	return true
}

func (f *fakeGitHub) defaultReadCount(login, repo string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.defaultReads[login+" "+repo]
}

func (f *fakeGitHub) removeRules(repoAtRef string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.rules, repoAtRef)
}

// setupHarness signs in alice with a repository without a rules file, and a poll interval that only pollNow can beat.
func setupHarness(t *testing.T) (*harness, string) {
	t.Helper()
	h := newHarness(t, func(s *Server) { s.pollInterval = time.Hour })
	alice := h.signIn(aliceToken)
	h.settings(alice, map[string]string{"repo": noRulesRepo, "model": "alice/model"})
	h.waitRequests(alice, func(r Requests) bool { return r.FetchedAt != nil })
	return h, alice
}

func TestRepositoryWithoutRulesShowsSetup(t *testing.T) {
	h, alice := setupHarness(t)
	_, data := h.do("GET", "/api/review-requests", alice, nil)
	for _, part := range []string{`"repo":"getsentry/no-rules"`, `"rows":[]`, `"lanes":[]`, `"defaultBranch":"main"`, `"setup":{"defaultBranch":"main","pullRequest":null}`} {
		if !strings.Contains(string(data), part) {
			t.Errorf("requests have no %s: %s", part, data)
		}
	}
	if n := h.github.pollCount(aliceToken); n != 0 {
		t.Errorf("%d review request searches without a rules file", n)
	}

	h.github.mu.Lock()
	h.github.setupPulls[noRulesRepo] = fakeSetupPull{Number: 31, HTMLURL: "https://github.com/getsentry/no-rules/pull/31"}
	h.github.mu.Unlock()
	if got := h.repoll(alice, "alice"); got.Setup == nil || got.Setup.PullRequest == nil ||
		*got.Setup.PullRequest != (github.PullLink{Number: 31, URL: "https://github.com/getsentry/no-rules/pull/31"}) {
		t.Errorf("setup with an open PR %+v", got.Setup)
	}
	_, data = h.do("GET", "/api/review-requests", alice, nil)
	if !strings.Contains(string(data), `"setup":{"defaultBranch":"main","pullRequest":{"number":31,"url":"https://github.com/getsentry/no-rules/pull/31"}}`) {
		t.Errorf("requests with an open setup PR: %s", data)
	}

	h.github.setRules(noRulesRepo+"@main", laneRules)
	got := h.repoll(alice, "alice")
	if got.Setup != nil || h.github.pollCount(aliceToken) != 1 {
		t.Errorf("requests after the merge of the setup PR %+v", got)
	}
	if _, data = h.do("GET", "/api/review-requests", alice, nil); strings.Contains(string(data), "setup") {
		t.Errorf("a repository with rules has a setup state: %s", data)
	}
	h.json("GET", "/api/repos/getsentry/no-rules/classifications", alice, nil, http.StatusOK, nil)
}

func TestRoutesRefuseRepositoryWithoutRules(t *testing.T) {
	h := newHarness(t, nil)
	alice := h.signIn(aliceToken)
	h.settings(alice, map[string]string{"repo": aliceRepo, "openRouterKey": aliceKey, "model": "alice/model"})
	h.waitRequests(alice, func(r Requests) bool { return r.FetchedAt != nil })
	for _, route := range [][2]string{
		{"GET", "/api/repos/getsentry/no-rules/pulls/101"},
		{"GET", "/api/repos/getsentry/no-rules/pulls/101/diff"},
		{"GET", "/api/repos/getsentry/no-rules/pulls/101/review"},
		{"POST", "/api/repos/getsentry/no-rules/pulls/101/review"},
		{"GET", "/api/repos/getsentry/no-rules/pulls/101/classification"},
		{"POST", "/api/repos/getsentry/no-rules/pulls/101/classification"},
		{"GET", "/api/repos/GetSentry/No-Rules/classifications"},
		{"POST", "/api/repos/getsentry/no-rules/pulls/101/approve-and-merge"},
	} {
		h.wantError(route[0], route[1], alice, map[string]string{"headSha": "sha-1"}, http.StatusConflict, noRulesText)
	}
	var list []any
	h.json("GET", "/api/tasks", alice, nil, http.StatusOK, &list)
	if len(list) != 0 || len(h.github.recorded()) != 0 {
		t.Errorf("a refused route started tasks %v or wrote %v", list, h.github.recorded())
	}
	if n := h.github.defaultReadCount("alice", noRulesRepo); n != 1 {
		t.Errorf("%d checks of the default branch, want 1 for all routes", n)
	}
}

func TestRulesCheckIsCachedForEachUser(t *testing.T) {
	h := newHarness(t, nil)
	alice, bob := h.signIn(aliceToken), h.signIn(bobToken)
	const path = "/api/repos/getsentry/sentry-cocoa/classifications"
	h.json("GET", path, alice, nil, http.StatusOK, nil)
	h.github.removeRules("getsentry/sentry-cocoa@main")
	h.json("GET", path, alice, nil, http.StatusOK, nil)
	h.wantError("GET", path, bob, nil, http.StatusConflict, "getsentry/sentry-cocoa has no .github/sheepdog.yml on main. Set it up first, or set global rules in Settings")
	if a, b := h.github.defaultReadCount("alice", "getsentry/sentry-cocoa"), h.github.defaultReadCount("bob", "getsentry/sentry-cocoa"); a != 1 || b != 1 {
		t.Errorf("checks of alice %d and bob %d, want 1 each", a, b)
	}

	age := func(by time.Duration) {
		h.server.mu.Lock()
		defer h.server.mu.Unlock()
		key := rulesKey{h.userID("alice"), "getsentry/sentry-cocoa"}
		check := h.server.rulesChecks[key]
		check.at = time.Now().Add(-by)
		h.server.rulesChecks[key] = check
	}
	age(29 * time.Second)
	h.json("GET", path, alice, nil, http.StatusOK, nil)
	age(30 * time.Second)
	h.wantError("GET", path, alice, nil, http.StatusConflict, "getsentry/sentry-cocoa has no .github/sheepdog.yml on main. Set it up first, or set global rules in Settings")
	if n := h.github.defaultReadCount("alice", "getsentry/sentry-cocoa"); n != 2 {
		t.Errorf("%d checks of alice after 30 seconds, want 2", n)
	}
}

func TestSetupTemplate(t *testing.T) {
	h := newHarness(t, nil)
	alice, bob := h.signIn(aliceToken), h.signIn(bobToken)
	h.settings(alice, map[string]string{"model": "alice/model"})
	for session, model := range map[string]string{alice: "alice/model", bob: ""} {
		var template struct{ Content string }
		h.json("GET", "/api/repos/getsentry/no-rules/setup/template", session, nil, http.StatusOK, &template)
		parsed, err := rules.Parse([]byte(template.Content))
		if err != nil {
			t.Fatalf("the template with model %q is not valid: %v\n%s", model, err, template.Content)
		}
		if parsed.Classification.Model != model || parsed.Classification.CI || len(parsed.Lanes) != 1 || !slices.Equal(parsed.NeverAutomatic, []string{".github/**"}) {
			t.Errorf("template with model %q: %+v", model, parsed)
		}
		lane := parsed.Lanes[0]
		if lane.Name != "safe" || !slices.Equal(lane.Authors, []string{"dependabot[bot]"}) || lane.MaxScore == nil || *lane.MaxScore != 2 ||
			!lane.NoMajorBumps || !lane.RequireChecks || lane.MaxChangedLines == nil || lane.Action != rules.ActionApproveAndAutomerge {
			t.Errorf("safe lane %+v", lane)
		}
		if !strings.Contains(template.Content, ".github/workflows/sheepdog.yml") || !strings.Contains(template.Content, "OPENROUTER_API_KEY") {
			t.Errorf("the template does not explain the CI workflow:\n%s", template.Content)
		}
	}
	if !strings.Contains(rulesTemplate("", false), "# classification:\n#   model: ") {
		t.Errorf("the template without a model has no placeholder:\n%s", rulesTemplate("", false))
	}
	for _, model := range []string{`odd/"model" #1 \x`, "a: b"} {
		if parsed, err := rules.Parse([]byte(rulesTemplate(model, false))); err != nil || parsed.Classification.Model != model {
			t.Errorf("template with model %q: %+v, %v", model, parsed.Classification, err)
		}
	}
	h.wantError("GET", "/api/repos/get%20sentry/x/setup/template", alice, nil, http.StatusBadRequest, `invalid repository name "get sentry/x"`)
}

func TestSetupOpensPullRequest(t *testing.T) {
	h, alice := setupHarness(t)
	bob := h.signIn(bobToken)
	h.settings(bob, map[string]string{"repo": bobRepo})
	h.waitRequests(bob, func(r Requests) bool { return r.FetchedAt != nil })
	content := rulesTemplate("alice/model", false)
	events := h.events(alice)
	events.nextRequests()

	var pr struct {
		Number int    `json:"number"`
		URL    string `json:"url"`
	}
	status, data := h.do("POST", "/api/repos/GetSentry/No-Rules/setup", alice, map[string]string{"content": content})
	if status != http.StatusCreated || strings.TrimSpace(string(data)) != `{"number":900,"url":"https://github.com/getsentry/no-rules/pull/900"}` {
		t.Fatalf("setup = %d %s", status, data)
	}
	json.Unmarshal(data, &pr)
	writes := h.github.recorded()
	if len(writes) != 3 {
		t.Fatalf("writes %v", writes)
	}
	if writes[0] != `alice POST /repos/getsentry/no-rules/git/refs {"ref":"refs/heads/sheepdog/setup","sha":"sha-main"}` {
		t.Errorf("create ref %s", writes[0])
	}
	var file struct{ Message, Content, Branch string }
	json.Unmarshal([]byte(strings.TrimPrefix(writes[1], "alice PUT /repos/getsentry/no-rules/contents/.github/sheepdog.yml ")), &file)
	if decoded, _ := base64.StdEncoding.DecodeString(file.Content); !strings.HasPrefix(writes[1], "alice PUT /repos/getsentry/no-rules/contents/.github/sheepdog.yml ") ||
		string(decoded) != content || file.Branch != "sheepdog/setup" || file.Message != "chore: add Sheepdog rules" {
		t.Errorf("create file %s", writes[1])
	}
	var pull struct{ Title, Body, Head, Base string }
	json.Unmarshal([]byte(strings.TrimPrefix(writes[2], "alice POST /repos/getsentry/no-rules/pulls ")), &pull)
	if pull.Title != "Add Sheepdog rules" || pull.Head != "sheepdog/setup" || pull.Base != "main" || !strings.Contains(pull.Body, ".github/sheepdog.yml") {
		t.Errorf("create PR %s", writes[2])
	}

	if r := events.nextRequests(); r.Setup == nil || r.Setup.PullRequest == nil || r.Setup.PullRequest.Number != 900 || r.Setup.PullRequest.URL != pr.URL {
		t.Errorf("requests event after the setup %+v", r.Setup)
	}
	h.wantError("POST", "/api/repos/getsentry/no-rules/setup", alice, map[string]string{"content": content}, http.StatusConflict,
		"The branch sheepdog/setup already exists. Its pull request is https://github.com/getsentry/no-rules/pull/900")
	h.github.mu.Lock()
	delete(h.github.setupPulls, noRulesRepo)
	h.github.mu.Unlock()
	h.wantError("POST", "/api/repos/getsentry/no-rules/setup", alice, map[string]string{"content": content}, http.StatusConflict,
		"The branch sheepdog/setup already exists and has no open pull request to main. Delete the branch on GitHub and try again")
	if n := len(h.github.recorded()); n != 3 {
		t.Errorf("%d writes after the refused setups, want 3", n)
	}
	if got := h.waitRequests(bob, func(r Requests) bool { return r.FetchedAt != nil }); got.Setup != nil || !slices.Equal(numbers(got), []int{202}) {
		t.Errorf("requests of bob %+v", got)
	}
}

func TestSetupRefuses(t *testing.T) {
	h, alice := setupHarness(t)
	const path = "/api/repos/getsentry/no-rules/setup"
	h.wantError("POST", path, alice, map[string]string{"content": "version: 2\n"}, http.StatusBadRequest, ".github/sheepdog.yml is not valid: version must be 1, not 2")
	h.wantError("POST", path, alice, map[string]string{}, http.StatusBadRequest, ".github/sheepdog.yml is not valid: the file is empty")
	h.wantError("POST", path, alice, map[string]string{"content": "version: 1\nlanes:\n  manual:\n"}, http.StatusBadRequest,
		`.github/sheepdog.yml is not valid: line 3: the lane name "manual" is reserved for pull requests without a lane`)
	h.wantError("POST", "/api/repos/getsentry/alice-repo/setup", alice, map[string]string{"content": "version: 1\n"}, http.StatusConflict,
		"getsentry/alice-repo already has .github/sheepdog.yml on main")
	if writes := h.github.recorded(); len(writes) != 0 {
		t.Errorf("a refused setup wrote to GitHub: %v", writes)
	}
}

func TestSetupReportsGitHubError(t *testing.T) {
	h, alice := setupHarness(t)
	h.github.mu.Lock()
	h.github.failCreateRef = "Repository rule violations found\n\nCannot create ref due to creations being restricted.\n\n"
	h.github.mu.Unlock()
	h.wantError("POST", "/api/repos/getsentry/no-rules/setup", alice, map[string]string{"content": "version: 1\n"}, http.StatusBadGateway,
		"GitHub: Repository rule violations found\n\nCannot create ref due to creations being restricted.\n\n (HTTP 422)")
	if writes := h.github.recorded(); len(writes) != 1 || !strings.Contains(writes[0], "/git/refs") {
		t.Errorf("writes after a failed create ref %v", writes)
	}
}
