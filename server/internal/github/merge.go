package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// MergePR is a PR with the facts for the lane rules and the merge settings of its repository.
type MergePR struct {
	Row
	ID                 string
	AutoMergeAllowed   bool
	SquashMergeAllowed bool
	MergeCommitAllowed bool
	RebaseMergeAllowed bool
}

// MergeMethod chooses squash, then a merge commit, then rebase. A repository allows at least one of them.
func (pr MergePR) MergeMethod() string {
	switch {
	case pr.SquashMergeAllowed:
		return "squash"
	case pr.MergeCommitAllowed:
		return "merge"
	default:
		return "rebase"
	}
}

const mergePRQuery = `
query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    autoMergeAllowed squashMergeAllowed mergeCommitAllowed rebaseMergeAllowed
    pullRequest(number: $number) { id ...rowFields }
  }
}` + rowFields

func (c *Client) MergePR(ctx context.Context, repo string, number int) (MergePR, error) {
	if err := ValidRepo(repo); err != nil {
		return MergePR{}, err
	}
	owner, name, _ := strings.Cut(repo, "/")
	var data struct {
		Repository *struct {
			AutoMergeAllowed   bool `json:"autoMergeAllowed"`
			SquashMergeAllowed bool `json:"squashMergeAllowed"`
			MergeCommitAllowed bool `json:"mergeCommitAllowed"`
			RebaseMergeAllowed bool `json:"rebaseMergeAllowed"`
			PullRequest        *struct {
				ID string `json:"id"`
				rowNode
			} `json:"pullRequest"`
		} `json:"repository"`
	}
	if err := c.graphql(ctx, mergePRQuery, map[string]any{"owner": owner, "name": name, "number": number}, &data); err != nil {
		return MergePR{}, err
	}
	if data.Repository == nil || data.Repository.PullRequest == nil {
		return MergePR{}, errors.New("GitHub: pull request not found")
	}
	r := data.Repository
	return MergePR{
		Row:                r.PullRequest.row(),
		ID:                 r.PullRequest.ID,
		AutoMergeAllowed:   r.AutoMergeAllowed,
		SquashMergeAllowed: r.SquashMergeAllowed,
		MergeCommitAllowed: r.MergeCommitAllowed,
		RebaseMergeAllowed: r.RebaseMergeAllowed,
	}, nil
}

// write sends body as JSON and decodes the response into out when out is not nil.
func (c *Client) write(ctx context.Context, method, path string, body, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	_, response, err := c.send(req, maxBodyBytes)
	if err != nil || out == nil {
		return err
	}
	return json.Unmarshal(response, out)
}

func (c *Client) AddLabels(ctx context.Context, repo string, number int, labels []string) error {
	if err := ValidRepo(repo); err != nil {
		return err
	}
	return c.write(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/issues/%d/labels", repo, number), map[string]any{"labels": labels}, nil)
}

// Approve approves the commit sha, so that an approval never covers a commit that was pushed later.
func (c *Client) Approve(ctx context.Context, repo string, number int, sha string) error {
	if err := ValidRepo(repo); err != nil {
		return err
	}
	return c.write(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/pulls/%d/reviews", repo, number), map[string]string{"commit_id": sha, "event": "APPROVE"}, nil)
}

// Merge merges the PR only when its head is still sha.
func (c *Client) Merge(ctx context.Context, repo string, number int, sha, method string) error {
	if err := ValidRepo(repo); err != nil {
		return err
	}
	return c.write(ctx, http.MethodPut, fmt.Sprintf("/repos/%s/pulls/%d/merge", repo, number), map[string]string{"sha": sha, "merge_method": method}, nil)
}

const enableAutoMergeMutation = `
mutation($id: ID!, $method: PullRequestMergeMethod!, $sha: GitObjectID!) {
  enablePullRequestAutoMerge(input: {pullRequestId: $id, mergeMethod: $method, expectedHeadOid: $sha}) { clientMutationId }
}`

// EnableAutoMerge turns on auto-merge only when the head of the PR is still sha.
func (c *Client) EnableAutoMerge(ctx context.Context, pr MergePR, sha string) error {
	var data json.RawMessage
	return c.graphql(ctx, enableAutoMergeMutation, map[string]any{"id": pr.ID, "method": strings.ToUpper(pr.MergeMethod()), "sha": sha}, &data)
}

// ReviewComment is a comment of a review on one line of the diff. Side is RIGHT for a line of the new file and LEFT for a deleted line.
type ReviewComment struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Side string `json:"side"`
	Body string `json:"body"`
}

// CreateReview posts a review with the event COMMENT on the commit, and returns the URL of the review.
// GitHub refuses the whole review when one comment names a line outside the diff.
func (c *Client) CreateReview(ctx context.Context, repo string, number int, commit, body string, comments []ReviewComment) (string, error) {
	if err := ValidRepo(repo); err != nil {
		return "", err
	}
	if comments == nil {
		comments = []ReviewComment{}
	}
	var created struct {
		HTMLURL string `json:"html_url"`
	}
	err := c.write(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/pulls/%d/reviews", repo, number),
		map[string]any{"commit_id": commit, "body": body, "event": "COMMENT", "comments": comments}, &created)
	return created.HTMLURL, err
}
