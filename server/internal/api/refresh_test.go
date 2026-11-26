package api

import (
	"net/http"
	"testing"
	"time"
)

// waitPolls waits until the search count of the token reaches n.
func (h *harness) waitPolls(token string, n int) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for h.github.pollCount(token) < n {
		if time.Now().After(deadline) {
			h.t.Fatalf("%d polls, want %d", h.github.pollCount(token), n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// stablePolls fails when the token gets polled during ten poll intervals.
func (h *harness) stablePolls(token, state string) int {
	h.t.Helper()
	before := h.github.pollCount(token)
	time.Sleep(10 * h.server.pollInterval)
	if n := h.github.pollCount(token); n != before {
		h.t.Fatalf("%s: %d timer polls", state, n-before)
	}
	return before
}

// repoSettings changes the settings of the user for the repository and returns the answer.
func (h *harness) repoSettings(session, repo string, body map[string]bool) repoSettingsResponse {
	h.t.Helper()
	var got repoSettingsResponse
	h.json("PUT", "/api/repos/"+repo+"/settings", session, body, http.StatusOK, &got)
	return got
}

func TestAutoRefreshOffStopsOnlyTimerPolls(t *testing.T) {
	h := newHarness(t, nil)
	alice, bob := h.signIn(aliceToken), h.signIn(bobToken)
	if got := h.repoSettings(alice, "GetSentry/Alice-Repo", map[string]bool{"autoRefresh": false}); got != (repoSettingsResponse{Repo: aliceRepo}) {
		t.Fatalf("the answer %+v", got)
	}
	h.json("PUT", "/api/settings", bob, map[string]string{"repo": bobRepo}, http.StatusOK, nil)

	h.json("PUT", "/api/settings", alice, map[string]string{"repo": aliceRepo}, http.StatusOK, nil)
	h.waitPolls(aliceToken, 1)
	polls := h.stablePolls(aliceToken, "after the repository selection")
	if h.github.pollCount(bobToken) < 3 {
		t.Errorf("bob has auto refresh on and got %d polls", h.github.pollCount(bobToken))
	}

	h.json("POST", "/api/review-requests/refresh", alice, nil, http.StatusAccepted, nil)
	h.waitPolls(aliceToken, polls+1)
	polls = h.stablePolls(aliceToken, "after a refresh")

	var got repoSettingsResponse
	h.json("GET", "/api/repos/"+aliceRepo+"/settings", alice, nil, http.StatusOK, &got)
	if got.AutoRefresh {
		t.Error("GET has auto refresh on")
	}
	if got := h.repoSettings(alice, aliceRepo, map[string]bool{"autoRefresh": true}); !got.AutoRefresh {
		t.Error("the answer has auto refresh off")
	}
	h.waitPolls(aliceToken, polls+3)
	if got := h.waitRequests(alice, func(r Requests) bool { return r.FetchedAt != nil }); len(got.Rows) != 1 {
		t.Errorf("requests after auto refresh %+v", got)
	}
}

func TestAutoRefreshBelongsToTheRepository(t *testing.T) {
	h := newHarness(t, nil)
	alice := h.signIn(aliceToken)
	h.repoSettings(alice, aliceRepo, map[string]bool{"autoRefresh": false})
	h.json("PUT", "/api/settings", alice, map[string]string{"repo": "getsentry/sentry-cocoa"}, http.StatusOK, nil)
	h.waitPolls(aliceToken, 3)
	var got repoSettingsResponse
	h.json("GET", "/api/repos/getsentry/sentry-cocoa/settings", alice, nil, http.StatusOK, &got)
	if got != (repoSettingsResponse{Repo: "getsentry/sentry-cocoa", AutoRefresh: true}) {
		t.Errorf("settings of another repository %+v", got)
	}

	polls := h.github.pollCount(aliceToken)
	h.json("PUT", "/api/settings", alice, map[string]string{"repo": aliceRepo}, http.StatusOK, nil)
	h.waitPolls(aliceToken, polls+1)
	h.stablePolls(aliceToken, "back in the repository without auto refresh")
}

func TestTurningAutoRefreshOnPollsAtOnce(t *testing.T) {
	h := newHarness(t, func(s *Server) { s.pollInterval = time.Hour })
	alice := h.signIn(aliceToken)
	h.repoSettings(alice, aliceRepo, map[string]bool{"autoRefresh": false})
	h.json("PUT", "/api/settings", alice, map[string]string{"repo": aliceRepo}, http.StatusOK, nil)
	h.waitPolls(aliceToken, 1)
	h.repoSettings(alice, aliceRepo, map[string]bool{"autoRefresh": true})
	h.waitPolls(aliceToken, 2)
	// A setting that does not change, and a setting of another repository, poll not again.
	h.repoSettings(alice, aliceRepo, map[string]bool{"autoRefresh": true})
	h.repoSettings(alice, bobRepo, map[string]bool{"autoRefresh": false})
	h.repoSettings(alice, bobRepo, map[string]bool{"autoRefresh": true})
	time.Sleep(100 * time.Millisecond)
	if n := h.github.pollCount(aliceToken); n != 2 {
		t.Errorf("%d polls, want 2", n)
	}
	if got := h.repoSettings(alice, noRulesRepo, map[string]bool{"autoClassify": true}); !got.AutoClassify || !got.AutoRefresh {
		t.Errorf("settings of a repository without rules %+v", got)
	}
	h.wantError("PUT", "/api/repos/get%20sentry/x/settings", alice, map[string]bool{"autoRefresh": true}, http.StatusBadRequest, `invalid repository name "get sentry/x"`)
}

func TestRefreshCoalesces(t *testing.T) {
	h := newHarness(t, nil)
	alice := h.signIn(aliceToken)
	h.repoSettings(alice, aliceRepo, map[string]bool{"autoRefresh": false})
	h.json("PUT", "/api/settings", alice, map[string]string{"repo": aliceRepo}, http.StatusOK, nil)
	h.waitPolls(aliceToken, 1)
	polls := h.stablePolls(aliceToken, "before the refresh")

	hold := make(chan struct{})
	h.github.mu.Lock()
	h.github.holdSearch = hold
	h.github.mu.Unlock()
	h.json("POST", "/api/review-requests/refresh", alice, nil, http.StatusAccepted, nil)
	h.waitPolls(aliceToken, polls+1)
	for range 5 {
		h.json("POST", "/api/review-requests/refresh", alice, nil, http.StatusAccepted, nil)
	}
	close(hold)
	h.waitPolls(aliceToken, polls+2)
	h.stablePolls(aliceToken, "after the held poll")
}

func TestRefreshNeedsAPoller(t *testing.T) {
	h := newHarness(t, nil)
	alice := h.signIn(aliceToken)
	h.wantError("POST", "/api/review-requests/refresh", alice, nil, http.StatusConflict, "Select a repository first")
	if n := h.github.pollCount(aliceToken); n != 0 {
		t.Errorf("%d polls without a repository", n)
	}

	h.json("PUT", "/api/settings", alice, map[string]string{"repo": aliceRepo}, http.StatusOK, nil)
	h.waitPolls(aliceToken, 1)
	h.github.revoke(aliceToken, true)
	h.waitRequests(alice, func(r Requests) bool { return r.Error == rejectedAgain })
	h.wantError("POST", "/api/review-requests/refresh", alice, nil, http.StatusConflict, rejectedAgain)
}
