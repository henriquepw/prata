package store

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrBudgetOpen means the division does not add up to the Receita mensal.
var ErrBudgetOpen = errors.New("division does not close the monthly income")

// Budget is the Receita mensal and its division as the user typed them, keyed by category id: Limits are cents, or
// hundredths of a percent when Percent.
type Budget struct {
	Income  int64
	Percent bool
	Limits  map[string]int64
}

// BasisPoints is limit as hundredths of a percent of total, 0 when total is.
func BasisPoints(limit, total int64) int64 {
	if total == 0 {
		return 0
	}

	return (limit*20000 + total) / (2 * total)
}

// SaveBudget stores the Receita mensal and the monthly limits of the live categories in one transaction. With
// the limits untouched, it rescales them to the new Receita mensal in proportion; otherwise, with a Receita mensal
// defined, they must add up to it, or nothing is stored and it returns ErrBudgetOpen. The cents lost to rounding go to
// the default category.
func (s *Store) SaveBudget(ctx context.Context, session Session, budget Budget) error {
	tx, err := s.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("starting budget: %w", err)
	}
	defer tx.Rollback()

	query := `
		SELECT id, name, color, monthly_limit, is_daily, is_default
		FROM categories
		WHERE account_id = ? AND deleted_at IS NULL
	`

	categories := []Category{}
	if err := tx.SelectContext(ctx, &categories, query, session.AccountID); err != nil {
		return fmt.Errorf("listing categories: %w", err)
	}

	var stored, typed int64
	unchanged := true
	for _, category := range categories {
		stored += category.MonthlyLimit
		typed += budget.Limits[category.ID]
	}

	for _, category := range categories {
		current := category.MonthlyLimit
		if budget.Percent {
			current = BasisPoints(current, stored)
		}

		unchanged = unchanged && budget.Limits[category.ID] == current
	}

	limits := map[string]int64{}
	for _, category := range categories {
		limits[category.ID] = budget.Limits[category.ID]
	}

	switch {
	case budget.Income == 0 && budget.Percent:
		return ErrBudgetOpen
	case budget.Income > 0 && budget.Percent && !unchanged:
		if typed != 10000 {
			return ErrBudgetOpen
		}

		for _, category := range categories {
			limits[category.ID] = budget.Income * limits[category.ID] / 10000
		}
	case budget.Income > 0 && unchanged:
		for _, category := range categories {
			limits[category.ID] = category.MonthlyLimit * budget.Income / max(stored, 1)
		}
	}

	if budget.Income > 0 {
		var total int64
		defaultID := ""
		for _, category := range categories {
			total += limits[category.ID]
			if category.IsDefault {
				defaultID = category.ID
			}
		}

		if budget.Percent || unchanged {
			limits[defaultID] += budget.Income - total
			total = budget.Income
		}

		if total != budget.Income || defaultID == "" {
			return ErrBudgetOpen
		}
	}

	query = "UPDATE accounts SET monthly_income = ?, updated_at = ? WHERE id = ?"
	if _, err := tx.ExecContext(ctx, query, budget.Income, formatTime(time.Now()), session.AccountID); err != nil {
		return fmt.Errorf("saving monthly income: %w", err)
	}

	changes := make([]Change, 0, len(categories))
	for _, category := range categories {
		changes = append(changes, Change{Entity: "CATEGORY", EntityID: category.ID, Kind: KindUpdate, Fields: map[string]any{"monthly_limit": limits[category.ID]}})
	}

	reason, err := commitChanges(ctx, tx, session, changes)
	if err != nil {
		return err
	}

	if reason != "" {
		return fmt.Errorf("saving limits: rejected as %s", reason)
	}

	return tx.Commit()
}
