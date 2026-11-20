package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/henriquepw/prata/server/internal/store"
	"github.com/oklog/ulid/v2"
)

const monthlyPrefix = "MONTHLY:"

// monthlyRule is the rule of a recurrence on the given day of every month.
func monthlyRule(day int) string {
	return monthlyPrefix + strconv.Itoa(day)
}

// ruleDay is the day of a "MONTHLY:<day>" rule.
func ruleDay(rule string) string {
	return strings.TrimPrefix(rule, monthlyPrefix)
}

func (h handler) recurrences(w http.ResponseWriter, r *http.Request) {
	recurrences, err := h.db.Recurrences(r.Context(), sessionFrom(r.Context()).AccountID)
	if err != nil {
		internalError(w, "listing recurrences", err)
		return
	}

	s, ok := h.shell(w, r, "Recorrências")
	if !ok {
		return
	}

	render(w, r, http.StatusOK, recurrencesPage(s, recurrences))
}

func (h handler) newRecurrence(w http.ResponseWriter, r *http.Request) {
	categories, ok := h.liveCategories(w, r)
	if !ok {
		return
	}

	form := recurrenceForm{Kind: "OUTCOME", StartAt: time.Now().In(location(r)).Format(time.DateOnly), Categories: categories}
	render(w, r, http.StatusOK, recurrenceSheet(form))
}

func (h handler) editRecurrence(w http.ResponseWriter, r *http.Request) {
	recurrence, ok := h.recurrence(w, r)
	if !ok {
		return
	}

	categories, ok := h.liveCategories(w, r)
	if !ok {
		return
	}

	render(w, r, http.StatusOK, recurrenceSheet(storedRecurrenceForm(r.Context(), recurrence, categories)))
}

// saveRecurrence creates the recurrence, or updates the one in the path, from the sheet's form. A category left
// as displayed is not written, so a stored reference to a deleted one, displayed as empty, survives editing
// other fields.
func (h handler) saveRecurrence(w http.ResponseWriter, r *http.Request) {
	categories, ok := h.liveCategories(w, r)
	if !ok {
		return
	}

	form := recurrenceForm{
		ID:          r.PathValue("id"),
		Description: strings.TrimSpace(r.PostFormValue("description")),
		Kind:        r.PostFormValue("kind"),
		Amount:      strings.TrimSpace(r.PostFormValue("amount")),
		CategoryID:  r.PostFormValue("category_id"),
		Day:         strings.TrimSpace(r.PostFormValue("day")),
		StartAt:     r.PostFormValue("start_at"),
		EndAt:       r.PostFormValue("end_at"),
		Categories:  categories,
		Errors:      map[string]string{},
	}
	amount := parseEntry(r.Context(), form.Errors, form.Amount, form.Kind)
	if form.CategoryID != "" && !hasCategory(categories, form.CategoryID) {
		form.Errors["category_id"] = "Escolha uma categoria."
	}

	day, err := strconv.Atoi(form.Day)
	if err != nil || day < 1 || day > 31 {
		form.Errors["day"] = "Informe um dia de 1 a 31."
	}

	if !isDate(form.StartAt) {
		form.Errors["start_at"] = "Informe o início."
	}

	if form.EndAt != "" && (!isDate(form.EndAt) || form.EndAt < form.StartAt) {
		form.Errors["end_at"] = "Informe um fim igual ou depois do início."
	}

	change := store.Change{Entity: "RECURRENCE", EntityID: form.ID, Kind: store.KindUpdate, Fields: map[string]any{
		"description": orNil(form.Description), "kind": form.Kind, "amount": amount, "category_id": orNil(form.CategoryID),
		"rule": monthlyRule(day), "start_at": form.StartAt, "end_at": orNil(form.EndAt),
	}}
	if form.ID == "" {
		change.EntityID, change.Kind = ulid.Make().String(), store.KindCreate
	} else {
		stored, ok := h.recurrence(w, r)
		if !ok {
			return
		}

		if form.CategoryID == storedRecurrenceForm(r.Context(), stored, categories).CategoryID {
			delete(change.Fields, "category_id")
		}
	}

	h.commit(w, r, form.Errors, recurrenceSheet(form), change)
}

// deleteRecurrence tombstones the recurrence only; its confirmed transactions stay.
func (h handler) deleteRecurrence(w http.ResponseWriter, r *http.Request) {
	recurrence, ok := h.recurrence(w, r)
	if !ok {
		return
	}

	categories, ok := h.liveCategories(w, r)
	if !ok {
		return
	}

	form := storedRecurrenceForm(r.Context(), recurrence, categories)
	h.commit(w, r, form.Errors, recurrenceSheet(form), store.Change{Entity: "RECURRENCE", EntityID: recurrence.ID, Kind: store.KindDelete})
}

// storedRecurrenceForm fills the sheet with the stored recurrence as displayed: its category only when it is
// among the live ones.
func storedRecurrenceForm(ctx context.Context, recurrence store.Recurrence, categories []store.Category) recurrenceForm {
	form := recurrenceForm{
		ID:          recurrence.ID,
		Description: orEmpty(recurrence.Description),
		Kind:        recurrence.Kind,
		Amount:      amount(ctx, recurrence.Amount),
		Day:         ruleDay(recurrence.Rule),
		StartAt:     recurrence.StartAt,
		EndAt:       orEmpty(recurrence.EndAt),
		Categories:  categories,
		Errors:      map[string]string{},
	}
	if id := orEmpty(recurrence.CategoryID); hasCategory(categories, id) {
		form.CategoryID = id
	}

	return form
}

// recurrence reads the live recurrence in the path, answering 404 when the account has none.
func (h handler) recurrence(w http.ResponseWriter, r *http.Request) (store.Recurrence, bool) {
	recurrence, err := h.db.Recurrence(r.Context(), sessionFrom(r.Context()).AccountID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return store.Recurrence{}, false
	}

	if err != nil {
		internalError(w, "reading recurrence", err)
		return store.Recurrence{}, false
	}

	return recurrence, true
}
