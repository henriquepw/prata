package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Tag is a live tag as the web shows it; StartAt and EndAt are RFC 3339 in UTC, EndAt nil when open-ended.
type Tag struct {
	ID      string  `db:"id"`
	Name    string  `db:"name"`
	StartAt string  `db:"start_at"`
	EndAt   *string `db:"end_at"`
}

// Tags returns the account's live tags, newest start first.
func (s *Store) Tags(ctx context.Context, accountID string) ([]Tag, error) {
	tags := []Tag{}
	err := s.SelectContext(ctx, &tags, "SELECT id, name, start_at, end_at FROM tags WHERE account_id = ? AND deleted_at IS NULL ORDER BY start_at DESC, name, id", accountID)

	return tags, err
}

// Tag returns one live tag of the account.
func (s *Store) Tag(ctx context.Context, accountID, id string) (Tag, error) {
	var tag Tag
	err := s.GetContext(ctx, &tag, "SELECT id, name, start_at, end_at FROM tags WHERE account_id = ? AND id = ? AND deleted_at IS NULL", accountID, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Tag{}, ErrNotFound
	}

	if err != nil {
		return Tag{}, fmt.Errorf("reading tag: %w", err)
	}

	return tag, nil
}
