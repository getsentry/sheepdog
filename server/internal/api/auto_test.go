package api

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"sheepdog/internal/github"
	"sheepdog/internal/review"
	"sheepdog/internal/store"
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
	h.server.autoWork(ctx, id, aliceRepo, rows(1, 2, 3), nil)
	h.openrouter.waitStarted(t)
	h.openrouter.waitStarted(t)
	if keys := running(); !slices.Equal(keys, []string{aliceRepo + "#1", aliceRepo + "#2"}) {
		t.Fatalf("running %v", keys)
	}
	h.server.autoWork(ctx, id, aliceRepo, rows(1, 2, 3, 4), nil)
	if keys := running(); len(keys) != 2 {
		t.Errorf("running %v, want only two", keys)
	}

	for _, task := range h.server.tasks.List(id) {
		h.json("DELETE", "/api/tasks/"+task.ID, alice, nil, http.StatusNoContent, nil)
		h.openrouter.waitCancelled(t)
	}
	// A stopped classification does not start again for the same head commit.
	h.server.autoWork(ctx, id, aliceRepo, rows(1, 2, 3), nil)
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

func (f *fakeOpenRouter) reviewCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reviews
}

func (h *harness) waitReviews(n int) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for h.openrouter.reviewCount() < n {
		if time.Now().After(deadline) {
			h.t.Fatalf("%d review requests, want %d", h.openrouter.reviewCount(), n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// reviewsStay fails when OpenRouter gets more than n review requests during three polls.
func (h *harness) reviewsStay(session, login string, n int) {
	h.t.Helper()
	for range 3 {
		h.repoll(session, login)
		time.Sleep(20 * time.Millisecond)
	}
	if c := h.openrouter.reviewCount(); c != n {
		h.t.Fatalf("%d review requests, want %d", c, n)
	}
}

func TestAutoReviewRunsOnceForEachHeadCommit(t *testing.T) {
	h, alice := laneHarness(t)
	h.settings(alice, map[string]string{"openRouterKey": aliceKey, "model": "alice/model"})
	h.reviewsStay(alice, "alice", 0)

	if got := h.repoSettings(alice, aliceRepo, map[string]bool{"autoReview": true}); got != (repoSettingsResponse{Repo: aliceRepo, AutoRefresh: true, AutoReview: true}) {
		t.Fatalf("the answer %+v", got)
	}
	// The poll interval is an hour, so only the poll of the switch starts the review.
	h.waitReviews(1)
	var stored []store.Review
	deadline := time.Now().Add(5 * time.Second)
	for len(stored) == 0 && time.Now().Before(deadline) {
		h.json("GET", "/api/repos/"+aliceRepo+"/reviews", alice, nil, http.StatusOK, &stored)
		time.Sleep(10 * time.Millisecond)
	}
	if len(stored) != 1 || stored[0].Number != 101 || stored[0].HeadSHA != "sha-1" || stored[0].Model != "alice/model" {
		t.Fatalf("stored reviews %+v", stored)
	}
	h.reviewsStay(alice, "alice", 1)
	if n := h.openrouter.classificationCount(); n != 0 {
		t.Errorf("auto review started %d classifications", n)
	}

	// The fake detail keeps sha-1, so the stored review stays of sha-1, and the new head commit gets one try.
	h.github.setPull(func(p *fakePull) { p.headSHA = "sha-2" })
	h.repoll(alice, "alice")
	h.waitReviews(2)
	h.reviewsStay(alice, "alice", 2)

	h.github.setPull(func(p *fakePull) { p.headSHA = "sha-3"; p.draft = true })
	h.reviewsStay(alice, "alice", 2)

	var settings settingsResponse
	h.json("GET", "/api/settings", alice, nil, http.StatusOK, &settings)
	if !slices.Equal(settings.AutoReviewRepos, []string{aliceRepo}) || len(settings.AutoClassifyRepos) != 0 {
		t.Errorf("settings lists %v and %v", settings.AutoReviewRepos, settings.AutoClassifyRepos)
	}
}

func TestAutoReviewSkipsReviewedPRs(t *testing.T) {
	h, alice := laneHarness(t)
	h.settings(alice, map[string]string{"openRouterKey": aliceKey, "model": "alice/model"})
	c := &store.Review{Repo: aliceRepo, Number: 101, HeadSHA: "sha-1", Model: "m", Review: review.Review{Risk: "green", Findings: []review.Finding{}}}
	if err := h.server.store.AddReview(context.Background(), h.userID("alice"), c); err != nil {
		t.Fatal(err)
	}
	h.repoSettings(alice, aliceRepo, map[string]bool{"autoReview": true})
	h.reviewsStay(alice, "alice", 0)
}

func TestAutoReviewDoesNotRepeatAFailure(t *testing.T) {
	h := newHarness(t, func(s *Server) { s.pollInterval = time.Hour })
	h.github.setRules(bobRepo+"@main", laneRules)
	bob := h.signIn(bobToken)
	h.repoSettings(bob, bobRepo, map[string]bool{"autoReview": true, "autoClassify": true})
	h.json("PUT", "/api/settings", bob, map[string]string{"repo": bobRepo, "openRouterKey": bobWrongKey, "model": "bob/model"}, http.StatusOK, nil)
	h.waitReviews(1)
	h.waitClassifications(1)
	h.reviewsStay(bob, "bob", 1)
	if n := h.openrouter.classificationCount(); n != 1 {
		t.Errorf("%d classification requests, want 1", n)
	}
}

func TestAutoReviewRunsTwoAtATimeApartFromClassifications(t *testing.T) {
	h := newHarness(t, nil)
	alice := h.signIn(aliceToken)
	h.settings(alice, map[string]string{"openRouterKey": aliceKey, "model": "slow/model"})
	h.repoSettings(alice, aliceRepo, map[string]bool{"autoReview": true, "autoClassify": true})
	id := h.userID("alice")
	rows := []Row{
		{Row: github.Row{Number: 1, HeadSHA: "sha-1"}, autoClassify: true},
		{Row: github.Row{Number: 2, HeadSHA: "sha-1"}},
		{Row: github.Row{Number: 3, HeadSHA: "sha-1"}},
	}
	h.server.autoWork(context.Background(), id, aliceRepo, rows, nil)
	for range 3 {
		h.openrouter.waitStarted(t)
	}
	kinds := map[string][]string{}
	for _, task := range h.server.tasks.List(id) {
		kinds[task.Kind] = append(kinds[task.Kind], task.Key)
	}
	slices.Sort(kinds["review"])
	if !slices.Equal(kinds["classify"], []string{aliceRepo + "#1"}) || !slices.Equal(kinds["review"], []string{aliceRepo + "#1", aliceRepo + "#2"}) {
		t.Errorf("running %v", kinds)
	}
	for _, task := range h.server.tasks.List(id) {
		h.server.tasks.Cancel(id, task.ID)
		h.openrouter.waitCancelled(t)
	}
}
