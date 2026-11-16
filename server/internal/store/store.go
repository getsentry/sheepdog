package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"

	_ "modernc.org/sqlite"

	"reviewqueue/internal/review"
)

var ErrNotFound = errors.New("store: not found")

// SessionIdleTimeout is how long a session stays valid without a request.
const SessionIdleTimeout = 30 * 24 * time.Hour

const schema = `
CREATE TABLE IF NOT EXISTS users (
	id INTEGER PRIMARY KEY,
	github_id INTEGER NOT NULL UNIQUE,
	login TEXT NOT NULL,
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
	// CREATE TABLE IF NOT EXISTS keeps a users table from before the github_id column, and then every sign-in fails.
	var hasGitHubID bool
	if err := db.QueryRow(`SELECT COUNT(*) > 0 FROM pragma_table_info('users') WHERE name = 'github_id'`).Scan(&hasGitHubID); err != nil {
		db.Close()
		return nil, err
	}
	if !hasGitHubID {
		db.Close()
		return nil, fmt.Errorf("the database %s is from an older version without users.github_id: delete it and start again", path)
	}
	return &Store{db: db, sealer: s}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

const (
	githubTokenColumn   = "github_token_enc"
	openRouterKeyColumn = "openrouter_key_enc"
)

// UpsertUser finds the user by the GitHub user ID, because another person can take a login after a rename.
// It creates the user or replaces the login and the GitHub token, and returns the user ID.
func (s *Store) UpsertUser(ctx context.Context, githubID int64, login, githubToken string) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	// The sealed token needs the user ID, so a new user gets an empty token first.
	var id int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO users (github_id, login, github_token_enc, created_at) VALUES (?, ?, x'', ?)
		ON CONFLICT (github_id) DO UPDATE SET login = excluded.login
		RETURNING id`,
		githubID, login, now(),
	).Scan(&id)
	if err != nil {
		return 0, err
	}
	sealed := s.sealer.seal(githubToken, id, githubTokenColumn)
	if _, err := tx.ExecContext(ctx, `UPDATE users SET github_token_enc = ? WHERE id = ?`, sealed, id); err != nil {
		return 0, err
	}
	return id, tx.Commit()
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
	if u.GitHubToken, err = s.sealer.open(githubEnc, u.ID, githubTokenColumn); err != nil {
		return User{}, err
	}
	if openRouterEnc != nil {
		if u.OpenRouterKey, err = s.sealer.open(openRouterEnc, u.ID, openRouterKeyColumn); err != nil {
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
			sealed = s.sealer.seal(*settings.OpenRouterKey, userID, openRouterKeyColumn)
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

// SessionUserID returns the user of a session token and marks the session as used. It deletes an expired session.
func (s *Store) SessionUserID(ctx context.Context, token string) (int64, error) {
	var userID int64
	err := s.db.QueryRowContext(ctx,
		`UPDATE sessions SET last_used_at = ? WHERE token_hash = ? AND last_used_at > ? RETURNING user_id`,
		now(), hashToken(token), expiredBefore(),
	).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		if err := s.DeleteSession(ctx, token); err != nil {
			return 0, err
		}
		return 0, ErrNotFound
	}
	return userID, err
}

func (s *Store) DeleteExpiredSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE last_used_at <= ?`, expiredBefore())
	return err
}

func (s *Store) DeleteUserSessions(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

func expiredBefore() int64 {
	return now() - SessionIdleTimeout.Milliseconds()
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, hashToken(token))
	return err
}

// Review is a finished AI review of one pull request.
type Review struct {
	Repo    string `json:"repo"`
	Number  int    `json:"number"`
	HeadSHA string `json:"headSha"`
	Model   string `json:"model"`
	review.Review
	DiffTruncated bool      `json:"diffTruncated"`
	PRUpdatedAt   time.Time `json:"prUpdatedAt"`
	CreatedAt     time.Time `json:"createdAt"`
}

// AddReview stores a review and sets its CreatedAt.
func (s *Store) AddReview(ctx context.Context, userID int64, r *Review) error {
	findings, err := json.Marshal(r.Findings)
	if err != nil {
		return err
	}
	createdAt := now()
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO reviews (user_id, repo, number, head_sha, model, risk, summary, findings_json, diff_truncated, pr_updated_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		userID, r.Repo, r.Number, r.HeadSHA, r.Model, r.Risk, r.Summary, string(findings), r.DiffTruncated,
		r.PRUpdatedAt.UTC().Format(time.RFC3339), createdAt)
	if err != nil {
		return err
	}
	r.CreatedAt = time.UnixMilli(createdAt)
	return nil
}

func (s *Store) LatestReview(ctx context.Context, userID int64, repo string, number int) (Review, error) {
	var (
		r                     Review
		findings, prUpdatedAt string
		createdAt             int64
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT repo, number, head_sha, model, risk, summary, findings_json, diff_truncated, pr_updated_at, created_at
		FROM reviews WHERE user_id = ? AND repo = ? AND number = ?
		ORDER BY created_at DESC, rowid DESC LIMIT 1`, userID, repo, number,
	).Scan(&r.Repo, &r.Number, &r.HeadSHA, &r.Model, &r.Risk, &r.Summary, &findings, &r.DiffTruncated, &prUpdatedAt, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Review{}, ErrNotFound
	}
	if err != nil {
		return Review{}, err
	}
	if err := json.Unmarshal([]byte(findings), &r.Findings); err != nil {
		return Review{}, err
	}
	if r.PRUpdatedAt, err = time.Parse(time.RFC3339, prUpdatedAt); err != nil {
		return Review{}, err
	}
	r.CreatedAt = time.UnixMilli(createdAt)
	return r, nil
}

func now() int64 {
	return time.Now().UnixMilli()
}
