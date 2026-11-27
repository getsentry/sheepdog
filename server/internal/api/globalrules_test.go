package api

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"sheepdog/internal/rules"
)

const globalRulesText = laneRules + "classification:\n  model: global/model\n"

func TestGlobalRulesInSettings(t *testing.T) {
	h := newHarness(t, nil)
	alice, bob := h.signIn(aliceToken), h.signIn(bobToken)
	var settings settingsResponse
	h.json("GET", "/api/settings", alice, nil, http.StatusOK, &settings)
	if settings.GlobalRules != "" {
		t.Errorf("rules before a change %q", settings.GlobalRules)
	}
	h.wantError("PUT", "/api/settings", alice, map[string]string{"globalRules": "version: 2\n"}, http.StatusBadRequest,
		"The global rules are not valid: version must be 1, not 2")
	h.json("PUT", "/api/settings", alice, map[string]string{"globalRules": globalRulesText}, http.StatusOK, &settings)
	if settings.GlobalRules != globalRulesText {
		t.Errorf("the answer has the rules %q", settings.GlobalRules)
	}
	h.json("GET", "/api/settings", bob, nil, http.StatusOK, &settings)
	if settings.GlobalRules != "" {
		t.Errorf("bob has the rules of alice %q", settings.GlobalRules)
	}
	h.json("PUT", "/api/settings", alice, map[string]string{"globalRules": " \n"}, http.StatusOK, &settings)
	if settings.GlobalRules != "" {
		t.Errorf("rules after an empty text %q", settings.GlobalRules)
	}

	h.settings(alice, map[string]string{"model": "alice/model"})
	for session, model := range map[string]string{alice: "alice/model", bob: ""} {
		var template struct{ Content string }
		h.json("GET", "/api/global-rules/template", session, nil, http.StatusOK, &template)
		parsed, err := rules.Parse([]byte(template.Content))
		if err != nil || parsed.Classification.Model != model || len(parsed.Lanes) != 1 {
			t.Errorf("global template with model %q: %+v, %v\n%s", model, parsed, err, template.Content)
		}
		if !strings.HasPrefix(template.Content, "# Global rules for Sheepdog.") || strings.Contains(template.Content, "workflows") {
			t.Errorf("global template:\n%s", template.Content)
		}
	}
}

func TestRepositoryWithoutRulesUsesGlobalRules(t *testing.T) {
	h, alice := setupHarness(t)
	h.settings(alice, map[string]string{"openRouterKey": aliceKey})
	h.wantError("GET", "/api/repos/getsentry/no-rules/classifications", alice, nil, http.StatusConflict, noRulesText)

	h.json("PUT", "/api/settings", alice, map[string]string{"globalRules": globalRulesText}, http.StatusOK, nil)
	got := h.waitRequests(alice, func(r Requests) bool { return len(r.Rows) == 1 })
	if !got.GlobalRules || got.Setup == nil || got.Setup.DefaultBranch != "main" || !slices.Equal(got.Lanes, []string{"safe", "quick_look"}) ||
		got.Rows[0].Lane == nil || got.Rows[0].Lane.Name != "quick_look" {
		t.Fatalf("requests with global rules %+v", got)
	}
	_, data := h.do("GET", "/api/review-requests", alice, nil)
	if !strings.Contains(string(data), `"globalRules":true`) {
		t.Errorf("no globalRules in %s", data)
	}
	h.json("GET", "/api/repos/getsentry/no-rules/classifications", alice, nil, http.StatusOK, nil)
	h.json("GET", "/api/repos/getsentry/no-rules/pulls/101", alice, nil, http.StatusOK, nil)

	// Auto classify and a click on Classify use the model of the global rules.
	h.repoSettings(alice, noRulesRepo, map[string]bool{"autoClassify": true})
	h.waitClassifications(1)
	h.json("POST", "/api/repos/getsentry/no-rules/pulls/101/classification", alice, nil, http.StatusAccepted, nil)
	h.waitClassifications(2)
	h.openrouter.mu.Lock()
	bodies := slices.Clone(h.openrouter.bodies)
	h.openrouter.mu.Unlock()
	global := 0
	for _, body := range bodies {
		if strings.Contains(body, `"model":"global/model"`) {
			global++
		}
	}
	if len(bodies) != 2 || global != 2 {
		t.Errorf("%d of %d classifications used the global model, want 2 of 2", global, len(bodies))
	}

	h.classify("alice", noRulesRepo, 101, "sha-1", 1)
	h.repollUntil(alice, "alice", func(r Requests) bool { return r.Rows[0].Lane.Name == "safe" })
	var result map[string]string
	h.json("POST", "/api/repos/getsentry/no-rules/pulls/101/approve-and-merge", alice, map[string]string{"headSha": "sha-1"}, http.StatusOK, &result)
	if result["result"] != "merged" || !slices.Contains(h.github.recorded(), `alice POST /repos/getsentry/no-rules/issues/101/labels {"labels":["ready-to-merge"]}`) {
		t.Errorf("approve and merge with global rules: %v, writes %v", result, h.github.recorded())
	}

	// The rules file of the repository wins over the global rules.
	h.github.setRules(noRulesRepo+"@main", "version: 1\nlanes:\n  repo_lane:\n    max_changed_lines: 10\n")
	h.server.mu.Lock()
	clear(h.server.rulesChecks)
	h.server.mu.Unlock()
	h.repollUntil(alice, "alice", func(r Requests) bool {
		return !r.GlobalRules && r.Setup == nil && slices.Equal(r.Lanes, []string{"repo_lane"})
	})

	h.github.removeRules(noRulesRepo + "@main")
	h.server.mu.Lock()
	clear(h.server.rulesChecks)
	h.server.mu.Unlock()
	h.json("PUT", "/api/settings", alice, map[string]string{"globalRules": ""}, http.StatusOK, nil)
	got = h.waitRequests(alice, func(r Requests) bool { return len(r.Rows) == 0 })
	if got.GlobalRules || got.Setup == nil {
		t.Errorf("requests after the delete of the global rules %+v", got)
	}
	h.wantError("GET", "/api/repos/getsentry/no-rules/classifications", alice, nil, http.StatusConflict, noRulesText)
}
