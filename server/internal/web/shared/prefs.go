package shared

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/henriquepw/prata/server/internal/i18n"
	"github.com/henriquepw/prata/server/internal/importfile"
	"github.com/henriquepw/prata/server/internal/store"
)

// VisitorSession holds the Formato de data, Formato numérico and Moeda that pages without a session follow; their
// Idioma comes from visitor.
var VisitorSession = store.Session{DateFormat: "DD/MM/YYYY", NumberFormat: "1.234,56", Currency: importfile.CurrencyBRL}

// Option is a choice of a settings or form field: its schema value.
type Option struct{ Value string }

func (o Option) value() string { return o.Value }

// Find is the item of items whose schema value is value.
func Find[T interface{ value() string }](items []T, value string) (T, bool) {
	i := slices.IndexFunc(items, func(item T) bool { return item.value() == value })
	if i < 0 {
		var zero T
		return zero, false
	}

	return items[i], true
}

// Language is an Idioma: its schema value and its name, written in itself.
type Language struct {
	Option
	Label string
}

var Languages = []Language{{Option{i18n.PortugueseBR}, "Português (Brasil)"}, {Option{i18n.English}, "English"}}

// DateFormat is a Formato de data: its schema value, whose label is the i18n key "date_format." + Value, and its Go
// layout.
type DateFormat struct {
	Option
	Layout string
}

var DateFormats = []DateFormat{{Option{"DD/MM/YYYY"}, "02/01/2006"}, {Option{"MM/DD/YYYY"}, "01/02/2006"}, {Option{"YYYY-MM-DD"}, "2006-01-02"}}

// NumberFormat is a Formato numérico: its schema value, which is also its label, and its separators.
type NumberFormat struct {
	Option
	Thousands, Decimal string
}

var NumberFormats = []NumberFormat{{Option{"1.234,56"}, ".", ","}, {Option{"1,234.56"}, ",", "."}}

// The Modos do limite diário: fixed, the same limit every day; dynamic, what is left over the days left.
const (
	DailyLimitFixed   = "FIXED"
	DailyLimitDynamic = "DYNAMIC"
)

// DailyLimitModes are the Modos do limite diário, whose labels are the i18n keys "daily_limit_mode." + Value.
var DailyLimitModes = []Option{{DailyLimitFixed}, {DailyLimitDynamic}}

// Currency is a Moeda: its schema value and its symbol.
type Currency struct {
	Option
	Symbol string
}

var Currencies = []Currency{{Option{importfile.CurrencyBRL}, "R$"}, {Option{"USD"}, "$"}, {Option{"EUR"}, "€"}}

// CurrencySymbol is the symbol of the request's Moeda, as "R$".
func CurrencySymbol(ctx context.Context) string {
	c, _ := Find(Currencies, SessionFrom(ctx).Currency)

	return c.Symbol
}

// DateLayout is the Go layout of the request's Formato de data, as "02/01/2006".
func DateLayout(ctx context.Context) string {
	f, _ := Find(DateFormats, SessionFrom(ctx).DateFormat)

	return f.Layout
}

// Clock writes t as the status bar clock: the date in the Formato de data and the time in 24h.
func Clock(ctx context.Context, t time.Time) string {
	return t.Format(DateLayout(ctx) + " 15:04:05")
}

// Money writes signed cents with the request's Moeda symbol in its Formato numérico, as "−R$ 1.234,56".
func Money(ctx context.Context, cents int64) string {
	if cents < 0 {
		return "−" + CurrencySymbol(ctx) + " " + Amount(ctx, -cents)
	}

	return CurrencySymbol(ctx) + " " + Amount(ctx, cents)
}

// Amount writes non-negative cents in the request's Formato numérico, as "1.234,56", for display and form fields.
func Amount(ctx context.Context, cents int64) string {
	f, _ := Find(NumberFormats, SessionFrom(ctx).NumberFormat)
	whole := strconv.FormatInt(cents/100, 10)
	for i := len(whole) - 3; i > 0; i -= 3 {
		whole = whole[:i] + f.Thousands + whole[i:]
	}

	return fmt.Sprintf("%s%s%02d", whole, f.Decimal, cents%100)
}

// ParseAmount reads an amount typed in the request's Formato numérico, as "1.234,56", as cents; empty is zero.
// Thousands separators must split the whole part in groups of three, so "1,50" typed for "1.234,56" fails
// instead of reading as 150.
func ParseAmount(ctx context.Context, s string) (int64, bool) {
	f, _ := Find(NumberFormats, SessionFrom(ctx).NumberFormat)
	whole, frac, _ := strings.Cut(s, f.Decimal)
	groups := strings.Split(whole, f.Thousands)
	if len(frac) > 2 || slices.ContainsFunc(groups[1:], func(g string) bool { return len(g) != 3 }) {
		return 0, false
	}

	cents, err := strconv.ParseInt(strings.Join(groups, "")+(frac + "00")[:2], 10, 64)

	return cents, err == nil
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
