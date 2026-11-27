package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"sheepdog/internal/review"
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

func TestOpenRefusesDatabaseWithoutGitHubID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE users (id INTEGER PRIMARY KEY, login TEXT NOT NULL UNIQUE)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := Open(path, newKey(t)); err == nil || !strings.Contains(err.Error(), "delete it") {
		t.Fatalf("got %v, want an error that asks to delete the old database", err)
	}
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

func TestClassificationsByUserAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	key := newKey(t)
	ctx := context.Background()
	s := openStore(t, path, key)
	alice, _ := s.UpsertUser(ctx, 1, "alice", "ghp_a")
	bob, _ := s.UpsertUser(ctx, 2, "bob", "ghp_b")
	repo := "getsentry/sentry-cocoa"
	updated := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	if _, err := s.LatestClassification(ctx, alice, repo, 7); !errors.Is(err, ErrNotFound) {
		t.Fatalf("classification before add: %v", err)
	}
	first := Classification{Repo: repo, Number: 7, HeadSHA: "old", Model: "m/one", Classification: review.Classification{Score: 9, Reason: "first"}}
	second := Classification{Repo: repo, Number: 7, HeadSHA: "new", Model: "m/two", DiffTruncated: true, PRUpdatedAt: updated, Classification: review.Classification{Score: 2, Reason: "second"}}
	for _, c := range []*Classification{&first, &second} {
		if err := s.AddClassification(ctx, alice, c); err != nil {
			t.Fatal(err)
		}
	}
	if second.CreatedAt.IsZero() {
		t.Error("AddClassification did not set CreatedAt")
	}
	s.Close()

	s = openStore(t, path, key)
	got, err := s.LatestClassification(ctx, alice, repo, 7)
	if err != nil {
		t.Fatal(err)
	}
	if got.HeadSHA != "new" || got.Model != "m/two" || got.Score != 2 || got.Reason != "second" || !got.DiffTruncated ||
		!got.PRUpdatedAt.Equal(updated) || !got.CreatedAt.Equal(second.CreatedAt) {
		t.Errorf("latest classification = %+v", got)
	}
	if _, err := s.LatestClassification(ctx, bob, repo, 7); !errors.Is(err, ErrNotFound) {
		t.Errorf("bob sees the classification of alice: %v", err)
	}
}

func TestLatestClassificationsOfRepository(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "test.db"), newKey(t))
	ctx := context.Background()
	alice, _ := s.UpsertUser(ctx, 1, "alice", "ghp_a")
	bob, _ := s.UpsertUser(ctx, 2, "bob", "ghp_b")
	add := func(user int64, repo string, number, score int) {
		t.Helper()
		c := Classification{Repo: repo, Number: number, Classification: review.Classification{Score: score, Reason: "r"}}
		if err := s.AddClassification(ctx, user, &c); err != nil {
			t.Fatal(err)
		}
	}
	add(alice, "acme/app", 9, 2)
	add(alice, "acme/app", 3, 8)
	add(alice, "acme/app", 9, 5)
	add(alice, "acme/other", 3, 1)
	add(bob, "acme/app", 4, 7)

	list, err := s.LatestClassifications(ctx, alice, "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Number != 3 || list[0].Score != 8 || list[1].Number != 9 || list[1].Score != 5 {
		t.Errorf("classifications %+v", list)
	}
	if list, _ := s.LatestClassifications(ctx, bob, "acme/other"); list == nil || len(list) != 0 {
		t.Errorf("bob sees %+v, want an empty list", list)
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

func TestStarredReposByUser(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "test.db"), newKey(t))
	ctx := context.Background()
	alice, _ := s.UpsertUser(ctx, 1, "alice", "ghp_a")
	bob, _ := s.UpsertUser(ctx, 2, "bob", "ghp_b")
	starred := func(user int64) []string {
		t.Helper()
		list, err := s.StarredRepos(ctx, user)
		if err != nil {
			t.Fatal(err)
		}
		return list
	}
	if list := starred(alice); list == nil || len(list) != 0 {
		t.Errorf("stars before the first star %#v, want an empty list", list)
	}
	for _, repo := range []string{"acme/zeta", "acme/alpha", "acme/zeta"} {
		if err := s.StarRepo(ctx, alice, repo); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.StarRepo(ctx, bob, "acme/bob"); err != nil {
		t.Fatal(err)
	}
	if list := starred(alice); !slices.Equal(list, []string{"acme/alpha", "acme/zeta"}) {
		t.Errorf("alice stars %v", list)
	}
	if list := starred(bob); !slices.Equal(list, []string{"acme/bob"}) {
		t.Errorf("bob stars %v", list)
	}
	for range 2 {
		if err := s.UnstarRepo(ctx, alice, "acme/zeta"); err != nil {
			t.Fatal(err)
		}
	}
	if list := starred(alice); !slices.Equal(list, []string{"acme/alpha"}) {
		t.Errorf("alice stars after the removal %v", list)
	}

	if _, err := s.db.Exec(`DELETE FROM users WHERE id = ?`, alice); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM starred_repos`).Scan(&rows); err != nil || rows != 1 {
		t.Errorf("%d star rows after the delete of alice, %v; want only the star of bob", rows, err)
	}
}

func TestStarLimit(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "test.db"), newKey(t))
	ctx := context.Background()
	alice, _ := s.UpsertUser(ctx, 1, "alice", "ghp_a")
	bob, _ := s.UpsertUser(ctx, 2, "bob", "ghp_b")
	for i := range MaxStars {
		if err := s.StarRepo(ctx, alice, fmt.Sprintf("acme/repo-%03d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.StarRepo(ctx, alice, "acme/one-more"); !errors.Is(err, ErrTooManyStars) {
		t.Errorf("star over the limit: %v", err)
	}
	if err := s.StarRepo(ctx, alice, "acme/repo-000"); err != nil {
		t.Errorf("star again at the limit: %v", err)
	}
	if err := s.StarRepo(ctx, bob, "acme/one-more"); err != nil {
		t.Errorf("the limit of alice stopped bob: %v", err)
	}
	if list, _ := s.StarredRepos(ctx, alice); len(list) != MaxStars {
		t.Errorf("alice has %d stars, want %d", len(list), MaxStars)
	}
}

func TestOpenAddsStarsToAnOlderDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	key := newKey(t)
	ctx := context.Background()
	s, err := Open(path, key)
	if err != nil {
		t.Fatal(err)
	}
	alice, _ := s.UpsertUser(ctx, 1, "alice", "ghp_a")
	if _, err := s.db.Exec(`DROP TABLE starred_repos`); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s = openStore(t, path, key)
	if err := s.StarRepo(ctx, alice, "acme/app"); err != nil {
		t.Fatal(err)
	}
	if list, err := s.StarredRepos(ctx, alice); err != nil || !slices.Equal(list, []string{"acme/app"}) {
		t.Errorf("stars %v, %v", list, err)
	}
	if u, err := s.User(ctx, alice); err != nil || u.GitHubToken != "ghp_a" {
		t.Errorf("the user of the older database = %+v, %v", u, err)
	}
}

func TestPrefsByUserAndRepo(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "test.db"), newKey(t))
	ctx := context.Background()
	alice, _ := s.UpsertUser(ctx, 1, "alice", "ghp_a")
	bob, _ := s.UpsertUser(ctx, 2, "bob", "ghp_b")
	prefs := func(user int64, repo string) Prefs {
		t.Helper()
		p, err := s.Prefs(ctx, user, repo)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	update := func(user int64, repo string, u PrefsUpdate) {
		t.Helper()
		if err := s.UpdatePrefs(ctx, user, repo, u); err != nil {
			t.Fatal(err)
		}
	}
	defaults := Prefs{AutoRefresh: true}
	if p := prefs(alice, "acme/app"); p != defaults {
		t.Errorf("prefs without a stored row %+v", p)
	}
	off, on := false, true
	update(alice, "acme/app", PrefsUpdate{AutoClassify: &on})
	if p := prefs(alice, "acme/app"); p != (Prefs{AutoRefresh: true, AutoClassify: true}) {
		t.Errorf("prefs after auto classify on %+v", p)
	}
	update(alice, "acme/app", PrefsUpdate{AutoRefresh: &off})
	if p := prefs(alice, "acme/app"); p != (Prefs{AutoClassify: true}) {
		t.Errorf("prefs after auto refresh off %+v", p)
	}
	if p := prefs(alice, "acme/web"); p != defaults {
		t.Errorf("prefs of another repository %+v", p)
	}
	if p := prefs(bob, "acme/app"); p != defaults {
		t.Errorf("bob prefs %+v", p)
	}
	update(bob, "acme/app", PrefsUpdate{AutoRefresh: &off})
	update(alice, "acme/app", PrefsUpdate{AutoRefresh: &on, AutoClassify: &off})
	update(alice, "acme/web", PrefsUpdate{})
	if p := prefs(alice, "acme/app"); p != defaults {
		t.Errorf("prefs after both changes %+v", p)
	}
	if p := prefs(bob, "acme/app"); p != (Prefs{}) {
		t.Errorf("bob prefs after the changes of alice %+v", p)
	}
	if p := prefs(alice, "acme/web"); p != defaults {
		t.Errorf("prefs after an empty update %+v", p)
	}

	if _, err := s.db.Exec(`DELETE FROM users WHERE id = ?`, alice); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM repo_prefs`).Scan(&rows); err != nil || rows != 1 {
		t.Errorf("%d preference rows after the delete of alice, %v; want only the row of bob", rows, err)
	}
}

func TestOpenAddsPreferencesToAnOlderDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	key := newKey(t)
	ctx := context.Background()
	s, err := Open(path, key)
	if err != nil {
		t.Fatal(err)
	}
	alice, _ := s.UpsertUser(ctx, 1, "alice", "ghp_a")
	if _, err := s.db.Exec(`DROP TABLE repo_prefs`); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s = openStore(t, path, key)
	if p, err := s.Prefs(ctx, alice, "acme/app"); err != nil || p != (Prefs{AutoRefresh: true}) {
		t.Errorf("prefs of the older database = %+v, %v", p, err)
	}
	off := false
	if err := s.UpdatePrefs(ctx, alice, "acme/app", PrefsUpdate{AutoRefresh: &off}); err != nil {
		t.Fatal(err)
	}
	if p, err := s.Prefs(ctx, alice, "acme/app"); err != nil || p != (Prefs{}) {
		t.Errorf("prefs after the change = %+v, %v", p, err)
	}
}

func TestGlobalRulesByUser(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "test.db"), newKey(t))
	ctx := context.Background()
	alice, _ := s.UpsertUser(ctx, 1, "alice", "ghp_a")
	bob, _ := s.UpsertUser(ctx, 2, "bob", "ghp_b")
	globalRules := func(user int64) string {
		t.Helper()
		content, err := s.GlobalRules(ctx, user)
		if err != nil {
			t.Fatal(err)
		}
		return content
	}
	set := func(user int64, content string) {
		t.Helper()
		if err := s.SetGlobalRules(ctx, user, content); err != nil {
			t.Fatal(err)
		}
	}
	if content := globalRules(alice); content != "" {
		t.Errorf("rules before a change %q", content)
	}
	set(alice, "version: 1\n")
	set(alice, "version: 1\nlanes: {}\n")
	set(bob, "version: 1\n")
	if content := globalRules(alice); content != "version: 1\nlanes: {}\n" {
		t.Errorf("alice rules %q", content)
	}
	set(bob, "")
	set(bob, "")
	if content := globalRules(bob); content != "" {
		t.Errorf("bob rules after the delete %q", content)
	}

	if _, err := s.db.Exec(`DELETE FROM users WHERE id = ?`, alice); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM global_rules`).Scan(&rows); err != nil || rows != 0 {
		t.Errorf("%d rule rows after the delete of alice, %v", rows, err)
	}
}

func TestOpenAddsGlobalRulesToAnOlderDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	key := newKey(t)
	ctx := context.Background()
	s, err := Open(path, key)
	if err != nil {
		t.Fatal(err)
	}
	alice, _ := s.UpsertUser(ctx, 1, "alice", "ghp_a")
	if _, err := s.db.Exec(`DROP TABLE global_rules`); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s = openStore(t, path, key)
	if err := s.SetGlobalRules(ctx, alice, "version: 1\n"); err != nil {
		t.Fatal(err)
	}
	if content, err := s.GlobalRules(ctx, alice); err != nil || content != "version: 1\n" {
		t.Errorf("rules of the older database %q, %v", content, err)
	}
}

func TestAutoClassifyRepos(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "test.db"), newKey(t))
	ctx := context.Background()
	alice, _ := s.UpsertUser(ctx, 1, "alice", "ghp_a")
	bob, _ := s.UpsertUser(ctx, 2, "bob", "ghp_b")
	repos := func(user int64) []string {
		t.Helper()
		list, err := s.AutoClassifyRepos(ctx, user)
		if err != nil {
			t.Fatal(err)
		}
		return list
	}
	if list := repos(alice); list == nil || len(list) != 0 {
		t.Errorf("repositories without settings %#v, want an empty list", list)
	}
	off, on := false, true
	for _, repo := range []string{"acme/zeta", "acme/alpha", "acme/off"} {
		if err := s.UpdatePrefs(ctx, alice, repo, PrefsUpdate{AutoClassify: &on}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.UpdatePrefs(ctx, alice, "acme/off", PrefsUpdate{AutoClassify: &off}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdatePrefs(ctx, alice, "acme/refresh", PrefsUpdate{AutoRefresh: &off}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdatePrefs(ctx, bob, "acme/bob", PrefsUpdate{AutoClassify: &on}); err != nil {
		t.Fatal(err)
	}
	if list := repos(alice); !slices.Equal(list, []string{"acme/alpha", "acme/zeta"}) {
		t.Errorf("alice repositories %v", list)
	}
	if list := repos(bob); !slices.Equal(list, []string{"acme/bob"}) {
		t.Errorf("bob repositories %v", list)
	}
}

func TestLatestReviewsOfRepository(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "test.db"), newKey(t))
	ctx := context.Background()
	alice, _ := s.UpsertUser(ctx, 1, "alice", "ghp_a")
	bob, _ := s.UpsertUser(ctx, 2, "bob", "ghp_b")
	add := func(user int64, repo string, number int, risk string) {
		t.Helper()
		r := Review{Repo: repo, Number: number, HeadSHA: "sha-" + risk, Review: review.Review{Risk: risk, Summary: "s", Findings: []review.Finding{}}}
		if err := s.AddReview(ctx, user, &r); err != nil {
			t.Fatal(err)
		}
	}
	add(alice, "acme/app", 9, "green")
	add(alice, "acme/app", 3, "red")
	add(alice, "acme/app", 9, "yellow")
	add(alice, "acme/other", 3, "green")
	add(bob, "acme/app", 4, "red")

	list, err := s.LatestReviews(ctx, alice, "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Number != 3 || list[0].Risk != "red" || list[1].Number != 9 || list[1].Risk != "yellow" || list[1].HeadSHA != "sha-yellow" {
		t.Errorf("reviews %+v", list)
	}
	if list, _ := s.LatestReviews(ctx, bob, "acme/other"); list == nil || len(list) != 0 {
		t.Errorf("bob sees %+v, want an empty list", list)
	}
}

func TestAutoReviewPrefs(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "test.db"), newKey(t))
	ctx := context.Background()
	alice, _ := s.UpsertUser(ctx, 1, "alice", "ghp_a")
	on, off := true, false
	if err := s.UpdatePrefs(ctx, alice, "acme/app", PrefsUpdate{AutoReview: &on}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdatePrefs(ctx, alice, "acme/web", PrefsUpdate{AutoClassify: &on}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdatePrefs(ctx, alice, "acme/web", PrefsUpdate{AutoReview: &on}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdatePrefs(ctx, alice, "acme/web", PrefsUpdate{AutoReview: &off}); err != nil {
		t.Fatal(err)
	}
	if p, err := s.Prefs(ctx, alice, "acme/app"); err != nil || p != (Prefs{AutoRefresh: true, AutoReview: true}) {
		t.Errorf("acme/app prefs %+v, %v", p, err)
	}
	if p, err := s.Prefs(ctx, alice, "acme/web"); err != nil || p != (Prefs{AutoRefresh: true, AutoClassify: true}) {
		t.Errorf("acme/web prefs %+v, %v", p, err)
	}
	if list, err := s.AutoReviewRepos(ctx, alice); err != nil || !slices.Equal(list, []string{"acme/app"}) {
		t.Errorf("auto review repositories %v, %v", list, err)
	}
	if list, err := s.AutoClassifyRepos(ctx, alice); err != nil || !slices.Equal(list, []string{"acme/web"}) {
		t.Errorf("auto classify repositories %v, %v", list, err)
	}
}

func TestOpenAddsAutoReviewToOlderPrefs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	key := newKey(t)
	ctx := context.Background()
	s, err := Open(path, key)
	if err != nil {
		t.Fatal(err)
	}
	alice, _ := s.UpsertUser(ctx, 1, "alice", "ghp_a")
	if _, err := s.db.Exec(`DROP TABLE repo_prefs;
		CREATE TABLE repo_prefs (
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			repo TEXT NOT NULL,
			auto_refresh INTEGER NOT NULL,
			auto_classify INTEGER NOT NULL,
			PRIMARY KEY (user_id, repo)
		);
		INSERT INTO repo_prefs VALUES (1, 'acme/app', 0, 1)`); err != nil {
		t.Fatal(err)
	}
	s.Close()

	for range 2 {
		s = openStore(t, path, key)
		if p, err := s.Prefs(ctx, alice, "acme/app"); err != nil || p != (Prefs{AutoClassify: true}) {
			t.Errorf("prefs of the older table %+v, %v", p, err)
		}
		s.Close()
	}
	s = openStore(t, path, key)
	on := true
	if err := s.UpdatePrefs(ctx, alice, "acme/app", PrefsUpdate{AutoReview: &on}); err != nil {
		t.Fatal(err)
	}
	if p, err := s.Prefs(ctx, alice, "acme/app"); err != nil || p != (Prefs{AutoClassify: true, AutoReview: true}) {
		t.Errorf("prefs after auto review on %+v, %v", p, err)
	}
}
