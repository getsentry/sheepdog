package api

import (
	"net/http"
	"strings"
	"testing"
)

func TestPullDiff(t *testing.T) {
	h := newHarness(t, nil)
	alice, bob := h.signIn(aliceToken), h.signIn(bobToken)
	var got struct {
		Diff      string `json:"diff"`
		Truncated bool   `json:"truncated"`
	}
	h.json("GET", "/api/repos/GetSentry/Sentry-Cocoa/pulls/7/diff", alice, nil, http.StatusOK, &got)
	if got.Diff != "diff of /repos/getsentry/sentry-cocoa/pulls/7 for alice" || got.Truncated {
		t.Errorf("alice diff %+v", got)
	}
	h.json("GET", "/api/repos/getsentry/sentry-cocoa/pulls/7/diff", bob, nil, http.StatusOK, &got)
	if got.Diff != "diff of /repos/getsentry/sentry-cocoa/pulls/7 for bob" {
		t.Errorf("bob diff %+v", got)
	}
	h.wantError("GET", "/api/repos/getsentry/sentry-cocoa/pulls/x/diff", alice, nil, http.StatusBadRequest, "The pull request number is not valid")

	h.github.revoke(aliceToken, true)
	h.wantError("GET", "/api/repos/getsentry/sentry-cocoa/pulls/7/diff", alice, nil, http.StatusUnauthorized, rejectedAgain)
}

func TestCutDiff(t *testing.T) {
	for _, c := range []struct {
		diff  string
		limit int
		want  string
		cut   bool
	}{
		{"a\nb\n", 4, "a\nb\n", false},
		{"a\nbc\nd\n", 6, "a\nbc\n", true},
		{"a\nbcdef", 4, "a\n", true},
		{"abcdef", 3, "abc", true},
		{"ab€", 4, "ab", true},
	} {
		if got, cut := cutDiff(c.diff, c.limit); got != c.want || cut != c.cut {
			t.Errorf("cutDiff(%q, %d) = %q, %v; want %q, %v", c.diff, c.limit, got, cut, c.want, c.cut)
		}
	}
	if got, _ := cutDiff(strings.Repeat("x\n", maxShownDiffBytes), maxShownDiffBytes); len(got) != maxShownDiffBytes {
		t.Errorf("cut length %d, want %d", len(got), maxShownDiffBytes)
	}
}
