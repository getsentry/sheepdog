package github

import (
	"encoding/json"
	"errors"
	"slices"
	"time"
)

type PRDetail struct {
	Title          string         `json:"title"`
	Body           string         `json:"body"`
	HeadRefName    string         `json:"headRefName"`
	BaseRefName    string         `json:"baseRefName"`
	HeadRefOid     string         `json:"headRefOid"`
	ChangedFiles   int            `json:"changedFiles"`
	CreatedAt      time.Time      `json:"createdAt"`
	UpdatedAt      time.Time      `json:"updatedAt"`
	ReviewDecision string         `json:"reviewDecision"`
	Labels         []string       `json:"labels"`
	ReviewRequests []string       `json:"reviewRequests"`
	LatestReviews  []LatestReview `json:"latestReviews"`
	Checks         CheckSummary   `json:"checks"`
}

type LatestReview struct {
	Author string `json:"author"`
	State  string `json:"state"`
}

// A CheckRun reports status and conclusion, a StatusContext reports only state.
type Check struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}

type CheckSummary struct {
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Pending int `json:"pending"`
	Skipped int `json:"skipped"`
}

var (
	failedResults  = []string{"FAILURE", "ERROR", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STARTUP_FAILURE"}
	skippedResults = []string{"SKIPPED", "NEUTRAL"}
	pendingResults = []string{"PENDING", "EXPECTED"}
)

func Summarize(checks []Check) CheckSummary {
	var summary CheckSummary
	for _, check := range checks {
		result := check.Conclusion
		if result == "" {
			result = check.State
		}
		status := check.Status
		if status == "" {
			status = "COMPLETED"
		}
		switch {
		case slices.Contains(failedResults, result):
			summary.Failed++
		case slices.Contains(skippedResults, result):
			summary.Skipped++
		case status != "COMPLETED" || slices.Contains(pendingResults, result):
			summary.Pending++
		default:
			summary.Passed++
		}
	}
	return summary
}

// toDetail reads the data object of the pull request query.
func toDetail(data []byte) (PRDetail, error) {
	var response struct {
		Repository *struct {
			PullRequest *struct {
				Title          string    `json:"title"`
				Body           string    `json:"body"`
				HeadRefName    string    `json:"headRefName"`
				BaseRefName    string    `json:"baseRefName"`
				HeadRefOid     string    `json:"headRefOid"`
				ChangedFiles   int       `json:"changedFiles"`
				CreatedAt      time.Time `json:"createdAt"`
				UpdatedAt      time.Time `json:"updatedAt"`
				ReviewDecision string    `json:"reviewDecision"`
				Labels         nodes[struct {
					Name string `json:"name"`
				}] `json:"labels"`
				ReviewRequests nodes[struct {
					RequestedReviewer *struct {
						Login string `json:"login"`
						Name  string `json:"name"`
					} `json:"requestedReviewer"`
				}] `json:"reviewRequests"`
				LatestReviews nodes[struct {
					Author *struct {
						Login string `json:"login"`
					} `json:"author"`
					State string `json:"state"`
				}] `json:"latestReviews"`
				Commits nodes[struct {
					Commit struct {
						StatusCheckRollup *struct {
							Contexts nodes[Check] `json:"contexts"`
						} `json:"statusCheckRollup"`
					} `json:"commit"`
				}] `json:"commits"`
			} `json:"pullRequest"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return PRDetail{}, err
	}
	if response.Repository == nil || response.Repository.PullRequest == nil {
		return PRDetail{}, errors.New("GitHub: pull request not found")
	}
	pr := response.Repository.PullRequest

	detail := PRDetail{
		Title:          pr.Title,
		Body:           pr.Body,
		HeadRefName:    pr.HeadRefName,
		BaseRefName:    pr.BaseRefName,
		HeadRefOid:     pr.HeadRefOid,
		ChangedFiles:   pr.ChangedFiles,
		CreatedAt:      pr.CreatedAt,
		UpdatedAt:      pr.UpdatedAt,
		ReviewDecision: pr.ReviewDecision,
		Labels:         []string{},
		ReviewRequests: []string{},
		LatestReviews:  []LatestReview{},
	}
	for _, label := range pr.Labels.Nodes {
		detail.Labels = append(detail.Labels, label.Name)
	}
	for _, request := range pr.ReviewRequests.Nodes {
		name := "unknown"
		if reviewer := request.RequestedReviewer; reviewer != nil && reviewer.Login != "" {
			name = reviewer.Login
		} else if reviewer != nil && reviewer.Name != "" {
			name = reviewer.Name
		}
		detail.ReviewRequests = append(detail.ReviewRequests, name)
	}
	for _, review := range pr.LatestReviews.Nodes {
		author := "ghost"
		if review.Author != nil {
			author = review.Author.Login
		}
		detail.LatestReviews = append(detail.LatestReviews, LatestReview{Author: author, State: review.State})
	}
	var checks []Check
	for _, commit := range pr.Commits.Nodes {
		if rollup := commit.Commit.StatusCheckRollup; rollup != nil {
			checks = append(checks, rollup.Contexts.Nodes...)
		}
	}
	detail.Checks = Summarize(checks)
	return detail, nil
}
