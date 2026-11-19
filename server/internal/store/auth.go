package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
	"golang.org/x/crypto/argon2"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

var (
	// ErrSignupClosed means the installation already has a user and signup is not allowed.
	ErrSignupClosed = errors.New("signup closed")
	// ErrDuplicateID means an account, user, email or device already exists.
	ErrDuplicateID = errors.New("duplicate id")
	// ErrInvalidAPIKey means the API key is unknown or revoked.
	ErrInvalidAPIKey = errors.New("invalid api key")
)

// Signup creates an account from a device, adopting the device's local ids.
type Signup struct {
	Email      string
	Password   string
	DeviceName string
	AccountID  string
	OwnerID    string
	DeviceID   string
}

// Actor is who an API key acts as.
type Actor struct {
	AccountID string `db:"account_id"`
	UserID    string `db:"user_id"`
	DeviceID  string `db:"id"`
}

// CreateAccount creates the user, account, owner link, client device and hidden web device
// in one transaction and returns the client device's API key.
func (s *Store) CreateAccount(ctx context.Context, signup Signup, isSignupAllowed bool) (string, error) {
	tx, err := s.BeginTxx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("starting signup: %w", err)
	}
	defer tx.Rollback()

	var hasUsers bool
	if err := tx.GetContext(ctx, &hasUsers, "SELECT EXISTS (SELECT 1 FROM users)"); err != nil {
		return "", fmt.Errorf("counting users: %w", err)
	}

	if hasUsers && !isSignupAllowed {
		return "", ErrSignupClosed
	}

	key := newAPIKey()
	now := time.Now().UTC().Format(time.RFC3339)
	statements := []struct {
		query string
		args  []any
	}{
		{"INSERT INTO users (id, email, secret, created_at, updated_at) VALUES (?, ?, ?, ?, ?)",
			[]any{signup.OwnerID, signup.Email, hashPassword(signup.Password), now, now}},
		{"INSERT INTO accounts (id, name, created_by, created_at, updated_at) VALUES (?, ?, ?, ?, ?)",
			[]any{signup.AccountID, signup.Email, signup.OwnerID, now, now}},
		{"INSERT INTO account_users (id, account_id, user_id, role, created_at) VALUES (?, ?, ?, 'OWNER', ?)",
			[]any{ulid.Make().String(), signup.AccountID, signup.OwnerID, now}},
		{"INSERT INTO devices (id, user_id, account_id, name, api_key_hash, created_at) VALUES (?, ?, ?, ?, ?, ?)",
			[]any{signup.DeviceID, signup.OwnerID, signup.AccountID, signup.DeviceName, hashAPIKey(key), now}},
		{"INSERT INTO devices (id, user_id, account_id, name, created_at) VALUES (?, ?, ?, 'web', ?)",
			[]any{ulid.Make().String(), signup.OwnerID, signup.AccountID, now}},
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement.query, statement.args...); err != nil {
			if isConstraint(err) {
				return "", ErrDuplicateID
			}

			return "", fmt.Errorf("creating account: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("committing signup: %w", err)
	}

	return key, nil
}

// ActorByAPIKey returns who a live API key acts as.
func (s *Store) ActorByAPIKey(ctx context.Context, key string) (Actor, error) {
	var actor Actor
	err := s.GetContext(ctx, &actor, "SELECT id, user_id, account_id FROM devices WHERE api_key_hash = ?", hashAPIKey(key))
	if errors.Is(err, sql.ErrNoRows) {
		return Actor{}, ErrInvalidAPIKey
	}

	return actor, err
}

// RevokeAPIKey deletes the device's API key hash.
func (s *Store) RevokeAPIKey(ctx context.Context, deviceID string) error {
	_, err := s.ExecContext(ctx, "UPDATE devices SET api_key_hash = NULL WHERE id = ?", deviceID)

	return err
}

// newAPIKey returns 256 random bits; crypto/rand.Read never fails.
func newAPIKey() string {
	raw := make([]byte, 32)
	rand.Read(raw)

	return base64.RawURLEncoding.EncodeToString(raw)
}

// hashAPIKey uses SHA-256: the key already has 256 bits of entropy, so a slow hash adds nothing.
func hashAPIKey(key string) string {
	sum := sha256.Sum256([]byte(key))

	return hex.EncodeToString(sum[:])
}

// hashPassword returns an argon2id hash in PHC string format.
func hashPassword(password string) string {
	const memory, iterations, threads = 64 * 1024, 1, 4
	salt := make([]byte, 16)
	rand.Read(salt)
	hash := argon2.IDKey([]byte(password), salt, iterations, memory, threads, 32)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, memory, iterations, threads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash))
}

func isConstraint(err error) bool {
	var sqliteErr *sqlite.Error

	return errors.As(err, &sqliteErr) && sqliteErr.Code()&0xff == sqlite3.SQLITE_CONSTRAINT
}
