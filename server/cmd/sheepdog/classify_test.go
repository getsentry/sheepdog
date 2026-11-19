package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sheepdog/internal/github"
)

const (
	testToken = "ghs_testToken0000"
	testKey   = "sk-or-v1-testKey"
	repoRules = "version: 1\nclassification:\n  model: repo/model\n  ci: true\n"
)

// classifyHarness runs fake GitHub and OpenRouter servers. rules is the content of .github/sheepdog.yml on main, or empty for no file.
type classifyHarness struct {
	githubURL, openRouterURL string
	rules                    string
	openRouterStatus         int
	sentModel                string
	// comments is the JSON of the comment list. commentStatus makes every comment request fail.
	comments      string
	commentStatus int
	created       []string
	updated       map[string]string
}

func newClassifyHarness(t *testing.T) *classifyHarness {
	t.Helper()
	h := &classifyHarness{rules: repoRules, comments: "[]", updated: map[string]string{}}
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/graphql":
			io.WriteString(w, `{"data":{"repository":{"pullRequest":{"title":"Bump expo from 57.0.1 to 57.0.2","body":"","headRefName":"dependabot/expo",
				"baseRefName":"main","headRefOid":"abc123","changedFiles":1,"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z",
				"reviewDecision":null,"labels":{"nodes":[]},"reviewRequests":{"nodes":[]},"latestReviews":{"nodes":[]},"commits":{"nodes":[]}}}}}`)
		case r.URL.Path == "/repos/acme/app/pulls/7":
			io.WriteString(w, "diff --git a/package.json b/package.json\n")
		case r.URL.Path == "/repos/acme/app/contents/.github/sheepdog.yml" && r.URL.Query().Get("ref") == "main" && h.rules != "":
			io.WriteString(w, h.rules)
		case strings.HasPrefix(r.URL.Path, "/repos/acme/app/issues/") && h.commentStatus != 0:
			w.WriteHeader(h.commentStatus)
			io.WriteString(w, `{"message":"Resource not accessible by integration"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/app/issues/7/comments":
			io.WriteString(w, h.comments)
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/app/issues/7/comments":
			var body struct{ Body string }
			json.NewDecoder(r.Body).Decode(&body)
			h.created = append(h.created, body.Body)
			w.WriteHeader(http.StatusCreated)
			io.WriteString(w, `{}`)
		case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/repos/acme/app/issues/comments/"):
			var body struct{ Body string }
			json.NewDecoder(r.Body).Decode(&body)
			h.updated[strings.TrimPrefix(r.URL.Path, "/repos/acme/app/issues/comments/")] = body.Body
			io.WriteString(w, `{}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"message":"Not Found"}`)
		}
	}))
	t.Cleanup(gh.Close)
	or := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testKey || h.openRouterStatus != 0 {
			w.WriteHeader(max(h.openRouterStatus, http.StatusUnauthorized))
			return
		}
		var body struct {
			Model string `json:"model"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		h.sentModel = body.Model
		content, _ := json.Marshal(`{"score":2,"reason":"100% a lock file.\nNo code."}`)
		fmt.Fprintf(w, `{"choices":[{"message":{"content":%s}}]}`, content)
	}))
	t.Cleanup(or.Close)
	h.githubURL, h.openRouterURL = gh.URL, or.URL
	return h
}

func (h *classifyHarness) run(args []string, env map[string]string) (string, error) {
	values := map[string]string{
		"GITHUB_TOKEN":       testToken,
		"OPENROUTER_API_KEY": testKey,
		"GITHUB_REPOSITORY":  "acme/app",
		"GITHUB_API_URL":     h.githubURL,
	}
	for name, value := range env {
		values[name] = value
	}
	var out bytes.Buffer
	err := classify(context.Background(), args, func(name string) string { return values[name] }, &out, h.openRouterURL)
	return out.String(), err
}

func TestClassifyPrintsAnnotation(t *testing.T) {
	h := newClassifyHarness(t)
	summary := filepath.Join(t.TempDir(), "summary.md")
	out, err := h.run([]string{"--pr", "7"}, map[string]string{"GITHUB_STEP_SUMMARY": summary})
	if err != nil {
		t.Fatal(err)
	}
	if h.sentModel != "repo/model" {
		t.Errorf("model %q, want the model of the rules", h.sentModel)
	}
	line, ok := strings.CutPrefix(out, "::notice title=sheepdog-classification::")
	if !ok || strings.Count(out, "\n") != 1 || !strings.HasSuffix(out, "\n") {
		t.Fatalf("output %q, want one notice line", out)
	}
	line = strings.TrimSuffix(line, "\n")
	if strings.Contains(strings.ReplaceAll(line, "%25", ""), "%") || !strings.Contains(line, "100%25 a lock file.") {
		t.Errorf("percent signs are not escaped: %s", line)
	}
	var got github.CIClassification
	if err := json.Unmarshal([]byte(strings.ReplaceAll(line, "%25", "%")), &got); err != nil {
		t.Fatal(err)
	}
	want := github.CIClassification{Score: 2, Reason: "100% a lock file.\nNo code.", Model: "repo/model", HeadSHA: "abc123"}
	if got != want {
		t.Errorf("annotation %+v, want %+v", got, want)
	}
	if strings.Contains(out, testToken) || strings.Contains(out, testKey) {
		t.Error("the output contains a secret")
	}

	text, err := os.ReadFile(summary)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(text), "Score **2** of 10 from `repo/model` for commit `abc123`.") || !strings.Contains(string(text), "No code.") {
		t.Errorf("summary %q", text)
	}
}

func TestClassifyErrors(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		env   map[string]string
		rules string
		or    int
		want  string
	}{
		{name: "no pr", args: nil, want: "--pr must be the number of a pull request"},
		{name: "pr not a number", args: []string{"--pr", "x"}, want: `invalid value "x" for flag -pr`},
		{name: "extra argument", args: []string{"--pr", "7", "8"}, want: `unexpected argument "8"`},
		{name: "no token", args: []string{"--pr", "7"}, env: map[string]string{"GITHUB_TOKEN": ""}, want: "GITHUB_TOKEN is required"},
		{name: "no key", args: []string{"--pr", "7"}, env: map[string]string{"OPENROUTER_API_KEY": ""}, want: "OPENROUTER_API_KEY is required"},
		{name: "no repository", args: []string{"--pr", "7"}, env: map[string]string{"GITHUB_REPOSITORY": ""}, want: "GITHUB_REPOSITORY is required"},
		{name: "bad repository", args: []string{"--pr", "7"}, env: map[string]string{"GITHUB_REPOSITORY": "acme/.."}, want: `invalid repository name "acme/.."`},
		{name: "wrong token", args: []string{"--pr", "7"}, env: map[string]string{"GITHUB_TOKEN": "ghs_wrong"}, want: "load acme/app#7: GitHub rejected the token"},
		{name: "no rules file", args: []string{"--pr", "7"}, rules: "-", want: ".github/sheepdog.yml is missing on main: add it with classification.model"},
		{name: "no model", args: []string{"--pr", "7"}, rules: "version: 1\n", want: ".github/sheepdog.yml on main has no classification.model"},
		{name: "invalid rules", args: []string{"--pr", "7"}, rules: "version: 1\nclassification:\n  ci: true\n", want: ".github/sheepdog.yml on main is not valid: classification.ci needs a classification.model"},
		{name: "wrong key", args: []string{"--pr", "7"}, or: http.StatusUnauthorized, want: "classify with repo/model: Invalid API key"},
		{name: "OpenRouter error", args: []string{"--pr", "7"}, or: http.StatusBadGateway, want: "classify with repo/model: OpenRouter returned HTTP 502"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newClassifyHarness(t)
			switch c.rules {
			case "":
			case "-":
				h.rules = ""
			default:
				h.rules = c.rules
			}
			h.openRouterStatus = c.or
			out, err := h.run(c.args, c.env)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want an error with %q", err, c.want)
			}
			if out != "" {
				t.Errorf("output %q after an error", out)
			}
			if strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), testKey) {
				t.Errorf("the error contains a secret: %v", err)
			}
		})
	}
}

func TestNoticeEscapesMessage(t *testing.T) {
	got := notice("t", "50% done\r\nnext\nline %0A")
	if want := "::notice title=t::50%25 done%0D%0Anext%0Aline %250A"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUnknownCommandIsAnError(t *testing.T) {
	if err := run([]string{"clasify"}); err == nil || !strings.Contains(err.Error(), `unknown command "clasify"`) {
		t.Fatalf("got %v", err)
	}
}

const commentRules = "version: 1\nclassification:\n  model: repo/model\n  ci: true\n  comment: true\n"

func TestClassifyPostsNoCommentByDefault(t *testing.T) {
	h := newClassifyHarness(t)
	h.commentStatus = http.StatusTeapot
	if _, err := h.run([]string{"--pr", "7"}, nil); err != nil {
		t.Fatal(err)
	}
	if len(h.created) != 0 || len(h.updated) != 0 {
		t.Errorf("comments without classification.comment: %v %v", h.created, h.updated)
	}
}

func TestClassifyCreatesOneComment(t *testing.T) {
	h := newClassifyHarness(t)
	h.rules = commentRules
	// A comment of another user with the marker must not be edited.
	h.comments = `[{"id":5,"body":"<!-- sheepdog-classification -->fake 1/10","user":{"login":"mallory","type":"User"}}]`
	out, err := h.run([]string{"--pr", "7"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, "\n") != 1 {
		t.Errorf("output %q, want only the annotation", out)
	}
	if len(h.created) != 1 || len(h.updated) != 0 {
		t.Fatalf("created %v, updated %v", h.created, h.updated)
	}
	body := h.created[0]
	for _, part := range []string{"<!-- sheepdog-classification -->", "2/10, low danger", "> 100% a lock file.\n> No code.", "Model `repo/model`, commit abc123."} {
		if !strings.Contains(body, part) {
			t.Errorf("comment %q has no %q", body, part)
		}
	}
}

func TestClassifyUpdatesItsComment(t *testing.T) {
	h := newClassifyHarness(t)
	h.rules = commentRules
	h.comments = `[{"id":5,"body":"hello","user":{"login":"alice","type":"User"}},{"id":9,"body":"<!-- sheepdog-classification -->\nold","user":{"login":"github-actions[bot]","type":"Bot"}}]`
	if _, err := h.run([]string{"--pr", "7"}, nil); err != nil {
		t.Fatal(err)
	}
	if len(h.created) != 0 || !strings.Contains(h.updated["9"], "2/10") || len(h.updated) != 1 {
		t.Errorf("created %v, updated %v", h.created, h.updated)
	}
}

func TestClassifyCommentFailureIsAWarning(t *testing.T) {
	h := newClassifyHarness(t)
	h.rules = commentRules
	h.commentStatus = http.StatusForbidden
	out, err := h.run([]string{"--pr", "7"}, nil)
	if err != nil {
		t.Fatalf("a failed comment fails the job: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "::notice title=sheepdog-classification::") ||
		!strings.HasPrefix(lines[1], "::warning title=Sheepdog comment::") || !strings.Contains(lines[1], "pull-requests: write") {
		t.Errorf("output %q", out)
	}
}

func TestCommentBodyQuotesTheReason(t *testing.T) {
	body := commentBody(github.CIClassification{Score: 9, Reason: "Ask @getsentry/everyone.\n<!-- sheepdog-classification -->", Model: "m", HeadSHA: "abc", DiffTruncated: true})
	if strings.Contains(body, "@getsentry") || strings.Count(body, "<!-- sheepdog-classification -->") != 1 {
		t.Errorf("body %q can mention people or holds a second marker", body)
	}
	if !strings.Contains(body, "9/10, high danger") || !strings.Contains(body, "first 200000 characters") {
		t.Errorf("body %q", body)
	}
}
