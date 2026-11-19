package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

type ciSuite struct {
	event, path, check, title, message string
	noRun                              bool
}

func trustedSuite(message string) ciSuite {
	return ciSuite{event: CIEvent, path: CIWorkflowPath, check: CICheckName, title: CIAnnotationTitle, message: message}
}

func ciMessage(score int, headSHA string) string {
	return fmt.Sprintf(`{"score":%d,"reason":"Lock file only.","model":"a/b","headSha":%q,"diffTruncated":false}`, score, headSHA)
}

func ciNode(headRefOid, commitOid string, suites ...ciSuite) map[string]any {
	nodes := []any{}
	for _, s := range suites {
		var run any
		if !s.noRun {
			run = map[string]any{"event": s.event, "file": map[string]string{"path": s.path}}
		}
		annotations := []any{map[string]string{"title": "Cache", "message": "Cache restored"}, map[string]string{"title": s.title, "message": s.message}}
		nodes = append(nodes, map[string]any{
			"workflowRun": run,
			"checkRuns":   map[string]any{"nodes": []any{map[string]any{"name": s.check, "annotations": map[string]any{"nodes": annotations}}}},
		})
	}
	return map[string]any{
		"headRefOid": headRefOid,
		"commits":    map[string]any{"nodes": []any{map[string]any{"commit": map[string]any{"oid": commitOid, "checkSuites": map[string]any{"nodes": nodes}}}}},
	}
}

func TestCIClassificationsTrustOnlyTheSheepdogWorkflow(t *testing.T) {
	trusted := trustedSuite(ciMessage(2, "head"))
	other := func(change func(*ciSuite)) ciSuite {
		s := trusted
		change(&s)
		return s
	}
	cases := []struct {
		name string
		node map[string]any
		want int
	}{
		{name: "trusted", node: ciNode("head", "head", trusted), want: 2},
		{name: "event pull_request", node: ciNode("head", "head", other(func(s *ciSuite) { s.event = "pull_request" }))},
		{name: "event push", node: ciNode("head", "head", other(func(s *ciSuite) { s.event = "push" }))},
		{name: "another workflow file", node: ciNode("head", "head", other(func(s *ciSuite) { s.path = ".github/workflows/ci.yml" }))},
		{name: "another check name", node: ciNode("head", "head", other(func(s *ciSuite) { s.check = "classify" }))},
		{name: "another annotation title", node: ciNode("head", "head", other(func(s *ciSuite) { s.title = "sheepdog" }))},
		{name: "headSha of another commit", node: ciNode("head", "head", trustedSuite(ciMessage(2, "older")))},
		{name: "malformed annotation", node: ciNode("head", "head", trustedSuite(`score: 2`))},
		{name: "score out of range", node: ciNode("head", "head", trustedSuite(ciMessage(0, "head")))},
		{name: "last commit is not the head", node: ciNode("head", "older", trustedSuite(ciMessage(2, "head")))},
		{name: "suite without workflow run", node: ciNode("head", "head", other(func(s *ciSuite) { s.noRun = true }))},
		{
			name: "only the trusted suite counts, and the newest wins",
			node: ciNode("head", "head",
				other(func(s *ciSuite) { s.event = "pull_request"; s.message = ciMessage(9, "head") }),
				trustedSuite(ciMessage(3, "head")),
				trustedSuite(ciMessage(1, "head")),
				other(func(s *ciSuite) { s.path = ".github/workflows/ci.yml"; s.message = ciMessage(8, "head") })),
			want: 1,
		},
	}

	repository := map[string]any{"pr99": nil}
	numbers := []int{99}
	for i, c := range cases {
		repository[fmt.Sprintf("pr%d", i+1)] = c.node
		numbers = append(numbers, i+1)
	}
	client := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		req := readGraphQL(t, r)
		for _, part := range []string{
			"pr1: pullRequest(number: 1) { ...ciFields }", "pr99: pullRequest(number: 99) { ...ciFields }",
			"checkSuites(first: 50, filterBy: {appId: 15368})", `checkRuns(last: 1, filterBy: {checkName: "Sheepdog classification"})`,
		} {
			if !strings.Contains(req.Query, part) {
				t.Errorf("query has no %s:\n%s", part, req.Query)
			}
		}
		if req.Variables["owner"] != "acme" || req.Variables["name"] != "app" {
			t.Errorf("variables %v", req.Variables)
		}
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"repository": repository}})
	})
	got, err := client.CIClassifications(context.Background(), "acme/app", numbers)
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range cases {
		found, ok := got[i+1]
		switch {
		case c.want == 0 && ok:
			t.Errorf("%s: got %+v, want no score", c.name, found)
		case c.want != 0 && (!ok || found.Score != c.want):
			t.Errorf("%s: got %+v %v, want score %d", c.name, found, ok, c.want)
		}
	}
	if want := (CIClassification{Score: 2, Reason: "Lock file only.", Model: "a/b", HeadSHA: "head"}); got[1] != want {
		t.Errorf("trusted score %+v, want %+v", got[1], want)
	}
	if _, ok := got[99]; ok || len(got) != 2 {
		t.Errorf("scores %+v, want only the trusted ones", got)
	}
}

func TestCIClassificationsWithoutPRsSendNoRequest(t *testing.T) {
	client := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("request %s %s", r.Method, r.URL.Path)
	})
	got, err := client.CIClassifications(context.Background(), "acme/app", nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}
