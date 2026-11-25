package recurrencepage

import (
	"net/http"
	"slices"
	"strings"

	"github.com/henriquepw/prata/server/internal/l10n"
	"github.com/henriquepw/prata/server/internal/store"
	"github.com/henriquepw/prata/server/internal/web/components/view/entry"
	"github.com/henriquepw/prata/server/internal/web/form"
	"github.com/henriquepw/prata/server/internal/web/prefs"
	"github.com/henriquepw/prata/server/internal/web/session"
	"github.com/henriquepw/prata/shared/recurrence"
	"github.com/oklog/ulid/v2"
)

// occurrenceForm is the "confirm occurrence" sheet: the transaction sheet filled from the recurrence and the
// occurrence's date.
type occurrenceForm struct {
	entry.Form
	Recurrence store.Recurrence
	Date       string
}

// occurrence reads the recurrence and the date of the occurrence in the path, answering 404 when the recurrence has
// no occurrence on that date, and fills the sheet from them, with the recurrence's category only while it is live.
func (p Page) occurrence(w http.ResponseWriter, r *http.Request) (occurrenceForm, bool) {
	rec, ok := p.recurrence(w, r)
	if !ok {
		return occurrenceForm{}, false
	}

	date := r.PathValue("date")
	dates, err := recurrence.ExpandRule(rec.Rule, rec.StartAt, rec.EndAt, date, date)
	if err != nil || len(dates) == 0 {
		http.NotFound(w, r)
		return occurrenceForm{}, false
	}

	fm, ok := entry.Load(w, r, p.db)
	if !ok {
		return occurrenceForm{}, false
	}

	fm.Kind, fm.Amount, fm.Description, fm.OccurredAt = rec.Kind, prefs.Amount(r.Context(), rec.Amount), form.OrEmpty(rec.Description), date
	if id := form.OrEmpty(rec.CategoryID); form.HasCategory(fm.Categories, id) {
		fm.CategoryID = id
	}

	return occurrenceForm{Form: fm, Recurrence: rec, Date: date}, true
}

// OccurrenceSheet is the "confirm occurrence" sheet of the occurrence in the path.
func (p Page) OccurrenceSheet(w http.ResponseWriter, r *http.Request) {
	fm, ok := p.occurrence(w, r)
	if !ok {
		return
	}

	session.Render(w, r, http.StatusOK, occurrenceSheet(fm))
}

// ConfirmOccurrence turns the occurrence in the path into a transaction at local midnight of its date in UTC. The
// sheet posts what the user adjusted and gets the sheet's answer; the statement's row posts nothing but the month and
// the filter on screen and gets that month back with the new row flashing. On the row, an occurrence already confirmed
// elsewhere hits the unique (recurrence_id, occurrence_date) and answers 409 with "already confirmed"; any other rejection
// is a 500.
func (p Page) ConfirmOccurrence(w http.ResponseWriter, r *http.Request) {
	fm, ok := p.occurrence(w, r)
	if !ok {
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	isSheet := r.PostForm.Has("amount")
	if isSheet {
		fm.Kind = r.PostFormValue("kind")
		fm.Amount = strings.TrimSpace(r.PostFormValue("amount"))
		fm.Description = strings.TrimSpace(r.PostFormValue("description"))
		fm.OccurredAt = r.PostFormValue("occurred_at")
		fm.CategoryID = r.PostFormValue("category_id")
	}

	cents, occurredAt := fm.Parse(r)

	change := store.Change{Entity: "TRANSACTION", EntityID: ulid.Make().String(), Kind: store.KindCreate, Fields: map[string]any{
		"kind": fm.Kind, "amount": cents, "category_id": form.OrNil(fm.CategoryID), "description": form.OrNil(fm.Description),
		"recurrence_id": fm.Recurrence.ID, "occurrence_date": fm.Date, "occurred_at": occurredAt,
	}}
	if isSheet {
		session.Commit(p.db, w, r, fm.Errors, occurrenceSheet(fm), change)
		return
	}

	p.month.CreateInMonth(w, r, change, func() (string, error) {
		confirmed, err := p.db.ConfirmedOccurrences(r.Context(), session.From(r.Context()).AccountID)
		if err != nil || !slices.Contains(confirmed[fm.Recurrence.ID], fm.Date) {
			return "", err
		}

		return l10n.T(r.Context(), "month.error.confirmed"), nil
	})
}
