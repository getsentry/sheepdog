package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"sheepdog/internal/github"
	"sheepdog/internal/rules"
	"sheepdog/internal/store"
)

const (
	setupBranch  = "sheepdog/setup"
	setupMessage = "chore: add Sheepdog rules"
	setupTitle   = "Add Sheepdog rules"
	setupBody    = "This pull request adds `.github/sheepdog.yml`, the rules file of Sheepdog. Sheepdog works only with repositories " +
		"that have this file on the default branch. After the merge, Sheepdog shows the review requests of this repository and sorts them into the lanes of the file."
	docsURL = "https://github.com/getsentry/sheepdog/blob/main/server/README.md"
)

// rulesTemplate is a rules file for a new repository. Without a model, the classification block is a comment.
func rulesTemplate(model string) string {
	classification := `# The OpenRouter model for every classification of this repository. Set a model and remove the # signs.
# classification:
#   model: anthropic/claude-sonnet-4.5
#   # To classify every pull request in CI, add the Sheepdog workflow as .github/workflows/sheepdog.yml
#   # and the secret OPENROUTER_API_KEY, and then set ci: true. See ` + docsURL + `#classification-in-ci
#   ci: false
`
	if model != "" {
		quoted, _ := json.Marshal(model)
		classification = `# The OpenRouter model for every classification of this repository.
classification:
  model: ` + string(quoted) + `
  # To classify every pull request in CI, add the Sheepdog workflow as .github/workflows/sheepdog.yml
  # and the secret OPENROUTER_API_KEY, and then set ci: true. See ` + docsURL + `#classification-in-ci
  ci: false
`
	}
	return `# Rules for Sheepdog. Sheepdog works only with repositories that have this file on the default branch.
# Sheepdog reads the lanes from the base branch of each pull request, so a pull request cannot change its own lane.
# A pull request goes to the first lane whose conditions all match. Every other pull request needs a manual review.
# See ` + docsURL + `#review-lanes
version: 1

` + classification + `
lanes:
  # Small Dependabot updates without a major version change, with green checks and a classification score of at most 2.
  safe:
    authors: ["dependabot[bot]"]
    max_changed_lines: 50
    max_score: 2
    no_major_bumps: true
    require_checks: true
    action: approve-and-automerge

# A pull request that changes one of these paths always needs a manual review.
never_automatic:
  paths: [".github/**"]
`
}

func (s *Server) setupTemplate(w http.ResponseWriter, r *http.Request, user store.User) {
	if _, ok := repoPath(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"content": rulesTemplate(user.Model)})
}

// setUp opens a PR that adds the rules file, because rulesets often block a direct commit to the default branch.
func (s *Server) setUp(w http.ResponseWriter, r *http.Request, user store.User) {
	repo, ok := repoPath(w, r)
	if !ok {
		return
	}
	var body struct {
		Content string `json:"content"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if _, err := rules.Parse([]byte(body.Content)); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("%s is not valid: %v", github.RulesPath, err))
		return
	}

	ctx := r.Context()
	client := github.New(s.cfg.GitHubURL, user.GitHubToken)
	base, err := client.DefaultBranch(ctx, repo)
	if err != nil {
		githubError(w, err)
		return
	}
	existing, err := client.RulesFile(ctx, repo, base)
	if err != nil {
		githubError(w, err)
		return
	}
	if existing != nil {
		writeError(w, http.StatusConflict, fmt.Sprintf("%s already has %s on %s", repo, github.RulesPath, base))
		return
	}
	head, err := client.BranchHead(ctx, repo, setupBranch)
	if err != nil {
		githubError(w, err)
		return
	}
	if head != "" {
		refuseSetupBranch(w, r, client, repo, base)
		return
	}
	sha, err := client.BranchHead(ctx, repo, base)
	if err != nil {
		githubError(w, err)
		return
	}
	if sha == "" {
		writeError(w, http.StatusConflict, fmt.Sprintf("The branch %s of %s has no commits", base, repo))
		return
	}

	if err := client.CreateBranch(ctx, repo, setupBranch, sha); err != nil {
		githubError(w, err)
		return
	}
	if err := client.CreateFile(ctx, repo, github.RulesPath, setupBranch, setupMessage, []byte(body.Content)); err != nil {
		githubError(w, err)
		return
	}
	pr, err := client.CreatePullRequest(ctx, repo, setupBranch, base, setupTitle, setupBody)
	if err != nil {
		githubError(w, err)
		return
	}
	s.pollNow(user.ID)
	writeJSON(w, http.StatusCreated, pr)
}

// refuseSetupBranch writes a 409 error for the branch sheepdog/setup that already exists, with the link of its open PR.
func refuseSetupBranch(w http.ResponseWriter, r *http.Request, client *github.Client, repo, base string) {
	pr, err := client.OpenPullRequest(r.Context(), repo, setupBranch, base)
	if err != nil {
		githubError(w, err)
		return
	}
	if pr != nil {
		writeError(w, http.StatusConflict, fmt.Sprintf("The branch %s already exists. Its pull request is %s", setupBranch, pr.URL))
		return
	}
	writeError(w, http.StatusConflict, fmt.Sprintf("The branch %s already exists and has no open pull request to %s. Delete the branch on GitHub and try again", setupBranch, base))
}
