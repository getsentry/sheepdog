package review

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sheepdog/internal/openrouter"
)

var input = Input{
	Repo: "acme/app", Number: 7, Title: "Fix crash",
	BaseRefName: "main", HeadRefName: "fix/crash",
	Diff: "diff --git a/x b/x\n+fixed",
}

func TestPromptContainsAllParts(t *testing.T) {
	text, truncated := UserPrompt(input)
	if truncated {
		t.Fatal("small diff was truncated")
	}
	for _, part := range []string{
		"Pull request #7: Fix crash",
		"Branches: main <- fix/crash",
		"The repository has no AGENTS.md.",
		"No description.",
		"<diff>\ndiff --git a/x b/x\n+fixed\n</diff>",
	} {
		if !strings.Contains(text, part) {
			t.Errorf("prompt lacks %q", part)
		}
	}
}

func TestLargeDiffIsTruncated(t *testing.T) {
	agents := "Use tabs."
	big := Input{
		Repo: "acme/app", Number: 7, Title: "Big", BaseRefName: "main", HeadRefName: "big",
		Body: "Body", Agents: &agents, Diff: strings.Repeat("x", MaxDiffCharacters+10),
	}
	text, truncated := UserPrompt(big)
	if !truncated {
		t.Fatal("large diff was not truncated")
	}
	for _, part := range []string{`truncated="true"`, "Only the first 200000 characters of 200010 are included.", "Use tabs.", "<description>\nBody\n</description>"} {
		if !strings.Contains(text, part) {
			t.Errorf("prompt lacks %q", part)
		}
	}
	if strings.Contains(text, strings.Repeat("x", MaxDiffCharacters+1)) {
		t.Fatal("prompt contains more than the limit")
	}
	if !strings.Contains(text, strings.Repeat("x", MaxDiffCharacters)) {
		t.Fatal("prompt lacks the first part of the diff")
	}
}

func TestDiffLimitCountsCharactersNotBytes(t *testing.T) {
	_, truncated := UserPrompt(Input{Diff: strings.Repeat("ä", MaxDiffCharacters)})
	if truncated {
		t.Fatal("a diff of exactly the limit was truncated")
	}
}

func TestRequestUsesStrictJsonSchema(t *testing.T) {
	body, err := RequestBody("acme/model", "hi")
	if err != nil {
		t.Fatal(err)
	}
	var request struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		ResponseFormat struct {
			Type       string `json:"type"`
			JSONSchema struct {
				Name   string         `json:"name"`
				Strict bool           `json:"strict"`
				Schema map[string]any `json:"schema"`
			} `json:"json_schema"`
		} `json:"response_format"`
		Provider struct {
			RequireParameters bool `json:"require_parameters"`
		} `json:"provider"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatal(err)
	}
	if request.Model != "acme/model" || request.ResponseFormat.Type != "json_schema" ||
		request.ResponseFormat.JSONSchema.Name != "review" || !request.ResponseFormat.JSONSchema.Strict ||
		!request.Provider.RequireParameters {
		t.Fatalf("got %s", body)
	}
	if len(request.Messages) != 2 || request.Messages[0].Role != "system" || request.Messages[0].Content != System ||
		request.Messages[1].Role != "user" || request.Messages[1].Content != "hi" {
		t.Fatalf("messages %+v", request.Messages)
	}
	schema := request.ResponseFormat.JSONSchema.Schema
	if schema["additionalProperties"] != false || len(schema["required"].([]any)) != 3 {
		t.Fatalf("schema %v", schema)
	}
	finding := schema["properties"].(map[string]any)["findings"].(map[string]any)["items"].(map[string]any)
	if required := finding["required"].([]any); len(required) != 6 || required[2] != "line" || required[3] != "side" {
		t.Errorf("finding fields %v", required)
	}
	if line, _ := json.Marshal(finding["properties"].(map[string]any)["line"]); string(line) != `{"type":["integer","null"]}` {
		t.Errorf("line schema %s", line)
	}
}

const completionContent = `{"risk":"yellow","summary":"Changes the cache.","findings":[` +
	`{"severity":"high","file":"Sources/Cache.swift","line":42,"side":"new","title":"Race","detail":"Two threads write."},` +
	`{"severity":"low","file":"README.md","line":null,"side":"old","title":"Docs","detail":"Not updated."}]}`

func completion(t *testing.T, content string) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}}}})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestReviewIsParsedFromCompletion(t *testing.T) {
	review, err := Parse(completion(t, completionContent))
	if err != nil {
		t.Fatal(err)
	}
	if review.Risk != "yellow" || len(review.Findings) != 2 || review.Findings[0].Severity != "high" ||
		review.Findings[0].File != "Sources/Cache.swift" || review.Findings[0].Line == nil || *review.Findings[0].Line != 42 ||
		review.Findings[0].Side != "new" || review.Findings[1].Line != nil {
		t.Fatalf("got %+v", review)
	}
}

func TestFindingWithBadLineIsRejected(t *testing.T) {
	for _, finding := range []string{
		`{"severity":"low","file":"a","line":3,"side":"left","title":"t","detail":"d"}`,
		`{"severity":"low","file":"a","line":0,"side":"new","title":"t","detail":"d"}`,
		`{"severity":"low","file":"a","line":3,"title":"t","detail":"d"}`,
	} {
		if _, err := Parse(completion(t, `{"risk":"green","summary":"","findings":[`+finding+`]}`)); err == nil {
			t.Errorf("finding %s was accepted", finding)
		}
	}
}

func TestNumberedDiff(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/a.go b/a.go",
		"--- a/a.go",
		"+++ b/a.go",
		"@@ -10,3 +10,3 @@ func a() {",
		" \tx := 1",
		"--- not a header",
		"+\ty := 2",
		"\\ No newline at end of file",
		"diff --git a/b.go b/b.go",
		"new file mode 100644",
		"--- /dev/null",
		"+++ b/b.go",
		"@@ -0,0 +1 @@",
		"+package b",
		"",
	}, "\n")
	want := strings.Join([]string{
		"diff --git a/a.go b/a.go",
		"--- a/a.go",
		"+++ b/a.go",
		"@@ -10,3 +10,3 @@ func a() {",
		"new 10 |  \tx := 1",
		"old 11 | --- not a header",
		"new 11 | +\ty := 2",
		"\\ No newline at end of file",
		"diff --git a/b.go b/b.go",
		"new file mode 100644",
		"--- /dev/null",
		"+++ b/b.go",
		"@@ -0,0 +1 @@",
		"new 1 | +package b",
		"",
	}, "\n")
	if got := NumberedDiff(diff); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestCompletionErrorIsReported(t *testing.T) {
	_, err := Parse([]byte(`{"error":{"message":"No endpoints found","code":404}}`))
	var orErr *openrouter.Error
	if !errors.As(err, &orErr) || orErr.Message != "No endpoints found" {
		t.Fatalf("got %v", err)
	}
}

func TestEmptyCompletionIsReported(t *testing.T) {
	_, err := Parse([]byte(`{"choices":[]}`))
	if err == nil || err.Error() != "The model returned no review" {
		t.Fatalf("got %v", err)
	}
}

func TestUnknownRiskIsRejected(t *testing.T) {
	if _, err := Parse(completion(t, `{"risk":"blue","summary":"","findings":[]}`)); err == nil {
		t.Fatal("unknown risk was accepted")
	}
}

func TestRequestSendsPromptAndParsesReview(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path %q", r.URL.Path)
		}
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.Model != "acme/model" || !strings.Contains(body.Messages[1].Content, "<diff>\ndiff --git a/x b/x\n@@ -1 +1 @@\nnew 1 | +fixed\n</diff>") {
			t.Errorf("got %+v", body)
		}
		w.Write(completion(t, completionContent))
	}))
	defer server.Close()
	numbered := input
	numbered.Diff = "diff --git a/x b/x\n@@ -1 +1 @@\n+fixed"
	review, truncated, err := Request(context.Background(), openrouter.New(server.URL, "k"), "acme/model", numbered)
	if err != nil || truncated || review.Risk != "yellow" {
		t.Fatalf("got %+v, %v, %v", review, truncated, err)
	}
}
