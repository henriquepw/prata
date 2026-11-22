package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
)

// ErrCurrency means the account's Moeda is not BRL, the only one a Conexão accepts.
var ErrCurrency = errors.New("account currency is not BRL")

// Connection is a user's Pluggy Item feeding movements into one of their accounts.
type Connection struct {
	ID              string         `db:"id"`
	AccountName     string         `db:"account_name"`
	Institution     string         `db:"institution"`
	Status          string         `db:"status"`
	ExecutionStatus string         `db:"execution_status"`
	LastUpdatedAt   sql.NullString `db:"last_updated_at"`
}

// Connections are the user's Conexões, oldest first.
func (s *Store) Connections(ctx context.Context, userID string) ([]Connection, error) {
	query := `
		SELECT n.id, a.name AS account_name, n.institution, n.status, n.execution_status, n.last_updated_at
		FROM connections n
		JOIN accounts a ON a.id = n.account_id
		WHERE n.user_id = ?
		ORDER BY n.created_at, n.id
	`

	var connections []Connection
	if err := s.SelectContext(ctx, &connections, query, userID); err != nil {
		return nil, fmt.Errorf("listing connections: %w", err)
	}

	return connections, nil
}

// AddConnection stores the user's Conexão to a Pluggy Item, feeding the account. It returns ErrNotFound when the
// user does not belong to the account and ErrCurrency when the account's Moeda is not BRL.
func (s *Store) AddConnection(ctx context.Context, userID, accountID, itemID, institution, status, executionStatus, lastUpdatedAt string) error {
	query := `
		SELECT c.currency
		FROM account_users a
		JOIN accounts c ON c.id = a.account_id
		WHERE a.user_id = ? AND a.account_id = ? AND c.deleted_at IS NULL
	`

	var currency string
	err := s.GetContext(ctx, &currency, query, userID, accountID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}

	if err != nil {
		return fmt.Errorf("checking account: %w", err)
	}

	if currency != "BRL" {
		return ErrCurrency
	}

	query = `
		INSERT INTO connections (id, user_id, account_id, item_id, institution, status, execution_status, last_updated_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?)
	`
	if _, err := s.ExecContext(ctx, query, ulid.Make().String(), userID, accountID, itemID, institution, status, executionStatus, lastUpdatedAt, formatTime(time.Now())); err != nil {
		return fmt.Errorf("adding connection: %w", err)
	}

	return nil
}

// DeleteConnection removes the user's Conexão and its pending movements, keeping the confirmed and ignored ones. It
// returns ErrNotFound when the user has no such Conexão.
func (s *Store) DeleteConnection(ctx context.Context, userID, id string) error {
	tx, err := s.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("starting connection removal: %w", err)
	}
	defer tx.Rollback()

	query := "DELETE FROM connections WHERE id = ? AND user_id = ?"

	result, err := tx.ExecContext(ctx, query, id, userID)
	if err != nil {
		return fmt.Errorf("deleting connection: %w", err)
	}

	if n, _ := result.RowsAffected(); n == 0 {
		return ErrNotFound
	}

	query = "DELETE FROM movements WHERE connection_id = ? AND status = 'PENDING'"
	if _, err := tx.ExecContext(ctx, query, id); err != nil {
		return fmt.Errorf("deleting pending movements: %w", err)
	}

	return tx.Commit()
}
