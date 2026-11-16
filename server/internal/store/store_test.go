package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"reviewqueue/internal/review"
)

const (
	githubToken   = "ghp_plaintextGitHubToken0123456789"
	openRouterKey = "sk-or-v1-plaintextOpenRouterKey"
)

func newKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	rand.Read(key)
	return key
}

func openStore(t *testing.T, path string, key []byte) *Store {
	t.Helper()
	s, err := Open(path, key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSealerRoundTrip(t *testing.T) {
	s, err := newSealer(newKey(t))
	if err != nil {
		t.Fatal(err)
	}
	first, second := s.seal(githubToken, 1, githubTokenColumn), s.seal(githubToken, 1, githubTokenColumn)
	if bytes.Equal(first, second) {
		t.Error("two seals of the same text are equal; the nonce is not random")
	}
	if bytes.Contains(first, []byte(githubToken)) {
		t.Error("sealed bytes contain the plaintext")
	}
	got, err := s.open(first, 1, githubTokenColumn)
	if err != nil || got != githubToken {
		t.Fatalf("open = %q, %v", got, err)
	}
	if _, err := s.open(first[:5], 1, githubTokenColumn); err == nil {
		t.Error("open of a short ciphertext succeeded")
	}
	if _, err := s.open(first, 2, githubTokenColumn); err == nil {
		t.Error("open with another user ID succeeded")
	}
	if _, err := s.open(first, 1, openRouterKeyColumn); err == nil {
		t.Error("open with another column succeeded")
	}
}

func TestSecretCopiedToAnotherRowDoesNotDecrypt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s := openStore(t, path, newKey(t))
	ctx := context.Background()
	alice, err := s.UpsertUser(ctx, 1, "alice", githubToken)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := s.UpsertUser(ctx, 2, "bob", "ghp_bobToken")
	if err != nil {
		t.Fatal(err)
	}
	if again, err := s.UpsertUser(ctx, 1, "alice", githubToken+"2"); err != nil || again != alice {
		t.Fatalf("second sign-in of alice = %d, %v", again, err)
	}
	if u, err := s.User(ctx, alice); err != nil || u.GitHubToken != githubToken+"2" || u.Login != "alice" {
		t.Fatalf("alice = %+v, %v", u, err)
	}

	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`UPDATE users SET github_token_enc = (SELECT github_token_enc FROM users WHERE id = ?) WHERE id = ?`, alice, bob); err != nil {
		t.Fatal(err)
	}
	if _, err := s.User(ctx, bob); err == nil {
		t.Error("the GitHub token of alice decrypted in the row of bob")
	}
	if _, err := raw.Exec(`UPDATE users SET openrouter_key_enc = github_token_enc WHERE id = ?`, alice); err != nil {
		t.Fatal(err)
	}
	if _, err := s.User(ctx, alice); err == nil {
		t.Error("the GitHub token decrypted as the OpenRouter key")
	}
}

func TestWrongKeyCannotDecrypt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	ctx := context.Background()

	s, err := Open(path, newKey(t))
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.UpsertUser(ctx, 3, "alwx", githubToken)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	other := openStore(t, path, newKey(t))
	if _, err := other.User(ctx, id); err == nil {
		t.Fatal("a store with a wrong key decrypted the token")
	}
}

func TestUserSettingsAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	key := newKey(t)
	ctx := context.Background()

	s, err := Open(path, key)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.UpsertUser(ctx, 3, "alwx", "ghp_old")
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.UpsertUser(ctx, 3, "alwx", githubToken)
	if err != nil || again != id {
		t.Fatalf("second upsert = %d, %v; want id %d", again, err, id)
	}
	repo, model, orKey := "getsentry/sentry-react-native", "openai/gpt-5", openRouterKey
	if err := s.UpdateSettings(ctx, id, Settings{Repo: &repo, Model: &model, OpenRouterKey: &orKey}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s = openStore(t, path, key)
	u, err := s.User(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if u.Login != "alwx" || u.GitHubToken != githubToken || u.OpenRouterKey != openRouterKey || u.Repo != repo || u.Model != model {
		t.Errorf("user = %+v", u)
	}

	empty := ""
	if err := s.UpdateSettings(ctx, id, Settings{OpenRouterKey: &empty}); err != nil {
		t.Fatal(err)
	}
	u, err = s.User(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if u.OpenRouterKey != "" || u.Repo != repo || u.Model != model {
		t.Errorf("after key delete: %+v", u)
	}

	if _, err := s.User(ctx, id+100); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing user: %v", err)
	}
}

func TestUserIsFoundByGitHubID(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "test.db"), newKey(t))
	ctx := context.Background()
	first, err := s.UpsertUser(ctx, 1, "alice", "ghp_a")
	if err != nil {
		t.Fatal(err)
	}
	renamed, err := s.UpsertUser(ctx, 1, "alice-renamed", "ghp_a2")
	if err != nil || renamed != first {
		t.Fatalf("sign-in after a rename = %d, %v; want id %d", renamed, err, first)
	}
	if u, err := s.User(ctx, first); err != nil || u.Login != "alice-renamed" || u.GitHubToken != "ghp_a2" {
		t.Fatalf("renamed user = %+v, %v", u, err)
	}
	other, err := s.UpsertUser(ctx, 2, "alice-renamed", "ghp_b")
	if err != nil || other == first {
		t.Fatalf("another GitHub user with the same login = %d, %v; want a new row", other, err)
	}
	if u, err := s.User(ctx, first); err != nil || u.GitHubToken != "ghp_a2" {
		t.Errorf("the first user changed: %+v, %v", u, err)
	}
}

func TestUserIDsWithRepo(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "test.db"), newKey(t))
	ctx := context.Background()
	alice, _ := s.UpsertUser(ctx, 1, "alice", "ghp_a")
	s.UpsertUser(ctx, 2, "bob", "ghp_b")
	repo := "getsentry/sentry-cocoa"
	if err := s.UpdateSettings(ctx, alice, Settings{Repo: &repo}); err != nil {
		t.Fatal(err)
	}
	ids, err := s.UserIDsWithRepo(ctx)
	if err != nil || len(ids) != 1 || ids[0] != alice {
		t.Fatalf("ids = %v, %v; want [%d]", ids, err, alice)
	}
}

func TestSessionsByHash(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "test.db"), newKey(t))
	ctx := context.Background()
	alice, _ := s.UpsertUser(ctx, 1, "alice", "ghp_a")
	bob, _ := s.UpsertUser(ctx, 2, "bob", "ghp_b")

	aliceToken, err := s.CreateSession(ctx, alice)
	if err != nil {
		t.Fatal(err)
	}
	bobToken, err := s.CreateSession(ctx, bob)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliceToken) != 43 || aliceToken == bobToken {
		t.Fatalf("tokens %q, %q", aliceToken, bobToken)
	}

	var stored []byte
	if err := s.db.QueryRow(`SELECT token_hash FROM sessions WHERE user_id = ?`, alice).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, hashToken(aliceToken)) {
		t.Error("the sessions row does not hold the SHA-256 hash of the token")
	}

	if got, err := s.SessionUserID(ctx, aliceToken); err != nil || got != alice {
		t.Errorf("alice session = %d, %v", got, err)
	}
	if got, err := s.SessionUserID(ctx, bobToken); err != nil || got != bob {
		t.Errorf("bob session = %d, %v", got, err)
	}
	if _, err := s.SessionUserID(ctx, "unknown"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown session: %v", err)
	}

	if err := s.DeleteSession(ctx, aliceToken); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionUserID(ctx, aliceToken); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted session: %v", err)
	}
	if got, err := s.SessionUserID(ctx, bobToken); err != nil || got != bob {
		t.Errorf("bob session after alice sign-out = %d, %v", got, err)
	}
}

func (s *Store) ageSession(t *testing.T, token string, idle time.Duration) {
	t.Helper()
	if _, err := s.db.Exec(`UPDATE sessions SET last_used_at = ? WHERE token_hash = ?`, time.Now().Add(-idle).UnixMilli(), hashToken(token)); err != nil {
		t.Fatal(err)
	}
}

func (s *Store) sessionCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM sessions`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSessionsExpireWhenIdle(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "test.db"), newKey(t))
	ctx := context.Background()
	alice, _ := s.UpsertUser(ctx, 1, "alice", "ghp_a")
	active, _ := s.CreateSession(ctx, alice)
	old, _ := s.CreateSession(ctx, alice)
	expired, _ := s.CreateSession(ctx, alice)
	s.ageSession(t, active, SessionIdleTimeout-time.Hour)
	s.ageSession(t, old, SessionIdleTimeout+time.Hour)
	s.ageSession(t, expired, SessionIdleTimeout+time.Hour)

	if got, err := s.SessionUserID(ctx, active); err != nil || got != alice {
		t.Errorf("session used 29 days ago = %d, %v", got, err)
	}
	if _, err := s.SessionUserID(ctx, expired); !errors.Is(err, ErrNotFound) {
		t.Errorf("expired session: %v", err)
	}
	if n := s.sessionCount(t); n != 2 {
		t.Errorf("%d sessions after a request with an expired session, want 2", n)
	}

	if err := s.DeleteExpiredSessions(ctx); err != nil {
		t.Fatal(err)
	}
	if n := s.sessionCount(t); n != 1 {
		t.Errorf("%d sessions after cleanup, want 1", n)
	}
	if _, err := s.SessionUserID(ctx, active); err != nil {
		t.Errorf("active session after cleanup: %v", err)
	}
}

func TestDeleteUserSessions(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "test.db"), newKey(t))
	ctx := context.Background()
	alice, _ := s.UpsertUser(ctx, 1, "alice", "ghp_a")
	bob, _ := s.UpsertUser(ctx, 2, "bob", "ghp_b")
	first, _ := s.CreateSession(ctx, alice)
	second, _ := s.CreateSession(ctx, alice)
	bobSession, _ := s.CreateSession(ctx, bob)

	if err := s.DeleteUserSessions(ctx, alice); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{first, second} {
		if _, err := s.SessionUserID(ctx, token); !errors.Is(err, ErrNotFound) {
			t.Errorf("alice session after delete: %v", err)
		}
	}
	if got, err := s.SessionUserID(ctx, bobSession); err != nil || got != bob {
		t.Errorf("bob session = %d, %v", got, err)
	}
}

func TestRawRowsHoldNoPlaintext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s := openStore(t, path, newKey(t))
	ctx := context.Background()
	id, err := s.UpsertUser(ctx, 3, "alwx", githubToken)
	if err != nil {
		t.Fatal(err)
	}
	key := openRouterKey
	if err := s.UpdateSettings(ctx, id, Settings{OpenRouterKey: &key}); err != nil {
		t.Fatal(err)
	}
	session, err := s.CreateSession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}

	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	secrets := []string{githubToken, openRouterKey, session}
	for _, table := range []string{"users", "sessions"} {
		rows, err := raw.Query("SELECT * FROM " + table)
		if err != nil {
			t.Fatal(err)
		}
		columns, _ := rows.Columns()
		count := 0
		for rows.Next() {
			count++
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			for i, v := range values {
				var cell []byte
				switch v := v.(type) {
				case []byte:
					cell = v
				case string:
					cell = []byte(v)
				}
				for _, secret := range secrets {
					if bytes.Contains(cell, []byte(secret)) {
						t.Errorf("%s.%s contains a plaintext secret", table, columns[i])
					}
				}
			}
		}
		rows.Close()
		if count != 1 {
			t.Errorf("%s has %d rows, want 1", table, count)
		}
	}
}

func TestReviewsByUserAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	key := newKey(t)
	ctx := context.Background()
	s, err := Open(path, key)
	if err != nil {
		t.Fatal(err)
	}
	alice, _ := s.UpsertUser(ctx, 1, "alice", "ghp_a")
	bob, _ := s.UpsertUser(ctx, 2, "bob", "ghp_b")
	repo := "getsentry/sentry-cocoa"
	updated := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	if _, err := s.LatestReview(ctx, alice, repo, 7); !errors.Is(err, ErrNotFound) {
		t.Fatalf("review before add: %v", err)
	}
	first := Review{Repo: repo, Number: 7, HeadSHA: "old", Model: "m/one", Review: review.Review{Risk: "red", Summary: "first", Findings: []review.Finding{}}}
	if err := s.AddReview(ctx, alice, &first); err != nil {
		t.Fatal(err)
	}
	second := Review{
		Repo: repo, Number: 7, HeadSHA: "new", Model: "m/two", DiffTruncated: true, PRUpdatedAt: updated,
		Review: review.Review{Risk: "yellow", Summary: "second", Findings: []review.Finding{{Severity: "low", File: "a.swift", Title: "t", Detail: "d"}}},
	}
	if err := s.AddReview(ctx, alice, &second); err != nil {
		t.Fatal(err)
	}
	if second.CreatedAt.IsZero() {
		t.Error("AddReview did not set CreatedAt")
	}
	s.Close()

	s = openStore(t, path, key)
	got, err := s.LatestReview(ctx, alice, repo, 7)
	if err != nil {
		t.Fatal(err)
	}
	if got.HeadSHA != "new" || got.Model != "m/two" || got.Risk != "yellow" || got.Summary != "second" || !got.DiffTruncated ||
		!got.PRUpdatedAt.Equal(updated) || len(got.Findings) != 1 || got.Findings[0] != second.Findings[0] || !got.CreatedAt.Equal(second.CreatedAt) {
		t.Errorf("latest review = %+v", got)
	}
	if _, err := s.LatestReview(ctx, bob, repo, 7); !errors.Is(err, ErrNotFound) {
		t.Errorf("bob sees the review of alice: %v", err)
	}
	if _, err := s.LatestReview(ctx, alice, repo, 8); !errors.Is(err, ErrNotFound) {
		t.Errorf("other PR: %v", err)
	}
}

func TestPragmas(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "test.db"), newKey(t))
	var mode string
	var timeout, foreignKeys int
	if err := s.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`PRAGMA busy_timeout`).Scan(&timeout); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" || timeout != 5000 || foreignKeys != 1 {
		t.Errorf("journal_mode %q, busy_timeout %d, foreign_keys %d", mode, timeout, foreignKeys)
	}
}
