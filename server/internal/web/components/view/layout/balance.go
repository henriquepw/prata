package layout

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/henriquepw/prata/server/internal/store"
	"github.com/henriquepw/prata/server/internal/web/form"
	"github.com/henriquepw/prata/shared/recurrence"
)

// ReadBalance reads the realized balance and the occurrences pending up to the end of now's month, in now's location.
func ReadBalance(ctx context.Context, db *store.Store, accountID string, now time.Time) (Balance, error) {
	realized, err := db.RealizedBalance(ctx, accountID)
	if err != nil {
		return Balance{}, fmt.Errorf("reading realized balance: %w", err)
	}

	month, _, _ := form.MonthWindow(now)
	pending, err := Pending(ctx, db, accountID, now, month.AddDate(0, 1, -1))
	if err != nil {
		return Balance{}, err
	}

	b := Balance{Realized: realized, Projected: realized, Pending: pending}
	for _, p := range pending {
		b.Projected += form.Signed(p.Recurrence.Kind, p.Recurrence.Amount)
	}

	return b, nil
}

// Pending lists by date the occurrences from each live recurrence's start_at through the day of end, minus the
// confirmed ones, overdue before the day of now.
func Pending(ctx context.Context, db *store.Store, accountID string, now, end time.Time) ([]Occurrence, error) {
	recurrences, err := db.Recurrences(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("listing recurrences: %w", err)
	}

	confirmed, err := db.ConfirmedOccurrences(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("listing confirmed occurrences: %w", err)
	}

	today, pending := now.Format(time.DateOnly), []Occurrence{}
	for _, rec := range recurrences {
		dates, err := recurrence.ExpandRule(rec.Rule, rec.StartAt, rec.EndAt, rec.StartAt, end.Format(time.DateOnly))
		if err != nil {
			return nil, fmt.Errorf("expanding recurrence: %w", err)
		}

		for _, date := range dates {
			if !slices.Contains(confirmed[rec.ID], date) {
				pending = append(pending, Occurrence{Recurrence: rec, Date: date, Overdue: date < today})
			}
		}
	}

	slices.SortStableFunc(pending, func(a, b Occurrence) int { return cmp.Compare(a.Date, b.Date) })

	return pending, nil
}
