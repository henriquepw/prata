package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/henriquepw/prata/server/internal/i18n"
	"github.com/henriquepw/prata/server/internal/store"
	"github.com/henriquepw/prata/server/internal/web/shared"
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
	recurrences, err := h.db.Recurrences(r.Context(), shared.SessionFrom(r.Context()).AccountID)
	if err != nil {
		shared.InternalError(w, "listing recurrences", err)
		return
	}

	s, ok := h.shell(w, r, "nav.recurrences")
	if !ok {
		return
	}

	shared.Render(w, r, http.StatusOK, recurrencesPage(s, recurrences))
}

func (h handler) newRecurrence(w http.ResponseWriter, r *http.Request) {
	categories, ok := shared.LiveCategories(w, r, h.db)
	if !ok {
		return
	}

	form := recurrenceForm{Kind: "OUTCOME", StartAt: time.Now().In(shared.Location(r)).Format(time.DateOnly), Categories: categories}
	shared.Render(w, r, http.StatusOK, recurrenceSheet(form))
}

func (h handler) editRecurrence(w http.ResponseWriter, r *http.Request) {
	recurrence, ok := h.recurrence(w, r)
	if !ok {
		return
	}

	categories, ok := shared.LiveCategories(w, r, h.db)
	if !ok {
		return
	}

	shared.Render(w, r, http.StatusOK, recurrenceSheet(storedRecurrenceForm(r.Context(), recurrence, categories)))
}

// saveRecurrence creates the recurrence, or updates the one in the path, from the sheet's form. A category left
// as displayed is not written, so a stored reference to a deleted one, displayed as empty, survives editing
// other fields.
func (h handler) saveRecurrence(w http.ResponseWriter, r *http.Request) {
	categories, ok := shared.LiveCategories(w, r, h.db)
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
	amount := shared.ParseEntry(r.Context(), form.Errors, form.Amount, form.Kind)
	if form.CategoryID != "" && !shared.HasCategory(categories, form.CategoryID) {
		form.Errors["category_id"] = i18n.T(r.Context(), "form.error.category")
	}

	day, err := strconv.Atoi(form.Day)
	if err != nil || day < 1 || day > 31 {
		form.Errors["day"] = i18n.T(r.Context(), "recurrences.error.day")
	}

	if !shared.IsDate(form.StartAt) {
		form.Errors["start_at"] = i18n.T(r.Context(), "form.error.start")
	}

	if form.EndAt != "" && (!shared.IsDate(form.EndAt) || form.EndAt < form.StartAt) {
		form.Errors["end_at"] = i18n.T(r.Context(), "form.error.end")
	}

	change := store.Change{Entity: "RECURRENCE", EntityID: form.ID, Kind: store.KindUpdate, Fields: map[string]any{
		"description": shared.OrNil(form.Description), "kind": form.Kind, "amount": amount, "category_id": shared.OrNil(form.CategoryID),
		"rule": monthlyRule(day), "start_at": form.StartAt, "end_at": shared.OrNil(form.EndAt),
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

	categories, ok := shared.LiveCategories(w, r, h.db)
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
		Description: shared.OrEmpty(recurrence.Description),
		Kind:        recurrence.Kind,
		Amount:      shared.Amount(ctx, recurrence.Amount),
		Day:         ruleDay(recurrence.Rule),
		StartAt:     recurrence.StartAt,
		EndAt:       shared.OrEmpty(recurrence.EndAt),
		Categories:  categories,
		Errors:      map[string]string{},
	}
	if id := shared.OrEmpty(recurrence.CategoryID); shared.HasCategory(categories, id) {
		form.CategoryID = id
	}

	return form
}

// recurrence reads the live recurrence in the path, answering 404 when the account has none.
func (h handler) recurrence(w http.ResponseWriter, r *http.Request) (store.Recurrence, bool) {
	recurrence, err := h.db.Recurrence(r.Context(), shared.SessionFrom(r.Context()).AccountID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return store.Recurrence{}, false
	}

	if err != nil {
		shared.InternalError(w, "reading recurrence", err)
		return store.Recurrence{}, false
	}

	return recurrence, true
}
