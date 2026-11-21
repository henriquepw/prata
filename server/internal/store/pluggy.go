package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// PluggyCredentials are a user's own client id and secret of the Pluggy API.
type PluggyCredentials struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

// SavePluggyCredentials stores the user's credentials, replacing any, encrypted with the instance key.
func (s *Store) SavePluggyCredentials(ctx context.Context, key, userID string, credentials PluggyCredentials) error {
	plain, err := json.Marshal(credentials)
	if err != nil {
		return fmt.Errorf("encoding pluggy credentials: %w", err)
	}

	aead, err := newAEAD(key)
	if err != nil {
		return err
	}

	sealed := aead.Seal(nil, nil, plain, []byte(userID))
	query := `
		INSERT INTO pluggy_credentials (user_id, credentials, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT (user_id) DO UPDATE SET credentials = excluded.credentials, updated_at = excluded.updated_at
	`

	if _, err := s.ExecContext(ctx, query, userID, sealed, formatTime(time.Now())); err != nil {
		return fmt.Errorf("saving pluggy credentials: %w", err)
	}

	return nil
}

// PluggyCredentials returns the user's credentials, or ErrNotFound when there are none.
func (s *Store) PluggyCredentials(ctx context.Context, key, userID string) (PluggyCredentials, error) {
	query := "SELECT credentials FROM pluggy_credentials WHERE user_id = ?"

	var sealed []byte
	err := s.GetContext(ctx, &sealed, query, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return PluggyCredentials{}, ErrNotFound
	}

	if err != nil {
		return PluggyCredentials{}, fmt.Errorf("reading pluggy credentials: %w", err)
	}

	aead, err := newAEAD(key)
	if err != nil {
		return PluggyCredentials{}, err
	}

	plain, err := aead.Open(nil, nil, sealed, []byte(userID))
	if err != nil {
		return PluggyCredentials{}, fmt.Errorf("decrypting pluggy credentials: %w", err)
	}

	var credentials PluggyCredentials
	if err := json.Unmarshal(plain, &credentials); err != nil {
		return PluggyCredentials{}, fmt.Errorf("decoding pluggy credentials: %w", err)
	}

	return credentials, nil
}

// DeletePluggyCredentials removes the user's credentials, if any.
func (s *Store) DeletePluggyCredentials(ctx context.Context, userID string) error {
	query := "DELETE FROM pluggy_credentials WHERE user_id = ?"

	_, err := s.ExecContext(ctx, query, userID)

	return err
}

// newAEAD is AES-256-GCM keyed by the SHA-256 of the instance key, with a random nonce in each sealed value.
func newAEAD(key string) (cipher.AEAD, error) {
	sum := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, fmt.Errorf("creating cipher: %w", err)
	}

	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, fmt.Errorf("creating gcm: %w", err)
	}

	return aead, nil
}
