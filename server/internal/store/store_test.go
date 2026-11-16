package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
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
	first, second := s.seal(githubToken), s.seal(githubToken)
	if bytes.Equal(first, second) {
		t.Error("two seals of the same text are equal; the nonce is not random")
	}
	if bytes.Contains(first, []byte(githubToken)) {
		t.Error("sealed bytes contain the plaintext")
	}
	got, err := s.open(first)
	if err != nil || got != githubToken {
		t.Fatalf("open = %q, %v", got, err)
	}
	if _, err := s.open(first[:5]); err == nil {
		t.Error("open of a short ciphertext succeeded")
	}
}

func TestWrongKeyCannotDecrypt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	ctx := context.Background()

	s, err := Open(path, newKey(t))
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.UpsertUser(ctx, "alwx", githubToken)
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
	id, err := s.UpsertUser(ctx, "alwx", "ghp_old")
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.UpsertUser(ctx, "ALWX", githubToken)
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

func TestUserIDsWithRepo(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "test.db"), newKey(t))
	ctx := context.Background()
	alice, _ := s.UpsertUser(ctx, "alice", "ghp_a")
	s.UpsertUser(ctx, "bob", "ghp_b")
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
	alice, _ := s.UpsertUser(ctx, "alice", "ghp_a")
	bob, _ := s.UpsertUser(ctx, "bob", "ghp_b")

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

func TestRawRowsHoldNoPlaintext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s := openStore(t, path, newKey(t))
	ctx := context.Background()
	id, err := s.UpsertUser(ctx, "alwx", githubToken)
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
