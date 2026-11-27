package api

import (
	"context"
	"log"
	"strings"

	"sheepdog/internal/rules"
	"sheepdog/internal/store"
	"sheepdog/internal/tasks"
)

// maxAutoTasks is the number of tasks of one kind that may run at a time for a user before auto classify or auto review starts one more.
const maxAutoTasks = 2

// autoWork runs auto classify and auto review after a poll, when the user has them on for the repository.
// Auto classify starts a classification for the rows where only a score of the head commit is missing for an approve-and-automerge lane.
// Auto review starts a review for each row that is not a draft and has no review of its head commit.
func (s *Server) autoWork(ctx context.Context, userID int64, repo string, rows []Row, global *rules.Rules) {
	prefs, err := s.store.Prefs(ctx, userID, repo)
	if err != nil || !prefs.AutoClassify && !prefs.AutoReview {
		if err != nil && ctx.Err() == nil {
			log.Printf("read the automation of user %d: %v", userID, err)
		}
		return
	}
	// The poller has the user from its start, and the key or the model can be newer.
	user, err := s.store.User(ctx, userID)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("automation for user %d: %v", userID, err)
		}
		return
	}
	if user.OpenRouterKey == "" || user.Model == "" {
		return
	}
	if prefs.AutoClassify {
		s.autoStart(ctx, user, "classify", repo, rows, func(row Row) bool { return row.autoClassify }, func(number int) tasks.Work {
			return s.classifyWork(user, repo, number, global)
		})
	}
	if prefs.AutoReview {
		stored, err := s.store.LatestReviews(ctx, user.ID, repo)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("auto review for user %d: %v", userID, err)
			}
			return
		}
		reviewed := map[int]string{}
		for _, review := range stored {
			reviewed[review.Number] = review.HeadSHA
		}
		s.autoStart(ctx, user, "review", repo, rows, func(row Row) bool { return !row.IsDraft && reviewed[row.Number] != row.HeadSHA }, func(number int) tasks.Work {
			return s.reviewWork(user, repo, number, global)
		})
	}
}

// autoStart starts work of one kind for the rows that want it, as long as fewer than maxAutoTasks of the kind run.
// It starts each head commit of a PR at most once for each kind, so a failed or stopped task does not run again before a new commit.
func (s *Server) autoStart(ctx context.Context, user store.User, kind, repo string, rows []Row, wants func(Row) bool, work func(number int) tasks.Work) {
	free := maxAutoTasks
	for _, task := range s.tasks.List(user.ID) {
		if task.Kind == kind {
			free--
		}
	}

	s.mu.Lock()
	tried := s.autoTried[user.ID]
	next := map[string]string{}
	// The tries of the other kind stay, and the tries of this kind stay only for the rows of this poll.
	for key, head := range tried {
		if !strings.HasPrefix(key, kind+" ") {
			next[key] = head
		}
	}
	var start []int
	for _, row := range rows {
		key := kind + " " + tasks.ReviewKey(repo, row.Number)
		if head, ok := tried[key]; ok {
			next[key] = head
		}
		if wants(row) && next[key] != row.HeadSHA && len(start) < free {
			next[key] = row.HeadSHA
			start = append(start, row.Number)
		}
	}
	s.autoTried[user.ID] = next
	s.mu.Unlock()

	for _, number := range start {
		if ctx.Err() != nil {
			return
		}
		// ErrRunning means that a task of this kind runs already for the PR.
		s.tasks.Start(user.ID, work(number))
	}
}
