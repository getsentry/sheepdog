package api

import (
	"context"
	"log"

	"sheepdog/internal/rules"
	"sheepdog/internal/tasks"
)

// maxAutoClassifications is the number of classifications that may run at a time for a user before auto classify starts one more.
const maxAutoClassifications = 2

// autoClassify starts classifications for the rows where only a score of the head commit is missing for an approve-and-automerge lane.
// It starts each head commit of a PR at most once, so a failed or stopped classification does not run again before a new commit.
func (s *Server) autoClassify(ctx context.Context, userID int64, repo string, rows []Row, global *rules.Rules) {
	prefs, err := s.store.Prefs(ctx, userID, repo)
	if err != nil || !prefs.AutoClassify {
		if err != nil && ctx.Err() == nil {
			log.Printf("read auto classify of user %d: %v", userID, err)
		}
		return
	}
	// The poller has the user from its start, and the key or the model can be newer.
	user, err := s.store.User(ctx, userID)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("auto classify for user %d: %v", userID, err)
		}
		return
	}
	if user.OpenRouterKey == "" || user.Model == "" {
		return
	}
	free := maxAutoClassifications
	for _, task := range s.tasks.List(userID) {
		if task.Kind == "classify" {
			free--
		}
	}

	s.mu.Lock()
	tried := s.autoTried[userID]
	current := map[string]string{}
	var start []int
	for _, row := range rows {
		key := tasks.ReviewKey(repo, row.Number)
		if head, ok := tried[key]; ok {
			current[key] = head
		}
		if row.autoClassify && current[key] != row.HeadSHA && len(start) < free {
			current[key] = row.HeadSHA
			start = append(start, row.Number)
		}
	}
	s.autoTried[userID] = current
	s.mu.Unlock()

	for _, number := range start {
		if ctx.Err() != nil {
			return
		}
		// ErrRunning means that a classification of the PR runs already.
		s.tasks.Start(userID, s.classifyWork(user, repo, number, global))
	}
}
