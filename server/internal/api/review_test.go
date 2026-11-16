package api

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"reviewqueue/internal/store"
	"reviewqueue/internal/tasks"
)

const reviewPath = "/api/repos/getsentry/sentry-cocoa/pulls/7/review"

// expect skips comments and requests events and decodes the next event, which must have the given name.
func (s *stream) expect(name string, out any) {
	s.t.Helper()
	for {
		got, data := s.nextEvent()
		if got == "requests" {
			continue
		}
		if got != name {
			s.t.Fatalf("event %q %s, want %q", got, data, name)
		}
		if err := json.Unmarshal([]byte(data), out); err != nil {
			s.t.Fatal(err)
		}
		return
	}
}

func (h *harness) settings(session string, values map[string]string) {
	h.t.Helper()
	h.json("PUT", "/api/settings", session, values, http.StatusOK, nil)
}

func (f *fakeOpenRouter) waitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-f.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the review request did not reach OpenRouter")
	}
}

func (f *fakeOpenRouter) waitCancelled(t *testing.T) {
	t.Helper()
	select {
	case <-f.cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("the OpenRouter request did not stop")
	}
}

func TestReviewIsStoredAndSurvivesRestart(t *testing.T) {
	h := newHarness(t, nil)
	alice, bob := h.signIn(aliceToken), h.signIn(bobToken)
	h.settings(alice, map[string]string{"openRouterKey": aliceKey, "model": "alice/model"})
	events := h.events(alice)
	var list []tasks.Task
	events.expect("tasks", &list)

	var task tasks.Task
	h.json("POST", reviewPath, alice, nil, http.StatusAccepted, &task)
	if task.ID == "" || task.Kind != "review" || task.Key != "getsentry/sentry-cocoa#7" || task.Title != "Review of #7" || task.Subtitle != "getsentry/sentry-cocoa" {
		t.Errorf("task %+v", task)
	}
	if events.expect("tasks", &list); len(list) != 1 || list[0] != task {
		t.Errorf("tasks event %+v", list)
	}
	var done tasks.ReviewEvent
	events.expect("review", &done)
	if done.Repo != "getsentry/sentry-cocoa" || done.Number != 7 || done.Error != "" || done.Review == nil {
		t.Fatalf("review event %+v", done)
	}
	if r := done.Review; r.HeadSHA != "sha-1" || r.Model != "alice/model" || r.Risk != "yellow" || len(r.Findings) != 1 || r.CreatedAt.IsZero() {
		t.Errorf("review %+v", r)
	}
	if events.expect("tasks", &list); len(list) != 0 {
		t.Errorf("tasks after the review %+v", list)
	}

	sent := h.openrouter.lastBody()
	for _, part := range []string{"agents of alice at main", "diff of /repos/getsentry/sentry-cocoa/pulls/7 for alice", `"strict":true`, `"require_parameters":true`, `"model":"alice/model"`} {
		if !strings.Contains(sent, part) {
			t.Errorf("OpenRouter request has no %s", part)
		}
	}

	var stored store.Review
	h.json("GET", reviewPath, alice, nil, http.StatusOK, &stored)
	if !stored.CreatedAt.Equal(done.Review.CreatedAt) || stored.HeadSHA != "sha-1" || stored.Summary != "Looks fine." {
		t.Errorf("stored review %+v", stored)
	}
	h.wantError("GET", reviewPath, bob, nil, http.StatusNotFound, "This pull request has no review")

	h.close()
	restarted := newHarnessAt(t, h.dbPath, defaultAllowed, nil)
	var again store.Review
	restarted.json("GET", reviewPath, alice, nil, http.StatusOK, &again)
	if !again.CreatedAt.Equal(stored.CreatedAt) || again.HeadSHA != stored.HeadSHA || again.Model != stored.Model || !slices.Equal(again.Findings, stored.Findings) {
		t.Errorf("review after restart %+v", again)
	}
	restarted.wantError("GET", reviewPath, bob, nil, http.StatusNotFound, "This pull request has no review")
}

func TestReviewCancelConflictAndShutdown(t *testing.T) {
	h := newHarness(t, nil)
	alice, bob := h.signIn(aliceToken), h.signIn(bobToken)
	h.settings(alice, map[string]string{"openRouterKey": aliceKey, "model": "slow/model"})
	h.settings(bob, map[string]string{"openRouterKey": bobWrongKey, "model": "bob/model"})
	aliceEvents, bobEvents := h.events(alice), h.events(bob)
	var list []tasks.Task
	aliceEvents.expect("tasks", &list)
	bobEvents.expect("tasks", &list)

	h.json("POST", reviewPath, bob, nil, http.StatusAccepted, nil)
	bobEvents.expect("tasks", &list)
	var failed tasks.ReviewEvent
	bobEvents.expect("review", &failed)
	if failed.Error != "Invalid API key" || failed.Review != nil || failed.Number != 7 {
		t.Errorf("bob review event %+v", failed)
	}
	if bobEvents.expect("tasks", &list); len(list) != 0 {
		t.Errorf("bob tasks after the failure %+v", list)
	}

	var task tasks.Task
	h.json("POST", reviewPath, alice, nil, http.StatusAccepted, &task)
	// Events of bob would come first if they reached the stream of alice.
	if aliceEvents.expect("tasks", &list); len(list) != 1 || list[0].ID != task.ID {
		t.Errorf("alice tasks event %+v", list)
	}
	h.openrouter.waitStarted(t)
	h.wantError("POST", reviewPath, alice, nil, http.StatusConflict, "A review of this pull request is already running")

	h.json("GET", "/api/tasks", alice, nil, http.StatusOK, &list)
	if len(list) != 1 || list[0] != task {
		t.Errorf("alice tasks %+v", list)
	}
	h.json("GET", "/api/tasks", bob, nil, http.StatusOK, &list)
	if len(list) != 0 {
		t.Errorf("bob sees tasks %+v", list)
	}
	h.wantError("DELETE", "/api/tasks/"+task.ID, bob, nil, http.StatusNotFound, "No running task has this ID")

	h.json("DELETE", "/api/tasks/"+task.ID, alice, nil, http.StatusNoContent, nil)
	h.openrouter.waitCancelled(t)
	h.json("GET", "/api/tasks", alice, nil, http.StatusOK, &list)
	if len(list) != 0 {
		t.Errorf("tasks after cancel %+v", list)
	}
	if aliceEvents.expect("tasks", &list); len(list) != 0 {
		t.Errorf("tasks event after cancel %+v", list)
	}
	h.wantError("GET", reviewPath, alice, nil, http.StatusNotFound, "This pull request has no review")
	h.wantError("DELETE", "/api/tasks/"+task.ID, alice, nil, http.StatusNotFound, "No running task has this ID")

	h.json("POST", reviewPath, alice, nil, http.StatusAccepted, nil)
	h.openrouter.waitStarted(t)
	h.close()
	h.openrouter.waitCancelled(t)
}

func TestReviewNeedsKeyAndModel(t *testing.T) {
	h := newHarness(t, nil)
	alice := h.signIn(aliceToken)
	const message = "Set the OpenRouter API key and the classification model in Settings"
	h.wantError("POST", reviewPath, alice, nil, http.StatusBadRequest, message)
	h.settings(alice, map[string]string{"openRouterKey": aliceKey})
	h.wantError("POST", reviewPath, alice, nil, http.StatusBadRequest, message)
	h.wantError("POST", "/api/repos/getsentry/sentry-cocoa/pulls/0/review", alice, nil, http.StatusBadRequest, "The pull request number is not valid")
	h.wantError("GET", "/api/repos/get%20sentry/x/pulls/1/review", alice, nil, http.StatusBadRequest, `invalid repository name "get sentry/x"`)
}

func TestRepositoryNamesIgnoreCase(t *testing.T) {
	h := newHarness(t, nil)
	alice := h.signIn(aliceToken)
	var settings settingsResponse
	h.json("PUT", "/api/settings", alice, map[string]string{"repo": "GetSentry/Alice-Repo", "openRouterKey": aliceKey, "model": "slow/model"}, http.StatusOK, &settings)
	if settings.Repo != aliceRepo {
		t.Errorf("stored repository %q, want %q", settings.Repo, aliceRepo)
	}
	if got := h.waitRequests(alice, func(r Requests) bool { return r.FetchedAt != nil }); got.Repo != aliceRepo || !slices.Equal(numbers(got), []int{101}) {
		t.Errorf("requests %+v", got)
	}

	var task tasks.Task
	h.json("POST", "/api/repos/GetSentry/Sentry-Cocoa/pulls/7/review", alice, nil, http.StatusAccepted, &task)
	if task.Key != "getsentry/sentry-cocoa#7" || task.Subtitle != "getsentry/sentry-cocoa" {
		t.Errorf("task %+v", task)
	}
	h.openrouter.waitStarted(t)
	h.wantError("POST", reviewPath, alice, nil, http.StatusConflict, "A review of this pull request is already running")
	h.wantError("POST", "/api/repos/GETSENTRY/SENTRY-COCOA/pulls/7/review", alice, nil, http.StatusConflict, "A review of this pull request is already running")
	h.json("DELETE", "/api/tasks/"+task.ID, alice, nil, http.StatusNoContent, nil)
	h.openrouter.waitCancelled(t)

	h.settings(alice, map[string]string{"model": "alice/model"})
	h.json("POST", "/api/repos/getSentry/sentry-Cocoa/pulls/7/review", alice, nil, http.StatusAccepted, nil)
	waitFor(t, "the review is stored", func() bool {
		status, _ := h.do("GET", reviewPath, alice, nil)
		return status == http.StatusOK
	})
	var stored store.Review
	h.json("GET", "/api/repos/GetSentry/Sentry-Cocoa/pulls/7/review", alice, nil, http.StatusOK, &stored)
	if stored.Repo != "getsentry/sentry-cocoa" {
		t.Errorf("stored review repository %q", stored.Repo)
	}
}

const classificationPath = "/api/repos/getsentry/sentry-cocoa/pulls/7/classification"

func TestClassificationIsStoredAndSeparateFromReview(t *testing.T) {
	h := newHarness(t, nil)
	alice, bob := h.signIn(aliceToken), h.signIn(bobToken)
	h.wantError("POST", classificationPath, alice, nil, http.StatusBadRequest, "Set the OpenRouter API key and the classification model in Settings")
	h.settings(alice, map[string]string{"openRouterKey": aliceKey, "model": "alice/model"})
	h.wantError("GET", classificationPath, alice, nil, http.StatusNotFound, "This pull request has no classification")
	events := h.events(alice)
	var list []tasks.Task
	events.expect("tasks", &list)

	var task tasks.Task
	h.json("POST", classificationPath, alice, nil, http.StatusAccepted, &task)
	if task.Kind != "classify" || task.Key != "getsentry/sentry-cocoa#7" || task.Title != "Classification of #7" {
		t.Errorf("task %+v", task)
	}
	events.expect("tasks", &list)
	var done tasks.ClassificationEvent
	events.expect("classification", &done)
	if done.Error != "" || done.Classification == nil || done.Classification.Score != 4 || done.Classification.Reason != "Medium risk." || done.Classification.HeadSHA != "sha-1" {
		t.Fatalf("classification event %+v", done)
	}
	if sent := h.openrouter.lastBody(); !strings.Contains(sent, "agents of alice at main") || !strings.Contains(sent, `"name":"classification"`) {
		t.Errorf("OpenRouter request %s", sent)
	}

	var stored store.Classification
	h.json("GET", classificationPath, alice, nil, http.StatusOK, &stored)
	if stored.Score != 4 || !stored.CreatedAt.Equal(done.Classification.CreatedAt) {
		t.Errorf("stored classification %+v", stored)
	}
	h.wantError("GET", classificationPath, bob, nil, http.StatusNotFound, "This pull request has no classification")
	var all []store.Classification
	h.json("GET", "/api/repos/GetSentry/Sentry-Cocoa/classifications", alice, nil, http.StatusOK, &all)
	if len(all) != 1 || all[0].Number != 7 || all[0].Score != 4 {
		t.Errorf("classifications of alice %+v", all)
	}
	if status, body := h.do("GET", "/api/repos/getsentry/sentry-cocoa/classifications", bob, nil); status != http.StatusOK || strings.TrimSpace(string(body)) != "[]" {
		t.Errorf("classifications of bob = %d %s", status, body)
	}
	h.wantError("GET", reviewPath, alice, nil, http.StatusNotFound, "This pull request has no review")
}

func TestClassificationAndReviewRunTogether(t *testing.T) {
	h := newHarness(t, nil)
	alice := h.signIn(aliceToken)
	h.settings(alice, map[string]string{"openRouterKey": aliceKey, "model": "slow/model"})
	h.json("POST", reviewPath, alice, nil, http.StatusAccepted, nil)
	h.openrouter.waitStarted(t)
	h.json("POST", classificationPath, alice, nil, http.StatusAccepted, nil)
	h.openrouter.waitStarted(t)
	h.wantError("POST", classificationPath, alice, nil, http.StatusConflict, "A classification of this pull request is already running")
	h.wantError("POST", reviewPath, alice, nil, http.StatusConflict, "A review of this pull request is already running")
	var list []tasks.Task
	h.json("GET", "/api/tasks", alice, nil, http.StatusOK, &list)
	for _, task := range list {
		h.json("DELETE", "/api/tasks/"+task.ID, alice, nil, http.StatusNoContent, nil)
		h.openrouter.waitCancelled(t)
	}
	if len(list) != 2 {
		t.Errorf("tasks %+v, want a review and a classification", list)
	}
}
