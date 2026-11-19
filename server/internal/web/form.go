package web

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Form helpers shared by the sheets: pt-BR money, pure dates and optional fields.

// kind is a transaction or recurrence kind with its label.
type kind struct{ Value, Label string }

var kinds = []kind{{"OUTCOME", "Despesa"}, {"INCOME", "Receita"}}

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
