// Package entry is the transaction sheet's fields, shared by the pages that write a transaction: the transaction
// sheet and the confirmation of a recurrence's occurrence.
package entry

import (
	"net/http"

	"github.com/henriquepw/prata/server/internal/l10n"
	"github.com/henriquepw/prata/server/internal/store"
	"github.com/henriquepw/prata/server/internal/web/form"
	"github.com/henriquepw/prata/server/internal/web/session"
	"github.com/henriquepw/prata/server/pkg/id"
)

// Form is what the user typed in the transaction's fields, the date as YYYY-MM-DD, the live categories and tags to
// pick from and an error message per field name, "form" holding a rejected commit.
type Form struct {
	ID          id.ID
	Description string
	Kind        string
	Amount      string
	OccurredAt  string
	CategoryID  id.ID
	TagID       id.ID
	Categories  []store.Category
	Tags        []store.Tag
	Errors      map[string]string
}

// Load is an empty form with the live categories and tags to pick from, answering 500 when it cannot read them.
func Load(w http.ResponseWriter, r *http.Request, db *store.Store) (Form, bool) {
	categories, ok := form.LiveCategories(w, r, db)
	if !ok {
		return Form{}, false
	}

	tags, ok := form.LiveTags(w, r, db)
	if !ok {
		return Form{}, false
	}

	return Form{Categories: categories, Tags: tags, Errors: map[string]string{}}, true
}

// Parse checks the amount, the date and the category, setting their field errors, and returns the amount in cents and
// the date as local midnight in the user's time zone, in UTC.
func (f Form) Parse(r *http.Request) (cents int64, occurredAt string) {
	cents = form.ParseEntry(r.Context(), f.Errors, f.Amount, f.Kind)
	occurredAt, ok := form.ParseDate(f.OccurredAt, session.Location(r))
	if !ok {
		f.Errors["occurred_at"] = l10n.T(r.Context(), "form.error.date")
	}

	if f.CategoryID != "" && !form.HasCategory(f.Categories, f.CategoryID) {
		f.Errors["category_id"] = l10n.T(r.Context(), "form.error.category")
	}

	return cents, occurredAt
}
