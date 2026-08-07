package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Roles for web console users.
const (
	RoleAdmin  = "admin"
	RoleViewer = "viewer"
)

// User is a web console account (no password hash in JSON).
type User struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	Role      string `json:"role"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

// SessionInfo is returned after successful auth.
type SessionInfo struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
	User      User   `json:"user"`
}

var (
	ErrUserExists     = errors.New("username already exists")
	ErrUserNotFound   = errors.New("user not found")
	ErrInvalidCreds   = errors.New("invalid username or password")
	ErrInvalidSession = errors.New("invalid or expired session")
	ErrLastAdmin      = errors.New("cannot remove the last admin")
	ErrWeakPassword   = errors.New("password must be at least 8 characters")
)

// CountUsers returns number of web users.
func (s *Store) CountUsers(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM web_users`).Scan(&n)
	return n, err
}

// BootstrapAdmin creates the first admin if no users exist.
func (s *Store) BootstrapAdmin(ctx context.Context, username, password string) (created bool, err error) {
	n, err := s.CountUsers(ctx)
	if err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil
	}
	username = strings.TrimSpace(username)
	if username == "" {
		username = "admin"
	}
	if err := validatePassword(password); err != nil {
		return false, err
	}
	_, err = s.CreateUser(ctx, username, password, RoleAdmin)
	if err != nil {
		return false, err
	}
	return true, nil
}

func validatePassword(pw string) error {
	if len(pw) < 8 {
		return ErrWeakPassword
	}
	return nil
}

// CreateUser inserts a user with bcrypt password hash.
func (s *Store) CreateUser(ctx context.Context, username, password, role string) (*User, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, fmt.Errorf("username required")
	}
	if err := validatePassword(password); err != nil {
		return nil, err
	}
	if role != RoleAdmin && role != RoleViewer {
		role = RoleViewer
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Unix()
	res, err := s.db.ExecContext(ctx, `
INSERT INTO web_users (username, password_hash, role, created_at, updated_at)
VALUES (?, ?, ?, ?, ?)`, username, string(hash), role, now, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, ErrUserExists
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &User{ID: id, Username: username, Role: role, CreatedAt: now, UpdatedAt: now}, nil
}

// Authenticate checks username/password and returns a new session.
func (s *Store) Authenticate(ctx context.Context, username, password string) (*SessionInfo, error) {
	username = strings.TrimSpace(username)
	var u User
	var hash string
	err := s.db.QueryRowContext(ctx, `
SELECT id, username, password_hash, role, created_at, updated_at FROM web_users WHERE username = ?`, username).
		Scan(&u.ID, &u.Username, &hash, &u.Role, &u.CreatedAt, &u.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrInvalidCreds
	}
	if err != nil {
		return nil, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return nil, ErrInvalidCreds
	}
	return s.createSession(ctx, u)
}

func (s *Store) createSession(ctx context.Context, u User) (*SessionInfo, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(raw)
	now := time.Now().UTC()
	exp := now.Add(24 * time.Hour).Unix()
	// Store SHA-256 of token so DB leak doesn't equal live tokens (token still bearer secret).
	sum := sha256.Sum256([]byte(token))
	tokenKey := hex.EncodeToString(sum[:])
	_, err := s.db.ExecContext(ctx, `
INSERT INTO web_sessions (token, user_id, expires_at, created_at) VALUES (?, ?, ?, ?)`,
		tokenKey, u.ID, exp, now.Unix())
	if err != nil {
		return nil, err
	}
	return &SessionInfo{Token: token, ExpiresAt: exp, User: u}, nil
}

// SessionUser resolves a bearer session token to a user.
func (s *Store) SessionUser(ctx context.Context, token string) (*User, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, ErrInvalidSession
	}
	sum := sha256.Sum256([]byte(token))
	tokenKey := hex.EncodeToString(sum[:])
	now := time.Now().UTC().Unix()
	var u User
	err := s.db.QueryRowContext(ctx, `
SELECT u.id, u.username, u.role, u.created_at, u.updated_at
FROM web_sessions s
JOIN web_users u ON u.id = s.user_id
WHERE s.token = ? AND s.expires_at > ?`, tokenKey, now).
		Scan(&u.ID, &u.Username, &u.Role, &u.CreatedAt, &u.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrInvalidSession
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// RevokeSession deletes a session by raw token.
func (s *Store) RevokeSession(ctx context.Context, token string) error {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	tokenKey := hex.EncodeToString(sum[:])
	_, err := s.db.ExecContext(ctx, `DELETE FROM web_sessions WHERE token = ?`, tokenKey)
	return err
}

// ListUsers returns all web users.
func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, username, role, created_at, updated_at FROM web_users ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.Role, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	if out == nil {
		out = []User{}
	}
	return out, rows.Err()
}

// SetPassword updates a user's password.
func (s *Store) SetPassword(ctx context.Context, userID int64, password string) error {
	if err := validatePassword(password); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Unix()
	res, err := s.db.ExecContext(ctx, `
UPDATE web_users SET password_hash = ?, updated_at = ? WHERE id = ?`, string(hash), now, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrUserNotFound
	}
	// Invalidate sessions for this user.
	_, _ = s.db.ExecContext(ctx, `DELETE FROM web_sessions WHERE user_id = ?`, userID)
	return nil
}

// DeleteUser removes a user; refuses to delete the last admin.
func (s *Store) DeleteUser(ctx context.Context, userID int64) error {
	var role string
	err := s.db.QueryRowContext(ctx, `SELECT role FROM web_users WHERE id = ?`, userID).Scan(&role)
	if err == sql.ErrNoRows {
		return ErrUserNotFound
	}
	if err != nil {
		return err
	}
	if role == RoleAdmin {
		var admins int64
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM web_users WHERE role = ?`, RoleAdmin).Scan(&admins); err != nil {
			return err
		}
		if admins <= 1 {
			return ErrLastAdmin
		}
	}
	// Explicit session cleanup (also covered by FK ON DELETE CASCADE when enabled).
	_, _ = s.db.ExecContext(ctx, `DELETE FROM web_sessions WHERE user_id = ?`, userID)
	_, err = s.db.ExecContext(ctx, `DELETE FROM web_users WHERE id = ?`, userID)
	return err
}

// PurgeExpiredSessions removes expired session rows.
func (s *Store) PurgeExpiredSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM web_sessions WHERE expires_at < ?`, time.Now().UTC().Unix())
	return err
}
