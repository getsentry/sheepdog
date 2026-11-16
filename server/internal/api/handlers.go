package api

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"reviewqueue/internal/github"
	"reviewqueue/internal/openrouter"
	"reviewqueue/internal/store"
	"reviewqueue/internal/tasks"
)

const repoCacheTTL = 10 * time.Minute

func (s *Server) signIn(w http.ResponseWriter, r *http.Request) {
	if !s.signIns.allow(clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, "Too many sign-in attempts. Try again in a minute")
		return
	}
	var body struct {
		GitHubToken string `json:"githubToken"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	token := strings.TrimSpace(body.GitHubToken)
	if token == "" {
		writeError(w, http.StatusBadRequest, "githubToken is required")
		return
	}

	viewer, err := github.New(s.cfg.GitHubURL, token).Viewer(r.Context())
	if errors.Is(err, github.ErrUnauthorized) {
		writeError(w, http.StatusUnauthorized, "GitHub rejected the token")
		return
	}
	if err != nil {
		githubError(w, err)
		return
	}
	if !hasScopes(viewer.Scopes) {
		writeError(w, http.StatusBadRequest, "The token needs the scopes repo and read:org")
		return
	}
	if !s.cfg.Allowed(viewer.Login) {
		writeError(w, http.StatusForbidden, "This GitHub account is not allowed on this server")
		return
	}

	id, err := s.store.UpsertUser(r.Context(), viewer.Login, token)
	if err != nil {
		internalError(w, err)
		return
	}
	session, err := s.store.CreateSession(r.Context(), id)
	if err != nil {
		internalError(w, err)
		return
	}
	// The poller of this user can have stopped on the old token.
	if err := s.restartPoll(id); err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"sessionToken": session, "login": viewer.Login})
}

// admin:org and write:org include read:org, and GitHub lists only the granted scope.
func hasScopes(scopes []string) bool {
	org := slices.ContainsFunc(scopes, func(s string) bool {
		return s == "read:org" || s == "write:org" || s == "admin:org"
	})
	return org && slices.Contains(scopes, "repo")
}

func (s *Server) signOut(w http.ResponseWriter, r *http.Request, _ store.User) {
	token, _ := bearerToken(r)
	if err := s.store.DeleteSession(r.Context(), token); err != nil {
		internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, _ *http.Request, user store.User) {
	writeJSON(w, http.StatusOK, map[string]string{"login": user.Login})
}

type settingsResponse struct {
	Repo             string `json:"repo"`
	Model            string `json:"model"`
	HasOpenRouterKey bool   `json:"hasOpenRouterKey"`
}

func toSettings(user store.User) settingsResponse {
	return settingsResponse{Repo: user.Repo, Model: user.Model, HasOpenRouterKey: user.OpenRouterKey != ""}
}

func (s *Server) getSettings(w http.ResponseWriter, _ *http.Request, user store.User) {
	writeJSON(w, http.StatusOK, toSettings(user))
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request, user store.User) {
	var body struct {
		Repo          *string `json:"repo"`
		Model         *string `json:"model"`
		OpenRouterKey *string `json:"openRouterKey"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	for _, field := range []*string{body.Repo, body.Model, body.OpenRouterKey} {
		if field != nil {
			*field = strings.TrimSpace(*field)
		}
	}
	if body.Repo != nil && *body.Repo != "" {
		if err := github.ValidRepo(*body.Repo); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	err := s.store.UpdateSettings(r.Context(), user.ID, store.Settings{
		Repo:          body.Repo,
		Model:         body.Model,
		OpenRouterKey: body.OpenRouterKey,
	})
	if err != nil {
		internalError(w, err)
		return
	}
	if body.Repo != nil && *body.Repo != user.Repo {
		if err := s.restartPoll(user.ID); err != nil {
			internalError(w, err)
			return
		}
	}
	updated, err := s.store.User(r.Context(), user.ID)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toSettings(updated))
}

func (s *Server) repos(w http.ResponseWriter, r *http.Request, user store.User) {
	s.mu.Lock()
	cached, ok := s.repoCache[user.ID]
	s.mu.Unlock()
	if ok && time.Since(cached.at) < repoCacheTTL {
		writeJSON(w, http.StatusOK, cached.repos)
		return
	}
	repos, err := github.New(s.cfg.GitHubURL, user.GitHubToken).Repositories(r.Context())
	if err != nil {
		githubError(w, err)
		return
	}
	if repos == nil {
		repos = []string{}
	}
	s.mu.Lock()
	s.repoCache[user.ID] = cachedRepos{at: time.Now(), repos: repos}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, repos)
}

func (s *Server) models(w http.ResponseWriter, r *http.Request, user store.User) {
	if user.OpenRouterKey == "" {
		writeError(w, http.StatusBadRequest, "Set an OpenRouter API key first")
		return
	}
	models, err := openrouter.New(s.cfg.OpenRouterURL, user.OpenRouterKey).Models(r.Context())
	if errors.Is(err, openrouter.ErrInvalidKey) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, models)
}

func (s *Server) reviewRequests(w http.ResponseWriter, _ *http.Request, user store.User) {
	writeJSON(w, http.StatusOK, s.requestsFor(user))
}

// pullPath reads the repository and the PR number from the path, or writes a 400 error.
func pullPath(w http.ResponseWriter, r *http.Request) (string, int, bool) {
	repo := r.PathValue("owner") + "/" + r.PathValue("name")
	if err := github.ValidRepo(repo); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return "", 0, false
	}
	number, err := strconv.Atoi(r.PathValue("number"))
	if err != nil || number <= 0 {
		writeError(w, http.StatusBadRequest, "The pull request number is not valid")
		return "", 0, false
	}
	return repo, number, true
}

func (s *Server) pullRequest(w http.ResponseWriter, r *http.Request, user store.User) {
	repo, number, ok := pullPath(w, r)
	if !ok {
		return
	}
	detail, err := github.New(s.cfg.GitHubURL, user.GitHubToken).PullRequest(r.Context(), repo, number)
	if err != nil {
		githubError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) getReview(w http.ResponseWriter, r *http.Request, user store.User) {
	repo, number, ok := pullPath(w, r)
	if !ok {
		return
	}
	review, err := s.store.LatestReview(r.Context(), user.ID, repo, number)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "This pull request has no review")
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, review)
}

func (s *Server) startReview(w http.ResponseWriter, r *http.Request, user store.User) {
	repo, number, ok := pullPath(w, r)
	if !ok {
		return
	}
	if user.OpenRouterKey == "" || user.Model == "" {
		writeError(w, http.StatusBadRequest, "Set the OpenRouter API key and the classification model in Settings")
		return
	}
	task, err := s.tasks.Start(user.ID, tasks.ReviewWork{
		Store:      s.store,
		GitHub:     github.New(s.cfg.GitHubURL, user.GitHubToken),
		OpenRouter: openrouter.New(s.cfg.OpenRouterURL, user.OpenRouterKey),
		UserID:     user.ID,
		Login:      user.Login,
		Model:      user.Model,
		Repo:       repo,
		Number:     number,
		Publish:    func(e tasks.ReviewEvent) { s.hub.publish(user.ID, "review", e) },
	})
	if errors.Is(err, tasks.ErrRunning) {
		writeError(w, http.StatusConflict, "A review of this pull request is already running")
		return
	}
	writeJSON(w, http.StatusAccepted, task)
}

func (s *Server) listTasks(w http.ResponseWriter, _ *http.Request, user store.User) {
	writeJSON(w, http.StatusOK, s.tasks.List(user.ID))
}

func (s *Server) cancelTask(w http.ResponseWriter, r *http.Request, user store.User) {
	if !s.tasks.Cancel(user.ID, r.PathValue("id")) {
		writeError(w, http.StatusNotFound, "No running task has this ID")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
