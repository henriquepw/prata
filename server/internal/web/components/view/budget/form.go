// Package budget is the division form of the monthly income, shared by the Categories and the welcome pages.
package budget

import (
	"cmp"
	"fmt"
	"net/http"

	"github.com/a-h/templ"
	"github.com/henriquepw/prata/server/internal/l10n"
	"github.com/henriquepw/prata/server/internal/store"
	"github.com/henriquepw/prata/server/internal/web/components/ui/style"
	"github.com/henriquepw/prata/server/internal/web/prefs"
	"github.com/henriquepw/prata/server/internal/web/session"
	"github.com/henriquepw/prata/server/pkg/id"
)

// Form is the division on the Categories screen: the monthly income as typed and in cents, the Mode the limits
// are typed in ("value" or "percent"), each category's limit as the text of its value and percent fields and in
// cents and hundredths of a percent, and an error message per field name ("income", "limit_<id>", and "form" for a
// division that does not close the monthly income).
type Form struct {
	Income     string
	income     int64
	Mode       string
	Categories []store.Category
	Values     map[id.ID]string
	Percents   map[id.ID]string
	Cents      map[id.ID]int64
	Points     map[id.ID]int64
	Errors     map[string]string
	// Welcome is the form of the welcome screen: it posts to /welcome as a plain form, always shows the limit fields
	// and leaves the categories without a sheet.
	Welcome bool
}

// The Form modes: limits typed in value or in percent of the monthly income.
const (
	InValue   = "value"
	InPercent = "percent"
)

// NewForm is an empty form in mode over categories.
func NewForm(categories []store.Category, mode string) Form {
	return Form{Mode: mode, Categories: categories, Values: map[id.ID]string{}, Percents: map[id.ID]string{},
		Cents: map[id.ID]int64{}, Points: map[id.ID]int64{}, Errors: map[string]string{}}
}

func (f Form) percent() bool { return f.Mode == InPercent }

// gap is what is missing to close the monthly income, negative when it exceeds, in cents and in hundredths of a
// percent; the typed mode decides, and the other follows from it.
func (f Form) gap() (cents, points int64) {
	cents, points = f.income, store.HundredPercent
	for _, category := range f.Categories {
		cents -= f.Cents[category.ID]
		points -= f.Points[category.ID]
	}

	if f.percent() {
		return f.income * points / store.HundredPercent, points
	}

	if cents < 0 {
		return cents, -store.BasisPoints(-cents, f.income)
	}

	return cents, store.BasisPoints(cents, f.income)
}

// gapSign is the sign of the gap in the typed mode: 1 when something is missing, -1 when it exceeds.
func (f Form) gapSign() int64 {
	cents, points := f.gap()

	return style.IfElse(f.percent(), sign(points), sign(cents))
}

// closed tells whether the division adds up exactly to a monthly income, in the typed mode.
func (f Form) closed() bool {
	cents, points := f.gap()

	return f.income > 0 && (f.percent() && points == 0 || !f.percent() && cents == 0)
}

// width is the CSS width of a slice of the 100% bar, from hundredths of a percent; none below zero.
func width(points int64) templ.SafeCSS {
	return templ.SafeCSS(fmt.Sprintf("width: %.2f%%", float64(max(0, points))/100))
}

func sign(n int64) int64 { return int64(cmp.Compare(n, 0)) }

func abs(n int64) int64 { return max(n, -n) }

// Parse reads the division form: the typed form, with an error message per invalid field, and the Budget it
// stores when there is none.
func Parse(r *http.Request, categories []store.Category) (Form, store.Budget) {
	form := NewForm(categories, r.PostFormValue("mode"))
	if !form.percent() {
		form.Mode = InValue
	}

	form.Income = r.PostFormValue("income")
	budget := store.Budget{Percent: form.percent(), Limits: map[id.ID]int64{}}

	var incomeOK bool
	if form.income, incomeOK = prefs.ParseAmount(r.Context(), form.Income); !incomeOK || form.income <= 0 {
		form.Errors["income"] = l10n.T(r.Context(), "budget.error.income")
	}

	budget.Income = form.income
	for _, category := range categories {
		typed := r.PostFormValue("limit_" + category.ID.String())
		limit, ok := prefs.ParseAmount(r.Context(), typed)
		if !ok || limit < 0 {
			form.Errors["limit_"+category.ID.String()] = l10n.T(r.Context(), "budget.error.limit")
		}

		budget.Limits[category.ID] = limit
		if form.percent() {
			form.Cents[category.ID], form.Points[category.ID] = form.income*limit/store.HundredPercent, limit
			form.Values[category.ID], form.Percents[category.ID] = prefs.Amount(r.Context(), form.Cents[category.ID]), typed
		} else {
			form.Cents[category.ID], form.Points[category.ID] = limit, store.BasisPoints(limit, form.income)
			form.Values[category.ID], form.Percents[category.ID] = typed, prefs.Amount(r.Context(), form.Points[category.ID])
		}
	}

	return form, budget
}

// Stored fills the screen with the stored monthly income and division, typed in percent.
func Stored(r *http.Request, categories []store.Category) Form {
	form := NewForm(categories, InPercent)
	if form.income = session.From(r.Context()).MonthlyIncome; form.income > 0 {
		form.Income = prefs.Amount(r.Context(), form.income)
	}

	division := store.Division(categories)
	for _, category := range categories {
		form.Cents[category.ID], form.Points[category.ID] = category.MonthlyLimit, division[category.ID]
		form.Values[category.ID], form.Percents[category.ID] = prefs.Amount(r.Context(), category.MonthlyLimit), prefs.Amount(r.Context(), division[category.ID])
	}

	return form
}
