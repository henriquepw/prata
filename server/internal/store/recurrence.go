package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Recurrence is a live recurrence as the web shows it; Amount is in cents and Rule is "MONTHLY:<day>".
type Recurrence struct {
	ID          string  `db:"id"`
	CategoryID  *string `db:"category_id"`
	Kind        string  `db:"kind"`
	Amount      int64   `db:"amount"`
	Description *string `db:"description"`
	Rule        string  `db:"rule"`
	StartAt     string  `db:"start_at"`
	EndAt       *string `db:"end_at"`
}

const recurrenceColumns = "id, category_id, kind, amount, description, rule, start_at, end_at"

// Recurrences returns the account's live recurrences by start date.
func (s *Store) Recurrences(ctx context.Context, accountID string) ([]Recurrence, error) {
	recurrences := []Recurrence{}
	err := s.SelectContext(ctx, &recurrences, "SELECT "+recurrenceColumns+" FROM recurrences WHERE account_id = ? AND deleted_at IS NULL ORDER BY start_at, id", accountID)

	return recurrences, err
}

// Recurrence returns one live recurrence of the account.
func (s *Store) Recurrence(ctx context.Context, accountID, id string) (Recurrence, error) {
	var recurrence Recurrence
	err := s.GetContext(ctx, &recurrence, "SELECT "+recurrenceColumns+" FROM recurrences WHERE account_id = ? AND id = ? AND deleted_at IS NULL", accountID, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Recurrence{}, ErrNotFound
	}

	if err != nil {
		return Recurrence{}, fmt.Errorf("reading recurrence: %w", err)
	}

	return recurrence, nil
}
