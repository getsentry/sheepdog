package tasks

import (
	"context"
	"errors"
	"testing"

	"reviewqueue/internal/store"
)

func TestStoredReviewIsPublishedAfterCancel(t *testing.T) {
	var events []ReviewEvent
	work := ReviewWork{Repo: "acme/app", Number: 7, Login: "alice", Publish: func(e ReviewEvent) { events = append(events, e) }}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	work.report(ctx, store.Review{Repo: "acme/app", Number: 7, HeadSHA: "sha-1"}, nil)
	if len(events) != 1 || events[0].Review == nil || events[0].Review.HeadSHA != "sha-1" || events[0].Error != "" {
		t.Fatalf("events after a cancel of a stored review %+v, want the review", events)
	}

	work.report(ctx, store.Review{}, context.Canceled)
	work.report(ctx, store.Review{}, errors.New("the request ended"))
	if len(events) != 1 {
		t.Errorf("a review that ended before it was stored sent %+v", events[1:])
	}

	work.report(context.Background(), store.Review{}, errors.New("Invalid API key"))
	if len(events) != 2 || events[1].Error != "Invalid API key" || events[1].Review != nil {
		t.Errorf("failed review events %+v", events)
	}
}
