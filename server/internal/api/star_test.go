package api

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"testing"
)

func TestStarsBelongToEachUser(t *testing.T) {
	h := newHarness(t, nil)
	alice, bob := h.signIn(aliceToken), h.signIn(bobToken)
	h.settings(alice, map[string]string{"repo": aliceRepo})

	var settings settingsResponse
	h.json("PUT", "/api/repos/GetSentry/Sentry-Cocoa/star", alice, nil, http.StatusOK, &settings)
	h.json("PUT", "/api/repos/getsentry/sentry-cocoa/star", alice, nil, http.StatusOK, &settings)
	// A repository without a rules file gets a star too.
	h.json("PUT", "/api/repos/"+noRulesRepo+"/star", alice, nil, http.StatusOK, &settings)
	if settings.Repo != aliceRepo || !slices.Equal(settings.Starred, []string{noRulesRepo, "getsentry/sentry-cocoa"}) {
		t.Errorf("alice settings after the stars %+v", settings)
	}
	h.json("GET", "/api/settings", alice, nil, http.StatusOK, &settings)
	if !slices.Equal(settings.Starred, []string{noRulesRepo, "getsentry/sentry-cocoa"}) {
		t.Errorf("alice stars %v", settings.Starred)
	}
	h.json("GET", "/api/settings", bob, nil, http.StatusOK, &settings)
	if settings.Starred == nil || len(settings.Starred) != 0 {
		t.Errorf("bob stars %#v, want an empty list", settings.Starred)
	}

	h.json("PUT", "/api/repos/"+bobRepo+"/star", bob, nil, http.StatusOK, nil)
	h.json("DELETE", "/api/repos/getsentry/sentry-cocoa/star", bob, nil, http.StatusOK, &settings)
	if !slices.Equal(settings.Starred, []string{bobRepo}) {
		t.Errorf("bob stars after a removal of a star of alice %v", settings.Starred)
	}
	for range 2 {
		h.json("DELETE", "/api/repos/getsentry/Sentry-Cocoa/star", alice, nil, http.StatusOK, &settings)
	}
	if !slices.Equal(settings.Starred, []string{noRulesRepo}) {
		t.Errorf("alice stars after the removal %v", settings.Starred)
	}

	h.wantError("PUT", "/api/repos/get%20sentry/x/star", alice, nil, http.StatusBadRequest, `invalid repository name "get sentry/x"`)
	if n := h.github.defaultReadCount("alice", noRulesRepo); n != 0 {
		t.Errorf("stars read the rules file %d times", n)
	}
}

func TestStarLimit(t *testing.T) {
	h := newHarness(t, nil)
	alice := h.signIn(aliceToken)
	ctx := context.Background()
	id, err := h.server.store.SessionUserID(ctx, alice)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 200 {
		if err := h.server.store.StarRepo(ctx, id, fmt.Sprintf("acme/repo-%03d", i)); err != nil {
			t.Fatal(err)
		}
	}
	h.wantError("PUT", "/api/repos/acme/one-more/star", alice, nil, http.StatusConflict, "You can star at most 200 repositories. Remove a star first")
	var settings settingsResponse
	h.json("PUT", "/api/repos/acme/repo-007/star", alice, nil, http.StatusOK, &settings)
	if len(settings.Starred) != 200 || slices.Contains(settings.Starred, "acme/one-more") {
		t.Errorf("%d stars after the limit", len(settings.Starred))
	}
	h.json("DELETE", "/api/repos/acme/repo-007/star", alice, nil, http.StatusOK, nil)
	h.json("PUT", "/api/repos/acme/one-more/star", alice, nil, http.StatusOK, &settings)
	if len(settings.Starred) != 200 || !slices.Contains(settings.Starred, "acme/one-more") {
		t.Errorf("%d stars after a removal and a new star", len(settings.Starred))
	}
}
