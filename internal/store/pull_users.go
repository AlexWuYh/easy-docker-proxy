package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// pullAuthCacheTTL skips bcrypt on repeated docker-login for the same user+password.
// Mutations (password / enabled / delete) wipe the cache.
const pullAuthCacheTTL = 30 * time.Second

type pullAuthCacheEntry struct {
	passSHA   string
	expiresAt time.Time
}

// PullUser is a docker-login account for the registry data plane (not web console).
type PullUser struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	Enabled   bool   `json:"enabled"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

var (
	ErrPullUserExists   = errors.New("pull username already exists")
	ErrPullUserNotFound = errors.New("pull user not found")
	ErrPullAuthInvalid  = errors.New("invalid pull credentials")
)

// CountPullUsers returns the number of pull accounts.
func (s *Store) CountPullUsers(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pull_users`).Scan(&n)
	return n, err
}

// BootstrapPullUser creates the first pull account when the table is empty.
func (s *Store) BootstrapPullUser(ctx context.Context, username, password string) (created bool, err error) {
	n, err := s.CountPullUsers(ctx)
	if err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil
	}
	username = strings.TrimSpace(username)
	if username == "" {
		username = "puller"
	}
	if err := validatePassword(password); err != nil {
		return false, err
	}
	_, err = s.CreatePullUser(ctx, username, password, true)
	if err != nil {
		return false, err
	}
	return true, nil
}

// CreatePullUser inserts a pull account with bcrypt password.
func (s *Store) CreatePullUser(ctx context.Context, username, password string, enabled bool) (*PullUser, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, fmt.Errorf("username required")
	}
	if err := validatePassword(password); err != nil {
		return nil, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Unix()
	en := 0
	if enabled {
		en = 1
	}
	res, err := s.db.ExecContext(ctx, `
INSERT INTO pull_users (username, password_hash, enabled, created_at, updated_at)
VALUES (?, ?, ?, ?, ?)`, username, string(hash), en, now, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, ErrPullUserExists
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &PullUser{ID: id, Username: username, Enabled: enabled, CreatedAt: now, UpdatedAt: now}, nil
}

func pullPasswordSHA(password string) string {
	sum := sha256.Sum256([]byte(password))
	return hex.EncodeToString(sum[:])
}

func (s *Store) pullAuthCacheGet(username, password string) bool {
	if s == nil {
		return false
	}
	s.pullAuthMu.Lock()
	defer s.pullAuthMu.Unlock()
	e, ok := s.pullAuthCache[username]
	if !ok || !time.Now().Before(e.expiresAt) {
		return false
	}
	return e.passSHA == pullPasswordSHA(password)
}

func (s *Store) pullAuthCachePut(username, password string) {
	if s == nil {
		return
	}
	s.pullAuthMu.Lock()
	if s.pullAuthCache == nil {
		s.pullAuthCache = make(map[string]pullAuthCacheEntry)
	}
	s.pullAuthCache[username] = pullAuthCacheEntry{
		passSHA:   pullPasswordSHA(password),
		expiresAt: time.Now().Add(pullAuthCacheTTL),
	}
	s.pullAuthMu.Unlock()
}

func (s *Store) invalidatePullAuthCache() {
	if s == nil {
		return
	}
	s.pullAuthMu.Lock()
	s.pullAuthCache = make(map[string]pullAuthCacheEntry)
	s.pullAuthMu.Unlock()
}

// AuthenticatePull verifies Basic credentials for the data plane.
// Returns username on success.
func (s *Store) AuthenticatePull(ctx context.Context, username, password string) (string, error) {
	username = strings.TrimSpace(username)
	if username != "" && s.pullAuthCacheGet(username, password) {
		return username, nil
	}
	var hash string
	var enabled int
	err := s.db.QueryRowContext(ctx, `
SELECT password_hash, enabled FROM pull_users WHERE username = ?`, username).Scan(&hash, &enabled)
	if err == sql.ErrNoRows {
		return "", ErrPullAuthInvalid
	}
	if err != nil {
		return "", err
	}
	if enabled == 0 {
		return "", ErrPullAuthInvalid
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return "", ErrPullAuthInvalid
	}
	s.pullAuthCachePut(username, password)
	return username, nil
}

// ListPullUsers returns all pull accounts (no password hashes).
func (s *Store) ListPullUsers(ctx context.Context) ([]PullUser, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, username, enabled, created_at, updated_at FROM pull_users ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PullUser
	for rows.Next() {
		var u PullUser
		var en int
		if err := rows.Scan(&u.ID, &u.Username, &en, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, err
		}
		u.Enabled = en != 0
		out = append(out, u)
	}
	if out == nil {
		out = []PullUser{}
	}
	return out, rows.Err()
}

// SetPullUserPassword updates password for a pull account.
func (s *Store) SetPullUserPassword(ctx context.Context, id int64, password string) error {
	if err := validatePassword(password); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Unix()
	res, err := s.db.ExecContext(ctx, `
UPDATE pull_users SET password_hash = ?, updated_at = ? WHERE id = ?`, string(hash), now, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrPullUserNotFound
	}
	s.invalidatePullAuthCache()
	return nil
}

// SetPullUserEnabled enables or disables a pull account.
func (s *Store) SetPullUserEnabled(ctx context.Context, id int64, enabled bool) error {
	en := 0
	if enabled {
		en = 1
	}
	now := time.Now().UTC().Unix()
	res, err := s.db.ExecContext(ctx, `
UPDATE pull_users SET enabled = ?, updated_at = ? WHERE id = ?`, en, now, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrPullUserNotFound
	}
	s.invalidatePullAuthCache()
	return nil
}

// DeletePullUser removes a pull account.
func (s *Store) DeletePullUser(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM pull_users WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrPullUserNotFound
	}
	s.invalidatePullAuthCache()
	return nil
}
