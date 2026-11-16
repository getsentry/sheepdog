package store

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"time"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("store: not found")

const schema = `
CREATE TABLE IF NOT EXISTS users (
	id INTEGER PRIMARY KEY,
	login TEXT NOT NULL UNIQUE COLLATE NOCASE,
	github_token_enc BLOB NOT NULL,
	openrouter_key_enc BLOB,
	model TEXT NOT NULL DEFAULT '',
	repo TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
	token_hash BLOB PRIMARY KEY,
	user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	created_at INTEGER NOT NULL,
	last_used_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS reviews (
	user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	repo TEXT NOT NULL,
	number INTEGER NOT NULL,
	head_sha TEXT NOT NULL,
	model TEXT NOT NULL,
	risk TEXT NOT NULL,
	summary TEXT NOT NULL,
	findings_json TEXT NOT NULL,
	diff_truncated INTEGER NOT NULL,
	pr_updated_at TEXT NOT NULL,
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS reviews_by_pr ON reviews(user_id, repo, number, created_at);
`

type Store struct {
	db     *sql.DB
	sealer sealer
}

type User struct {
	ID            int64
	Login         string
	GitHubToken   string
	OpenRouterKey string
	Model         string
	Repo          string
	CreatedAt     time.Time
}

// Settings holds a partial update. A nil field stays unchanged, and an empty OpenRouterKey deletes the key.
type Settings struct {
	Repo          *string
	Model         *string
	OpenRouterKey *string
}

func Open(path string, key []byte) (*Store, error) {
	s, err := newSealer(key)
	if err != nil {
		return nil, err
	}
	query := url.Values{"_pragma": {"journal_mode(WAL)", "busy_timeout(5000)", "foreign_keys(1)"}}
	db, err := sql.Open("sqlite", "file:"+path+"?"+query.Encode())
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, sealer: s}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

// UpsertUser creates the user or replaces the GitHub token of an existing user, and returns the user ID.
func (s *Store) UpsertUser(ctx context.Context, login, githubToken string) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO users (login, github_token_enc, created_at) VALUES (?, ?, ?)
		ON CONFLICT (login) DO UPDATE SET github_token_enc = excluded.github_token_enc
		RETURNING id`,
		login, s.sealer.seal(githubToken), now(),
	).Scan(&id)
	return id, err
}

func (s *Store) User(ctx context.Context, id int64) (User, error) {
	var (
		u                        User
		githubEnc, openRouterEnc []byte
		createdAt                int64
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, login, github_token_enc, openrouter_key_enc, model, repo, created_at
		FROM users WHERE id = ?`, id,
	).Scan(&u.ID, &u.Login, &githubEnc, &openRouterEnc, &u.Model, &u.Repo, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	if u.GitHubToken, err = s.sealer.open(githubEnc); err != nil {
		return User{}, err
	}
	if openRouterEnc != nil {
		if u.OpenRouterKey, err = s.sealer.open(openRouterEnc); err != nil {
			return User{}, err
		}
	}
	u.CreatedAt = time.UnixMilli(createdAt)
	return u, nil
}

func (s *Store) UpdateSettings(ctx context.Context, userID int64, settings Settings) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if settings.Repo != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE users SET repo = ? WHERE id = ?`, *settings.Repo, userID); err != nil {
			return err
		}
	}
	if settings.Model != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE users SET model = ? WHERE id = ?`, *settings.Model, userID); err != nil {
			return err
		}
	}
	if settings.OpenRouterKey != nil {
		var sealed []byte
		if *settings.OpenRouterKey != "" {
			sealed = s.sealer.seal(*settings.OpenRouterKey)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET openrouter_key_enc = ? WHERE id = ?`, sealed, userID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UserIDsWithRepo returns the users that have a repository selected.
func (s *Store) UserIDsWithRepo(ctx context.Context) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM users WHERE repo != '' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// CreateSession returns a new session token. The database keeps only its SHA-256 hash.
func (s *Store) CreateSession(ctx context.Context, userID int64) (string, error) {
	token := newSessionToken()
	t := now()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (token_hash, user_id, created_at, last_used_at) VALUES (?, ?, ?, ?)`,
		hashToken(token), userID, t, t)
	if err != nil {
		return "", err
	}
	return token, nil
}

// SessionUserID returns the user of a session token and marks the session as used.
func (s *Store) SessionUserID(ctx context.Context, token string) (int64, error) {
	var userID int64
	err := s.db.QueryRowContext(ctx,
		`UPDATE sessions SET last_used_at = ? WHERE token_hash = ? RETURNING user_id`,
		now(), hashToken(token),
	).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return userID, err
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, hashToken(token))
	return err
}

func now() int64 {
	return time.Now().UnixMilli()
}
