package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrNotFound means the row does not exist in the account or is deleted.
var ErrNotFound = errors.New("not found")

// Category is a live category as the web shows it; MonthlyLimit is in cents, 0 meaning no limit.
type Category struct {
	ID           string `db:"id"`
	Name         string `db:"name"`
	Color        string `db:"color"`
	MonthlyLimit int64  `db:"monthly_limit"`
}

// Categories returns the account's live categories by name.
func (s *Store) Categories(ctx context.Context, accountID string) ([]Category, error) {
	categories := []Category{}
	err := s.SelectContext(ctx, &categories, "SELECT id, name, color, monthly_limit FROM categories WHERE account_id = ? AND deleted_at IS NULL ORDER BY name, id", accountID)

	return categories, err
}

// Category returns one live category of the account.
func (s *Store) Category(ctx context.Context, accountID, id string) (Category, error) {
	var category Category
	err := s.GetContext(ctx, &category, "SELECT id, name, color, monthly_limit FROM categories WHERE account_id = ? AND id = ? AND deleted_at IS NULL", accountID, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Category{}, ErrNotFound
	}

	if err != nil {
		return Category{}, fmt.Errorf("reading category: %w", err)
	}

	return category, nil
}
