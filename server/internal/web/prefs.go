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

// visitorSession holds the Formato de data, Formato numérico and Moeda that pages without a session follow; their
// Idioma comes from visitor.
var visitorSession = store.Session{Preferences: store.Preferences{DateFormat: "DD/MM/YYYY", NumberFormat: "1.234,56"}, Currency: "BRL"}

// language is an Idioma: its schema value and its name, written in itself.
type language struct{ Value, Label string }

var languages = []language{{"pt-BR", "Português (Brasil)"}, {"en", "English"}}

func isLanguage(value string) bool {
	return slices.ContainsFunc(languages, func(l language) bool { return l.Value == value })
}

// dateFormat is a Formato de data: its schema value, whose label is the i18n key "date_format." + Value, and its Go
// layout.
type dateFormat struct{ Value, Layout string }

var dateFormats = []dateFormat{{"DD/MM/YYYY", "02/01/2006"}, {"MM/DD/YYYY", "01/02/2006"}, {"YYYY-MM-DD", "2006-01-02"}}

// numberFormat is a Formato numérico: its schema value, which is also its label, and its separators.
type numberFormat struct{ Value, Thousands, Decimal string }

var numberFormats = []numberFormat{{"1.234,56", ".", ","}, {"1,234.56", ",", "."}}

// currency is a Moeda: its schema value and its symbol.
type currency struct{ Value, Symbol string }

var currencies = []currency{{"BRL", "R$"}, {"USD", "$"}, {"EUR", "€"}}

func findCurrency(value string) (currency, bool) {
	i := slices.IndexFunc(currencies, func(c currency) bool { return c.Value == value })
	if i < 0 {
		return currency{}, false
	}

	return currencies[i], true
}

// currencySymbol is the symbol of the request's Moeda, as "R$".
func currencySymbol(ctx context.Context) string {
	c, _ := findCurrency(sessionFrom(ctx).Currency)

	return c.Symbol
}

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
	f, _ := findDateFormat(sessionFrom(ctx).DateFormat)

	return f.Layout
}

// clock writes t as the status bar clock: the date in the Formato de data and the time in 24h.
func clock(ctx context.Context, t time.Time) string {
	return t.Format(dateLayout(ctx) + " 15:04:05")
}

// money writes signed cents with the request's Moeda symbol in its Formato numérico, as "−R$ 1.234,56".
func money(ctx context.Context, cents int64) string {
	if cents < 0 {
		return "−" + currencySymbol(ctx) + " " + amount(ctx, -cents)
	}

	return currencySymbol(ctx) + " " + amount(ctx, cents)
}

// amount writes non-negative cents in the request's Formato numérico, as "1.234,56", for display and form fields.
func amount(ctx context.Context, cents int64) string {
	f, _ := findNumberFormat(sessionFrom(ctx).NumberFormat)
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
	f, _ := findNumberFormat(sessionFrom(ctx).NumberFormat)
	whole, frac, _ := strings.Cut(s, f.Decimal)
	groups := strings.Split(whole, f.Thousands)
	if len(frac) > 2 || slices.ContainsFunc(groups[1:], func(g string) bool { return len(g) != 3 }) {
		return 0, false
	}

	cents, err := strconv.ParseInt(strings.Join(groups, "")+(frac + "00")[:2], 10, 64)

	return cents, err == nil
}
