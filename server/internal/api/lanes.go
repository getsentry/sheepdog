package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"sheepdog/internal/github"
	"sheepdog/internal/rules"
	"sheepdog/internal/store"
)

// Row is a review request with its lane. A repository without a valid rules file gives rows without a lane.
type Row struct {
	github.Row
	Lane           *Lane             `json:"lane,omitempty"`
	Classification *CIClassification `json:"classification,omitempty"`
	// PostReviews is true when review.comment of the rules lets the user post a review of the row to GitHub.
	PostReviews bool `json:"postReviews,omitempty"`
	// autoClassify is true when auto classify may classify the row. See autoWork.
	autoClassify bool
}

// CIClassification is the score of the Sheepdog workflow for the head commit of a row.
type CIClassification struct {
	Source  string `json:"source"`
	Score   int    `json:"score"`
	Reason  string `json:"reason"`
	Model   string `json:"model"`
	HeadSHA string `json:"headSha"`
}

type Lane struct {
	Name   string   `json:"name"`
	Action string   `json:"action"`
	Notes  []string `json:"notes"`
}

// fetchRequests polls the review requests and puts each row in a lane with the rules of its base branch.
// Without a rules file on the default branch, it uses the global rules of the user for every row, and returns only the setup state without them.
func (s *Server) fetchRequests(ctx context.Context, client *github.Client, user store.User) (Requests, error) {
	missingOn, err := s.missingRules(ctx, client, user.ID, user.Repo)
	if err != nil {
		return Requests{}, err
	}
	var setup *Setup
	var global *rules.Rules
	if missingOn != "" {
		pr, err := client.OpenPullRequest(ctx, user.Repo, setupBranch, missingOn)
		if err != nil {
			return Requests{}, err
		}
		setup = &Setup{DefaultBranch: missingOn, PullRequest: pr}
		if global, err = s.globalRules(ctx, user.ID); err != nil {
			return Requests{}, err
		}
		if global == nil {
			return Requests{Rows: []Row{}, Lanes: []string{}, DefaultBranch: missingOn, Setup: setup}, nil
		}
	}
	rows, defaultBranch, err := client.ReviewRequests(ctx, user.Repo, user.Login)
	if err != nil {
		return Requests{}, err
	}
	stored, err := s.store.LatestClassifications(ctx, user.ID, user.Repo)
	if err != nil {
		return Requests{}, err
	}
	classifications := map[int]*rules.Classification{}
	for _, c := range stored {
		classifications[c.Number] = &rules.Classification{HeadSHA: c.HeadSHA, Score: c.Score}
	}

	result := Requests{Rows: make([]Row, 0, len(rows)), Lanes: []string{}, DefaultBranch: defaultBranch, Setup: setup, GlobalRules: global != nil, global: global}
	rulesByBase := map[string]*rules.Rules{}
	var problems []string
	var ciNumbers []int
	for _, row := range rows {
		laneRules, read := rulesByBase[row.BaseRef]
		if !read {
			var problem string
			if global != nil {
				laneRules = global
			} else if laneRules, problem, err = rulesAt(ctx, client, user.Repo, row.BaseRef); err != nil {
				return Requests{}, err
			}
			rulesByBase[row.BaseRef] = laneRules
			if problem != "" {
				problems = append(problems, problem)
			}
			if laneRules != nil {
				for _, lane := range laneRules.Lanes {
					if !slices.Contains(result.Lanes, lane.Name) {
						result.Lanes = append(result.Lanes, lane.Name)
					}
				}
			}
		}
		if laneRules != nil && laneRules.Classification.CI {
			ciNumbers = append(ciNumbers, row.Number)
		}
	}
	ci, err := client.CIClassifications(ctx, user.Repo, ciNumbers)
	if err != nil {
		return Requests{}, err
	}

	for _, row := range rows {
		out := Row{Row: row}
		if laneRules := rulesByBase[row.BaseRef]; laneRules != nil {
			classification := classifications[row.Number]
			if found, ok := ci[row.Number]; ok && found.HeadSHA == row.HeadSHA {
				out.Classification = &CIClassification{Source: "ci", Score: found.Score, Reason: found.Reason, Model: found.Model, HeadSHA: found.HeadSHA}
				classification = &rules.Classification{HeadSHA: found.HeadSHA, Score: found.Score}
			}
			lane := rules.Evaluate(*laneRules, lanePR(row), classification)
			out.Lane = &Lane{Name: lane.Name, Action: lane.Action, Notes: lane.Notes}
			out.autoClassify = lane.NeedsClassification && !laneRules.Classification.CI && !row.IsDraft
			out.PostReviews = laneRules.Review.Comment
		}
		result.Rows = append(result.Rows, out)
	}
	result.RulesError = strings.Join(problems, "; ")
	return result, nil
}

// rulesAt returns nil rules without a rules file, and a problem text for a file that is not valid.
func rulesAt(ctx context.Context, client *github.Client, repo, base string) (*rules.Rules, string, error) {
	text, err := client.RulesFile(ctx, repo, base)
	if err != nil || text == nil {
		return nil, "", err
	}
	parsed, err := rules.Parse([]byte(*text))
	if err != nil {
		return nil, fmt.Sprintf("%s on %s is not valid: %v", github.RulesPath, base, err), nil
	}
	return &parsed, "", nil
}

func lanePR(row github.Row) rules.PR {
	return rules.PR{
		Author:      row.Author,
		AuthorIsBot: row.AuthorBot,
		Title:       row.Title,
		Additions:   row.Additions,
		Deletions:   row.Deletions,
		Files:       row.Files,
		TotalFiles:  row.TotalFiles,
		HeadSHA:     row.HeadSHA,
		CheckState:  row.CheckState,
	}
}

// listed reports whether the PR is in the last polled list of the user.
func (s *Server) listed(userID int64, repo string, number int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := s.requests[userID]
	return result.Repo == repo && slices.ContainsFunc(result.Rows, func(row Row) bool { return row.Number == number })
}

// approveAndMerge evaluates the lane again with fresh data, because the lane of the list can be up to one poll old.
func (s *Server) approveAndMerge(w http.ResponseWriter, r *http.Request, user store.User) {
	repo, number, ok := pullPath(w, r)
	if !ok {
		return
	}
	global, ok := s.repoRules(w, r, user, repo)
	if !ok {
		return
	}
	var body struct {
		HeadSHA string `json:"headSha"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if body.HeadSHA = strings.TrimSpace(body.HeadSHA); body.HeadSHA == "" {
		writeError(w, http.StatusBadRequest, "headSha is required")
		return
	}
	if !s.listed(user.ID, repo, number) {
		writeError(w, http.StatusNotFound, "This pull request is not in your review requests")
		return
	}

	ctx := r.Context()
	client := github.New(s.cfg.GitHubURL, user.GitHubToken)
	pr, err := client.MergePR(ctx, repo, number)
	if err != nil {
		githubError(w, err)
		return
	}
	if pr.HeadSHA != body.HeadSHA {
		writeError(w, http.StatusConflict, "The pull request has new commits. Check them before you approve")
		return
	}
	laneRules := global
	if laneRules == nil {
		var problem string
		if laneRules, problem, err = rulesAt(ctx, client, repo, pr.BaseRef); err != nil {
			githubError(w, err)
			return
		}
		if problem != "" {
			writeError(w, http.StatusConflict, problem)
			return
		}
	}
	if laneRules == nil {
		writeError(w, http.StatusConflict, fmt.Sprintf("The repository has no %s on %s", github.RulesPath, pr.BaseRef))
		return
	}
	var classification *rules.Classification
	stored, err := s.store.LatestClassification(ctx, user.ID, repo, number)
	if err == nil {
		classification = &rules.Classification{HeadSHA: stored.HeadSHA, Score: stored.Score}
	} else if !errors.Is(err, store.ErrNotFound) {
		internalError(w, err)
		return
	}
	if laneRules.Classification.CI {
		ci, err := client.CIClassifications(ctx, repo, []int{number})
		if err != nil {
			githubError(w, err)
			return
		}
		if found, ok := ci[number]; ok && found.HeadSHA == pr.HeadSHA {
			classification = &rules.Classification{HeadSHA: found.HeadSHA, Score: found.Score}
		}
	}
	lane := rules.Evaluate(*laneRules, lanePR(pr.Row), classification)
	if lane.Action != rules.ActionApproveAndAutomerge {
		message := fmt.Sprintf("The lane %s does not allow approve and merge", lane.Name)
		if len(lane.Notes) > 0 {
			message += ": " + strings.Join(lane.Notes, "; ")
		}
		writeError(w, http.StatusConflict, message)
		return
	}

	defer s.pollNow(user.ID)
	if len(lane.Labels) > 0 {
		if err := client.AddLabels(ctx, repo, number, lane.Labels); err != nil {
			githubError(w, err)
			return
		}
	}
	if err := client.Approve(ctx, repo, number, pr.HeadSHA); err != nil {
		githubError(w, err)
		return
	}
	settled, err := s.settledMergeState(ctx, client, repo, number)
	if err != nil {
		githubError(w, err)
		return
	}
	if settled.MergeState == "CLEAN" {
		if err := client.Merge(ctx, repo, number, pr.HeadSHA, settled.MergeMethod()); err != nil {
			githubError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"result": "merged"})
		return
	}
	if !settled.AutoMergeAllowed {
		writeError(w, http.StatusConflict, "Approved, but not merged: GitHub cannot merge the pull request yet, and the repository does not allow auto-merge. Turn on auto-merge in the repository settings, or merge on GitHub")
		return
	}
	if err := client.EnableAutoMerge(ctx, settled, pr.HeadSHA); err != nil {
		githubError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"result": "auto-merge"})
}

// settledMergeState reads the PR again until GitHub knows its merge state after the approval, or until mergeWait ends.
func (s *Server) settledMergeState(ctx context.Context, client *github.Client, repo string, number int) (github.MergePR, error) {
	deadline := time.Now().Add(s.mergeWait)
	for {
		pr, err := client.MergePR(ctx, repo, number)
		if err != nil || pr.MergeState != "UNKNOWN" || !time.Now().Before(deadline) {
			return pr, err
		}
		select {
		case <-ctx.Done():
			return pr, ctx.Err()
		case <-time.After(s.mergeRetry):
		}
	}
}
