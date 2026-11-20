package web

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/henriquepw/prata/server/internal/store"
	"github.com/henriquepw/prata/shared/recurrence"
	"github.com/oklog/ulid/v2"
)

// overview is the home page's own content: this month's category usage and Error holding a rejected confirmation.
// The balances and the pending occurrences come from the shell's balance.
type overview struct {
	Usages []store.CategoryUsage
	Error  string
}

// balance is what the ticker and the overview show: the realized balance, the occurrences still pending up to the
// end of this month and the projected balance, which is the realized one plus every pending occurrence.
type balance struct {
	Realized, Projected int64
	Pending             []occurrence
}

// occurrence is a pending occurrence of a recurrence, Overdue when its date is before today.
type occurrence struct {
	Recurrence store.Recurrence
	Date       string
	Overdue    bool
}

func (b balance) overdue() int {
	count := 0
	for _, pending := range b.Pending {
		if pending.Overdue {
			count++
		}
	}

	return count
}

// balance reads the realized balance and the occurrences from each live recurrence's start_at to the end of now's
// month, minus the confirmed ones, in now's location.
func (h handler) balance(ctx context.Context, accountID string, now time.Time) (balance, error) {
	realized, err := h.db.RealizedBalance(ctx, accountID)
	if err != nil {
		return balance{}, fmt.Errorf("reading realized balance: %w", err)
	}

	recurrences, err := h.db.Recurrences(ctx, accountID)
	if err != nil {
		return balance{}, fmt.Errorf("listing recurrences: %w", err)
	}

	confirmed, err := h.db.ConfirmedOccurrences(ctx, accountID)
	if err != nil {
		return balance{}, fmt.Errorf("listing confirmed occurrences: %w", err)
	}

	month, _, _ := monthWindow(now)
	today, end := now.Format(time.DateOnly), month.AddDate(0, 1, -1).Format(time.DateOnly)
	b := balance{Realized: realized, Projected: realized}
	for _, rec := range recurrences {
		dates, err := recurrence.ExpandRule(rec.Rule, rec.StartAt, rec.EndAt, rec.StartAt, end)
		if err != nil {
			return balance{}, fmt.Errorf("expanding recurrence: %w", err)
		}

		for _, date := range dates {
			if slices.Contains(confirmed[rec.ID], date) {
				continue
			}

			b.Pending = append(b.Pending, occurrence{Recurrence: rec, Date: date, Overdue: date < today})
			b.Projected += signed(rec.Kind, rec.Amount)
		}
	}

	slices.SortStableFunc(b.Pending, func(a, b occurrence) int { return cmp.Compare(a.Date, b.Date) })

	return b, nil
}

func (h handler) home(w http.ResponseWriter, r *http.Request) {
	h.renderOverview(w, r, http.StatusOK, "")
}

// renderOverview reads the overview in the user's time zone: the shell's balance and the usage over this month as
// [from, to) in UTC.
func (h handler) renderOverview(w http.ResponseWriter, r *http.Request, status int, message string) {
	s, ok := h.shell(w, r, "Visão geral")
	if !ok {
		return
	}

	_, from, to := monthWindow(s.Now)
	usages, err := h.db.CategoryUsages(r.Context(), sessionFrom(r.Context()).AccountID, from, to)
	if err != nil {
		internalError(w, "reading category usage", err)
		return
	}

	render(w, r, status, homePage(s, overview{Usages: usages, Error: message}))
}

// confirmOccurrence turns the occurrence in the path into a transaction at local midnight of its date in UTC,
// with the recurrence's category only while it is live. An occurrence already confirmed elsewhere hits the unique
// (recurrence_id, occurrence_date) and reloads the overview with "já confirmada"; any other rejection is a 500.
func (h handler) confirmOccurrence(w http.ResponseWriter, r *http.Request) {
	rec, ok := h.recurrence(w, r)
	if !ok {
		return
	}

	date := r.PathValue("date")
	dates, err := recurrence.ExpandRule(rec.Rule, rec.StartAt, rec.EndAt, date, date)
	if err != nil || len(dates) == 0 {
		http.NotFound(w, r)
		return
	}

	occurredAt, ok := parseDate(date, location(r))
	if !ok {
		http.NotFound(w, r)
		return
	}

	categories, ok := h.liveCategories(w, r)
	if !ok {
		return
	}

	categoryID := orEmpty(rec.CategoryID)
	if !hasCategory(categories, categoryID) {
		categoryID = ""
	}

	session := sessionFrom(r.Context())
	reason, err := h.db.CommitChanges(r.Context(), session, store.Change{
		Entity: "TRANSACTION", EntityID: ulid.Make().String(), Kind: store.KindCreate, Fields: map[string]any{
			"kind": rec.Kind, "amount": rec.Amount, "category_id": orNil(categoryID), "description": orNil(orEmpty(rec.Description)),
			"recurrence_id": rec.ID, "occurrence_date": date, "occurred_at": occurredAt,
		},
	})
	if err != nil {
		internalError(w, "confirming occurrence", err)
		return
	}

	if reason == "" {
		redirect(w, r, "/")
		return
	}

	confirmed, err := h.db.ConfirmedOccurrences(r.Context(), session.AccountID)
	if err != nil {
		internalError(w, "listing confirmed occurrences", err)
		return
	}

	if !slices.Contains(confirmed[rec.ID], date) {
		internalError(w, "confirming occurrence", fmt.Errorf("rejected: %s", reason))
		return
	}

	h.renderOverview(w, r, http.StatusConflict, "Ocorrência já confirmada.")
}

// signed is the amount with the kind's sign: positive for INCOME, negative for OUTCOME.
func signed(kind string, amount int64) int64 {
	if kind == "INCOME" {
		return amount
	}

	return -amount
}
