package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
)

const (
	// SessionTTL is how long a web session lives after creation or renewal.
	SessionTTL         = 30 * 24 * time.Hour
	sessionRenewWithin = 24 * time.Hour
)

// ErrInvalidSession means the session token is unknown, expired or deleted.
var ErrInvalidSession = errors.New("invalid session")

// Session is who a web session acts as and when it expires.
type Session struct {
	UserID    string
	Email     string
	AccountID string
	ExpiresAt time.Time
}

// Authenticate returns the id of the user with this email and password.
func (s *Store) Authenticate(ctx context.Context, email, password string) (string, error) {
	var user struct {
		ID     string `db:"id"`
		Secret string `db:"secret"`
	}
	err := s.GetContext(ctx, &user, "SELECT id, secret FROM users WHERE email = ? AND deleted_at IS NULL", email)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrInvalidCredentials
	}

	if err != nil {
		return "", fmt.Errorf("finding user: %w", err)
	}

	if !verifyPassword(password, user.Secret) {
		return "", ErrInvalidCredentials
	}

	return user.ID, nil
}

// CreateSession stores a new session for the user, valid for 30 days from now, and returns its token.
func (s *Store) CreateSession(ctx context.Context, userID string, now time.Time) (string, error) {
	token := newToken()
	_, err := s.ExecContext(ctx, "INSERT INTO sessions (id, user_id, token_hash, expires_at, created_at) VALUES (?, ?, ?, ?, ?)",
		ulid.Make().String(), userID, hashToken(token), formatTime(now.Add(SessionTTL)), formatTime(now))
	if err != nil {
		return "", fmt.Errorf("creating session: %w", err)
	}

	return token, nil
}

// SessionByToken returns the live session of the token, renewing it to now + 30 days when 24h or less remain.
// An expired session is deleted and rejected.
func (s *Store) SessionByToken(ctx context.Context, token string, now time.Time) (Session, error) {
	var row struct {
		ID        string `db:"id"`
		UserID    string `db:"user_id"`
		Email     string `db:"email"`
		AccountID string `db:"account_id"`
		ExpiresAt string `db:"expires_at"`
	}
	err := s.GetContext(ctx, &row, `SELECT s.id, s.user_id, u.email, a.account_id, s.expires_at
		FROM sessions s JOIN users u ON u.id = s.user_id JOIN account_users a ON a.user_id = s.user_id
		WHERE s.token_hash = ? ORDER BY a.created_at LIMIT 1`, hashToken(token))
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrInvalidSession
	}

	if err != nil {
		return Session{}, fmt.Errorf("finding session: %w", err)
	}

	expiresAt, err := time.Parse(time.RFC3339, row.ExpiresAt)
	if err != nil {
		return Session{}, fmt.Errorf("parsing session expiry: %w", err)
	}

	if !now.Before(expiresAt) {
		if _, err := s.ExecContext(ctx, "DELETE FROM sessions WHERE id = ?", row.ID); err != nil {
			return Session{}, fmt.Errorf("deleting expired session: %w", err)
		}

		return Session{}, ErrInvalidSession
	}

	if expiresAt.Sub(now) <= sessionRenewWithin {
		expiresAt = now.Add(SessionTTL)
		if _, err := s.ExecContext(ctx, "UPDATE sessions SET expires_at = ? WHERE id = ?", formatTime(expiresAt), row.ID); err != nil {
			return Session{}, fmt.Errorf("renewing session: %w", err)
		}
	}

	return Session{UserID: row.UserID, Email: row.Email, AccountID: row.AccountID, ExpiresAt: expiresAt.UTC()}, nil
}

// DeleteSession deletes only the session of this token.
func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash = ?", hashToken(token))

	return err
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}
