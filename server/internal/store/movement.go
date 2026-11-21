package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/henriquepw/prata/server/internal/importfile"
	"github.com/jmoiron/sqlx"
	"github.com/oklog/ulid/v2"
)

// AccountRef is an account a user can reach.
type AccountRef struct {
	ID       string `db:"id"`
	Name     string `db:"name"`
	Currency string `db:"currency"`
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
		SELECT c.id, c.name, c.currency
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

// ConfirmMovement turns a pending movement of the session's account into a transaction from the web device, with
// the movement's kind, amount and date (local midnight in loc) and the given description and category, and marks it
// confirmed in the same transaction. An income keeps no category and an expense without one gets the default. It
// returns ErrNotFound when the movement is not pending in the account, so a second confirm creates nothing.
func (s *Store) ConfirmMovement(ctx context.Context, session Session, id, description, categoryID string, loc *time.Location) (Reason, error) {
	tx, err := s.BeginTxx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("starting confirm: %w", err)
	}
	defer tx.Rollback()

	query := "SELECT kind, amount, occurred_at FROM movements WHERE id = ? AND account_id = ? AND status = 'PENDING'"

	var movement Movement
	if err := tx.GetContext(ctx, &movement, query, id, session.AccountID); errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	} else if err != nil {
		return "", fmt.Errorf("reading movement: %w", err)
	}

	day, err := time.ParseInLocation(time.DateOnly, movement.OccurredAt, loc)
	if err != nil {
		return "", fmt.Errorf("reading movement date: %w", err)
	}

	fields := map[string]any{"kind": movement.Kind, "amount": movement.Amount, "occurred_at": day.UTC().Format(time.RFC3339)}
	if description != "" {
		fields["description"] = description
	}

	if categoryID != "" && movement.Kind == "OUTCOME" {
		fields["category_id"] = categoryID
	}

	reason, err := commitChanges(ctx, tx, session, []Change{{Entity: "TRANSACTION", EntityID: ulid.Make().String(), Kind: KindCreate, Fields: fields}})
	if err != nil || reason != "" {
		return reason, err
	}

	query = "UPDATE movements SET status = 'CONFIRMED' WHERE id = ?"
	if _, err := tx.ExecContext(ctx, query, id); err != nil {
		return "", fmt.Errorf("confirming movement: %w", err)
	}

	return "", tx.Commit()
}

// IgnoreMovement marks a pending movement of the session's account as ignored, for good: importing the file again
// does not bring it back. It returns ErrNotFound when the movement is not pending in the account.
func (s *Store) IgnoreMovement(ctx context.Context, session Session, id string) error {
	query := "UPDATE movements SET status = 'IGNORED' WHERE id = ? AND account_id = ? AND status = 'PENDING'"

	result, err := s.ExecContext(ctx, query, id, session.AccountID)
	if err != nil {
		return fmt.Errorf("ignoring movement: %w", err)
	}

	if n, _ := result.RowsAffected(); n == 0 {
		return ErrNotFound
	}

	return nil
}

// ConfirmMovements turns every pending movement of the session's account among ids into a transaction from the web
// device, with the bank's description, kind, amount and date (local midnight in loc) and, on an expense, the given
// category (the default one when empty), as one commit marking them confirmed in the same SQL transaction. Movements
// not pending in the account are skipped, so repeating the batch creates nothing.
func (s *Store) ConfirmMovements(ctx context.Context, session Session, ids []string, categoryID string, loc *time.Location) (Reason, error) {
	if len(ids) == 0 {
		return "", nil
	}

	tx, err := s.BeginTxx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("starting confirm: %w", err)
	}
	defer tx.Rollback()

	query, args, err := sqlx.In(`
		SELECT id, description, kind, amount, occurred_at
		FROM movements
		WHERE id IN (?) AND account_id = ? AND status = 'PENDING'
		ORDER BY occurred_at, id
	`, ids, session.AccountID)
	if err != nil {
		return "", fmt.Errorf("building confirm: %w", err)
	}

	var movements []Movement
	if err := tx.SelectContext(ctx, &movements, query, args...); err != nil {
		return "", fmt.Errorf("reading movements: %w", err)
	}

	if len(movements) == 0 {
		return "", nil
	}

	changes := make([]Change, 0, len(movements))
	for _, movement := range movements {
		day, err := time.ParseInLocation(time.DateOnly, movement.OccurredAt, loc)
		if err != nil {
			return "", fmt.Errorf("reading movement date: %w", err)
		}

		fields := map[string]any{"kind": movement.Kind, "amount": movement.Amount, "occurred_at": day.UTC().Format(time.RFC3339), "description": movement.Description}
		if categoryID != "" && movement.Kind == "OUTCOME" {
			fields["category_id"] = categoryID
		}

		changes = append(changes, Change{Entity: "TRANSACTION", EntityID: ulid.Make().String(), Kind: KindCreate, Fields: fields})
	}

	reason, err := commitChanges(ctx, tx, session, changes)
	if err != nil || reason != "" {
		return reason, err
	}

	query, args, err = sqlx.In("UPDATE movements SET status = 'CONFIRMED' WHERE id IN (?)", movementIDs(movements))
	if err != nil {
		return "", fmt.Errorf("building confirm: %w", err)
	}

	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return "", fmt.Errorf("confirming movements: %w", err)
	}

	return "", tx.Commit()
}

// IgnoreMovements marks every pending movement of the session's account among ids as ignored, for good. Movements
// not pending in the account are skipped.
func (s *Store) IgnoreMovements(ctx context.Context, session Session, ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	query, args, err := sqlx.In("UPDATE movements SET status = 'IGNORED' WHERE id IN (?) AND account_id = ? AND status = 'PENDING'", ids, session.AccountID)
	if err != nil {
		return fmt.Errorf("building ignore: %w", err)
	}

	if _, err := s.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("ignoring movements: %w", err)
	}

	return nil
}

func movementIDs(movements []Movement) []string {
	ids := make([]string, len(movements))
	for i, movement := range movements {
		ids[i] = movement.ID
	}

	return ids
}
