package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
)

var ErrUnauthorized = errors.New("GitHub rejected the token")

const (
	maxDiffBytes = 50 << 20
	maxBodyBytes = 10 << 20
)

var repoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return e.Message }

type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: time.Minute},
	}
}

// Dot segments match the pattern but would change the REST path.
func ValidRepo(repo string) error {
	owner, name, _ := strings.Cut(repo, "/")
	if !repoPattern.MatchString(repo) || slices.Contains([]string{".", ".."}, owner) || slices.Contains([]string{".", ".."}, name) {
		return fmt.Errorf("invalid repository name %q", repo)
	}
	return nil
}

func (c *Client) get(ctx context.Context, path, accept string, limit int64) (*http.Response, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Accept", accept)
	return c.send(req, limit)
}

// send reads at most limit bytes of the response body.
func (c *Client) send(req *http.Request, limit int64) (*http.Response, []byte, error) {
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, nil, err
	}
	if int64(len(body)) > limit {
		return nil, nil, fmt.Errorf("The GitHub response is larger than %d MB", limit>>20)
	}
	// "partial-results" comes with a successful response that leaves out other SSO organizations, so it is not an error.
	if sso := resp.Header.Get("X-GitHub-SSO"); strings.HasPrefix(strings.TrimSpace(sso), "required") {
		return resp, body, &Error{Status: resp.StatusCode, Message: ssoMessage(sso)}
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return resp, body, ErrUnauthorized
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp, body, &Error{Status: resp.StatusCode, Message: fmt.Sprintf("GitHub: %s (HTTP %d)", failureMessage(body, resp.StatusCode), resp.StatusCode)}
	}
	return resp, body, nil
}

// failureMessage adds the details of the errors list, because a message like "Unprocessable Entity" alone does not name the problem.
// GitHub sends each detail as a text or as an object with a message.
func failureMessage(body []byte, status int) string {
	var failure struct {
		Message string            `json:"message"`
		Errors  []json.RawMessage `json:"errors"`
	}
	json.Unmarshal(body, &failure)
	message := failure.Message
	if message == "" {
		message = http.StatusText(status)
	}
	var details []string
	for _, raw := range failure.Errors {
		var text string
		var detail struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(raw, &text) != nil && json.Unmarshal(raw, &detail) == nil {
			text = detail.Message
		}
		if text != "" {
			details = append(details, text)
		}
	}
	if len(details) > 0 {
		message += ": " + strings.Join(details, "; ")
	}
	return message
}

// The header is "required; url=<authorization URL>".
func ssoMessage(header string) string {
	link := "https://github.com/settings/tokens"
	for part := range strings.SplitSeq(header, ";") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(part), "url="); ok {
			link = value
		}
	}
	return "The organization uses SAML single sign-on. Authorize the token for SSO at " + link
}

func (c *Client) graphql(ctx context.Context, query string, variables map[string]any, data any) error {
	payload, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/graphql", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	_, body, err := c.send(req, maxBodyBytes)
	if err != nil {
		return err
	}
	var response struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return err
	}
	if len(response.Errors) > 0 {
		messages := make([]string, len(response.Errors))
		for i, e := range response.Errors {
			messages[i] = e.Message
		}
		return &Error{Status: http.StatusOK, Message: "GitHub GraphQL: " + strings.Join(messages, "; ")}
	}
	return json.Unmarshal(response.Data, data)
}

type Viewer struct {
	ID     int64
	Login  string
	Scopes []string
}

func (c *Client) Viewer(ctx context.Context) (Viewer, error) {
	resp, body, err := c.get(ctx, "/user", "application/vnd.github+json", maxBodyBytes)
	if err != nil {
		return Viewer{}, err
	}
	var user struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	}
	if err := json.Unmarshal(body, &user); err != nil {
		return Viewer{}, err
	}
	if user.ID == 0 {
		return Viewer{}, errors.New("The GitHub user has no ID")
	}
	var scopes []string
	for scope := range strings.SplitSeq(resp.Header.Get("X-OAuth-Scopes"), ",") {
		if scope = strings.TrimSpace(scope); scope != "" {
			scopes = append(scopes, scope)
		}
	}
	return Viewer{ID: user.ID, Login: user.Login, Scopes: scopes}, nil
}

const repositoriesQuery = `
query($endCursor: String) {
  viewer {
    repositories(first: 100, after: $endCursor, affiliations: [OWNER, COLLABORATOR, ORGANIZATION_MEMBER], ownerAffiliations: [OWNER, COLLABORATOR, ORGANIZATION_MEMBER], isArchived: false, orderBy: {field: PUSHED_AT, direction: DESC}) {
      nodes { nameWithOwner }
      pageInfo { hasNextPage endCursor }
    }
  }
}`

func (c *Client) Repositories(ctx context.Context) ([]string, error) {
	var names []string
	var cursor *string
	for {
		var data struct {
			Viewer struct {
				Repositories struct {
					Nodes []struct {
						NameWithOwner string `json:"nameWithOwner"`
					} `json:"nodes"`
					PageInfo struct {
						HasNextPage bool   `json:"hasNextPage"`
						EndCursor   string `json:"endCursor"`
					} `json:"pageInfo"`
				} `json:"repositories"`
			} `json:"viewer"`
		}
		if err := c.graphql(ctx, repositoriesQuery, map[string]any{"endCursor": cursor}, &data); err != nil {
			return nil, err
		}
		repos := data.Viewer.Repositories
		for _, node := range repos.Nodes {
			names = append(names, node.NameWithOwner)
		}
		if !repos.PageInfo.HasNextPage {
			return names, nil
		}
		cursor = &repos.PageInfo.EndCursor
	}
}

const reviewRequestsQuery = `
query($q: String!, $owner: String!, $login: String!) {
  search(query: $q, type: ISSUE, first: 100) {
    nodes {
      ... on PullRequest {
        ...rowFields
        reviewRequests(first: 50) {
          nodes { requestedReviewer {
            __typename
            ... on User { login }
            ... on Team { combinedSlug name }
          } }
        }
      }
    }
  }
  repositoryOwner(login: $owner) {
    ... on Organization {
      teams(first: 100, userLogins: [$login]) { nodes { combinedSlug } }
    }
  }
}` + rowFields

func (c *Client) ReviewRequests(ctx context.Context, repo, login string) ([]Row, error) {
	if err := ValidRepo(repo); err != nil {
		return nil, err
	}
	owner, _, _ := strings.Cut(repo, "/")
	var data json.RawMessage
	err := c.graphql(ctx, reviewRequestsQuery, map[string]any{
		"q":     "is:pr is:open archived:false repo:" + repo + " review-requested:@me",
		"owner": owner,
		"login": login,
	}, &data)
	if err != nil {
		return nil, err
	}
	return ToRows(data, login)
}

const pullRequestQuery = `
query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      title body headRefName baseRefName headRefOid changedFiles createdAt updatedAt reviewDecision
      labels(first: 50) { nodes { name } }
      reviewRequests(first: 50) { nodes { requestedReviewer { __typename ... on User { login } ... on Team { name } } } }
      latestReviews(first: 50) { nodes { author { login } state } }
      commits(last: 1) { nodes { commit { statusCheckRollup { contexts(first: 100) {
        totalCount nodes { __typename ... on CheckRun { status conclusion } ... on StatusContext { state } }
      } } } } }
    }
  }
}`

func (c *Client) PullRequest(ctx context.Context, repo string, number int) (PRDetail, error) {
	if err := ValidRepo(repo); err != nil {
		return PRDetail{}, err
	}
	owner, name, _ := strings.Cut(repo, "/")
	var data json.RawMessage
	err := c.graphql(ctx, pullRequestQuery, map[string]any{"owner": owner, "name": name, "number": number}, &data)
	if err != nil {
		return PRDetail{}, err
	}
	return toDetail(data)
}

// The GITHUB_TOKEN of a CI job with contents and pull-requests read access cannot read team reviewers or checks, so the
// classification input reads only the fields that it needs.
const pullRequestInputQuery = `
query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) { title body headRefName baseRefName headRefOid updatedAt }
  }
}`

// PullRequestInput returns a PRDetail with only Title, Body, HeadRefName, BaseRefName, HeadRefOid, and UpdatedAt.
func (c *Client) PullRequestInput(ctx context.Context, repo string, number int) (PRDetail, error) {
	if err := ValidRepo(repo); err != nil {
		return PRDetail{}, err
	}
	owner, name, _ := strings.Cut(repo, "/")
	var data struct {
		Repository struct {
			PullRequest *struct {
				Title       string    `json:"title"`
				Body        string    `json:"body"`
				HeadRefName string    `json:"headRefName"`
				BaseRefName string    `json:"baseRefName"`
				HeadRefOid  string    `json:"headRefOid"`
				UpdatedAt   time.Time `json:"updatedAt"`
			} `json:"pullRequest"`
		} `json:"repository"`
	}
	if err := c.graphql(ctx, pullRequestInputQuery, map[string]any{"owner": owner, "name": name, "number": number}, &data); err != nil {
		return PRDetail{}, err
	}
	pr := data.Repository.PullRequest
	if pr == nil {
		return PRDetail{}, &Error{Status: http.StatusNotFound, Message: "GitHub: the pull request does not exist"}
	}
	return PRDetail{Title: pr.Title, Body: pr.Body, HeadRefName: pr.HeadRefName, BaseRefName: pr.BaseRefName, HeadRefOid: pr.HeadRefOid, UpdatedAt: pr.UpdatedAt}, nil
}

func (c *Client) Diff(ctx context.Context, repo string, number int) (string, error) {
	if err := ValidRepo(repo); err != nil {
		return "", err
	}
	_, body, err := c.get(ctx, fmt.Sprintf("/repos/%s/pulls/%d", repo, number), "application/vnd.github.diff", maxDiffBytes)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// Read from the base branch, so that a PR cannot change the review instructions. Nil means no file.
func (c *Client) AgentsFile(ctx context.Context, repo, ref string) (*string, error) {
	return c.file(ctx, repo, "AGENTS.md", ref)
}

// RulesFile reads .github/sheepdog.yml. Read it from the base branch, so that a PR cannot change its own lane. An empty ref reads the
// default branch. Nil means no file.
func (c *Client) RulesFile(ctx context.Context, repo, ref string) (*string, error) {
	return c.file(ctx, repo, RulesPath, ref)
}

const RulesPath = ".github/sheepdog.yml"

func (c *Client) file(ctx context.Context, repo, path, ref string) (*string, error) {
	if err := ValidRepo(repo); err != nil {
		return nil, err
	}
	path = "/repos/" + repo + "/contents/" + path
	if ref != "" {
		path += "?ref=" + url.QueryEscape(ref)
	}
	_, body, err := c.get(ctx, path, "application/vnd.github.raw", maxBodyBytes)
	if e, ok := errors.AsType[*Error](err); ok && e.Status == http.StatusNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	text := string(body)
	return &text, nil
}
