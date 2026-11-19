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

// ClassificationEvent reports the end of a classification. It has a classification or an error.
type ClassificationEvent struct {
	Repo           string                `json:"repo"`
	Number         int                   `json:"number"`
	Classification *store.Classification `json:"classification,omitempty"`
	Error          string                `json:"error,omitempty"`
}

// ClassifyWork gives one pull request a danger score from 1 to 10 with OpenRouter and stores it.
type ClassifyWork struct {
	Store      *store.Store
	GitHub     *github.Client
	OpenRouter *openrouter.Client
	UserID     int64
	Login      string
	Model      string
	Repo       string
	Number     int
	Publish    func(ClassificationEvent)
}

func (w ClassifyWork) Kind() string     { return "classify" }
func (w ClassifyWork) Key() string      { return ReviewKey(w.Repo, w.Number) }
func (w ClassifyWork) Title() string    { return fmt.Sprintf("Classification of #%d", w.Number) }
func (w ClassifyWork) Subtitle() string { return w.Repo }

func (w ClassifyWork) Run(ctx context.Context) {
	result, err := w.classify(ctx)
	w.report(ctx, result, err)
}

// report publishes a stored classification also after a cancel, because it is in the database.
func (w ClassifyWork) report(ctx context.Context, result store.Classification, err error) {
	if err != nil && ctx.Err() != nil {
		log.Printf("classification %s for %s cancelled", w.Key(), w.Login)
		return
	}
	event := ClassificationEvent{Repo: w.Repo, Number: w.Number}
	if err != nil {
		log.Printf("classification %s for %s failed: %v", w.Key(), w.Login, err)
		event.Error = err.Error()
	} else {
		log.Printf("classification %s for %s done: %d", w.Key(), w.Login, result.Score)
		event.Classification = &result
	}
	w.Publish(event)
}

func (w ClassifyWork) classify(ctx context.Context) (store.Classification, error) {
	detail, input, err := review.LoadInput(ctx, w.GitHub, w.Repo, w.Number)
	if err != nil {
		return store.Classification{}, err
	}
	model, err := w.model(ctx, detail.BaseRefName)
	if err != nil {
		return store.Classification{}, err
	}
	result, truncated, err := review.Classify(ctx, w.OpenRouter, model, input)
	if err != nil {
		return store.Classification{}, err
	}
	stored := store.Classification{
		Repo:           w.Repo,
		Number:         w.Number,
		HeadSHA:        detail.HeadRefOid,
		Model:          model,
		Classification: result,
		DiffTruncated:  truncated,
		PRUpdatedAt:    detail.UpdatedAt,
	}
	if err := w.Store.AddClassification(ctx, w.UserID, &stored); err != nil {
		return store.Classification{}, err
	}
	return stored, nil
}

// model returns classification.model of the rules on the base branch, so that every user gets a score from the same model.
// A missing or invalid rules file keeps the model of the user.
func (w ClassifyWork) model(ctx context.Context, base string) (string, error) {
	text, err := w.GitHub.RulesFile(ctx, w.Repo, base)
	if err != nil || text == nil {
		return w.Model, err
	}
	parsed, err := rules.Parse([]byte(*text))
	if err != nil || parsed.Classification.Model == "" {
		return w.Model, nil
	}
	return parsed.Classification.Model, nil
}
