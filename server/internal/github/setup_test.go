package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestDefaultBranchAndHead(t *testing.T) {
	client := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.EscapedPath() {
		case "/repos/acme/app":
			io.WriteString(w, `{"full_name":"acme/app","default_branch":"trunk"}`)
		case "/repos/acme/app/git/ref/heads/release/1%231":
			io.WriteString(w, `{"ref":"refs/heads/release/1#1","object":{"type":"commit","sha":"abc123"}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"message":"Not Found"}`)
		}
	})
	ctx := context.Background()
	if branch, err := client.DefaultBranch(ctx, "acme/app"); err != nil || branch != "trunk" {
		t.Errorf("default branch %q, %v", branch, err)
	}
	if sha, err := client.BranchHead(ctx, "acme/app", "release/1#1"); err != nil || sha != "abc123" {
		t.Errorf("head %q, %v", sha, err)
	}
	if sha, err := client.BranchHead(ctx, "acme/app", "sheepdog/setup"); err != nil || sha != "" {
		t.Errorf("missing branch %q, %v", sha, err)
	}
	if _, err := client.DefaultBranch(ctx, "acme/gone"); err == nil || err.Error() != "GitHub: Not Found (HTTP 404)" {
		t.Errorf("missing repository: %v", err)
	}
}

func TestRulesFileWithoutRefReadsDefaultBranch(t *testing.T) {
	client := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/app/contents/.github/sheepdog.yml" || r.URL.RawQuery != "" {
			t.Errorf("got %s", r.URL)
		}
		io.WriteString(w, "version: 1\n")
	})
	if text, err := client.RulesFile(context.Background(), "acme/app", ""); err != nil || text == nil || *text != "version: 1\n" {
		t.Fatalf("got %v, %v", text, err)
	}
}

func TestOpenPullRequestFiltersHeadAndBase(t *testing.T) {
	var answer string
	client := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.Method != http.MethodGet || r.URL.Path != "/repos/acme/app/pulls" ||
			q.Get("head") != "acme:sheepdog/setup" || q.Get("base") != "main" || q.Get("state") != "open" {
			t.Errorf("got %s %s", r.Method, r.URL)
		}
		io.WriteString(w, answer)
	})
	answer = `[]`
	if pr, err := client.OpenPullRequest(context.Background(), "acme/app", "sheepdog/setup", "main"); err != nil || pr != nil {
		t.Errorf("no PR: %+v, %v", pr, err)
	}
	answer = `[{"number":12,"html_url":"https://github.com/acme/app/pull/12","url":"https://api.github.com/repos/acme/app/pulls/12"}]`
	pr, err := client.OpenPullRequest(context.Background(), "acme/app", "sheepdog/setup", "main")
	if err != nil || pr == nil || *pr != (PullLink{Number: 12, URL: "https://github.com/acme/app/pull/12"}) {
		t.Errorf("open PR: %+v, %v", pr, err)
	}
}

func TestSetupWrites(t *testing.T) {
	var got []string
	var file struct{ Message, Content, Branch string }
	client := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = append(got, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/repos/acme/app/git/refs":
			if string(body) != `{"ref":"refs/heads/sheepdog/setup","sha":"abc123"}` {
				t.Errorf("ref body %s", body)
			}
			io.WriteString(w, `{"ref":"refs/heads/sheepdog/setup"}`)
		case "/repos/acme/app/contents/.github/sheepdog.yml":
			json.Unmarshal(body, &file)
			io.WriteString(w, `{"content":{}}`)
		case "/repos/acme/app/pulls":
			if string(body) != `{"base":"main","body":"Text.","head":"sheepdog/setup","title":"Add Sheepdog rules"}` {
				t.Errorf("pull body %s", body)
			}
			w.WriteHeader(http.StatusCreated)
			io.WriteString(w, `{"number":12,"html_url":"https://github.com/acme/app/pull/12"}`)
		}
	})
	ctx := context.Background()
	if err := client.CreateBranch(ctx, "acme/app", "sheepdog/setup", "abc123"); err != nil {
		t.Fatal(err)
	}
	if err := client.CreateFile(ctx, "acme/app", RulesPath, "sheepdog/setup", "chore: add Sheepdog rules", []byte("version: 1\n")); err != nil {
		t.Fatal(err)
	}
	if content, _ := base64.StdEncoding.DecodeString(file.Content); string(content) != "version: 1\n" || file.Branch != "sheepdog/setup" || file.Message != "chore: add Sheepdog rules" {
		t.Errorf("file %+v", file)
	}
	pr, err := client.CreatePullRequest(ctx, "acme/app", "sheepdog/setup", "main", "Add Sheepdog rules", "Text.")
	if err != nil || pr != (PullLink{Number: 12, URL: "https://github.com/acme/app/pull/12"}) {
		t.Errorf("PR %+v, %v", pr, err)
	}
	equal(t, got, []string{"POST /repos/acme/app/git/refs", "PUT /repos/acme/app/contents/.github/sheepdog.yml", "POST /repos/acme/app/pulls"})
}
