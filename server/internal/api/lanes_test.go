package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"sheepdog/internal/review"
	"sheepdog/internal/store"
)

type fakePull struct {
	author, title, headSHA, base, checkState string
	additions, deletions                     int
	files                                    []string
	draft                                    bool
	// mergeStates are the merge states of the PR reads of approve and merge in order. The last one repeats.
	mergeStates         []string
	autoMerge, noSquash bool
	// ciSuites are the GitHub Actions check suites of the head commit.
	ciSuites []any
}

func defaultPull() fakePull {
	return fakePull{
		author: "dependabot", title: "Bump expo from 57.0.1 to 57.0.2", headSHA: "sha-1", base: "main", checkState: "SUCCESS",
		additions: 1, deletions: 2, files: []string{"package.json", "package-lock.json"}, mergeStates: []string{"CLEAN"},
	}
}

const laneRules = `version: 1
lanes:
  safe:
    authors: ["dependabot[bot]"]
    paths: ["package.json", "package-lock.json"]
    max_score: 2
    no_major_bumps: true
    require_checks: true
    action: approve-and-automerge
    add_labels: [ready-to-merge]
  quick_look:
    max_changed_lines: 300
never_automatic:
  paths: [".github/**", "src/sentry.ts"]
`

func (f *fakeGitHub) setPull(change func(*fakePull)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(&f.pull)
}

func (f *fakeGitHub) setRules(repoAtRef, content string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rules[repoAtRef] = content
}

func (f *fakeGitHub) record(login, write string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, login+" "+write)
}

func (f *fakeGitHub) recorded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.writes)
}

func (f *fakeGitHub) pullNode(id identity, mergeState string) string {
	f.mu.Lock()
	p := f.pull
	f.mu.Unlock()
	files := []map[string]string{}
	for _, path := range p.files {
		files = append(files, map[string]string{"path": path})
	}
	var rollup any
	if p.checkState != "" {
		rollup = map[string]string{"state": p.checkState}
	}
	node, _ := json.Marshal(map[string]any{
		"id": fmt.Sprintf("PR_%d", id.number), "number": id.number, "title": p.title, "url": "u", "isDraft": p.draft,
		"updatedAt": "2026-01-01T00:00:00Z", "additions": p.additions, "deletions": p.deletions,
		"headRefOid": p.headSHA, "baseRefName": p.base, "mergeStateStatus": mergeState,
		"author":         map[string]string{"__typename": authorType(p.author), "login": p.author},
		"files":          map[string]any{"totalCount": len(p.files), "nodes": files},
		"commits":        map[string]any{"nodes": []any{map[string]any{"commit": map[string]any{"statusCheckRollup": rollup}}}},
		"reviewRequests": map[string]any{"nodes": []any{map[string]any{"requestedReviewer": map[string]string{"__typename": "User", "login": id.login}}}},
	})
	return string(node)
}

func (f *fakeGitHub) serveRules(w http.ResponseWriter, r *http.Request, login string) {
	repo := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/repos/"), "/contents/.github/sheepdog.yml")
	ref := r.URL.Query().Get("ref")
	f.mu.Lock()
	if ref == "" {
		ref = "main"
		f.defaultReads[login+" "+repo]++
	}
	content, ok := f.rules[repo+"@"+ref]
	f.mu.Unlock()
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"message":"Not Found"}`)
		return
	}
	io.WriteString(w, content)
}

func (f *fakeGitHub) serveMergePR(w http.ResponseWriter, id identity, variables map[string]any) {
	if variables["number"] != float64(id.number) {
		io.WriteString(w, `{"data":{"repository":{"pullRequest":null}}}`)
		return
	}
	f.mu.Lock()
	p := f.pull
	state := p.mergeStates[min(f.mergeReads, len(p.mergeStates)-1)]
	f.mergeReads++
	f.mu.Unlock()
	fmt.Fprintf(w, `{"data":{"repository":{"autoMergeAllowed":%t,"squashMergeAllowed":%t,"mergeCommitAllowed":true,"rebaseMergeAllowed":true,"pullRequest":%s}}}`,
		p.autoMerge, !p.noSquash, f.pullNode(id, state))
}

func (f *fakeGitHub) serveCI(w http.ResponseWriter, id identity, query string) {
	f.mu.Lock()
	f.ciQueries++
	p := f.pull
	f.mu.Unlock()
	repository := map[string]any{}
	if alias := fmt.Sprintf("pr%d", id.number); strings.Contains(query, alias+": pullRequest(number: "+strconv.Itoa(id.number)+")") {
		repository[alias] = map[string]any{
			"headRefOid": p.headSHA,
			"commits":    map[string]any{"nodes": []any{map[string]any{"commit": map[string]any{"oid": p.headSHA, "checkSuites": map[string]any{"nodes": p.ciSuites}}}}},
		}
	}
	json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"repository": repository}})
}

func ciSuite(event, path string, score int, headSHA string) map[string]any {
	message := fmt.Sprintf(`{"score":%d,"reason":"Lock file only.","model":"repo/model","headSha":%q,"diffTruncated":false}`, score, headSHA)
	return map[string]any{
		"workflowRun": map[string]any{"event": event, "file": map[string]string{"path": path}},
		"checkRuns": map[string]any{"nodes": []any{map[string]any{
			"name":        "Sheepdog classification",
			"annotations": map[string]any{"nodes": []any{map[string]string{"title": "sheepdog-classification", "message": message}}},
		}}},
	}
}

func (f *fakeGitHub) serveWrite(w http.ResponseWriter, r *http.Request, login string) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	failApprove := f.failApprove
	f.mu.Unlock()
	if strings.HasSuffix(r.URL.Path, "/reviews") && failApprove != "" {
		w.WriteHeader(http.StatusUnprocessableEntity)
		fmt.Fprintf(w, `{"message":"Unprocessable Entity","errors":[%q],"status":"422"}`, failApprove)
		return
	}
	f.record(login, r.Method+" "+r.URL.Path+" "+string(body))
	if !f.serveSetupWrite(w, r, body) {
		io.WriteString(w, `{}`)
	}
}

// laneHarness signs in alice with her repository, the lane rules on main, and a poll interval that only pollNow can beat.
func laneHarness(t *testing.T) (*harness, string) {
	t.Helper()
	h := newHarness(t, func(s *Server) {
		s.pollInterval = time.Hour
		s.mergeRetry = 10 * time.Millisecond
	})
	h.github.setRules(aliceRepo+"@main", laneRules)
	alice := h.signIn(aliceToken)
	h.settings(alice, map[string]string{"repo": aliceRepo})
	h.waitRequests(alice, func(r Requests) bool { return r.FetchedAt != nil })
	return h, alice
}

func (h *harness) classify(login, repo string, number int, sha string, score int) {
	h.t.Helper()
	c := &store.Classification{Repo: repo, Number: number, HeadSHA: sha, Model: "m", Classification: review.Classification{Score: score, Reason: "r"}}
	if err := h.server.store.AddClassification(context.Background(), h.userID(login), c); err != nil {
		h.t.Fatal(err)
	}
}

// repoll starts a poll at once and returns its result.
func (h *harness) repoll(session, login string) Requests {
	h.t.Helper()
	before := h.waitRequests(session, func(r Requests) bool { return r.FetchedAt != nil }).FetchedAt
	h.server.pollNow(h.userID(login))
	return h.waitRequests(session, func(r Requests) bool { return r.FetchedAt.After(*before) })
}

const approvePath = "/api/repos/getsentry/alice-repo/pulls/101/approve-and-merge"

func TestRowsHaveLanes(t *testing.T) {
	h, alice := laneHarness(t)
	status, data := h.do("GET", "/api/review-requests", alice, nil)
	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	var raw struct {
		Lanes []string                     `json:"lanes"`
		Rows  []map[string]json.RawMessage `json:"rows"`
	}
	if err := json.Unmarshal(data, &raw); err != nil || len(raw.Rows) != 1 {
		t.Fatalf("%s: %v", data, err)
	}
	if !slices.Equal(raw.Lanes, []string{"safe", "quick_look"}) {
		t.Errorf("lanes %v", raw.Lanes)
	}
	row := raw.Rows[0]
	if string(row["headSha"]) != `"sha-1"` ||
		string(row["lane"]) != `{"name":"quick_look","action":"suggest","notes":["Needs a classification of the current commit"]}` {
		t.Errorf("row %s", data)
	}
	for _, hidden := range []string{"BaseRef", "Files", "TotalFiles", "CheckState", "MergeState", "baseRef", "files"} {
		if _, ok := row[hidden]; ok {
			t.Errorf("the row has the internal field %s", hidden)
		}
	}
	if strings.Contains(string(data), "rulesError") {
		t.Errorf("valid rules give a rules error: %s", data)
	}

	h.classify("alice", aliceRepo, 101, "sha-0", 1)
	if got := h.repoll(alice, "alice"); got.Rows[0].Lane.Name != "quick_look" {
		t.Errorf("a classification of an older commit counts: %+v", got.Rows[0].Lane)
	}
	h.classify("alice", aliceRepo, 101, "sha-1", 1)
	if got := h.repoll(alice, "alice"); got.Rows[0].Lane.Name != "safe" || got.Rows[0].Lane.Action != "approve-and-automerge" ||
		!slices.Equal(got.Rows[0].Lane.Notes, []string{"Author dependabot", "Changes only lane paths", "Score 1", "No major update", "Checks are green"}) {
		t.Errorf("lane after the classification %+v", got.Rows[0].Lane)
	}

	h.github.setPull(func(p *fakePull) { p.files = append(p.files, "src/sentry.ts") })
	if got := h.repoll(alice, "alice"); got.Rows[0].Lane.Name != "manual" || !slices.Equal(got.Rows[0].Lane.Notes, []string{"Changes src/sentry.ts"}) {
		t.Errorf("a never_automatic path %+v", got.Rows[0].Lane)
	}
}

func TestRulesComeFromBaseBranch(t *testing.T) {
	h, alice := laneHarness(t)
	h.classify("alice", aliceRepo, 101, "sha-1", 1)
	h.github.setRules(aliceRepo+"@release", "version: 1\nlanes:\n  release_lane:\n    action: approve-and-automerge\n")
	h.github.setPull(func(p *fakePull) { p.base = "release" })
	got := h.repoll(alice, "alice")
	if !slices.Equal(got.Lanes, []string{"release_lane"}) || got.Rows[0].Lane.Name != "release_lane" {
		t.Errorf("rules of the base branch release: %+v", got)
	}

	// The PR adds a lane for itself, but the rules of main put it in manual.
	h.github.setPull(func(p *fakePull) { p.base = "main"; p.files = []string{".github/sheepdog.yml"} })
	got = h.repoll(alice, "alice")
	if got.Rows[0].Lane.Name != "manual" || !slices.Equal(got.Rows[0].Lane.Notes, []string{"Changes .github/sheepdog.yml"}) {
		t.Errorf("a PR that changes the rules %+v", got.Rows[0].Lane)
	}
}

func TestRowsWithoutRulesHaveNoLane(t *testing.T) {
	h, alice := laneHarness(t)
	h.github.setRules(aliceRepo+"@main", "version: 1\nlanes:\n  safe:\n    authors: [dependabot[bot]]\n")
	got := h.repoll(alice, "alice")
	if got.Rows[0].Lane != nil || len(got.Lanes) != 0 || !strings.HasPrefix(got.RulesError, ".github/sheepdog.yml on main is not valid: ") {
		t.Errorf("invalid rules %+v", got)
	}

	h.github.setRules(aliceRepo+"@main", laneRules)
	h.github.setPull(func(p *fakePull) { p.base = "release" })
	h.repoll(alice, "alice")
	_, data := h.do("GET", "/api/review-requests", alice, nil)
	if strings.Contains(string(data), `"lane"`) || strings.Contains(string(data), "rulesError") || !strings.Contains(string(data), `"lanes":[]`) {
		t.Errorf("no rules file on the base branch: %s", data)
	}

	bob := h.signIn(bobToken)
	if status, data := h.do("GET", "/api/review-requests", bob, nil); status != http.StatusOK || !strings.Contains(string(data), `"lanes":[]`) || !strings.Contains(string(data), `"rows":[]`) {
		t.Errorf("a user without a repository: %d %s", status, data)
	}
}

func TestClassificationStartsPoll(t *testing.T) {
	h, alice := laneHarness(t)
	h.settings(alice, map[string]string{"openRouterKey": aliceKey, "model": "alice/model"})
	h.github.setRules(aliceRepo+"@main", strings.Replace(laneRules, "max_score: 2", "max_score: 4", 1))
	events := h.events(alice)
	if r := events.nextRequests(); r.Rows[0].Lane.Name != "quick_look" {
		t.Fatalf("lane before the classification %+v", r.Rows[0].Lane)
	}
	polled := h.github.pollCount(aliceToken)

	h.json("POST", "/api/repos/getsentry/alice-repo/pulls/101/classification", alice, nil, http.StatusAccepted, nil)
	name, data := events.nextEvent()
	for ; name == "tasks" || name == "classification"; name, data = events.nextEvent() {
	}
	var r Requests
	if err := json.Unmarshal([]byte(data), &r); name != "requests" || err != nil {
		t.Fatalf("event %s %s: %v", name, data, err)
	}
	if r.Rows[0].Lane == nil || r.Rows[0].Lane.Name != "safe" || r.FetchedAt == nil {
		t.Errorf("requests event after the classification %+v", r)
	}
	if n := h.github.pollCount(aliceToken); n != polled+1 {
		t.Errorf("%d polls after the classification, want 1", n-polled)
	}
}

func TestPollNowKeepsList(t *testing.T) {
	h, alice := laneHarness(t)
	events := h.events(alice)
	events.nextRequests()
	for range 5 {
		h.server.pollNow(h.userID("alice"))
		if r := events.nextRequests(); len(r.Rows) != 1 {
			t.Fatalf("pollNow emptied the list: %+v", r)
		}
	}
}

func TestApproveAndMergeRefuses(t *testing.T) {
	h, alice := laneHarness(t)
	body := map[string]string{"headSha": "sha-1"}
	h.wantError("POST", approvePath, alice, map[string]string{}, http.StatusBadRequest, "headSha is required")
	h.wantError("POST", approvePath, alice, body, http.StatusConflict, "The lane quick_look does not allow approve and merge: Needs a classification of the current commit")

	h.classify("alice", aliceRepo, 101, "sha-1", 3)
	h.wantError("POST", approvePath, alice, body, http.StatusConflict, "The lane quick_look does not allow approve and merge: Score 3, more than 2")
	h.classify("alice", aliceRepo, 101, "sha-1", 1)
	h.wantError("POST", approvePath, alice, map[string]string{"headSha": "sha-0"}, http.StatusConflict, "The pull request has new commits. Check them before you approve")

	h.github.setPull(func(p *fakePull) { p.checkState = "PENDING" })
	h.wantError("POST", approvePath, alice, body, http.StatusConflict, "The lane quick_look does not allow approve and merge: Checks are not green")
	h.github.setPull(func(p *fakePull) { p.checkState = "SUCCESS"; p.title = "Bump expo from 57.0.1 to 58.0.0" })
	h.wantError("POST", approvePath, alice, body, http.StatusConflict, "The lane quick_look does not allow approve and merge: Major update from 57 to 58")
	h.github.setPull(func(p *fakePull) { p.title = defaultPull().title; p.files = []string{"src/sentry.ts"} })
	h.wantError("POST", approvePath, alice, body, http.StatusConflict, "The lane manual does not allow approve and merge: Changes src/sentry.ts")
	h.github.setPull(func(p *fakePull) { p.files = defaultPull().files })

	h.github.setRules(aliceRepo+"@main", "version: 2\n")
	h.wantError("POST", approvePath, alice, body, http.StatusConflict, ".github/sheepdog.yml on main is not valid: version must be 1, not 2")
	h.github.mu.Lock()
	delete(h.github.rules, aliceRepo+"@main")
	h.github.mu.Unlock()
	h.wantError("POST", approvePath, alice, body, http.StatusConflict, "The repository has no .github/sheepdog.yml on main")

	h.wantError("POST", "/api/repos/getsentry/alice-repo/pulls/102/approve-and-merge", alice, body, http.StatusNotFound, "This pull request is not in your review requests")
	if writes := h.github.recorded(); len(writes) != 0 {
		t.Errorf("a refused approve and merge wrote to GitHub: %v", writes)
	}
}

func TestApproveAndMergeMerges(t *testing.T) {
	h, alice := laneHarness(t)
	h.classify("alice", aliceRepo, 101, "sha-1", 1)
	h.github.setPull(func(p *fakePull) { p.mergeStates = []string{"BLOCKED", "UNKNOWN", "UNKNOWN", "CLEAN"} })
	polled := h.github.pollCount(aliceToken)

	var result map[string]string
	h.json("POST", approvePath, alice, map[string]string{"headSha": "sha-1"}, http.StatusOK, &result)
	if result["result"] != "merged" {
		t.Errorf("result %v", result)
	}
	want := []string{
		`alice POST /repos/getsentry/alice-repo/issues/101/labels {"labels":["ready-to-merge"]}`,
		`alice POST /repos/getsentry/alice-repo/pulls/101/reviews {"commit_id":"sha-1","event":"APPROVE"}`,
		`alice PUT /repos/getsentry/alice-repo/pulls/101/merge {"merge_method":"squash","sha":"sha-1"}`,
	}
	if writes := h.github.recorded(); !slices.Equal(writes, want) {
		t.Errorf("writes\n%v\nwant\n%v", strings.Join(writes, "\n"), strings.Join(want, "\n"))
	}
	waitFor(t, "a poll after the merge", func() bool { return h.github.pollCount(aliceToken) > polled })
}

func TestApproveAndMergeTurnsOnAutoMerge(t *testing.T) {
	h, alice := laneHarness(t)
	h.server.mergeWait = 50 * time.Millisecond
	h.classify("alice", aliceRepo, 101, "sha-1", 1)
	h.github.setPull(func(p *fakePull) { p.mergeStates = []string{"BLOCKED", "UNKNOWN"}; p.noSquash = true })
	body := map[string]string{"headSha": "sha-1"}

	h.wantError("POST", approvePath, alice, body, http.StatusConflict, "Approved, but not merged: GitHub cannot merge the pull request yet, and the repository does not allow auto-merge. Turn on auto-merge in the repository settings, or merge on GitHub")

	h.github.setPull(func(p *fakePull) { p.autoMerge = true })
	var result map[string]string
	h.json("POST", approvePath, alice, body, http.StatusOK, &result)
	if result["result"] != "auto-merge" {
		t.Errorf("result %v", result)
	}
	writes := h.github.recorded()
	if last := writes[len(writes)-1]; last != `alice automerge {"id":"PR_101","method":"MERGE","sha":"sha-1"}` {
		t.Errorf("auto-merge request %s", last)
	}
	if slices.ContainsFunc(writes, func(w string) bool { return strings.Contains(w, "/merge ") }) {
		t.Errorf("a PR that is not CLEAN was merged: %v", writes)
	}
}

func TestApproveAndMergeReportsGitHubError(t *testing.T) {
	h, alice := laneHarness(t)
	h.classify("alice", aliceRepo, 101, "sha-1", 1)
	h.github.mu.Lock()
	h.github.failApprove = "Can not approve your own pull request"
	h.github.mu.Unlock()
	h.wantError("POST", approvePath, alice, map[string]string{"headSha": "sha-1"}, http.StatusBadGateway,
		"GitHub: Unprocessable Entity: Can not approve your own pull request (HTTP 422)")
	if writes := h.github.recorded(); len(writes) != 1 || !strings.Contains(writes[0], "/labels") {
		t.Errorf("writes after a failed approval %v", writes)
	}
}

func TestApproveAndMergeIsPerUser(t *testing.T) {
	h, alice := laneHarness(t)
	bob := h.signIn(bobToken)
	h.settings(bob, map[string]string{"repo": bobRepo})
	h.github.setRules(bobRepo+"@main", laneRules)
	h.waitRequests(bob, func(r Requests) bool { return r.FetchedAt != nil })
	h.classify("alice", aliceRepo, 101, "sha-1", 1)
	h.classify("bob", bobRepo, 202, "sha-1", 1)
	body := map[string]string{"headSha": "sha-1"}

	h.wantError("POST", approvePath, bob, body, http.StatusNotFound, "This pull request is not in your review requests")
	h.wantError("POST", "/api/repos/getsentry/bob-repo/pulls/202/approve-and-merge", alice, body, http.StatusNotFound, "This pull request is not in your review requests")
	if writes := h.github.recorded(); len(writes) != 0 {
		t.Errorf("writes for a PR of another user %v", writes)
	}

	h.json("POST", "/api/repos/getsentry/bob-repo/pulls/202/approve-and-merge", bob, body, http.StatusOK, nil)
	for _, write := range h.github.recorded() {
		if !strings.HasPrefix(write, "bob POST /repos/getsentry/bob-repo/") && !strings.HasPrefix(write, "bob PUT /repos/getsentry/bob-repo/") {
			t.Errorf("write %s does not use the token of bob for his PR", write)
		}
	}
}

func authorType(login string) string {
	if login == "dependabot" {
		return "Bot"
	}
	return "User"
}

const ciRules = laneRules + "classification:\n  model: repo/model\n  ci: true\n"

func TestCIScoreSetsLane(t *testing.T) {
	h, alice := laneHarness(t)
	h.github.setRules(aliceRepo+"@main", ciRules)
	if got := h.repoll(alice, "alice"); got.Rows[0].Classification != nil || got.Rows[0].Lane.Name != "quick_look" {
		t.Errorf("row without a CI score %+v", got.Rows[0])
	}

	h.github.setPull(func(p *fakePull) {
		p.ciSuites = []any{ciSuite("pull_request_target", ".github/workflows/sheepdog.yml", 1, "sha-1")}
	})
	h.classify("alice", aliceRepo, 101, "sha-1", 9)
	got := h.repoll(alice, "alice")
	want := CIClassification{Source: "ci", Score: 1, Reason: "Lock file only.", Model: "repo/model", HeadSHA: "sha-1"}
	if c := got.Rows[0].Classification; c == nil || *c != want {
		t.Errorf("CI score %+v, want %+v", c, want)
	}
	if lane := got.Rows[0].Lane; lane.Name != "safe" || !slices.Contains(lane.Notes, "Score 1") {
		t.Errorf("the CI score does not win over the app score: %+v", lane)
	}
	_, data := h.do("GET", "/api/review-requests", alice, nil)
	if !strings.Contains(string(data), `"classification":{"source":"ci","score":1,"reason":"Lock file only.","model":"repo/model","headSha":"sha-1"}`) {
		t.Errorf("row JSON %s", data)
	}

	h.github.setPull(func(p *fakePull) {
		p.ciSuites = []any{ciSuite("pull_request", ".github/workflows/sheepdog.yml", 1, "sha-1")}
	})
	got = h.repoll(alice, "alice")
	if got.Rows[0].Classification != nil || !slices.Contains(got.Rows[0].Lane.Notes, "Score 9, more than 2") {
		t.Errorf("a score from the event pull_request counts: %+v %+v", got.Rows[0].Classification, got.Rows[0].Lane)
	}

	h.github.setPull(func(p *fakePull) {
		p.headSHA = "sha-2"
		p.ciSuites = []any{ciSuite("pull_request_target", ".github/workflows/sheepdog.yml", 1, "sha-1")}
	})
	got = h.repoll(alice, "alice")
	if got.Rows[0].Classification != nil || got.Rows[0].Lane.Name != "quick_look" {
		t.Errorf("a CI score of an older commit counts: %+v %+v", got.Rows[0].Classification, got.Rows[0].Lane)
	}
}

func TestPollWithoutCIReadsNoCheckSuites(t *testing.T) {
	h, alice := laneHarness(t)
	h.github.setRules(aliceRepo+"@main", laneRules+"classification:\n  model: repo/model\n")
	h.github.setPull(func(p *fakePull) {
		p.ciSuites = []any{ciSuite("pull_request_target", ".github/workflows/sheepdog.yml", 1, "sha-1")}
	})
	got := h.repoll(alice, "alice")
	if got.Rows[0].Classification != nil || got.Rows[0].Lane.Name != "quick_look" {
		t.Errorf("a CI score counts without ci: true: %+v", got.Rows[0])
	}
	h.github.mu.Lock()
	defer h.github.mu.Unlock()
	if h.github.ciQueries != 0 {
		t.Errorf("%d queries read check suites without ci: true", h.github.ciQueries)
	}
	for _, query := range h.github.searches {
		if strings.Contains(query, "checkSuites") {
			t.Errorf("the search query reads check suites:\n%s", query)
		}
	}
}

func TestApproveAndMergeUsesCIScore(t *testing.T) {
	h, alice := laneHarness(t)
	h.github.setRules(aliceRepo+"@main", ciRules)
	h.github.setPull(func(p *fakePull) {
		p.ciSuites = []any{ciSuite("pull_request_target", ".github/workflows/other.yml", 1, "sha-1")}
	})
	body := map[string]string{"headSha": "sha-1"}
	h.wantError("POST", approvePath, alice, body, http.StatusConflict, "The lane quick_look does not allow approve and merge: Needs a classification of the current commit")

	h.github.setPull(func(p *fakePull) {
		p.ciSuites = []any{ciSuite("pull_request_target", ".github/workflows/sheepdog.yml", 1, "sha-1")}
	})
	var result map[string]string
	h.json("POST", approvePath, alice, body, http.StatusOK, &result)
	if result["result"] != "merged" {
		t.Errorf("result %v", result)
	}
}
