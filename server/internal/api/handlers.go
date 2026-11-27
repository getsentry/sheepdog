package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"sheepdog/internal/github"
	"sheepdog/internal/openrouter"
	"sheepdog/internal/rules"
	"sheepdog/internal/store"
	"sheepdog/internal/tasks"
)

const repoCacheTTL = 10 * time.Minute

func (s *Server) signIn(w http.ResponseWriter, r *http.Request) {
	if o := r.Header.Get("Origin"); o != "" && o != origin(r) {
		writeError(w, http.StatusForbidden, crossOrigin)
		return
	}
	ip := signInKey(clientIP(r))
	if s.signIns.blocked(ip) {
		writeError(w, http.StatusTooManyRequests, "Too many failed sign-in attempts. Try again in a minute")
		return
	}
	if !s.createSession(w, r) {
		s.signIns.fail(ip)
	}
}

// createSession writes the response of a sign-in and returns true on success.
func (s *Server) createSession(w http.ResponseWriter, r *http.Request) bool {
	var body struct {
		GitHubToken string `json:"githubToken"`
	}
	if !readJSON(w, r, &body) {
		return false
	}
	token := strings.TrimSpace(body.GitHubToken)
	if token == "" {
		writeError(w, http.StatusBadRequest, "githubToken is required")
		return false
	}

	viewer, err := github.New(s.cfg.GitHubURL, token).Viewer(r.Context())
	if errors.Is(err, github.ErrUnauthorized) {
		writeError(w, http.StatusUnauthorized, "GitHub rejected the token")
		return false
	}
	if err != nil {
		githubError(w, err)
		return false
	}
	if !s.cfg.Allowed(viewer.Login) {
		writeError(w, http.StatusForbidden, notAllowed)
		return false
	}
	if !hasScopes(viewer.Scopes) {
		writeError(w, http.StatusBadRequest, "The token needs the scopes repo and read:org")
		return false
	}

	id, err := s.store.UpsertUser(r.Context(), viewer.ID, viewer.Login, token)
	if err != nil {
		internalError(w, err)
		return false
	}
	session, err := s.store.CreateSession(r.Context(), id)
	if err != nil {
		internalError(w, err)
		return false
	}
	s.mu.Lock()
	delete(s.repoCache, id)
	run := s.polls[id]
	s.mu.Unlock()
	// A restart empties the list on the other devices of the user, so it runs only for a new token, a renamed login, or a stopped poller.
	if run == nil || run.token != token || run.login != viewer.Login {
		if err := s.restartPoll(id); err != nil {
			internalError(w, err)
			return false
		}
	}
	setSessionCookie(w, r, session)
	writeJSON(w, http.StatusOK, map[string]string{"sessionToken": session, "login": viewer.Login})
	return true
}

// admin:org and write:org include read:org, and GitHub lists only the granted scope.
func hasScopes(scopes []string) bool {
	org := slices.ContainsFunc(scopes, func(s string) bool {
		return s == "read:org" || s == "write:org" || s == "admin:org"
	})
	return org && slices.Contains(scopes, "repo")
}

func (s *Server) signOut(w http.ResponseWriter, r *http.Request, user store.User) {
	token, _, _ := sessionToken(r)
	if err := s.store.DeleteSession(r.Context(), token); err != nil {
		internalError(w, err)
		return
	}
	s.hub.close(user.ID, token)
	setSessionCookie(w, r, "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) signOutEverywhere(w http.ResponseWriter, r *http.Request, user store.User) {
	if err := s.store.DeleteUserSessions(r.Context(), user.ID); err != nil {
		internalError(w, err)
		return
	}
	s.hub.close(user.ID, "")
	setSessionCookie(w, r, "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, _ *http.Request, user store.User) {
	writeJSON(w, http.StatusOK, map[string]string{"login": user.Login})
}

type settingsResponse struct {
	Repo             string   `json:"repo"`
	Model            string   `json:"model"`
	HasOpenRouterKey bool     `json:"hasOpenRouterKey"`
	Starred          []string `json:"starred"`
	// AutoClassifyRepos are the repositories with auto classify on, in name order.
	AutoClassifyRepos []string `json:"autoClassifyRepos"`
	// AutoReviewRepos are the repositories with auto review on, in name order.
	AutoReviewRepos []string `json:"autoReviewRepos"`
	// GlobalRules is the rules file text for repositories without .github/sheepdog.yml, or "" without one.
	GlobalRules string `json:"globalRules"`
}

func (s *Server) writeSettings(w http.ResponseWriter, r *http.Request, user store.User) {
	starred, err := s.store.StarredRepos(r.Context(), user.ID)
	if err != nil {
		internalError(w, err)
		return
	}
	autoClassify, err := s.store.AutoClassifyRepos(r.Context(), user.ID)
	if err != nil {
		internalError(w, err)
		return
	}
	autoReview, err := s.store.AutoReviewRepos(r.Context(), user.ID)
	if err != nil {
		internalError(w, err)
		return
	}
	globalRules, err := s.store.GlobalRules(r.Context(), user.ID)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settingsResponse{
		Repo: user.Repo, Model: user.Model, HasOpenRouterKey: user.OpenRouterKey != "", Starred: starred,
		AutoClassifyRepos: autoClassify, AutoReviewRepos: autoReview, GlobalRules: globalRules,
	})
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request, user store.User) {
	s.writeSettings(w, r, user)
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request, user store.User) {
	var body struct {
		Repo          *string `json:"repo"`
		Model         *string `json:"model"`
		OpenRouterKey *string `json:"openRouterKey"`
		// An empty text deletes the global rules.
		GlobalRules *string `json:"globalRules"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if body.GlobalRules != nil && strings.TrimSpace(*body.GlobalRules) == "" {
		*body.GlobalRules = ""
	}
	if body.GlobalRules != nil && *body.GlobalRules != "" {
		if _, err := rules.Parse([]byte(*body.GlobalRules)); err != nil {
			writeError(w, http.StatusBadRequest, "The global rules are not valid: "+err.Error())
			return
		}
	}
	for _, field := range []*string{body.Repo, body.Model, body.OpenRouterKey} {
		if field != nil {
			*field = strings.TrimSpace(*field)
		}
	}
	if body.Repo != nil {
		*body.Repo = strings.ToLower(*body.Repo)
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
	if body.GlobalRules != nil {
		if err := s.store.SetGlobalRules(r.Context(), user.ID, *body.GlobalRules); err != nil {
			internalError(w, err)
			return
		}
	}
	if body.Repo != nil && *body.Repo != user.Repo {
		if err := s.restartPoll(user.ID); err != nil {
			internalError(w, err)
			return
		}
	} else if body.GlobalRules != nil {
		s.pollNow(user.ID)
	}
	updated, err := s.store.User(r.Context(), user.ID)
	if err != nil {
		internalError(w, err)
		return
	}
	s.writeSettings(w, r, updated)
}

type repoSettingsResponse struct {
	Repo         string `json:"repo"`
	AutoRefresh  bool   `json:"autoRefresh"`
	AutoClassify bool   `json:"autoClassify"`
	AutoReview   bool   `json:"autoReview"`
}

// repoSettings reads the switches of the user for the repository, and PUT changes them first.
// They need no rules file, so that a repository in the setup state keeps its settings.
func (s *Server) repoSettings(w http.ResponseWriter, r *http.Request, user store.User) {
	repo, ok := repoPath(w, r)
	if !ok {
		return
	}
	before, err := s.store.Prefs(r.Context(), user.ID, repo)
	if err != nil {
		internalError(w, err)
		return
	}
	prefs := before
	if r.Method == http.MethodPut {
		var body struct {
			AutoRefresh  *bool `json:"autoRefresh"`
			AutoClassify *bool `json:"autoClassify"`
			AutoReview   *bool `json:"autoReview"`
		}
		if !readJSON(w, r, &body) {
			return
		}
		update := store.PrefsUpdate{AutoRefresh: body.AutoRefresh, AutoClassify: body.AutoClassify, AutoReview: body.AutoReview}
		if err := s.store.UpdatePrefs(r.Context(), user.ID, repo, update); err != nil {
			internalError(w, err)
			return
		}
		if prefs, err = s.store.Prefs(r.Context(), user.ID, repo); err != nil {
			internalError(w, err)
			return
		}
		// A switch that turns on for the selected repository checks GitHub at once.
		turnedOn := prefs.AutoRefresh && !before.AutoRefresh || prefs.AutoClassify && !before.AutoClassify || prefs.AutoReview && !before.AutoReview
		if repo == user.Repo && turnedOn {
			s.pollNow(user.ID)
		}
	}
	writeJSON(w, http.StatusOK, repoSettingsResponse{Repo: repo, AutoRefresh: prefs.AutoRefresh, AutoClassify: prefs.AutoClassify, AutoReview: prefs.AutoReview})
}

// star adds a star for PUT and removes it for DELETE, and returns the settings. Stars need no rules file, because they are not PR work.
func (s *Server) star(w http.ResponseWriter, r *http.Request, user store.User) {
	repo, ok := repoPath(w, r)
	if !ok {
		return
	}
	var err error
	if r.Method == http.MethodPut {
		err = s.store.StarRepo(r.Context(), user.ID, repo)
	} else {
		err = s.store.UnstarRepo(r.Context(), user.ID, repo)
	}
	if errors.Is(err, store.ErrTooManyStars) {
		writeError(w, http.StatusConflict, fmt.Sprintf("You can star at most %d repositories. Remove a star first", store.MaxStars))
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}
	s.writeSettings(w, r, user)
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
	if cancelled(w, err) {
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

// refresh asks for one poll at once. The result comes as a requests event.
func (s *Server) refresh(w http.ResponseWriter, _ *http.Request, user store.User) {
	if user.Repo == "" {
		writeError(w, http.StatusConflict, "Select a repository first")
		return
	}
	if !s.pollNow(user.ID) {
		writeError(w, http.StatusConflict, rejectedAgain)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// repoPath reads the repository from the path, or writes a 400 error.
// GitHub names ignore case, so the repository is in lower case for stored reviews and task keys.
func repoPath(w http.ResponseWriter, r *http.Request) (string, bool) {
	repo := strings.ToLower(r.PathValue("owner") + "/" + r.PathValue("name"))
	if err := github.ValidRepo(repo); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return "", false
	}
	return repo, true
}

// pullPath reads the repository and the PR number from the path, or writes a 400 error.
func pullPath(w http.ResponseWriter, r *http.Request) (string, int, bool) {
	repo, ok := repoPath(w, r)
	if !ok {
		return "", 0, false
	}
	number, err := strconv.Atoi(r.PathValue("number"))
	if err != nil || number <= 0 {
		writeError(w, http.StatusBadRequest, "The pull request number is not valid")
		return "", 0, false
	}
	return repo, number, true
}

const rulesCheckTTL = 30 * time.Second

type rulesKey struct {
	userID int64
	repo   string
}

type rulesCheck struct {
	at        time.Time
	missingOn string
}

// missingRules returns the default branch when it has no rules file, and "" when it has one. It keeps the answer for requireRules.
func (s *Server) missingRules(ctx context.Context, client *github.Client, userID int64, repo string) (string, error) {
	text, err := client.RulesFile(ctx, repo, "")
	if err != nil {
		return "", err
	}
	missingOn := ""
	if text == nil {
		if missingOn, err = client.DefaultBranch(ctx, repo); err != nil {
			return "", err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for key, check := range s.rulesChecks {
		if now.Sub(check.at) >= rulesCheckTTL {
			delete(s.rulesChecks, key)
		}
	}
	s.rulesChecks[rulesKey{userID, repo}] = rulesCheck{at: now, missingOn: missingOn}
	return missingOn, nil
}

// requireRules writes 409 when the repository has neither a rules file on its default branch nor global rules of the user.
func (s *Server) requireRules(w http.ResponseWriter, r *http.Request, user store.User, repo string) bool {
	_, ok := s.repoRules(w, r, user, repo)
	return ok
}

// repoRules returns the global rules of the user when the default branch has no rules file, and nil when it has one.
// It writes 409 when there are no rules at all. It uses a check of the default branch of the last 30 seconds.
func (s *Server) repoRules(w http.ResponseWriter, r *http.Request, user store.User, repo string) (*rules.Rules, bool) {
	s.mu.Lock()
	check, ok := s.rulesChecks[rulesKey{user.ID, repo}]
	s.mu.Unlock()
	missingOn := check.missingOn
	if !ok || time.Since(check.at) >= rulesCheckTTL {
		var err error
		if missingOn, err = s.missingRules(r.Context(), github.New(s.cfg.GitHubURL, user.GitHubToken), user.ID, repo); err != nil {
			githubError(w, err)
			return nil, false
		}
	}
	if missingOn == "" {
		return nil, true
	}
	global, err := s.globalRules(r.Context(), user.ID)
	if err != nil {
		internalError(w, err)
		return nil, false
	}
	if global == nil {
		writeError(w, http.StatusConflict, fmt.Sprintf("%s has no %s on %s. Set it up first, or set global rules in Settings", repo, github.RulesPath, missingOn))
		return nil, false
	}
	return global, true
}

// globalRules returns the global rules of the user, or nil without them. The server checks them before it stores them.
func (s *Server) globalRules(ctx context.Context, userID int64) (*rules.Rules, error) {
	text, err := s.store.GlobalRules(ctx, userID)
	if err != nil || text == "" {
		return nil, err
	}
	parsed, err := rules.Parse([]byte(text))
	if err != nil {
		return nil, fmt.Errorf("the global rules are not valid: %w", err)
	}
	return &parsed, nil
}

func (s *Server) pullRequest(w http.ResponseWriter, r *http.Request, user store.User) {
	repo, number, ok := pullPath(w, r)
	if !ok || !s.requireRules(w, r, user, repo) {
		return
	}
	detail, err := github.New(s.cfg.GitHubURL, user.GitHubToken).PullRequest(r.Context(), repo, number)
	if err != nil {
		githubError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// maxShownDiffBytes is the largest diff that a client gets, because a browser cannot show a diff of many MB.
const maxShownDiffBytes = 2 << 20

func (s *Server) pullDiff(w http.ResponseWriter, r *http.Request, user store.User) {
	repo, number, ok := pullPath(w, r)
	if !ok || !s.requireRules(w, r, user, repo) {
		return
	}
	diff, err := github.New(s.cfg.GitHubURL, user.GitHubToken).Diff(r.Context(), repo, number)
	if err != nil {
		githubError(w, err)
		return
	}
	shown, truncated := cutDiff(diff, maxShownDiffBytes)
	writeJSON(w, http.StatusOK, map[string]any{"diff": shown, "truncated": truncated})
}

// cutDiff keeps at most limit bytes of the diff, and ends a cut diff after its last full line.
func cutDiff(diff string, limit int) (string, bool) {
	if len(diff) <= limit {
		return diff, false
	}
	cut := diff[:limit]
	if end := strings.LastIndexByte(cut, '\n'); end >= 0 {
		cut = cut[:end+1]
	}
	return strings.ToValidUTF8(cut, ""), true
}

func (s *Server) getReview(w http.ResponseWriter, r *http.Request, user store.User) {
	repo, number, ok := pullPath(w, r)
	if !ok || !s.requireRules(w, r, user, repo) {
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
	global, ok := s.repoRules(w, r, user, repo)
	if !ok {
		return
	}
	if user.OpenRouterKey == "" || user.Model == "" {
		writeError(w, http.StatusBadRequest, "Set the OpenRouter API key and the default model in Settings")
		return
	}
	task, err := s.tasks.Start(user.ID, s.reviewWork(user, repo, number, global))
	if errors.Is(err, tasks.ErrRunning) {
		writeError(w, http.StatusConflict, "A review of this pull request is already running")
		return
	}
	writeJSON(w, http.StatusAccepted, task)
}

func (s *Server) listReviews(w http.ResponseWriter, r *http.Request, user store.User) {
	repo, ok := repoPath(w, r)
	if !ok || !s.requireRules(w, r, user, repo) {
		return
	}
	list, err := s.store.LatestReviews(r.Context(), user.ID, repo)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) listClassifications(w http.ResponseWriter, r *http.Request, user store.User) {
	repo, ok := repoPath(w, r)
	if !ok || !s.requireRules(w, r, user, repo) {
		return
	}
	list, err := s.store.LatestClassifications(r.Context(), user.ID, repo)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) getClassification(w http.ResponseWriter, r *http.Request, user store.User) {
	repo, number, ok := pullPath(w, r)
	if !ok || !s.requireRules(w, r, user, repo) {
		return
	}
	c, err := s.store.LatestClassification(r.Context(), user.ID, repo, number)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "This pull request has no classification")
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) startClassification(w http.ResponseWriter, r *http.Request, user store.User) {
	repo, number, ok := pullPath(w, r)
	if !ok {
		return
	}
	global, ok := s.repoRules(w, r, user, repo)
	if !ok {
		return
	}
	if user.OpenRouterKey == "" || user.Model == "" {
		writeError(w, http.StatusBadRequest, "Set the OpenRouter API key and the default model in Settings")
		return
	}
	task, err := s.tasks.Start(user.ID, s.classifyWork(user, repo, number, global))
	if errors.Is(err, tasks.ErrRunning) {
		writeError(w, http.StatusConflict, "A classification of this pull request is already running")
		return
	}
	writeJSON(w, http.StatusAccepted, task)
}

// reviewWork uses review.model of the global rules for a repository with the global rules, instead of the model of the user.
func (s *Server) reviewWork(user store.User, repo string, number int, global *rules.Rules) tasks.ReviewWork {
	model := user.Model
	if global != nil && global.Review.Model != "" {
		model = global.Review.Model
	}
	return tasks.ReviewWork{
		Store:      s.store,
		GitHub:     github.New(s.cfg.GitHubURL, user.GitHubToken),
		OpenRouter: openrouter.New(s.cfg.OpenRouterURL, user.OpenRouterKey),
		UserID:     user.ID,
		Login:      user.Login,
		Model:      model,
		Repo:       repo,
		Number:     number,
		Publish:    func(e tasks.ReviewEvent) { s.hub.publish(user.ID, "review", e) },
	}
}

// classifyWork polls at once after a stored classification, so that the lane changes without a wait.
// For a repository with the global rules, classification.model of the global rules replaces the model of the user.
func (s *Server) classifyWork(user store.User, repo string, number int, global *rules.Rules) tasks.ClassifyWork {
	model := user.Model
	if global != nil && global.Classification.Model != "" {
		model = global.Classification.Model
	}
	return tasks.ClassifyWork{
		Store:      s.store,
		GitHub:     github.New(s.cfg.GitHubURL, user.GitHubToken),
		OpenRouter: openrouter.New(s.cfg.OpenRouterURL, user.OpenRouterKey),
		UserID:     user.ID,
		Login:      user.Login,
		Model:      model,
		Repo:       repo,
		Number:     number,
		Publish: func(e tasks.ClassificationEvent) {
			s.hub.publish(user.ID, "classification", e)
			if e.Classification != nil {
				s.pollNow(user.ID)
			}
		},
	}
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
