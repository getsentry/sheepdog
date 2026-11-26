package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// PullLink names a pull request for a link in the client.
type PullLink struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
}

type pullResponse struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	_, body, err := c.get(ctx, path, "application/vnd.github+json", maxBodyBytes)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, out)
}

func (c *Client) DefaultBranch(ctx context.Context, repo string) (string, error) {
	if err := ValidRepo(repo); err != nil {
		return "", err
	}
	var data struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := c.getJSON(ctx, "/repos/"+repo, &data); err != nil {
		return "", err
	}
	if data.DefaultBranch == "" {
		return "", errors.New("GitHub: the repository has no default branch")
	}
	return data.DefaultBranch, nil
}

// BranchHead returns the head commit of the branch. Empty means no branch.
func (c *Client) BranchHead(ctx context.Context, repo, branch string) (string, error) {
	if err := ValidRepo(repo); err != nil {
		return "", err
	}
	var data struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	err := c.getJSON(ctx, "/repos/"+repo+"/git/ref/heads/"+branchPath(branch), &data)
	if e, ok := errors.AsType[*Error](err); ok && e.Status == http.StatusNotFound {
		return "", nil
	}
	return data.Object.SHA, err
}

// branchPath escapes each part of a branch name for a REST path, and keeps the slashes.
func branchPath(branch string) string {
	parts := strings.Split(branch, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func (c *Client) CreateBranch(ctx context.Context, repo, branch, sha string) error {
	if err := ValidRepo(repo); err != nil {
		return err
	}
	return c.write(ctx, http.MethodPost, "/repos/"+repo+"/git/refs", map[string]string{"ref": "refs/heads/" + branch, "sha": sha}, nil)
}

// CreateFile commits a new file on the branch.
func (c *Client) CreateFile(ctx context.Context, repo, path, branch, message string, content []byte) error {
	if err := ValidRepo(repo); err != nil {
		return err
	}
	return c.write(ctx, http.MethodPut, "/repos/"+repo+"/contents/"+path, map[string]string{
		"message": message,
		"content": base64.StdEncoding.EncodeToString(content),
		"branch":  branch,
	}, nil)
}

// OpenPullRequest returns the open PR from the branch of the repository to base. Nil means none.
func (c *Client) OpenPullRequest(ctx context.Context, repo, branch, base string) (*PullLink, error) {
	if err := ValidRepo(repo); err != nil {
		return nil, err
	}
	owner, _, _ := strings.Cut(repo, "/")
	query := url.Values{"head": {owner + ":" + branch}, "base": {base}, "state": {"open"}}
	var pulls []pullResponse
	if err := c.getJSON(ctx, "/repos/"+repo+"/pulls?"+query.Encode(), &pulls); err != nil {
		return nil, err
	}
	if len(pulls) == 0 {
		return nil, nil
	}
	return &PullLink{Number: pulls[0].Number, URL: pulls[0].HTMLURL}, nil
}

func (c *Client) CreatePullRequest(ctx context.Context, repo, branch, base, title, body string) (PullLink, error) {
	if err := ValidRepo(repo); err != nil {
		return PullLink{}, err
	}
	var pull pullResponse
	err := c.write(ctx, http.MethodPost, "/repos/"+repo+"/pulls", map[string]string{"title": title, "body": body, "head": branch, "base": base}, &pull)
	return PullLink{Number: pull.Number, URL: pull.HTMLURL}, err
}
