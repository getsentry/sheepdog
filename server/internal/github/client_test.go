package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type graphqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

func fakeGitHub(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("Authorization header is %q", r.Header.Get("Authorization"))
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	return New(server.URL, "test-token")
}

func readGraphQL(t *testing.T, r *http.Request) graphqlRequest {
	t.Helper()
	if r.Method != http.MethodPost || r.URL.Path != "/graphql" {
		t.Errorf("got %s %s", r.Method, r.URL.Path)
	}
	var req graphqlRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		t.Fatal(err)
	}
	return req
}

func TestViewerReturnsLoginAndScopes(t *testing.T) {
	client := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user" {
			t.Errorf("path %q", r.URL.Path)
		}
		w.Header().Set("X-OAuth-Scopes", "read:org, repo")
		io.WriteString(w, `{"login":"me","id":12345678901}`)
	})
	viewer, err := client.Viewer(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if viewer.Login != "me" || viewer.ID != 12345678901 {
		t.Fatalf("viewer %+v", viewer)
	}
	equal(t, viewer.Scopes, []string{"read:org", "repo"})
}

func TestViewerWithoutIDIsAnError(t *testing.T) {
	client := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-OAuth-Scopes", "read:org, repo")
		io.WriteString(w, `{"login":"me"}`)
	})
	if _, err := client.Viewer(context.Background()); err == nil || err.Error() != "The GitHub user has no ID" {
		t.Fatalf("err = %v", err)
	}
}

func TestRejectedTokenIsUnauthorized(t *testing.T) {
	client := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"message":"Bad credentials"}`)
	})
	if _, err := client.Viewer(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("got %v", err)
	}
}

func TestSSOHeaderAsksForAuthorization(t *testing.T) {
	const link = "https://github.com/orgs/acme/sso?authorization_request=abc"
	client := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-GitHub-SSO", "required; url="+link)
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, `{"message":"Resource protected by organization SAML enforcement."}`)
	})
	_, err := client.Diff(context.Background(), "acme/app", 1)
	if err == nil || !strings.Contains(err.Error(), "Authorize the token for SSO") || !strings.Contains(err.Error(), link) {
		t.Fatalf("got %v", err)
	}
}

func TestPartialSSOResultsAreNotAnError(t *testing.T) {
	client := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-GitHub-SSO", "partial-results; organizations=21955855")
		io.WriteString(w, "diff --git a/x b/x")
	})
	diff, err := client.Diff(context.Background(), "acme/app", 1)
	if err != nil || diff != "diff --git a/x b/x" {
		t.Fatalf("got %q, %v", diff, err)
	}
}

func TestOtherErrorsCarryGitHubMessage(t *testing.T) {
	client := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, `{"message":"API rate limit exceeded"}`)
	})
	_, err := client.Diff(context.Background(), "acme/app", 1)
	if err == nil || err.Error() != "GitHub: API rate limit exceeded (HTTP 403)" {
		t.Fatalf("got %v", err)
	}
}

func TestGraphQLErrorsAreReported(t *testing.T) {
	client := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":null,"errors":[{"message":"Something went wrong"}]}`)
	})
	_, err := client.Repositories(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Something went wrong") {
		t.Fatalf("got %v", err)
	}
}

func TestInvalidRepositoryNameIsRejected(t *testing.T) {
	client := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("no request expected")
	})
	for _, repo := range []string{"acme", "acme/app review-requested:other", "acme/app/x", "../app", ""} {
		if _, err := client.ReviewRequests(context.Background(), repo, "me"); err == nil {
			t.Errorf("%q was accepted", repo)
		}
	}
	if err := ValidRepo("getsentry/sentry-react-native"); err != nil {
		t.Fatal(err)
	}
}

func TestRepositoriesArePaginated(t *testing.T) {
	calls := 0
	client := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		req := readGraphQL(t, r)
		calls++
		if calls == 1 {
			if req.Variables["endCursor"] != nil {
				t.Errorf("first cursor %v", req.Variables["endCursor"])
			}
			io.WriteString(w, `{"data":{"viewer":{"repositories":{"nodes":[{"nameWithOwner":"acme/a"},{"nameWithOwner":"acme/b"}],"pageInfo":{"hasNextPage":true,"endCursor":"c1"}}}}}`)
			return
		}
		if req.Variables["endCursor"] != "c1" {
			t.Errorf("second cursor %v", req.Variables["endCursor"])
		}
		io.WriteString(w, `{"data":{"viewer":{"repositories":{"nodes":[{"nameWithOwner":"me/c"}],"pageInfo":{"hasNextPage":false,"endCursor":"c2"}}}}}`)
	})
	repos, err := client.Repositories(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	equal(t, repos, []string{"acme/a", "acme/b", "me/c"})
}

func TestReviewRequestsSendSearchAndTeams(t *testing.T) {
	client := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		req := readGraphQL(t, r)
		want := map[string]any{
			"q":     "is:pr is:open archived:false repo:acme/app review-requested:@me",
			"owner": "acme",
			"login": "me",
		}
		for key, value := range want {
			if req.Variables[key] != value {
				t.Errorf("%s is %v", key, req.Variables[key])
			}
		}
		io.WriteString(w, `{"data":`+rowsFixture+`}`)
	})
	rows, err := client.ReviewRequests(context.Background(), "acme/app", "me")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 5 || rows[0].Number != 5 {
		t.Fatalf("got %+v", rows)
	}
}

func TestPullRequestDetail(t *testing.T) {
	client := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		req := readGraphQL(t, r)
		if req.Variables["owner"] != "acme" || req.Variables["name"] != "app" || req.Variables["number"] != float64(7) {
			t.Errorf("variables %v", req.Variables)
		}
		io.WriteString(w, `{"data":{"repository":{"pullRequest":{
			"title": "Fix crash",
			"body": "Fixes it",
			"headRefName": "fix/crash",
			"baseRefName": "main",
			"headRefOid": "abc123",
			"changedFiles": 2,
			"createdAt": "2026-09-29T01:10:35Z",
			"updatedAt": "2026-09-30T01:10:35Z",
			"reviewDecision": "REVIEW_REQUIRED",
			"labels": {"nodes": [{"name": "dependencies"}]},
			"reviewRequests": {"nodes": [
				{"requestedReviewer": {"__typename": "User", "login": "lucas"}},
				{"requestedReviewer": {"__typename": "Team", "name": "Mobile"}}
			]},
			"latestReviews": {"nodes": [{"author": {"login": "sentry"}, "state": "COMMENTED"}]},
			"commits": {"nodes": [{"commit": {"statusCheckRollup": {"contexts": {"totalCount": 2, "nodes": [
				{"__typename": "CheckRun", "status": "COMPLETED", "conclusion": "SUCCESS"},
				{"__typename": "StatusContext", "state": "PENDING"}
			]}}}}]}
		}}}}`)
	})
	detail, err := client.PullRequest(context.Background(), "acme/app", 7)
	if err != nil {
		t.Fatal(err)
	}
	equal(t, detail.ReviewRequests, []string{"lucas", "Mobile"})
	equal(t, detail.Labels, []string{"dependencies"})
	equal(t, detail.LatestReviews, []LatestReview{{Author: "sentry", State: "COMMENTED"}})
	if detail.ChangedFiles != 2 || detail.BaseRefName != "main" || detail.HeadRefName != "fix/crash" ||
		detail.HeadRefOid != "abc123" || detail.ReviewDecision != "REVIEW_REQUIRED" || detail.Body != "Fixes it" ||
		detail.Title != "Fix crash" {
		t.Fatalf("got %+v", detail)
	}
	if detail.Checks != (CheckSummary{Passed: 1, Pending: 1}) {
		t.Fatalf("checks %+v", detail.Checks)
	}
}

func TestPullRequestWithoutChecksOrDecision(t *testing.T) {
	client := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":{"repository":{"pullRequest":{
			"body": "", "headRefName": "a", "baseRefName": "main", "changedFiles": 1,
			"createdAt": "2026-09-29T01:10:35Z", "updatedAt": "2026-09-29T01:10:35Z", "reviewDecision": null,
			"labels": {"nodes": []}, "reviewRequests": {"nodes": []}, "latestReviews": {"nodes": []},
			"commits": {"nodes": [{"commit": {"statusCheckRollup": null}}]}
		}}}}`)
	})
	detail, err := client.PullRequest(context.Background(), "acme/app", 7)
	if err != nil {
		t.Fatal(err)
	}
	if detail.ReviewDecision != "" || detail.Checks != (CheckSummary{}) || detail.Labels == nil {
		t.Fatalf("got %+v", detail)
	}
}

func TestChecksAreSummarized(t *testing.T) {
	checks := []Check{
		{Status: "COMPLETED", Conclusion: "SUCCESS"},
		{Status: "COMPLETED", Conclusion: "SKIPPED"},
		{Status: "COMPLETED", Conclusion: "FAILURE"},
		{Status: "IN_PROGRESS", Conclusion: ""},
		{State: "PENDING"},
		{State: "ERROR"},
		{State: "SUCCESS"},
	}
	if got := Summarize(checks); got != (CheckSummary{Passed: 2, Failed: 2, Pending: 2, Skipped: 1}) {
		t.Fatalf("got %+v", got)
	}
}

func TestDiffUsesDiffMediaType(t *testing.T) {
	client := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/app/pulls/7" || r.Header.Get("Accept") != "application/vnd.github.diff" {
			t.Errorf("got %s with Accept %q", r.URL.Path, r.Header.Get("Accept"))
		}
		io.WriteString(w, "diff --git a/x b/x\n+fixed\n")
	})
	diff, err := client.Diff(context.Background(), "acme/app", 7)
	if err != nil || diff != "diff --git a/x b/x\n+fixed\n" {
		t.Fatalf("got %q, %v", diff, err)
	}
}

func TestAgentsFileIsReadFromRef(t *testing.T) {
	client := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/app/contents/AGENTS.md" || r.URL.Query().Get("ref") != "release/1.0" ||
			r.Header.Get("Accept") != "application/vnd.github.raw" {
			t.Errorf("got %s with Accept %q", r.URL, r.Header.Get("Accept"))
		}
		io.WriteString(w, "Use tabs.")
	})
	text, err := client.AgentsFile(context.Background(), "acme/app", "release/1.0")
	if err != nil || text == nil || *text != "Use tabs." {
		t.Fatalf("got %v, %v", text, err)
	}
}

func TestMissingAgentsFileIsNil(t *testing.T) {
	client := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"message":"Not Found"}`)
	})
	text, err := client.AgentsFile(context.Background(), "acme/app", "main")
	if err != nil || text != nil {
		t.Fatalf("got %v, %v", text, err)
	}
}

func TestCancelledContextStopsRequest(t *testing.T) {
	client := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Diff(ctx, "acme/app", 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestResponseSizeIsLimited(t *testing.T) {
	var size atomic.Int64
	size.Store(maxDiffBytes)
	client := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write(bytes.Repeat([]byte("+"), int(size.Load())))
	})
	diff, err := client.Diff(context.Background(), "acme/app", 7)
	if err != nil || len(diff) != maxDiffBytes {
		t.Fatalf("diff at the limit: %d bytes, %v", len(diff), err)
	}
	size.Store(maxDiffBytes + 1)
	if _, err := client.Diff(context.Background(), "acme/app", 7); err == nil || err.Error() != "The GitHub response is larger than 50 MB" {
		t.Errorf("larger diff: %v", err)
	}
	size.Store(maxBodyBytes + 1)
	if _, err := client.AgentsFile(context.Background(), "acme/app", "main"); err == nil || err.Error() != "The GitHub response is larger than 10 MB" {
		t.Errorf("larger AGENTS.md: %v", err)
	}
	if _, err := client.Repositories(context.Background()); err == nil || err.Error() != "The GitHub response is larger than 10 MB" {
		t.Errorf("larger GraphQL response: %v", err)
	}
}

func TestRulesFileIsReadFromRef(t *testing.T) {
	client := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/app/contents/.github/sheepdog.yml" || r.URL.Query().Get("ref") != "release/1.0" ||
			r.Header.Get("Accept") != "application/vnd.github.raw" {
			t.Errorf("got %s with Accept %q", r.URL, r.Header.Get("Accept"))
		}
		io.WriteString(w, "version: 1\n")
	})
	text, err := client.RulesFile(context.Background(), "acme/app", "release/1.0")
	if err != nil || text == nil || *text != "version: 1\n" {
		t.Fatalf("got %v, %v", text, err)
	}
}

func TestMissingRulesFileIsNil(t *testing.T) {
	client := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"message":"Not Found"}`)
	})
	text, err := client.RulesFile(context.Background(), "acme/app", "main")
	if err != nil || text != nil {
		t.Fatalf("got %v, %v", text, err)
	}
}

func TestErrorDetailsAreAdded(t *testing.T) {
	for body, want := range map[string]string{
		`{"message":"Unprocessable Entity","errors":["Can not approve your own pull request"]}`:                              "GitHub: Unprocessable Entity: Can not approve your own pull request (HTTP 422)",
		`{"message":"Validation Failed","errors":[{"resource":"Label","code":"invalid","message":"Bad name"},{"code":"x"}]}`: "GitHub: Validation Failed: Bad name (HTTP 422)",
		`{"message":"Validation Failed","errors":[]}`:                                                                        "GitHub: Validation Failed (HTTP 422)",
		`not json`: "GitHub: Unprocessable Entity (HTTP 422)",
	} {
		client := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnprocessableEntity)
			io.WriteString(w, body)
		})
		if err := client.Approve(context.Background(), "acme/app", 1, "abc"); err == nil || err.Error() != want {
			t.Errorf("body %s: got %v, want %s", body, err, want)
		}
	}
}

const mergePRResponse = `{"data":{"repository":{
	"autoMergeAllowed": true, "squashMergeAllowed": false, "mergeCommitAllowed": true, "rebaseMergeAllowed": true,
	"pullRequest": {
		"id": "PR_kw1", "number": 7, "title": "Bump expo from 57.0.1 to 57.0.2", "url": "u", "isDraft": false,
		"updatedAt": "2026-11-10T10:00:00Z", "additions": 3, "deletions": 1,
		"headRefOid": "abc123", "baseRefName": "main", "mergeStateStatus": "BLOCKED",
		"author": {"__typename": "Bot", "login": "dependabot"},
		"files": {"totalCount": 2, "nodes": [{"path": "package.json"}, {"path": "package-lock.json"}]},
		"commits": {"nodes": [{"commit": {"statusCheckRollup": {"state": "SUCCESS"}}}]}
	}
}}}`

func TestMergePRReadsFactsAndSettings(t *testing.T) {
	client := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		req := readGraphQL(t, r)
		if req.Variables["owner"] != "acme" || req.Variables["name"] != "app" || req.Variables["number"] != float64(7) ||
			!strings.Contains(req.Query, "fragment rowFields") {
			t.Errorf("request %+v", req)
		}
		io.WriteString(w, mergePRResponse)
	})
	pr, err := client.MergePR(context.Background(), "acme/app", 7)
	if err != nil {
		t.Fatal(err)
	}
	equal(t, pr.Files, []string{"package.json", "package-lock.json"})
	if pr.ID != "PR_kw1" || pr.Number != 7 || pr.HeadSHA != "abc123" || pr.BaseRef != "main" || pr.MergeState != "BLOCKED" ||
		pr.CheckState != "SUCCESS" || pr.TotalFiles != 2 || pr.Author != "dependabot" || !pr.AuthorBot || !pr.AutoMergeAllowed || pr.MergeMethod() != "merge" {
		t.Fatalf("got %+v", pr)
	}
}

func TestMergeMethodPrefersSquash(t *testing.T) {
	for _, c := range []struct {
		pr   MergePR
		want string
	}{
		{MergePR{SquashMergeAllowed: true, MergeCommitAllowed: true, RebaseMergeAllowed: true}, "squash"},
		{MergePR{MergeCommitAllowed: true, RebaseMergeAllowed: true}, "merge"},
		{MergePR{RebaseMergeAllowed: true}, "rebase"},
	} {
		if got := c.pr.MergeMethod(); got != c.want {
			t.Errorf("%+v: %s, want %s", c.pr, got, c.want)
		}
	}
}

func TestMissingMergePRIsAnError(t *testing.T) {
	client := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":{"repository":{"pullRequest":null}}}`)
	})
	if _, err := client.MergePR(context.Background(), "acme/app", 7); err == nil || err.Error() != "GitHub: pull request not found" {
		t.Fatalf("got %v", err)
	}
}

func TestWritesSendTheHeadSHA(t *testing.T) {
	var got []string
	client := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("%s has Content-Type %q", r.URL.Path, r.Header.Get("Content-Type"))
		}
		got = append(got, r.Method+" "+r.URL.Path+" "+string(body))
		io.WriteString(w, `{"data":{"enablePullRequestAutoMerge":{"clientMutationId":null}}}`)
	})
	ctx := context.Background()
	if err := client.AddLabels(ctx, "acme/app", 7, []string{"ready-to-merge"}); err != nil {
		t.Fatal(err)
	}
	if err := client.Approve(ctx, "acme/app", 7, "abc123"); err != nil {
		t.Fatal(err)
	}
	if err := client.Merge(ctx, "acme/app", 7, "abc123", "squash"); err != nil {
		t.Fatal(err)
	}
	if err := client.EnableAutoMerge(ctx, MergePR{ID: "PR_kw1", RebaseMergeAllowed: true}, "abc123"); err != nil {
		t.Fatal(err)
	}
	equal(t, got[:3], []string{
		`POST /repos/acme/app/issues/7/labels {"labels":["ready-to-merge"]}`,
		`POST /repos/acme/app/pulls/7/reviews {"commit_id":"abc123","event":"APPROVE"}`,
		`PUT /repos/acme/app/pulls/7/merge {"merge_method":"squash","sha":"abc123"}`,
	})
	var mutation graphqlRequest
	if err := json.Unmarshal([]byte(strings.TrimPrefix(got[3], "POST /graphql ")), &mutation); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mutation.Query, "enablePullRequestAutoMerge") || !strings.Contains(mutation.Query, "expectedHeadOid: $sha") ||
		mutation.Variables["id"] != "PR_kw1" || mutation.Variables["method"] != "REBASE" || mutation.Variables["sha"] != "abc123" {
		t.Errorf("mutation %+v", mutation)
	}
}
