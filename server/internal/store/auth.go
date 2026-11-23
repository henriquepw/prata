package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
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
	// ErrInvalidCredentials means the email is unknown or the password is wrong.
	ErrInvalidCredentials = errors.New("invalid credentials")
)

// Signup creates an account adopting the given ids; DeviceID, DeviceName are empty for a web signup. Language,
// the Idioma of the signup page, DateFormat, NumberFormat and Currency only come from the web; empty is the default.
type Signup struct {
	Email        string `json:"email"`
	Password     string `json:"password"`
	DeviceName   string `json:"device_name"`
	AccountID    string `json:"account_id"`
	OwnerID      string `json:"owner_id"`
	DeviceID     string `json:"device_id"`
	Language     string `json:"-"`
	DateFormat   string `json:"-"`
	NumberFormat string `json:"-"`
	Currency     string `json:"-"`
}

type Signin struct {
	Email      string `json:"email"`
	Password   string `json:"password"`
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device_name"`
}

// Actor is who an API key acts as.
type Actor struct {
	AccountID string `db:"account_id"`
	UserID    string `db:"user_id"`
	DeviceID  string `db:"id"`
}

// hasUsers is the check that tells an empty installation, the only one where signup is always open.
func hasUsers(ctx context.Context, q sqlx.QueryerContext) (bool, error) {
	query := "SELECT EXISTS (SELECT 1 FROM users)"

	var exists bool
	if err := sqlx.GetContext(ctx, q, &exists, query); err != nil {
		return false, fmt.Errorf("counting users: %w", err)
	}

	return exists, nil
}

// IsEmpty reports whether the installation has no user yet.
func (s *Store) IsEmpty(ctx context.Context) (bool, error) {
	exists, err := hasUsers(ctx, s)

	return !exists, err
}

// CreateAccount creates the user, account, owner link, hidden web device and, unless DeviceID is empty
// (signup from the web), the client device in one transaction, and returns the client device's API key.
func (s *Store) CreateAccount(ctx context.Context, signup Signup, isSignupAllowed bool) (string, error) {
	tx, err := s.BeginTxx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("starting signup: %w", err)
	}
	defer tx.Rollback()

	hasUsers, err := hasUsers(ctx, tx)
	if err != nil {
		return "", err
	}

	if hasUsers && !isSignupAllowed {
		return "", ErrSignupClosed
	}

	type statement struct {
		query string
		args  []any
	}
	now := formatTime(time.Now())
	webDeviceID := ulid.Make().String()
	insertUser := `
		INSERT INTO users (id, email, secret, language, date_format, number_format, created_at, updated_at)
		VALUES (?, ?, ?, COALESCE(NULLIF(?, ''), 'pt-BR'), COALESCE(NULLIF(?, ''), 'DD/MM/YYYY'), COALESCE(NULLIF(?, ''), '1.234,56'), ?, ?)
	`
	statements := []statement{
		{
			insertUser,
			[]any{signup.OwnerID, signup.Email, hashPassword(signup.Password), signup.Language, signup.DateFormat, signup.NumberFormat, now, now},
		},
		{
			"INSERT INTO accounts (id, name, currency, created_by, created_at, updated_at) VALUES (?, ?, COALESCE(NULLIF(?, ''), 'BRL'), ?, ?, ?)",
			[]any{signup.AccountID, signup.Email, signup.Currency, signup.OwnerID, now, now},
		},
		{
			"INSERT INTO account_users (id, account_id, user_id, role, created_at) VALUES (?, ?, ?, 'OWNER', ?)",
			[]any{ulid.Make().String(), signup.AccountID, signup.OwnerID, now},
		},
		{
			"INSERT INTO devices (id, user_id, account_id, name, created_at) VALUES (?, ?, ?, 'web', ?)",
			[]any{webDeviceID, signup.OwnerID, signup.AccountID, now},
		},
	}

	var key string
	if signup.DeviceID != "" {
		key = newToken()
		statements = append(statements, statement{
			"INSERT INTO devices (id, user_id, account_id, name, api_key_hash, created_at) VALUES (?, ?, ?, ?, ?, ?)",
			[]any{signup.DeviceID, signup.OwnerID, signup.AccountID, signup.DeviceName, hashToken(key), now},
		})
	}

	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement.query, statement.args...); err != nil {
			if isConstraint(err) {
				return "", ErrDuplicateID
			}

			return "", fmt.Errorf("creating account: %w", err)
		}
	}

	if err := seedCategories(ctx, tx, Actor{AccountID: signup.AccountID, UserID: signup.OwnerID, DeviceID: webDeviceID}, now); err != nil {
		return "", err
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("committing signup: %w", err)
	}

	return key, nil
}

// Signin registers a device on the user's account, or reissues the key of the user's own device with that id
// unless it is the account's web device (its first), and returns who the new API key acts as and the key.
func (s *Store) Signin(ctx context.Context, dto Signin) (Actor, string, error) {
	userID, err := s.Authenticate(ctx, dto.Email, dto.Password)
	if err != nil {
		return Actor{}, "", err
	}

	query := `
		INSERT INTO devices (id, user_id, account_id, name, api_key_hash, created_at)
		SELECT ?, user_id, account_id, ?, ?, ?
		FROM account_users
		WHERE user_id = ?
		ORDER BY created_at
		LIMIT 1
		ON CONFLICT (id) DO UPDATE SET
			name = excluded.name,
			api_key_hash = excluded.api_key_hash
		WHERE devices.user_id = excluded.user_id
			AND devices.account_id = excluded.account_id
			AND devices.rowid > (SELECT MIN(rowid) FROM devices WHERE account_id = excluded.account_id)
		RETURNING account_id
	`

	key := newToken()
	actor := Actor{UserID: userID, DeviceID: dto.DeviceID}
	err = s.GetContext(ctx, &actor.AccountID, query, dto.DeviceID, dto.DeviceName, hashToken(key), formatTime(time.Now()), userID)
	if errors.Is(err, sql.ErrNoRows) {
		return Actor{}, "", ErrDuplicateID
	}

	if err != nil {
		return Actor{}, "", fmt.Errorf("creating device: %w", err)
	}

	return actor, key, nil
}

// ActorByAPIKey returns who a live API key acts as.
func (s *Store) ActorByAPIKey(ctx context.Context, key string) (Actor, error) {
	query := "SELECT id, user_id, account_id FROM devices WHERE api_key_hash = ?"

	var actor Actor
	err := s.GetContext(ctx, &actor, query, hashToken(key))
	if errors.Is(err, sql.ErrNoRows) {
		return Actor{}, ErrInvalidAPIKey
	}

	return actor, err
}

// RevokeAPIKey deletes the device's API key hash.
func (s *Store) RevokeAPIKey(ctx context.Context, deviceID string) error {
	query := "UPDATE devices SET api_key_hash = NULL WHERE id = ?"

	_, err := s.ExecContext(ctx, query, deviceID)

	return err
}

// Device is a device of the account with a live API key; times are RFC3339 UTC, LastSeenAt empty before
// its first sync.
type Device struct {
	ID         string `db:"id"`
	Name       string `db:"name"`
	LastSeenAt string `db:"last_seen_at"`
	CreatedAt  string `db:"created_at"`
}

// Devices returns the account's devices with a live API key, newest first; the hidden web device has none.
func (s *Store) Devices(ctx context.Context, accountID string) ([]Device, error) {
	query := `
		SELECT id, COALESCE(name, '') AS name, COALESCE(last_seen_at, '') AS last_seen_at, created_at
		FROM devices
		WHERE account_id = ? AND api_key_hash IS NOT NULL
		ORDER BY created_at DESC, id
	`

	devices := []Device{}
	err := s.SelectContext(ctx, &devices, query, accountID)

	return devices, err
}

// RevokeDevice deletes the API key hash of the account's device, or returns ErrNotFound when it has no live key.
func (s *Store) RevokeDevice(ctx context.Context, accountID, deviceID string) error {
	query := `
		UPDATE devices
		SET api_key_hash = NULL
		WHERE account_id = ? AND id = ? AND api_key_hash IS NOT NULL
	`

	result, err := s.ExecContext(ctx, query, accountID, deviceID)
	if err != nil {
		return fmt.Errorf("revoking device: %w", err)
	}

	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("counting revoked devices: %w", err)
	}

	if n == 0 {
		return ErrNotFound
	}

	return nil
}

// ResetPassword replaces the user's password, deletes all their web sessions and revokes all their API keys.
func (s *Store) ResetPassword(ctx context.Context, email, password string) error {
	tx, err := s.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("starting password reset: %w", err)
	}
	defer tx.Rollback()

	query := `
		UPDATE users
		SET secret = ?, updated_at = ?
		WHERE email = ? AND deleted_at IS NULL
		RETURNING id
	`

	var userID string
	err = tx.GetContext(ctx, &userID, query, hashPassword(password), formatTime(time.Now()), email)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("no user with email %s", email)
	}

	if err != nil {
		return fmt.Errorf("updating password: %w", err)
	}

	query = "DELETE FROM sessions WHERE user_id = ?"
	if _, err := tx.ExecContext(ctx, query, userID); err != nil {
		return fmt.Errorf("deleting sessions: %w", err)
	}

	query = "UPDATE devices SET api_key_hash = NULL WHERE user_id = ?"
	if _, err := tx.ExecContext(ctx, query, userID); err != nil {
		return fmt.Errorf("revoking api keys: %w", err)
	}

	return tx.Commit()
}

// newToken returns 256 random bits for an API key or session token; crypto/rand.Read never fails.
func newToken() string {
	raw := make([]byte, 32)
	rand.Read(raw)

	return base64.RawURLEncoding.EncodeToString(raw)
}

// hashToken uses SHA-256: the token already has 256 bits of entropy, so a slow hash adds nothing.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))

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

// verifyPassword checks password against an argon2id hash in PHC string format.
func verifyPassword(password, phc string) bool {
	parts := strings.Split(phc, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return false
	}

	var memory, iterations uint32
	var threads uint8
	_, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &threads)
	salt, saltErr := base64.RawStdEncoding.DecodeString(parts[4])
	hash, hashErr := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || saltErr != nil || hashErr != nil {
		return false
	}

	got := argon2.IDKey([]byte(password), salt, iterations, memory, threads, uint32(len(hash)))

	return subtle.ConstantTimeCompare(got, hash) == 1
}

func isConstraint(err error) bool {
	var sqliteErr *sqlite.Error

	return errors.As(err, &sqliteErr) && sqliteErr.Code()&0xff == sqlite3.SQLITE_CONSTRAINT
}
