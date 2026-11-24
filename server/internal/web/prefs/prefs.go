package prefs

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/henriquepw/prata/server/internal/i18n"
	"github.com/henriquepw/prata/server/internal/importfile"
	"github.com/henriquepw/prata/server/internal/web/session"
)

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
	c, _ := Find(Currencies, session.From(ctx).Currency)

	return c.Symbol
}

// DateLayout is the Go layout of the request's Formato de data, as "02/01/2006".
func DateLayout(ctx context.Context) string {
	f, _ := Find(DateFormats, session.From(ctx).DateFormat)

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
	f, _ := Find(NumberFormats, session.From(ctx).NumberFormat)
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
	f, _ := Find(NumberFormats, session.From(ctx).NumberFormat)
	whole, frac, _ := strings.Cut(s, f.Decimal)
	groups := strings.Split(whole, f.Thousands)
	if len(frac) > 2 || slices.ContainsFunc(groups[1:], func(g string) bool { return len(g) != 3 }) {
		return 0, false
	}

	cents, err := strconv.ParseInt(strings.Join(groups, "")+(frac + "00")[:2], 10, 64)

	return cents, err == nil
}

// FormatDate writes a stored RFC 3339 time in loc with layout, or the raw value when it does not parse.
func FormatDate(stored string, loc *time.Location, layout string) string {
	t, err := time.Parse(time.RFC3339, stored)
	if err != nil {
		return stored
	}

	return t.In(loc).Format(layout)
}

// LocalTime writes an RFC 3339 time in loc in the request's Formato de data and 24h time, as "02/01/2006 15:04", or
// "nunca" when it is empty.
func LocalTime(ctx context.Context, value string, loc *time.Location) string {
	if value == "" {
		return i18n.T(ctx, "devices.never")
	}

	return FormatDate(value, loc, DateLayout(ctx)+" 15:04")
}
