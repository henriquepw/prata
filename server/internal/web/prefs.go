package web

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/henriquepw/prata/server/internal/store"
)

// prefs are what a request's pages follow: the user's Preferências and the account's Moeda. requireSession puts
// them in the context; pages without a session follow defaultPrefs.
type prefs struct {
	store.Preferences
	Currency string
}

var defaultPrefs = prefs{store.Preferences{Language: "pt-BR", DateFormat: "DD/MM/YYYY", NumberFormat: "1.234,56"}, "BRL"}

type prefsKey struct{}

func prefsFrom(ctx context.Context) prefs {
	if p, ok := ctx.Value(prefsKey{}).(prefs); ok {
		return p
	}

	return defaultPrefs
}

// dateFormat is a Formato de data: its schema value, its label and its Go layout.
type dateFormat struct{ Value, Label, Layout string }

var dateFormats = []dateFormat{
	{"DD/MM/YYYY", "DD/MM/AAAA", "02/01/2006"},
	{"MM/DD/YYYY", "MM/DD/AAAA", "01/02/2006"},
	{"YYYY-MM-DD", "AAAA-MM-DD", "2006-01-02"},
}

// numberFormat is a Formato numérico: its schema value, which is also its label, and its separators.
type numberFormat struct{ Value, Thousands, Decimal string }

var numberFormats = []numberFormat{{"1.234,56", ".", ","}, {"1,234.56", ",", "."}}

func findDateFormat(value string) (dateFormat, bool) {
	i := slices.IndexFunc(dateFormats, func(f dateFormat) bool { return f.Value == value })
	if i < 0 {
		return dateFormat{}, false
	}

	return dateFormats[i], true
}

func findNumberFormat(value string) (numberFormat, bool) {
	i := slices.IndexFunc(numberFormats, func(f numberFormat) bool { return f.Value == value })
	if i < 0 {
		return numberFormat{}, false
	}

	return numberFormats[i], true
}

// dateLayout is the Go layout of the request's Formato de data, as "02/01/2006".
func dateLayout(ctx context.Context) string {
	f, _ := findDateFormat(prefsFrom(ctx).DateFormat)

	return f.Layout
}

// clock writes t as the status bar clock: the date in the Formato de data and the time in 24h.
func clock(ctx context.Context, t time.Time) string {
	return t.Format(dateLayout(ctx) + " 15:04:05")
}

// money writes signed cents in the request's Formato numérico, as "−R$ 1.234,56".
func money(ctx context.Context, cents int64) string {
	if cents < 0 {
		return "−R$ " + amount(ctx, -cents)
	}

	return "R$ " + amount(ctx, cents)
}

// amount writes non-negative cents in the request's Formato numérico, as "1.234,56", for display and form fields.
func amount(ctx context.Context, cents int64) string {
	f, _ := findNumberFormat(prefsFrom(ctx).NumberFormat)
	whole := strconv.FormatInt(cents/100, 10)
	for i := len(whole) - 3; i > 0; i -= 3 {
		whole = whole[:i] + f.Thousands + whole[i:]
	}

	return fmt.Sprintf("%s%s%02d", whole, f.Decimal, cents%100)
}

// parseAmount reads an amount typed in the request's Formato numérico, as "1.234,56", as cents; empty is zero.
// Thousands separators must split the whole part in groups of three, so "1,50" typed for "1.234,56" fails
// instead of reading as 150.
func parseAmount(ctx context.Context, s string) (int64, bool) {
	f, _ := findNumberFormat(prefsFrom(ctx).NumberFormat)
	whole, frac, _ := strings.Cut(s, f.Decimal)
	groups := strings.Split(whole, f.Thousands)
	if len(frac) > 2 || slices.ContainsFunc(groups[1:], func(g string) bool { return len(g) != 3 }) {
		return 0, false
	}

	cents, err := strconv.ParseInt(strings.Join(groups, "")+(frac + "00")[:2], 10, 64)

	return cents, err == nil
}
