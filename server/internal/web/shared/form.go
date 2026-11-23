package shared

import (
	"context"
	"net/http"
	"slices"
	"time"

	"github.com/henriquepw/prata/server/internal/i18n"
	"github.com/henriquepw/prata/server/internal/store"
)

// Form helpers shared by the sheets: entries, dates, optional fields and the live options of a select.

// Kinds are the transaction and recurrence kinds; each one's label is the i18n key "kind." + kind.
var Kinds = []string{"OUTCOME", "INCOME"}

// ParseEntry reads the amount in cents, typed in the request's Formato numérico, and checks the kind every
// transaction and recurrence has, setting their field errors.
func ParseEntry(ctx context.Context, errs map[string]string, typed, kind string) int64 {
	cents, ok := ParseAmount(ctx, typed)
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

// FormatDate writes a stored RFC 3339 time in loc with layout, or the raw value when it does not parse.
func FormatDate(stored string, loc *time.Location, layout string) string {
	t, err := time.Parse(time.RFC3339, stored)
	if err != nil {
		return stored
	}

	return t.In(loc).Format(layout)
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
	categories, err := db.Categories(r.Context(), SessionFrom(r.Context()).AccountID)
	if err != nil {
		InternalError(w, "listing categories", err)
		return nil, false
	}

	return categories, true
}

// LiveTags reads the tags a sheet can pick from, answering 500 when it cannot.
func LiveTags(w http.ResponseWriter, r *http.Request, db *store.Store) ([]store.Tag, bool) {
	tags, err := db.Tags(r.Context(), SessionFrom(r.Context()).AccountID)
	if err != nil {
		InternalError(w, "listing tags", err)
		return nil, false
	}

	return tags, true
}

// LocalTime writes an RFC 3339 time in loc in the request's Formato de data and 24h time, as "02/01/2006 15:04", or
// "nunca" when it is empty.
func LocalTime(ctx context.Context, value string, loc *time.Location) string {
	if value == "" {
		return i18n.T(ctx, "devices.never")
	}

	return FormatDate(value, loc, DateLayout(ctx)+" 15:04")
}
