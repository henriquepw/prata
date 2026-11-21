package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/oklog/ulid/v2"
)

// ErrNotFound means the row does not exist in the account or is deleted.
var ErrNotFound = errors.New("not found")

// Category is a live category as the web shows it; MonthlyLimit is in cents, 0 meaning no limit.
type Category struct {
	ID           string `db:"id"`
	Name         string `db:"name"`
	Color        string `db:"color"`
	MonthlyLimit int64  `db:"monthly_limit"`
	IsDaily      bool   `db:"is_daily"`
	IsDefault    bool   `db:"is_default"`
}

// Categories returns the account's live categories by name.
func (s *Store) Categories(ctx context.Context, accountID string) ([]Category, error) {
	query := `
		SELECT id, name, color, monthly_limit, is_daily, is_default
		FROM categories
		WHERE account_id = ? AND deleted_at IS NULL
		ORDER BY name, id
	`

	categories := []Category{}
	err := s.SelectContext(ctx, &categories, query, accountID)

	return categories, err
}

// Category returns one live category of the account.
func (s *Store) Category(ctx context.Context, accountID, id string) (Category, error) {
	query := `
		SELECT id, name, color, monthly_limit, is_daily, is_default
		FROM categories
		WHERE account_id = ? AND id = ? AND deleted_at IS NULL
	`

	var category Category
	err := s.GetContext(ctx, &category, query, accountID, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Category{}, ErrNotFound
	}

	if err != nil {
		return Category{}, fmt.Errorf("reading category: %w", err)
	}

	return category, nil
}

// initialCategories are the categories every account is born with; Livre is the default and the only daily one.
var initialCategories = []struct {
	name, color        string
	isDaily, isDefault int
}{
	{"Gastos fixos", "BLUE", 0, 0},
	{"Investimento", "GREEN", 0, 0},
	{"Meta", "YELLOW", 0, 0},
	{"Livre", "GRAY", 1, 1},
}

// seedCategories creates the initial categories as CATEGORY CREATE ops from the actor's device, so a new device pulls them.
func seedCategories(ctx context.Context, tx *sqlx.Tx, actor Actor, now string) error {
	for _, category := range initialCategories {
		payload, err := json.Marshal(map[string]any{
			"name": category.name, "color": category.color, "monthly_limit": 0,
			"is_daily": category.isDaily, "is_default": category.isDefault, "created_at": now, "updated_at": now,
		})
		if err != nil {
			return fmt.Errorf("encoding initial category: %w", err)
		}

		id := ulid.Make().String()
		op := Op{ID: ulid.Make().String(), Entity: "CATEGORY", EntityID: id, Kind: KindCreate, Payload: payload,
			ProtocolVersion: ProtocolVersion, DeviceID: actor.DeviceID, OwnerID: actor.UserID, CreatedAt: now}

		result, err := commitOp(ctx, tx, actor, op)
		if err != nil {
			return err
		}

		if result.Reason != "" {
			return fmt.Errorf("seeding category %s: %s", category.name, result.Reason)
		}
	}

	return nil
}
