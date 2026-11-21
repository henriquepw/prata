package store

import (
	"context"
	"fmt"
	"time"

	"github.com/henriquepw/prata/server/internal/importfile"
	"github.com/oklog/ulid/v2"
)

// AccountRef is an account a user can reach.
type AccountRef struct {
	ID   string `db:"id"`
	Name string `db:"name"`
}

// Movement is a line read from a Conta bancária that waits for review, shown with the name of its account.
type Movement struct {
	ID              string `db:"id"`
	AccountName     string `db:"account_name"`
	Institution     string `db:"institution"`
	BankAccount     string `db:"bank_account"`
	BankAccountKind string `db:"bank_account_kind"`
	Description     string `db:"description"`
	Kind            string `db:"kind"`
	Amount          int64  `db:"amount"`
	OccurredAt      string `db:"occurred_at"`
}

// UserAccounts are the accounts the user belongs to.
func (s *Store) UserAccounts(ctx context.Context, userID string) ([]AccountRef, error) {
	query := `
		SELECT c.id, c.name
		FROM account_users a
		JOIN accounts c ON c.id = a.account_id
		WHERE a.user_id = ? AND c.deleted_at IS NULL
		ORDER BY a.created_at
	`

	var accounts []AccountRef
	if err := s.SelectContext(ctx, &accounts, query, userID); err != nil {
		return nil, fmt.Errorf("listing accounts: %w", err)
	}

	return accounts, nil
}

// PendingMovements are the pending movements of the accounts the user belongs to, newest first.
func (s *Store) PendingMovements(ctx context.Context, userID string) ([]Movement, error) {
	query := `
		SELECT
			m.id, c.name AS account_name, m.institution, m.bank_account, m.bank_account_kind,
			m.description, m.kind, m.amount, m.occurred_at
		FROM movements m
		JOIN accounts c ON c.id = m.account_id
		JOIN account_users a ON a.account_id = m.account_id
		WHERE a.user_id = ? AND m.status = 'PENDING'
		ORDER BY m.occurred_at DESC, m.id
	`

	var movements []Movement
	if err := s.SelectContext(ctx, &movements, query, userID); err != nil {
		return nil, fmt.Errorf("listing movements: %w", err)
	}

	return movements, nil
}

// ImportMovements stores every line of the statements as a pending movement of the account, all or none, and
// returns how many were new and how many were already there, in any status. It returns ErrNotFound when the user
// does not belong to the account.
func (s *Store) ImportMovements(ctx context.Context, userID, accountID string, statements []importfile.Statement) (added, existing int, err error) {
	tx, err := s.BeginTxx(ctx, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("starting import: %w", err)
	}
	defer tx.Rollback()

	query := "SELECT EXISTS (SELECT 1 FROM account_users WHERE account_id = ? AND user_id = ?)"

	var isMember bool
	if err := tx.GetContext(ctx, &isMember, query, accountID, userID); err != nil {
		return 0, 0, fmt.Errorf("checking account: %w", err)
	}

	if !isMember {
		return 0, 0, ErrNotFound
	}

	query = `
		INSERT INTO movements (
			id, account_id, institution, bank_account, bank_account_kind, external_id,
			description, kind, amount, occurred_at, created_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING
	`

	now := formatTime(time.Now())
	for _, statement := range statements {
		for _, row := range statement.Rows {
			result, err := tx.ExecContext(ctx, query, ulid.Make().String(), accountID, statement.Institution, statement.Account,
				statement.Kind, row.ID, row.Description, row.Kind, row.Amount, row.Date, now)
			if err != nil {
				return 0, 0, fmt.Errorf("importing movement: %w", err)
			}

			if n, _ := result.RowsAffected(); n == 1 {
				added++
			} else {
				existing++
			}
		}
	}

	return added, existing, tx.Commit()
}
