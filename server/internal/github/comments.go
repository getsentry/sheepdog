package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// IssueComment is a comment on the conversation of a pull request.
type IssueComment struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
	User struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"user"`
}

// maxCommentPages limits the comments that IssueComments reads to 1000.
const maxCommentPages = 10

func (c *Client) IssueComments(ctx context.Context, repo string, number int) ([]IssueComment, error) {
	if err := ValidRepo(repo); err != nil {
		return nil, err
	}
	var all []IssueComment
	for page := 1; page <= maxCommentPages; page++ {
		path := fmt.Sprintf("/repos/%s/issues/%d/comments?per_page=100&page=%d", repo, number, page)
		_, body, err := c.get(ctx, path, "application/vnd.github+json", maxBodyBytes)
		if err != nil {
			return nil, err
		}
		var comments []IssueComment
		if err := json.Unmarshal(body, &comments); err != nil {
			return nil, err
		}
		all = append(all, comments...)
		if len(comments) < 100 {
			break
		}
	}
	return all, nil
}

func (c *Client) CreateIssueComment(ctx context.Context, repo string, number int, body string) error {
	if err := ValidRepo(repo); err != nil {
		return err
	}
	return c.write(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/issues/%d/comments", repo, number), map[string]string{"body": body})
}

func (c *Client) UpdateIssueComment(ctx context.Context, repo string, id int64, body string) error {
	if err := ValidRepo(repo); err != nil {
		return err
	}
	return c.write(ctx, http.MethodPatch, fmt.Sprintf("/repos/%s/issues/comments/%d", repo, id), map[string]string{"body": body})
}
