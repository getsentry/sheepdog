package tasks

import (
	"context"
	"fmt"
	"log"

	"reviewqueue/internal/github"
	"reviewqueue/internal/openrouter"
	"reviewqueue/internal/review"
	"reviewqueue/internal/store"
)

// ReviewEvent reports the end of a review. It has a review or an error. A review that is cancelled before it is stored sends no event.
type ReviewEvent struct {
	Repo   string        `json:"repo"`
	Number int           `json:"number"`
	Review *store.Review `json:"review,omitempty"`
	Error  string        `json:"error,omitempty"`
}

// ReviewWork reviews one pull request with OpenRouter and stores the result.
type ReviewWork struct {
	Store      *store.Store
	GitHub     *github.Client
	OpenRouter *openrouter.Client
	UserID     int64
	Login      string
	Model      string
	Repo       string
	Number     int
	Publish    func(ReviewEvent)
}

func ReviewKey(repo string, number int) string {
	return fmt.Sprintf("%s#%d", repo, number)
}

func (w ReviewWork) Kind() string     { return "review" }
func (w ReviewWork) Key() string      { return ReviewKey(w.Repo, w.Number) }
func (w ReviewWork) Title() string    { return fmt.Sprintf("Review of #%d", w.Number) }
func (w ReviewWork) Subtitle() string { return w.Repo }

func (w ReviewWork) Run(ctx context.Context) {
	result, err := w.review(ctx)
	w.report(ctx, result, err)
}

// report publishes a stored review also after a cancel, because the review is in the database.
func (w ReviewWork) report(ctx context.Context, result store.Review, err error) {
	if err != nil && ctx.Err() != nil {
		log.Printf("review %s for %s cancelled", w.Key(), w.Login)
		return
	}
	event := ReviewEvent{Repo: w.Repo, Number: w.Number}
	if err != nil {
		log.Printf("review %s for %s failed: %v", w.Key(), w.Login, err)
		event.Error = err.Error()
	} else {
		log.Printf("review %s for %s done: %s, %d findings", w.Key(), w.Login, result.Risk, len(result.Findings))
		event.Review = &result
	}
	w.Publish(event)
}

// prompt loads the PR, its diff, and AGENTS.md from the base branch, so that a PR cannot change its own instructions.
func prompt(ctx context.Context, gh *github.Client, repo string, number int) (github.PRDetail, review.Input, error) {
	detail, err := gh.PullRequest(ctx, repo, number)
	if err != nil {
		return github.PRDetail{}, review.Input{}, err
	}
	diff, err := gh.Diff(ctx, repo, number)
	if err != nil {
		return github.PRDetail{}, review.Input{}, err
	}
	agents, err := gh.AgentsFile(ctx, repo, detail.BaseRefName)
	if err != nil {
		return github.PRDetail{}, review.Input{}, err
	}
	return detail, review.Input{
		Repo:        repo,
		Number:      number,
		Title:       detail.Title,
		BaseRefName: detail.BaseRefName,
		HeadRefName: detail.HeadRefName,
		Body:        detail.Body,
		Agents:      agents,
		Diff:        diff,
	}, nil
}

func (w ReviewWork) review(ctx context.Context) (store.Review, error) {
	detail, input, err := prompt(ctx, w.GitHub, w.Repo, w.Number)
	if err != nil {
		return store.Review{}, err
	}
	result, truncated, err := review.Request(ctx, w.OpenRouter, w.Model, input)
	if err != nil {
		return store.Review{}, err
	}
	stored := store.Review{
		Repo:          w.Repo,
		Number:        w.Number,
		HeadSHA:       detail.HeadRefOid,
		Model:         w.Model,
		Review:        result,
		DiffTruncated: truncated,
		PRUpdatedAt:   detail.UpdatedAt,
	}
	if err := w.Store.AddReview(ctx, w.UserID, &stored); err != nil {
		return store.Review{}, err
	}
	return stored, nil
}
