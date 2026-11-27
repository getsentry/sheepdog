package api

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"sheepdog/internal/review"
	"sheepdog/internal/store"
	"sheepdog/internal/tasks"
)

const postPath = "/api/repos/getsentry/alice-repo/pulls/101/review/post"

const postDiff = `diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -1,3 +1,3 @@
 package a
-var x = 1
+var x = 2
 // end
`

func TestPostReviewToGitHub(t *testing.T) {
	h, alice := laneHarness(t)
	h.settings(alice, map[string]string{"openRouterKey": aliceKey, "model": "alice/model"})
	h.github.mu.Lock()
	h.github.diff = postDiff
	h.github.mu.Unlock()
	h.wantError("POST", postPath, alice, nil, http.StatusNotFound, "This pull request has no review")

	// A review of an older commit has lines that can differ from the current diff.
	old := &store.Review{Repo: aliceRepo, Number: 101, HeadSHA: "sha-0", Model: "m", Review: review.Review{Risk: "green", Findings: []review.Finding{}}}
	if err := h.server.store.AddReview(context.Background(), h.userID("alice"), old); err != nil {
		t.Fatal(err)
	}
	h.wantError("POST", postPath, alice, nil, http.StatusConflict, "The pull request has new commits since this review. Review it again first")

	events := h.events(alice)
	var list []tasks.Task
	events.expect("tasks", &list)
	h.json("POST", "/api/repos/getsentry/alice-repo/pulls/101/review", alice, nil, http.StatusAccepted, nil)
	events.expect("tasks", &list)
	var done tasks.ReviewEvent
	events.expect("review", &done)
	if done.Review == nil || done.Review.HeadSHA != "sha-1" {
		t.Fatalf("review event %+v", done)
	}
	events.expect("tasks", &list)
	if got := h.repoll(alice, "alice"); got.Rows[0].PostReviews {
		t.Error("a row has postReviews without review.comment")
	}
	h.wantError("POST", postPath, alice, nil, http.StatusConflict, "Posting a review needs review.comment: true in .github/sheepdog.yml on main")
	if writes := h.github.recorded(); len(writes) != 0 {
		t.Fatalf("a refused post wrote %v", writes)
	}

	h.github.setRules(aliceRepo+"@main", laneRules+"review:\n  comment: true\n")
	h.repollUntil(alice, "alice", func(r Requests) bool { return r.Rows[0].PostReviews })
	h.server.mu.Lock()
	h.server.posting["1 "+tasks.ReviewKey(aliceRepo, 101)] = true
	h.server.mu.Unlock()
	h.wantError("POST", postPath, alice, nil, http.StatusConflict, "Sheepdog is posting this review already")
	h.server.mu.Lock()
	clear(h.server.posting)
	h.server.mu.Unlock()

	var posted store.Review
	h.json("POST", postPath, alice, nil, http.StatusOK, &posted)
	const url = "https://github.com/getsentry/alice-repo/pull/101#pullrequestreview-77"
	if posted.PostedURL != url || !posted.CreatedAt.Equal(done.Review.CreatedAt) {
		t.Errorf("posted review %+v", posted)
	}
	writes := h.github.recorded()
	if len(writes) != 1 || !strings.HasPrefix(writes[0], "alice POST /repos/getsentry/alice-repo/pulls/101/reviews ") {
		t.Fatalf("writes %v", writes)
	}
	var sent struct {
		CommitID string `json:"commit_id"`
		Event    string `json:"event"`
		Body     string `json:"body"`
		Comments []struct {
			Path, Side, Body string
			Line             int
		} `json:"comments"`
	}
	if err := json.Unmarshal([]byte(strings.SplitN(writes[0], " ", 4)[3]), &sent); err != nil {
		t.Fatal(err)
	}
	if sent.CommitID != "sha-1" || sent.Event != "COMMENT" || !strings.HasPrefix(sent.Body, review.PostMarker+"\n### Sheepdog review: Needs a careful review\n\nLooks fine.") ||
		len(sent.Comments) != 1 || sent.Comments[0].Path != "a.go" || sent.Comments[0].Line != 3 || sent.Comments[0].Side != "RIGHT" || sent.Comments[0].Body != "**Low**: t\n\nd" {
		t.Errorf("sent review %+v", sent)
	}
	var event tasks.ReviewEvent
	events.expect("review", &event)
	if event.Review == nil || event.Review.PostedURL != url {
		t.Errorf("review event after the post %+v", event)
	}

	h.wantError("POST", postPath, alice, nil, http.StatusConflict, "This review is on GitHub already: "+url)
	var stored store.Review
	h.json("GET", "/api/repos/getsentry/alice-repo/pulls/101/review", alice, nil, http.StatusOK, &stored)
	if stored.PostedURL != url {
		t.Errorf("stored review %+v", stored)
	}
	if n := len(h.github.recorded()); n != 1 {
		t.Errorf("%d writes after the second post, want 1", n)
	}
	bob := h.signIn(bobToken)
	h.wantError("POST", postPath, bob, nil, http.StatusNotFound, "This pull request has no review")
}

func TestPostReviewWithGlobalRules(t *testing.T) {
	h, alice := setupHarness(t)
	h.settings(alice, map[string]string{"openRouterKey": aliceKey})
	h.json("PUT", "/api/settings", alice, map[string]string{"globalRules": laneRules}, http.StatusOK, nil)
	c := &store.Review{Repo: noRulesRepo, Number: 101, HeadSHA: "sha-1", Model: "m", Review: review.Review{Risk: "red", Summary: "s", Findings: []review.Finding{}}}
	if err := h.server.store.AddReview(context.Background(), h.userID("alice"), c); err != nil {
		t.Fatal(err)
	}
	const path = "/api/repos/getsentry/no-rules/pulls/101/review/post"
	h.wantError("POST", path, alice, nil, http.StatusConflict, "Posting a review needs review.comment: true in your global rules")
	h.json("PUT", "/api/settings", alice, map[string]string{"globalRules": laneRules + "review:\n  comment: true\n"}, http.StatusOK, nil)
	got := h.waitRequests(alice, func(r Requests) bool { return len(r.Rows) == 1 && r.Rows[0].PostReviews })
	if !got.GlobalRules {
		t.Errorf("requests %+v", got)
	}
	var posted store.Review
	h.json("POST", path, alice, nil, http.StatusOK, &posted)
	if !strings.HasSuffix(posted.PostedURL, "/getsentry/no-rules/pull/101#pullrequestreview-77") {
		t.Errorf("posted %+v", posted)
	}
	deadline := time.Now().Add(time.Second)
	for !slices.ContainsFunc(h.github.recorded(), func(w string) bool { return strings.Contains(w, "/getsentry/no-rules/pulls/101/reviews") }) {
		if time.Now().After(deadline) {
			t.Fatalf("writes %v", h.github.recorded())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
