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

func TestAutoRefreshOffStopsOnlyTimerPolls(t *testing.T) {
	h := newHarness(t, nil)
	alice, bob := h.signIn(aliceToken), h.signIn(bobToken)
	var settings settingsResponse
	h.json("PUT", "/api/settings", alice, map[string]bool{"autoRefresh": false}, http.StatusOK, &settings)
	if settings.AutoRefresh {
		t.Fatal("the answer has auto refresh on")
	}
	h.json("PUT", "/api/settings", bob, map[string]string{"repo": bobRepo}, http.StatusOK, nil)

	h.json("PUT", "/api/settings", alice, map[string]string{"repo": aliceRepo}, http.StatusOK, &settings)
	if settings.AutoRefresh || settings.Repo != aliceRepo {
		t.Fatalf("settings after the repository %+v", settings)
	}
	h.waitPolls(aliceToken, 1)
	polls := h.stablePolls(aliceToken, "after the repository selection")
	if h.github.pollCount(bobToken) < 3 {
		t.Errorf("bob has auto refresh on and got %d polls", h.github.pollCount(bobToken))
	}

	h.json("POST", "/api/review-requests/refresh", alice, nil, http.StatusAccepted, nil)
	h.waitPolls(aliceToken, polls+1)
	polls = h.stablePolls(aliceToken, "after a refresh")

	h.json("GET", "/api/settings", alice, nil, http.StatusOK, &settings)
	if settings.AutoRefresh {
		t.Error("GET has auto refresh on")
	}
	h.json("PUT", "/api/settings", alice, map[string]bool{"autoRefresh": true}, http.StatusOK, &settings)
	if !settings.AutoRefresh {
		t.Error("the answer has auto refresh off")
	}
	h.waitPolls(aliceToken, polls+3)
	if got := h.waitRequests(alice, func(r Requests) bool { return r.FetchedAt != nil }); len(got.Rows) != 1 {
		t.Errorf("requests after auto refresh %+v", got)
	}
}

func TestTurningAutoRefreshOnPollsAtOnce(t *testing.T) {
	h := newHarness(t, func(s *Server) { s.pollInterval = time.Hour })
	alice := h.signIn(aliceToken)
	h.json("PUT", "/api/settings", alice, map[string]any{"repo": aliceRepo, "autoRefresh": false}, http.StatusOK, nil)
	h.waitPolls(aliceToken, 1)
	h.json("PUT", "/api/settings", alice, map[string]bool{"autoRefresh": true}, http.StatusOK, nil)
	h.waitPolls(aliceToken, 2)
	// A setting that does not change polls not again.
	h.json("PUT", "/api/settings", alice, map[string]bool{"autoRefresh": true}, http.StatusOK, nil)
	time.Sleep(100 * time.Millisecond)
	if n := h.github.pollCount(aliceToken); n != 2 {
		t.Errorf("%d polls, want 2", n)
	}
}

func TestRefreshCoalesces(t *testing.T) {
	h := newHarness(t, nil)
	alice := h.signIn(aliceToken)
	h.json("PUT", "/api/settings", alice, map[string]any{"repo": aliceRepo, "autoRefresh": false}, http.StatusOK, nil)
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
