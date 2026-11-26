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
	// Setup is set when the default branch has no rules file. Then the poll does not search for review requests.
	Setup *Setup `json:"setup,omitempty"`
}

// Setup names the default branch without a rules file, and the open setup PR or nil.
type Setup struct {
	DefaultBranch string           `json:"defaultBranch"`
	PullRequest   *github.PullLink `json:"pullRequest"`
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
// A signal that waits already covers the next call. It returns false when the user has no running poller.
func (s *Server) pollNow(userID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.polls[userID]
	if run == nil {
		return false
	}
	select {
	case run.now <- struct{}{}:
	default:
	}
	return true
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
		if err == nil {
			s.autoClassify(ctx, user.ID, user.Repo, polled.Rows)
		}
		ticker.Reset(s.pollInterval)
		if !s.waitPoll(ctx, run, user.ID, ticker.C) {
			return
		}
	}
}

// waitPoll waits for a poll-now signal, or for a tick while the user has auto refresh on. It returns false when ctx ends.
// It reads the preference at each tick, so a change needs no signal to the poller.
func (s *Server) waitPoll(ctx context.Context, run *pollRun, userID int64, tick <-chan time.Time) bool {
	for {
		select {
		case <-ctx.Done():
			return false
		case <-run.now:
			return true
		case <-tick:
			prefs, err := s.store.Prefs(ctx, userID)
			if err != nil && ctx.Err() == nil {
				log.Printf("read auto refresh of user %d: %v", userID, err)
			}
			if prefs.AutoRefresh {
				return true
			}
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
		result.Setup = polled.Setup
		result.Error = ""
	}
	s.requests[userID] = result
	s.hub.publish(userID, "requests", result)
	return true
}
