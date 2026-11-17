package api

import (
	"context"
	"errors"
	"log"
	"time"

	"sheepdog/internal/github"
	"sheepdog/internal/store"
)

// Requests is the result of the last poll. A failed poll keeps the rows and lanes of the poll before it.
type Requests struct {
	Repo      string     `json:"repo"`
	FetchedAt *time.Time `json:"fetchedAt"`
	Rows      []Row      `json:"rows"`
	// Lanes are the lane names in the order of the rules file, without manual.
	Lanes      []string `json:"lanes"`
	RulesError string   `json:"rulesError,omitempty"`
	Error      string   `json:"error,omitempty"`
}

func emptyRequests(repo string) Requests {
	return Requests{Repo: repo, Rows: []Row{}, Lanes: []string{}}
}

type pollRun struct {
	cancel context.CancelFunc
	token  string
	login  string
	// now asks the poller for a poll at once.
	now chan struct{}
}

func (s *Server) requestsFor(user store.User) Requests {
	s.mu.Lock()
	defer s.mu.Unlock()
	if result, ok := s.requests[user.ID]; ok {
		return result
	}
	return emptyRequests(user.Repo)
}

// pollNow starts the next poll of the user at once and keeps the current list until it ends.
func (s *Server) pollNow(userID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if run := s.polls[userID]; run != nil {
		select {
		case run.now <- struct{}{}:
		default:
		}
	}
}

// restartPoll stops the poller of the user and starts a new one with the stored token and repository.
func (s *Server) restartPoll(userID int64) error {
	s.mu.Lock()
	s.restarts[userID]++
	restart := s.restarts[userID]
	s.mu.Unlock()
	user, err := s.store.User(s.ctx, userID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// A later restart reads the user after the settings change that started this one, so its settings are at least as new.
	if s.restarts[userID] != restart {
		return nil
	}
	if run := s.polls[userID]; run != nil {
		run.cancel()
		delete(s.polls, userID)
	}
	empty := emptyRequests(user.Repo)
	delete(s.requests, userID)
	s.hub.publish(userID, "requests", empty)
	if user.Repo == "" || !s.cfg.Allowed(user.Login) || s.ctx.Err() != nil {
		return nil
	}
	ctx, cancel := context.WithCancel(s.ctx)
	run := &pollRun{cancel: cancel, token: user.GitHubToken, login: user.Login, now: make(chan struct{}, 1)}
	s.polls[userID] = run
	s.requests[userID] = empty
	s.wg.Go(func() { s.poll(ctx, run, user) })
	return nil
}

func (s *Server) poll(ctx context.Context, run *pollRun, user store.User) {
	defer run.cancel()
	client := github.New(s.cfg.GitHubURL, user.GitHubToken)
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()
	for {
		polled, err := s.fetchRequests(ctx, client, user)
		if ctx.Err() != nil {
			return
		}
		rejected := errors.Is(err, github.ErrUnauthorized)
		if err != nil {
			log.Printf("poll %s for %s: %v", user.Repo, user.Login, err)
		}
		if !s.record(user.ID, run, polled, err) {
			return
		}
		if rejected {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-run.now:
		}
	}
}

// record stores and publishes a poll result. It returns false when a newer poller replaced this one.
// It publishes under the server lock, so that a restart cannot publish its empty list before this result.
func (s *Server) record(userID int64, run *pollRun, polled Requests, err error) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.polls[userID] != run {
		return false
	}
	result := s.requests[userID]
	switch {
	case errors.Is(err, github.ErrUnauthorized):
		result.Error = rejectedAgain
		delete(s.polls, userID)
	case err != nil:
		result.Error = err.Error()
	default:
		now := time.Now()
		result.FetchedAt = &now
		result.Rows = polled.Rows
		result.Lanes = polled.Lanes
		result.RulesError = polled.RulesError
		result.Error = ""
	}
	s.requests[userID] = result
	s.hub.publish(userID, "requests", result)
	return true
}
