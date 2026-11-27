package tasks

import (
	"context"
	"fmt"
	"log"

	"sheepdog/internal/github"
	"sheepdog/internal/openrouter"
	"sheepdog/internal/review"
	"sheepdog/internal/rules"
	"sheepdog/internal/store"
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

func (w ReviewWork) review(ctx context.Context) (store.Review, error) {
	detail, input, err := review.LoadInput(ctx, w.GitHub, w.Repo, w.Number)
	if err != nil {
		return store.Review{}, err
	}
	model, err := rulesModel(ctx, w.GitHub, w.Repo, detail.BaseRefName, w.Model, func(r rules.Rules) string { return r.Review.Model })
	if err != nil {
		return store.Review{}, err
	}
	result, truncated, err := review.Request(ctx, w.OpenRouter, model, input)
	if err != nil {
		return store.Review{}, err
	}
	stored := store.Review{
		Repo:          w.Repo,
		Number:        w.Number,
		HeadSHA:       detail.HeadRefOid,
		Model:         model,
		Review:        result,
		DiffTruncated: truncated,
		PRUpdatedAt:   detail.UpdatedAt,
	}
	if err := w.Store.AddReview(ctx, w.UserID, &stored); err != nil {
		return store.Review{}, err
	}
	return stored, nil
}
