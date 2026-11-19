package web

import (
	"cmp"
	"net/http"
	"slices"
	"time"

	"github.com/henriquepw/prata/server/internal/store"
	"github.com/henriquepw/prata/shared/recurrence"
	"github.com/oklog/ulid/v2"
)

// overview is the home page: balances, this month's category usage and the pending occurrences, Error holding
// a rejected confirmation.
type overview struct {
	Realized, Projected int64
	Usages              []store.CategoryUsage
	Pending             []occurrence
	Error               string
}

// occurrence is a pending occurrence of a recurrence, Overdue when its date is before today.
type occurrence struct {
	Recurrence store.Recurrence
	Date       string
	Overdue    bool
}

func (h handler) home(w http.ResponseWriter, r *http.Request) {
	h.renderOverview(w, r, http.StatusOK, "")
}

// renderOverview reads the overview in the user's time zone: usage over this month as [from, to) in UTC, and
// the occurrences from each live recurrence's start_at to the end of this month, minus the confirmed ones.
func (h handler) renderOverview(w http.ResponseWriter, r *http.Request, status int, message string) {
	ctx, accountID := r.Context(), sessionFrom(r.Context()).AccountID
	now := time.Now().In(location(r))
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	page := overview{Error: message}

	realized, err := h.db.RealizedBalance(ctx, accountID)
	if err != nil {
		internalError(w, "reading realized balance", err)
		return
	}

	page.Usages, err = h.db.CategoryUsages(ctx, accountID, month.UTC().Format(time.RFC3339), month.AddDate(0, 1, 0).UTC().Format(time.RFC3339))
	if err != nil {
		internalError(w, "reading category usage", err)
		return
	}

	recurrences, err := h.db.Recurrences(ctx, accountID)
	if err != nil {
		internalError(w, "listing recurrences", err)
		return
	}

	confirmed, err := h.db.ConfirmedOccurrences(ctx, accountID)
	if err != nil {
		internalError(w, "listing confirmed occurrences", err)
		return
	}

	today, end := now.Format(time.DateOnly), month.AddDate(0, 1, -1).Format(time.DateOnly)
	page.Realized, page.Projected = realized, realized
	for _, rec := range recurrences {
		dates, err := recurrence.ExpandRule(rec.Rule, rec.StartAt, rec.EndAt, rec.StartAt, end)
		if err != nil {
			internalError(w, "expanding recurrence", err)
			return
		}

		for _, date := range dates {
			if slices.Contains(confirmed[rec.ID], date) {
				continue
			}

			page.Pending = append(page.Pending, occurrence{Recurrence: rec, Date: date, Overdue: date < today})
			page.Projected += signed(rec.Kind, rec.Amount)
		}
	}

	slices.SortStableFunc(page.Pending, func(a, b occurrence) int { return cmp.Compare(a.Date, b.Date) })
	render(w, r, status, homePage(newShell(r, "Visão geral"), page))
}

// confirmOccurrence turns the occurrence in the path into a transaction at local midnight of its date in UTC.
// An occurrence already confirmed elsewhere hits the unique (recurrence_id, occurrence_date) and reloads the
// overview with "já confirmada".
func (h handler) confirmOccurrence(w http.ResponseWriter, r *http.Request) {
	rec, ok := h.recurrence(w, r)
	if !ok {
		return
	}

	date := r.PathValue("date")
	dates, err := recurrence.ExpandRule(rec.Rule, rec.StartAt, rec.EndAt, date, date)
	occurredAt, ok := parseDate(date, location(r))
	if err != nil || len(dates) == 0 || !ok {
		http.NotFound(w, r)
		return
	}

	reason, err := h.db.CommitChanges(r.Context(), sessionFrom(r.Context()), store.Change{
		Entity: "TRANSACTION", EntityID: ulid.Make().String(), Kind: store.KindCreate, Fields: map[string]any{
			"kind": rec.Kind, "amount": rec.Amount, "category_id": orNil(orEmpty(rec.CategoryID)), "description": orNil(orEmpty(rec.Description)),
			"recurrence_id": rec.ID, "occurrence_date": date, "occurred_at": occurredAt,
		},
	})
	if err != nil {
		internalError(w, "confirming occurrence", err)
		return
	}

	if reason != "" {
		h.renderOverview(w, r, http.StatusConflict, "Ocorrência já confirmada.")
		return
	}

	redirect(w, r, "/")
}

// signed is the amount with the kind's sign: positive for INCOME, negative for OUTCOME.
func signed(kind string, amount int64) int64 {
	if kind == "INCOME" {
		return amount
	}

	return -amount
}
