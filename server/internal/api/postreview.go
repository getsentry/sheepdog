package api

import (
	"errors"
	"fmt"
	"log"
	"net/http"

	"sheepdog/internal/github"
	"sheepdog/internal/review"
	"sheepdog/internal/store"
	"sheepdog/internal/tasks"
)

// postReview posts the latest review of the PR to GitHub once, as a review with the event COMMENT and a comment on each line of a finding.
// It needs review.comment: true in the rules of the PR, and a review of the current head commit, so that the lines of the findings are in the diff.
func (s *Server) postReview(w http.ResponseWriter, r *http.Request, user store.User) {
	repo, number, ok := pullPath(w, r)
	if !ok {
		return
	}
	global, ok := s.repoRules(w, r, user, repo)
	if !ok {
		return
	}
	lock := fmt.Sprintf("%d %s", user.ID, tasks.ReviewKey(repo, number))
	s.mu.Lock()
	busy := s.posting[lock]
	s.posting[lock] = true
	s.mu.Unlock()
	if busy {
		writeError(w, http.StatusConflict, "Sheepdog is posting this review already")
		return
	}
	defer func() {
		s.mu.Lock()
		delete(s.posting, lock)
		s.mu.Unlock()
	}()

	ctx := r.Context()
	stored, err := s.store.LatestReview(ctx, user.ID, repo, number)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "This pull request has no review")
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}
	if stored.PostedURL != "" {
		writeError(w, http.StatusConflict, "This review is on GitHub already: "+stored.PostedURL)
		return
	}
	client := github.New(s.cfg.GitHubURL, user.GitHubToken)
	detail, err := client.PullRequestInput(ctx, repo, number)
	if err != nil {
		githubError(w, err)
		return
	}
	if detail.HeadRefOid != stored.HeadSHA {
		writeError(w, http.StatusConflict, "The pull request has new commits since this review. Review it again first")
		return
	}
	laneRules, where := global, "your global rules"
	if laneRules == nil {
		var problem string
		if laneRules, problem, err = rulesAt(ctx, client, repo, detail.BaseRefName); err != nil {
			githubError(w, err)
			return
		}
		if problem != "" {
			writeError(w, http.StatusConflict, problem)
			return
		}
		where = fmt.Sprintf("%s on %s", github.RulesPath, detail.BaseRefName)
	}
	if laneRules == nil || !laneRules.Review.Comment {
		writeError(w, http.StatusConflict, fmt.Sprintf("Posting a review needs review.comment: true in %s", where))
		return
	}
	diff, err := client.Diff(ctx, repo, number)
	if err != nil {
		githubError(w, err)
		return
	}
	body, comments := review.GitHubReview(stored.Review, stored.Model, stored.HeadSHA, diff)
	url, err := client.CreateReview(ctx, repo, number, stored.HeadSHA, body, comments)
	if err != nil {
		githubError(w, err)
		return
	}
	stored.PostedURL = url
	if err := s.store.SetReviewPosted(ctx, user.ID, repo, number, stored.CreatedAt, url); err != nil {
		log.Printf("store the GitHub review %s of %s#%d for %s: %v", url, repo, number, user.Login, err)
		internalError(w, err)
		return
	}
	s.hub.publish(user.ID, "review", tasks.ReviewEvent{Repo: repo, Number: number, Review: &stored})
	writeJSON(w, http.StatusOK, stored)
}
