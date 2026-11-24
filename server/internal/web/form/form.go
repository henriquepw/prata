package form

import (
	"context"
	"net/http"
	"slices"
	"time"

	"github.com/henriquepw/prata/server/internal/i18n"
	"github.com/henriquepw/prata/server/internal/store"
	"github.com/henriquepw/prata/server/internal/web/prefs"
	"github.com/henriquepw/prata/server/internal/web/session"
)

// Form helpers shared by the sheets: entries, dates, optional fields and the live options of a select.

// Kinds are the transaction and recurrence kinds; each one's label is the i18n key "kind." + kind.
var Kinds = []string{"OUTCOME", "INCOME"}

// ParseEntry reads the amount in cents, typed in the request's Formato numérico, and checks the kind every
// transaction and recurrence has, setting their field errors.
func ParseEntry(ctx context.Context, errs map[string]string, typed, kind string) int64 {
	cents, ok := prefs.ParseAmount(ctx, typed)
	if !ok || cents <= 0 {
		errs["amount"] = i18n.T(ctx, "form.error.amount")
	}

	if !slices.Contains(Kinds, kind) {
		errs["kind"] = i18n.T(ctx, "form.error.kind")
	}

	return cents
}

// IsDate reports whether s is a pure "YYYY-MM-DD" date, as <input type="date"> sends it.
func IsDate(s string) bool {
	_, err := time.Parse(time.DateOnly, s)

	return err == nil
}

// ParseDate reads a date input's YYYY-MM-DD as local midnight and returns it as RFC 3339 in UTC.
func ParseDate(s string, loc *time.Location) (string, bool) {
	t, err := time.ParseInLocation(time.DateOnly, s, loc)
	if err != nil {
		return "", false
	}

	return t.UTC().Format(time.RFC3339), true
}

// OrNil is the value of an optional field: nil (SQL NULL) when empty.
func OrNil(s string) any {
	if s == "" {
		return nil
	}

	return s
}

// OrEmpty shows an optional stored field, empty when NULL.
func OrEmpty(s *string) string {
	if s == nil {
		return ""
	}

	return *s
}

func HasCategory(categories []store.Category, id string) bool {
	return slices.ContainsFunc(categories, func(c store.Category) bool { return c.ID == id })
}

// DefaultCategory is the default one of categories, the zero Category when none is.
func DefaultCategory(categories []store.Category) store.Category {
	if i := slices.IndexFunc(categories, func(c store.Category) bool { return c.IsDefault }); i >= 0 {
		return categories[i]
	}

	return store.Category{}
}

func HasTag(tags []store.Tag, id string) bool {
	return slices.ContainsFunc(tags, func(t store.Tag) bool { return t.ID == id })
}

// LiveCategories reads the categories a sheet can pick from, answering 500 when it cannot.
func LiveCategories(w http.ResponseWriter, r *http.Request, db *store.Store) ([]store.Category, bool) {
	categories, err := db.Categories(r.Context(), session.From(r.Context()).AccountID)
	if err != nil {
		session.InternalError(w, "listing categories", err)
		return nil, false
	}

	return categories, true
}

// LiveTags reads the tags a sheet can pick from, answering 500 when it cannot.
func LiveTags(w http.ResponseWriter, r *http.Request, db *store.Store) ([]store.Tag, bool) {
	tags, err := db.Tags(r.Context(), session.From(r.Context()).AccountID)
	if err != nil {
		session.InternalError(w, "listing tags", err)
		return nil, false
	}

	return tags, true
}

// Signed is the amount with the kind's sign: positive for INCOME, negative for OUTCOME.
func Signed(kind string, amount int64) int64 {
	if kind == "INCOME" {
		return amount
	}

	return -amount
}

// MonthWindow is the month of t, as its first day at midnight in t's location and as [from, to) in RFC 3339 UTC.
func MonthWindow(t time.Time) (month time.Time, from, to string) {
	month = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())

	return month, month.UTC().Format(time.RFC3339), month.AddDate(0, 1, 0).UTC().Format(time.RFC3339)
}
