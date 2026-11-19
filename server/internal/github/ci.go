package github

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	// CIWorkflowPath is the only workflow file whose score the server trusts. GitHub runs a pull_request_target workflow from the base branch, so a PR cannot change it.
	CIWorkflowPath    = ".github/workflows/sheepdog.yml"
	CIEvent           = "pull_request_target"
	CICheckName       = "Sheepdog classification"
	CIAnnotationTitle = "sheepdog-classification"
	actionsAppID      = 15368
)

// CIClassification is the message of the annotation that `sheepdog classify` writes in its job.
type CIClassification struct {
	Score         int    `json:"score"`
	Reason        string `json:"reason"`
	Model         string `json:"model"`
	HeadSHA       string `json:"headSha"`
	DiffTruncated bool   `json:"diffTruncated"`
}

// The last check run of the name is the newest attempt of the job.
var ciFields = fmt.Sprintf(`
fragment ciFields on PullRequest {
  headRefOid
  commits(last: 1) { nodes { commit { oid checkSuites(first: 50, filterBy: {appId: %d}) { nodes {
    workflowRun { event file { path } }
    checkRuns(last: 1, filterBy: {checkName: %q}) { nodes { name annotations(first: 10) { nodes { title message } } } }
  } } } } }
}`, actionsAppID, CICheckName)

type ciPR struct {
	HeadRefOid string `json:"headRefOid"`
	Commits    nodes[struct {
		Commit struct {
			Oid         string `json:"oid"`
			CheckSuites nodes[struct {
				WorkflowRun *struct {
					Event string `json:"event"`
					File  *struct {
						Path string `json:"path"`
					} `json:"file"`
				} `json:"workflowRun"`
				CheckRuns nodes[struct {
					Name        string `json:"name"`
					Annotations nodes[struct {
						Title   string `json:"title"`
						Message string `json:"message"`
					}] `json:"annotations"`
				}] `json:"checkRuns"`
			}] `json:"checkSuites"`
		} `json:"commit"`
	}] `json:"commits"`
}

// CIClassifications reads the CI score of the head commit of each PR. A PR without a trusted score is not in the map.
func (c *Client) CIClassifications(ctx context.Context, repo string, numbers []int) (map[int]CIClassification, error) {
	if err := ValidRepo(repo); err != nil {
		return nil, err
	}
	result := map[int]CIClassification{}
	if len(numbers) == 0 {
		return result, nil
	}
	var query strings.Builder
	query.WriteString("query($owner: String!, $name: String!) {\n  repository(owner: $owner, name: $name) {\n")
	for _, number := range numbers {
		fmt.Fprintf(&query, "    pr%d: pullRequest(number: %d) { ...ciFields }\n", number, number)
	}
	query.WriteString("  }\n}" + ciFields)

	owner, name, _ := strings.Cut(repo, "/")
	var data struct {
		Repository map[string]*ciPR `json:"repository"`
	}
	if err := c.graphql(ctx, query.String(), map[string]any{"owner": owner, "name": name}, &data); err != nil {
		return nil, err
	}
	for _, number := range numbers {
		if pr := data.Repository[fmt.Sprintf("pr%d", number)]; pr != nil {
			if found, ok := pr.classification(); ok {
				result[number] = found
			}
		}
	}
	return result, nil
}

// classification returns the newest score from a check run of the Sheepdog workflow for the head commit.
func (pr ciPR) classification() (CIClassification, bool) {
	var found CIClassification
	ok := false
	for _, commit := range pr.Commits.Nodes {
		if commit.Commit.Oid != pr.HeadRefOid {
			continue
		}
		for _, suite := range commit.Commit.CheckSuites.Nodes {
			run := suite.WorkflowRun
			if run == nil || run.Event != CIEvent || run.File == nil || run.File.Path != CIWorkflowPath {
				continue
			}
			for _, check := range suite.CheckRuns.Nodes {
				if check.Name != CICheckName {
					continue
				}
				for _, annotation := range check.Annotations.Nodes {
					var c CIClassification
					if annotation.Title != CIAnnotationTitle || json.Unmarshal([]byte(annotation.Message), &c) != nil ||
						c.HeadSHA != pr.HeadRefOid || c.Score < 1 || c.Score > 10 {
						continue
					}
					found, ok = c, true
				}
			}
		}
	}
	return found, ok
}
