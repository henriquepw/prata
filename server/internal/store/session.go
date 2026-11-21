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

// Session is who a web session acts as, the user's Preferences, the account's Moeda and when it expires.
type Session struct {
	UserID    string
	Email     string
	AccountID string
	Preferences
	Currency string
	// MonthlyIncome is the account's Receita mensal in cents, 0 when not defined.
	MonthlyIncome int64
	ExpiresAt     time.Time
}

// Preferences are a user's Idioma, Formato de data and Formato numérico, spelled as the schema stores them
// ("pt-BR", "DD/MM/YYYY", "1.234,56").
type Preferences struct {
	Language     string `db:"language"`
	DateFormat   string `db:"date_format"`
	NumberFormat string `db:"number_format"`
}

// Authenticate returns the id of the user with this email and password.
func (s *Store) Authenticate(ctx context.Context, email, password string) (string, error) {
	query := "SELECT id, secret FROM users WHERE email = ? AND deleted_at IS NULL"

	var user struct {
		ID     string `db:"id"`
		Secret string `db:"secret"`
	}
	err := s.GetContext(ctx, &user, query, email)
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
	query := `
		INSERT INTO sessions (id, user_id, token_hash, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?)
	`

	token := newToken()
	_, err := s.ExecContext(ctx, query, ulid.Make().String(), userID, hashToken(token), formatTime(now.Add(SessionTTL)), formatTime(now))
	if err != nil {
		return "", fmt.Errorf("creating session: %w", err)
	}

	return token, nil
}

// SessionByToken returns the live session of the token, renewing it to now + 30 days when 24h or less remain.
// An expired session is deleted and rejected.
func (s *Store) SessionByToken(ctx context.Context, token string, now time.Time) (Session, error) {
	query := `
		SELECT s.id, s.user_id, u.email, u.language, u.date_format, u.number_format, a.account_id, c.currency,
			c.monthly_income, s.expires_at
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		JOIN account_users a ON a.user_id = s.user_id
		JOIN accounts c ON c.id = a.account_id
		WHERE s.token_hash = ?
		ORDER BY a.created_at
		LIMIT 1
	`

	var row struct {
		ID     string `db:"id"`
		UserID string `db:"user_id"`
		Email  string `db:"email"`
		Preferences
		AccountID     string `db:"account_id"`
		Currency      string `db:"currency"`
		MonthlyIncome int64  `db:"monthly_income"`
		ExpiresAt     string `db:"expires_at"`
	}
	err := s.GetContext(ctx, &row, query, hashToken(token))
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
		query = "DELETE FROM sessions WHERE id = ?"
		if _, err := s.ExecContext(ctx, query, row.ID); err != nil {
			return Session{}, fmt.Errorf("deleting expired session: %w", err)
		}

		return Session{}, ErrInvalidSession
	}

	if expiresAt.Sub(now) <= sessionRenewWithin {
		expiresAt = now.Add(SessionTTL)
		query = "UPDATE sessions SET expires_at = ? WHERE id = ?"
		if _, err := s.ExecContext(ctx, query, formatTime(expiresAt), row.ID); err != nil {
			return Session{}, fmt.Errorf("renewing session: %w", err)
		}
	}

	return Session{
		UserID:      row.UserID,
		Email:       row.Email,
		AccountID:   row.AccountID,
		Preferences: row.Preferences,
		Currency:    row.Currency,

		MonthlyIncome: row.MonthlyIncome,
		ExpiresAt:     expiresAt.UTC(),
	}, nil
}

// SaveSettings stores the user's Preferences and the account's Moeda, without converting any amount, in one
// transaction; the schema refuses values outside their options and then nothing is stored.
func (s *Store) SaveSettings(ctx context.Context, userID, accountID string, preferences Preferences, currency string) error {
	tx, err := s.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("starting settings: %w", err)
	}
	defer tx.Rollback()

	now := formatTime(time.Now())
	query := "UPDATE users SET language = ?, date_format = ?, number_format = ?, updated_at = ? WHERE id = ?"

	if _, err := tx.ExecContext(ctx, query, preferences.Language, preferences.DateFormat, preferences.NumberFormat, now, userID); err != nil {
		return fmt.Errorf("saving preferences: %w", err)
	}

	query = "UPDATE accounts SET currency = ?, updated_at = ? WHERE id = ?"

	if _, err := tx.ExecContext(ctx, query, currency, now, accountID); err != nil {
		return fmt.Errorf("saving currency: %w", err)
	}

	return tx.Commit()
}

// DeleteSession deletes only the session of this token.
func (s *Store) DeleteSession(ctx context.Context, token string) error {
	query := "DELETE FROM sessions WHERE token_hash = ?"

	_, err := s.ExecContext(ctx, query, hashToken(token))

	return err
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}
