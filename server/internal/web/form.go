package web

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/henriquepw/prata/server/internal/store"
)

// Form helpers shared by the sheets: pt-BR money, dates, optional fields and the live options of a select.

// brDate is how pages show a date.
const brDate = "02/01/2006"

// kind is a transaction or recurrence kind with its label.
type kind struct{ Value, Label string }

var kinds = []kind{{"OUTCOME", "Despesa"}, {"INCOME", "Receita"}}

func isKind(value string) bool {
	return slices.ContainsFunc(kinds, func(k kind) bool { return k.Value == value })
}

// parseEntry reads the amount in cents and checks the kind every transaction and recurrence has, setting their
// field errors.
func parseEntry(errs map[string]string, amount, kind string) int64 {
	cents, ok := parseCents(amount)
	if !ok || cents <= 0 {
		errs["amount"] = "Informe um valor maior que zero."
	}

	if !isKind(kind) {
		errs["kind"] = "Escolha o tipo."
	}

	return cents
}

// parseCents reads a pt-BR amount such as "1.234,56" as cents; empty is zero.
func parseCents(s string) (int64, bool) {
	whole, frac, _ := strings.Cut(strings.ReplaceAll(s, ".", ""), ",")
	if len(frac) > 2 {
		return 0, false
	}

	cents, err := strconv.ParseInt(whole+(frac + "00")[:2], 10, 64)

	return cents, err == nil
}

// formatCents writes non-negative cents as a pt-BR amount such as "1.234,56".
func formatCents(cents int64) string {
	whole := strconv.FormatInt(cents/100, 10)
	for i := len(whole) - 3; i > 0; i -= 3 {
		whole = whole[:i] + "." + whole[i:]
	}

	return fmt.Sprintf("%s,%02d", whole, cents%100)
}

// isDate reports whether s is a pure "YYYY-MM-DD" date, as <input type="date"> sends it.
func isDate(s string) bool {
	_, err := time.Parse(time.DateOnly, s)

	return err == nil
}

// parseDate reads a date input's YYYY-MM-DD as local midnight and returns it as RFC 3339 in UTC.
func parseDate(s string, loc *time.Location) (string, bool) {
	t, err := time.ParseInLocation(time.DateOnly, s, loc)
	if err != nil {
		return "", false
	}

	return t.UTC().Format(time.RFC3339), true
}

// formatDate writes a stored RFC 3339 time in loc with layout, or the raw value when it does not parse.
func formatDate(stored string, loc *time.Location, layout string) string {
	t, err := time.Parse(time.RFC3339, stored)
	if err != nil {
		return stored
	}

	return t.In(loc).Format(layout)
}

// orNil is the value of an optional field: nil (SQL NULL) when empty.
func orNil(s string) any {
	if s == "" {
		return nil
	}

	return s
}

// orEmpty shows an optional stored field, empty when NULL.
func orEmpty(s *string) string {
	if s == nil {
		return ""
	}

	return *s
}

func hasCategory(categories []store.Category, id string) bool {
	return slices.ContainsFunc(categories, func(c store.Category) bool { return c.ID == id })
}

func hasTag(tags []store.Tag, id string) bool {
	return slices.ContainsFunc(tags, func(t store.Tag) bool { return t.ID == id })
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

// liveTags reads the tags a sheet can pick from, answering 500 when it cannot.
func (h handler) liveTags(w http.ResponseWriter, r *http.Request) ([]store.Tag, bool) {
	tags, err := h.db.Tags(r.Context(), sessionFrom(r.Context()).AccountID)
	if err != nil {
		internalError(w, "listing tags", err)
		return nil, false
	}

	return tags, true
}
