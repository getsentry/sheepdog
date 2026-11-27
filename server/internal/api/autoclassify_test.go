package api

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"sheepdog/internal/github"
)

func (f *fakeOpenRouter) classificationCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.classifications
}

// waitClassifications waits until OpenRouter got n classification requests.
func (h *harness) waitClassifications(n int) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for h.openrouter.classificationCount() < n {
		if time.Now().After(deadline) {
			h.t.Fatalf("%d classification requests, want %d", h.openrouter.classificationCount(), n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// classificationsStay fails when OpenRouter gets more than n classification requests during three polls.
func (h *harness) classificationsStay(session, login string, n int) Requests {
	h.t.Helper()
	var got Requests
	for range 3 {
		got = h.repoll(session, login)
		time.Sleep(20 * time.Millisecond)
	}
	if c := h.openrouter.classificationCount(); c != n {
		h.t.Fatalf("%d classification requests, want %d", c, n)
	}
	return got
}

func TestAutoClassifyRunsOnceForEachHeadCommit(t *testing.T) {
	h, alice := laneHarness(t)
	h.settings(alice, map[string]string{"openRouterKey": aliceKey, "model": "alice/model"})
	h.classificationsStay(alice, "alice", 0)

	if got := h.repoSettings(alice, aliceRepo, map[string]bool{"autoClassify": true}); got != (repoSettingsResponse{Repo: aliceRepo, AutoRefresh: true, AutoClassify: true}) {
		t.Fatalf("the answer %+v", got)
	}
	// The poll interval is an hour, so only the poll of the switch starts the classification.
	h.waitClassifications(1)
	got := h.waitRequests(alice, func(r Requests) bool {
		return slices.Contains(r.Rows[0].Lane.Notes, "Score 4, more than 2")
	})
	if got.Rows[0].Lane.Name != "quick_look" {
		t.Errorf("lane after the classification %+v", got.Rows[0].Lane)
	}
	h.classificationsStay(alice, "alice", 1)

	// The fake detail keeps sha-1, so the new head commit stays without a score after its one try.
	h.github.setPull(func(p *fakePull) { p.headSHA = "sha-2" })
	h.repoll(alice, "alice")
	h.waitClassifications(2)
	h.classificationsStay(alice, "alice", 2)

	// Auto classify of another repository does not classify in this one.
	h.repoSettings(alice, aliceRepo, map[string]bool{"autoClassify": false})
	h.repoSettings(alice, bobRepo, map[string]bool{"autoClassify": true})
	h.github.setPull(func(p *fakePull) { p.headSHA = "sha-3" })
	h.classificationsStay(alice, "alice", 2)
}

func TestAutoClassifySkipsRows(t *testing.T) {
	h, alice := laneHarness(t)
	h.repoSettings(alice, aliceRepo, map[string]bool{"autoClassify": true})
	h.classificationsStay(alice, "alice", 0)
	h.settings(alice, map[string]string{"openRouterKey": aliceKey})
	h.classificationsStay(alice, "alice", 0)
	h.settings(alice, map[string]string{"model": "alice/model"})

	h.github.setPull(func(p *fakePull) { p.draft = true })
	h.classificationsStay(alice, "alice", 0)
	h.github.setPull(func(p *fakePull) { p.draft = false; p.checkState = "PENDING" })
	h.classificationsStay(alice, "alice", 0)
	h.github.setPull(func(p *fakePull) { p.checkState = "SUCCESS" })
	h.github.setRules(aliceRepo+"@main", laneRules+"classification:\n  model: repo/model\n  ci: true\n")
	h.classificationsStay(alice, "alice", 0)

	h.github.setRules(aliceRepo+"@main", laneRules)
	h.repoll(alice, "alice")
	h.waitClassifications(1)
}

func TestAutoClassifyDoesNotRepeatAFailure(t *testing.T) {
	h := newHarness(t, func(s *Server) { s.pollInterval = time.Hour })
	h.github.setRules(bobRepo+"@main", laneRules)
	bob := h.signIn(bobToken)
	h.repoSettings(bob, bobRepo, map[string]bool{"autoClassify": true})
	h.json("PUT", "/api/settings", bob, map[string]string{"repo": bobRepo, "openRouterKey": bobWrongKey, "model": "bob/model"}, http.StatusOK, nil)
	h.waitClassifications(1)
	h.classificationsStay(bob, "bob", 1)
}

func TestAutoClassifyRunsTwoAtATime(t *testing.T) {
	h := newHarness(t, nil)
	alice := h.signIn(aliceToken)
	h.settings(alice, map[string]string{"openRouterKey": aliceKey, "model": "slow/model"})
	h.repoSettings(alice, aliceRepo, map[string]bool{"autoClassify": true})
	id := h.userID("alice")
	rows := func(numbers ...int) []Row {
		var list []Row
		for _, n := range numbers {
			list = append(list, Row{Row: github.Row{Number: n, HeadSHA: "sha-1"}, autoClassify: true})
		}
		return list
	}
	running := func() []string {
		var keys []string
		for _, task := range h.server.tasks.List(id) {
			keys = append(keys, task.Key)
		}
		slices.Sort(keys)
		return keys
	}

	ctx := context.Background()
	h.server.autoClassify(ctx, id, aliceRepo, rows(1, 2, 3), nil)
	h.openrouter.waitStarted(t)
	h.openrouter.waitStarted(t)
	if keys := running(); !slices.Equal(keys, []string{aliceRepo + "#1", aliceRepo + "#2"}) {
		t.Fatalf("running %v", keys)
	}
	h.server.autoClassify(ctx, id, aliceRepo, rows(1, 2, 3, 4), nil)
	if keys := running(); len(keys) != 2 {
		t.Errorf("running %v, want only two", keys)
	}

	for _, task := range h.server.tasks.List(id) {
		h.json("DELETE", "/api/tasks/"+task.ID, alice, nil, http.StatusNoContent, nil)
		h.openrouter.waitCancelled(t)
	}
	// A stopped classification does not start again for the same head commit.
	h.server.autoClassify(ctx, id, aliceRepo, rows(1, 2, 3), nil)
	h.openrouter.waitStarted(t)
	if keys := running(); !slices.Equal(keys, []string{aliceRepo + "#3"}) {
		t.Errorf("running %v, want only #3", keys)
	}
	for _, task := range h.server.tasks.List(id) {
		if task.Kind != "classify" || !h.server.tasks.Cancel(id, task.ID) {
			t.Errorf("task %+v", task)
		}
	}
	h.openrouter.waitCancelled(t)
	if keys := running(); len(keys) != 0 {
		t.Errorf("running %v after the cancel", keys)
	}
}

func TestSettingsListAutoClassifyRepos(t *testing.T) {
	h := newHarness(t, nil)
	alice, bob := h.signIn(aliceToken), h.signIn(bobToken)
	h.repoSettings(alice, "GetSentry/Zeta", map[string]bool{"autoClassify": true})
	h.repoSettings(alice, aliceRepo, map[string]bool{"autoClassify": true})
	h.repoSettings(alice, noRulesRepo, map[string]bool{"autoRefresh": false})
	var settings settingsResponse
	h.json("GET", "/api/settings", alice, nil, http.StatusOK, &settings)
	if !slices.Equal(settings.AutoClassifyRepos, []string{aliceRepo, "getsentry/zeta"}) {
		t.Errorf("alice repositories %v", settings.AutoClassifyRepos)
	}
	h.repoSettings(alice, "getsentry/zeta", map[string]bool{"autoClassify": false})
	h.json("PUT", "/api/repos/getsentry/sentry-cocoa/star", alice, nil, http.StatusOK, &settings)
	if !slices.Equal(settings.AutoClassifyRepos, []string{aliceRepo}) {
		t.Errorf("alice repositories after a change %v", settings.AutoClassifyRepos)
	}
	h.json("GET", "/api/settings", bob, nil, http.StatusOK, &settings)
	if settings.AutoClassifyRepos == nil || len(settings.AutoClassifyRepos) != 0 {
		t.Errorf("bob repositories %#v", settings.AutoClassifyRepos)
	}
}
