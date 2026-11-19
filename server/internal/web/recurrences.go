package web

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/henriquepw/prata/server/internal/store"
	"github.com/oklog/ulid/v2"
)

func (h handler) recurrences(w http.ResponseWriter, r *http.Request) {
	recurrences, err := h.db.Recurrences(r.Context(), sessionFrom(r.Context()).AccountID)
	if err != nil {
		internalError(w, "listing recurrences", err)
		return
	}

	render(w, r, http.StatusOK, recurrencesPage(newShell(r, "Recorrências"), recurrences))
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

	render(w, r, http.StatusOK, recurrenceSheet(storedRecurrenceForm(recurrence, categories)))
}

// saveRecurrence creates the recurrence, or updates the one in the path, from the sheet's form.
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
	amount, ok := parseCents(form.Amount)
	if !ok || amount <= 0 {
		form.Errors["amount"] = "Informe um valor maior que zero."
	}

	if !slices.ContainsFunc(kinds, func(k kind) bool { return k.Value == form.Kind }) {
		form.Errors["kind"] = "Escolha o tipo."
	}

	if form.CategoryID != "" && !slices.ContainsFunc(categories, func(c store.Category) bool { return c.ID == form.CategoryID }) {
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
		form.Errors["end_at"] = "O fim não pode ser antes do início."
	}

	change := store.Change{Entity: "RECURRENCE", EntityID: form.ID, Kind: store.KindUpdate, Fields: map[string]any{
		"description": orNil(form.Description), "kind": form.Kind, "amount": amount, "category_id": orNil(form.CategoryID),
		"rule": "MONTHLY:" + strconv.Itoa(day), "start_at": form.StartAt, "end_at": orNil(form.EndAt),
	}}
	if form.ID == "" {
		change.EntityID, change.Kind = ulid.Make().String(), store.KindCreate
	} else if _, ok := h.recurrence(w, r); !ok {
		return
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

	form := storedRecurrenceForm(recurrence, categories)
	h.commit(w, r, form.Errors, recurrenceSheet(form), store.Change{Entity: "RECURRENCE", EntityID: recurrence.ID, Kind: store.KindDelete})
}

// storedRecurrenceForm fills the sheet with the stored recurrence.
func storedRecurrenceForm(recurrence store.Recurrence, categories []store.Category) recurrenceForm {
	return recurrenceForm{
		ID:          recurrence.ID,
		Description: orEmpty(recurrence.Description),
		Kind:        recurrence.Kind,
		Amount:      formatCents(recurrence.Amount),
		CategoryID:  orEmpty(recurrence.CategoryID),
		Day:         strings.TrimPrefix(recurrence.Rule, "MONTHLY:"),
		StartAt:     recurrence.StartAt,
		EndAt:       orEmpty(recurrence.EndAt),
		Categories:  categories,
		Errors:      map[string]string{},
	}
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

// liveCategories reads the categories a sheet can pick from, answering 500 when it cannot.
func (h handler) liveCategories(w http.ResponseWriter, r *http.Request) ([]store.Category, bool) {
	categories, err := h.db.Categories(r.Context(), sessionFrom(r.Context()).AccountID)
	if err != nil {
		internalError(w, "listing categories", err)
		return nil, false
	}

	return categories, true
}
