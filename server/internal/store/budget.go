package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"
)

// ErrDivisionNotClosed means the division does not add up to the Receita mensal.
var ErrDivisionNotClosed = errors.New("division does not close the monthly income")

// Budget is the Receita mensal and its division as the user typed them, keyed by category id: Limits are cents, or
// hundredths of a percent when Percent.
type Budget struct {
	Income  int64
	Percent bool
	Limits  map[string]int64
}

// HundredPercent is 100% in hundredths of a percent, the unit of a division typed in percent.
const HundredPercent = 10000

// BasisPoints is limit as hundredths of a percent of total, 0 when total is.
func BasisPoints(limit, total int64) int64 {
	if total == 0 {
		return 0
	}

	return (limit*20000 + total) / (2 * total)
}

// Division is the stored monthly limit of each of categories in hundredths of a percent of what they add up to, the
// rounding on the default category so that it closes 100%; all 0 while they add up to 0.
func Division(categories []Category) map[string]int64 {
	var stored, shared int64
	for _, category := range categories {
		stored += category.MonthlyLimit
	}

	shares := map[string]int64{}
	for _, category := range categories {
		shares[category.ID] = BasisPoints(category.MonthlyLimit, stored)
		shared += shares[category.ID]
	}

	if i := slices.IndexFunc(categories, func(c Category) bool { return c.IsDefault }); i >= 0 && stored > 0 {
		shares[categories[i].ID] += HundredPercent - shared
	}

	return shares
}

// SaveBudget stores the Receita mensal and the monthly limits of the live categories, as divide sets them, in one
// transaction; a division that does not close stores nothing and returns ErrDivisionNotClosed.
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

	limits, err := divide(categories, budget)
	if err != nil {
		return err
	}

	query = "UPDATE accounts SET monthly_income = ?, updated_at = ? WHERE id = ?"
	if _, err := tx.ExecContext(ctx, query, budget.Income, formatTime(time.Now()), session.AccountID); err != nil {
		return fmt.Errorf("saving monthly income: %w", err)
	}

	changes := make([]Change, 0, len(categories))
	for _, category := range categories {
		changes = append(changes, Change{Entity: "CATEGORY", EntityID: category.ID, Kind: KindUpdate,
			Fields: map[string]any{"monthly_limit": limits[category.ID]}})
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

// divide is the monthly limit, in cents, of each of categories for budget, which needs a Receita mensal. In percent,
// typed limits equal to the stored ones mean the Receita mensal alone changed: the stored limits are rescaled to it in
// proportion. Otherwise the typed limits are the division, converted from percent when Percent, and must add up to
// the Receita mensal, or 100%. The cents lost to rounding go to the default category.
func divide(categories []Category, budget Budget) (map[string]int64, error) {
	var stored, typed int64
	for _, category := range categories {
		stored += category.MonthlyLimit
	}

	current := Division(categories)
	untouched := true
	limits := map[string]int64{}
	for _, category := range categories {
		limits[category.ID] = budget.Limits[category.ID]
		typed += limits[category.ID]
		untouched = untouched && limits[category.ID] == current[category.ID]
	}

	if budget.Income <= 0 {
		return nil, ErrDivisionNotClosed
	}

	switch {
	case budget.Percent && untouched:
		for _, category := range categories {
			limits[category.ID] = category.MonthlyLimit * budget.Income / max(stored, 1)
		}
	case budget.Percent && typed != HundredPercent, !budget.Percent && typed != budget.Income:
		return nil, ErrDivisionNotClosed
	case budget.Percent:
		for _, category := range categories {
			limits[category.ID] = budget.Income * limits[category.ID] / HundredPercent
		}
	}

	i := slices.IndexFunc(categories, func(c Category) bool { return c.IsDefault })
	if i < 0 {
		return nil, ErrDivisionNotClosed
	}

	var total int64
	for _, limit := range limits {
		total += limit
	}

	limits[categories[i].ID] += budget.Income - total

	return limits, nil
}
