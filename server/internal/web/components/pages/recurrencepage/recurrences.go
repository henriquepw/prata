// Package recurrencepage is the Recurrences screen, its sheet and the confirmation of an occurrence.
package recurrencepage

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/henriquepw/prata/server/internal/l10n"
	"github.com/henriquepw/prata/server/internal/store"
	"github.com/henriquepw/prata/server/internal/web/components/view/layout"
	"github.com/henriquepw/prata/server/internal/web/form"
	"github.com/henriquepw/prata/server/internal/web/prefs"
	"github.com/henriquepw/prata/server/internal/web/session"
	"github.com/henriquepw/prata/server/pkg/id"
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

// Page is the recurrences' handlers: the Recurrences screen, its sheet and the occurrence sheet.
type Page struct {
	db    *store.Store
	shell layout.Func
	month Month
}

// Month is the Month as the confirmation of an occurrence from its statement answers: homepage.Page.CreateInMonth.
type Month interface {
	CreateInMonth(w http.ResponseWriter, r *http.Request, change store.Change, conflict func() (string, error))
}

// New returns the recurrences page over db; shell reads the console chrome of an authenticated page, as web's shell
// does, and month answers the confirmation of an occurrence made from the Month.
func New(db *store.Store, shell layout.Func, month Month) Page {
	return Page{db: db, shell: shell, month: month}
}

// Recurrences is the Recurrences screen.
func (p Page) Recurrences(w http.ResponseWriter, r *http.Request) {
	recurrences, err := p.db.Recurrences(r.Context(), session.From(r.Context()).AccountID)
	if err != nil {
		session.InternalError(w, "listing recurrences", err)
		return
	}

	s, ok := p.shell(w, r, "nav.recurrences")
	if !ok {
		return
	}

	session.Render(w, r, http.StatusOK, recurrencesPage(s, recurrences))
}

// NewSheet is the sheet of a new recurrence.
func (p Page) NewSheet(w http.ResponseWriter, r *http.Request) {
	categories, ok := form.LiveCategories(w, r, p.db)
	if !ok {
		return
	}

	fm := recurrenceForm{Kind: "OUTCOME", StartAt: time.Now().In(session.Location(r)).Format(time.DateOnly), Categories: categories}
	session.Render(w, r, http.StatusOK, recurrenceSheet(fm))
}

// Edit is the sheet of a recurrence.
func (p Page) Edit(w http.ResponseWriter, r *http.Request) {
	recurrence, ok := p.recurrence(w, r)
	if !ok {
		return
	}

	categories, ok := form.LiveCategories(w, r, p.db)
	if !ok {
		return
	}

	session.Render(w, r, http.StatusOK, recurrenceSheet(storedRecurrenceForm(r.Context(), recurrence, categories)))
}

// Save creates the recurrence, or updates the one in the path, from the sheet's form. A category left
// as displayed is not written, so a stored reference to a deleted one, displayed as empty, survives editing
// other fields.
func (p Page) Save(w http.ResponseWriter, r *http.Request) {
	categories, ok := form.LiveCategories(w, r, p.db)
	if !ok {
		return
	}

	fm := recurrenceForm{
		ID:          id.ID(r.PathValue("id")),
		Description: strings.TrimSpace(r.PostFormValue("description")),
		Kind:        r.PostFormValue("kind"),
		Amount:      strings.TrimSpace(r.PostFormValue("amount")),
		CategoryID:  id.ID(r.PostFormValue("category_id")),
		Day:         strings.TrimSpace(r.PostFormValue("day")),
		StartAt:     r.PostFormValue("start_at"),
		EndAt:       r.PostFormValue("end_at"),
		Categories:  categories,
		Errors:      map[string]string{},
	}
	amount := form.ParseEntry(r.Context(), fm.Errors, fm.Amount, fm.Kind)
	if fm.CategoryID != "" && !form.HasCategory(categories, fm.CategoryID) {
		fm.Errors["category_id"] = l10n.T(r.Context(), "form.error.category")
	}

	day, err := strconv.Atoi(fm.Day)
	if err != nil || day < 1 || day > 31 {
		fm.Errors["day"] = l10n.T(r.Context(), "recurrences.error.day")
	}

	if !form.IsDate(fm.StartAt) {
		fm.Errors["start_at"] = l10n.T(r.Context(), "form.error.start")
	}

	if fm.EndAt != "" && (!form.IsDate(fm.EndAt) || fm.EndAt < fm.StartAt) {
		fm.Errors["end_at"] = l10n.T(r.Context(), "form.error.end")
	}

	change := store.Change{Entity: "RECURRENCE", EntityID: fm.ID, Kind: store.KindUpdate, Fields: map[string]any{
		"description": form.OrNil(fm.Description), "kind": fm.Kind, "amount": amount, "category_id": form.OrNil(fm.CategoryID),
		"rule": monthlyRule(day), "start_at": fm.StartAt, "end_at": form.OrNil(fm.EndAt),
	}}
	if fm.ID == "" {
		change.EntityID, change.Kind = id.New(), store.KindCreate
	} else {
		stored, ok := p.recurrence(w, r)
		if !ok {
			return
		}

		if fm.CategoryID == storedRecurrenceForm(r.Context(), stored, categories).CategoryID {
			delete(change.Fields, "category_id")
		}
	}

	session.Commit(p.db, w, r, fm.Errors, recurrenceSheet(fm), change)
}

// Delete tombstones the recurrence only; its confirmed transactions stay.
func (p Page) Delete(w http.ResponseWriter, r *http.Request) {
	recurrence, ok := p.recurrence(w, r)
	if !ok {
		return
	}

	categories, ok := form.LiveCategories(w, r, p.db)
	if !ok {
		return
	}

	fm := storedRecurrenceForm(r.Context(), recurrence, categories)
	session.Commit(p.db, w, r, fm.Errors, recurrenceSheet(fm), store.Change{Entity: "RECURRENCE", EntityID: recurrence.ID, Kind: store.KindDelete})
}

// storedRecurrenceForm fills the sheet with the stored recurrence as displayed: its category only when it is
// among the live ones.
func storedRecurrenceForm(ctx context.Context, recurrence store.Recurrence, categories []store.Category) recurrenceForm {
	fm := recurrenceForm{
		ID:          recurrence.ID,
		Description: form.OrEmpty(recurrence.Description),
		Kind:        recurrence.Kind,
		Amount:      prefs.Amount(ctx, recurrence.Amount),
		Day:         ruleDay(recurrence.Rule),
		StartAt:     recurrence.StartAt,
		EndAt:       form.OrEmpty(recurrence.EndAt),
		Categories:  categories,
		Errors:      map[string]string{},
	}
	if id := form.OrEmpty(recurrence.CategoryID); form.HasCategory(categories, id) {
		fm.CategoryID = id
	}

	return fm
}

// recurrence reads the live recurrence in the path, answering 404 when the account has none.
func (p Page) recurrence(w http.ResponseWriter, r *http.Request) (store.Recurrence, bool) {
	recurrence, err := p.db.Recurrence(r.Context(), session.From(r.Context()).AccountID, id.ID(r.PathValue("id")))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return store.Recurrence{}, false
	}

	if err != nil {
		session.InternalError(w, "reading recurrence", err)
		return store.Recurrence{}, false
	}

	return recurrence, true
}
