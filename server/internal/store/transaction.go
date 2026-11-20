package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Transaction is a live transaction as the web shows it, with its live category ("Sem categoria" and GRAY
// when it has none or it was deleted) and the name of its live tag (empty without one); Amount is in cents and
// OccurredAt is RFC 3339 in UTC.
type Transaction struct {
	ID            string  `db:"id"`
	CategoryID    *string `db:"category_id"`
	CategoryName  string  `db:"category_name"`
	CategoryColor string  `db:"category_color"`
	TagID         *string `db:"tag_id"`
	TagName       string  `db:"tag_name"`
	Kind          string  `db:"kind"`
	Amount        int64   `db:"amount"`
	Description   *string `db:"description"`
	OccurredAt    string  `db:"occurred_at"`
}

const transactionSelect = `SELECT t.id, c.id AS category_id, COALESCE(c.name, 'Sem categoria') AS category_name,
	COALESCE(c.color, 'GRAY') AS category_color, t.tag_id, COALESCE(g.name, '') AS tag_name, t.kind, t.amount, t.description,
	t.occurred_at
	FROM transactions t LEFT JOIN categories c ON c.id = t.category_id AND c.deleted_at IS NULL
	LEFT JOIN tags g ON g.id = t.tag_id AND g.deleted_at IS NULL
	WHERE t.account_id = ? AND t.deleted_at IS NULL`

// Transactions returns the account's live transactions with occurred_at in [from, to), RFC 3339 in UTC, newest first.
func (s *Store) Transactions(ctx context.Context, accountID, from, to string) ([]Transaction, error) {
	transactions := []Transaction{}
	err := s.SelectContext(ctx, &transactions, transactionSelect+" AND t.occurred_at >= ? AND t.occurred_at < ? ORDER BY t.occurred_at DESC, t.id DESC", accountID, from, to)

	return transactions, err
}

// Transaction returns one live transaction of the account.
func (s *Store) Transaction(ctx context.Context, accountID, id string) (Transaction, error) {
	var transaction Transaction
	err := s.GetContext(ctx, &transaction, transactionSelect+" AND t.id = ?", accountID, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Transaction{}, ErrNotFound
	}

	if err != nil {
		return Transaction{}, fmt.Errorf("reading transaction: %w", err)
	}

	return transaction, nil
}
