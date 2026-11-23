package recurrencepage

import (
	"net/http"
	"slices"
	"strings"

	"github.com/henriquepw/prata/server/internal/i18n"
	"github.com/henriquepw/prata/server/internal/store"
	"github.com/henriquepw/prata/server/internal/web/components/entry"
	"github.com/henriquepw/prata/server/internal/web/shared"
	"github.com/henriquepw/prata/shared/recurrence"
	"github.com/oklog/ulid/v2"
)

// occurrenceForm is the "confirmar ocorrência" sheet: the transaction sheet filled from the recurrence and the
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

	form, ok := entry.Load(w, r, p.db)
	if !ok {
		return occurrenceForm{}, false
	}

	form.Kind, form.Amount, form.Description, form.OccurredAt = rec.Kind, shared.Amount(r.Context(), rec.Amount), shared.OrEmpty(rec.Description), date
	if id := shared.OrEmpty(rec.CategoryID); shared.HasCategory(form.Categories, id) {
		form.CategoryID = id
	}

	return occurrenceForm{Form: form, Recurrence: rec, Date: date}, true
}

// OccurrenceSheet is the "confirmar ocorrência" sheet of the occurrence in the path.
func (p Page) OccurrenceSheet(w http.ResponseWriter, r *http.Request) {
	form, ok := p.occurrence(w, r)
	if !ok {
		return
	}

	shared.Render(w, r, http.StatusOK, occurrenceSheet(form))
}

// ConfirmOccurrence turns the occurrence in the path into a transaction at local midnight of its date in UTC. The
// sheet posts what the user adjusted and gets the sheet's answer; the statement's row posts nothing but the month and
// the filter on screen and gets that month back with the new row flashing. On the row, an occurrence already confirmed
// elsewhere hits the unique (recurrence_id, occurrence_date) and answers 409 with "já confirmada"; any other rejection
// is a 500.
func (p Page) ConfirmOccurrence(w http.ResponseWriter, r *http.Request) {
	form, ok := p.occurrence(w, r)
	if !ok {
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	isSheet := r.PostForm.Has("amount")
	if isSheet {
		form.Kind = r.PostFormValue("kind")
		form.Amount = strings.TrimSpace(r.PostFormValue("amount"))
		form.Description = strings.TrimSpace(r.PostFormValue("description"))
		form.OccurredAt = r.PostFormValue("occurred_at")
		form.CategoryID = r.PostFormValue("category_id")
	}

	cents, occurredAt := form.Parse(r)

	change := store.Change{Entity: "TRANSACTION", EntityID: ulid.Make().String(), Kind: store.KindCreate, Fields: map[string]any{
		"kind": form.Kind, "amount": cents, "category_id": shared.OrNil(form.CategoryID), "description": shared.OrNil(form.Description),
		"recurrence_id": form.Recurrence.ID, "occurrence_date": form.Date, "occurred_at": occurredAt,
	}}
	if isSheet {
		shared.Commit(p.db, w, r, form.Errors, occurrenceSheet(form), change)
		return
	}

	p.month.CreateInMonth(w, r, change, func() (string, error) {
		confirmed, err := p.db.ConfirmedOccurrences(r.Context(), shared.SessionFrom(r.Context()).AccountID)
		if err != nil || !slices.Contains(confirmed[form.Recurrence.ID], form.Date) {
			return "", err
		}

		return i18n.T(r.Context(), "month.error.confirmed"), nil
	})
}
