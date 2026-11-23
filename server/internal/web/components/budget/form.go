// Package budget is the division form of the Receita mensal, shared by the Categorias and the welcome pages.
package budget

import (
	"cmp"
	"fmt"
	"net/http"

	"github.com/a-h/templ"
	"github.com/henriquepw/prata/server/internal/i18n"
	"github.com/henriquepw/prata/server/internal/store"
	"github.com/henriquepw/prata/server/internal/web/shared"
	"github.com/henriquepw/prata/server/internal/web/utils"
)

// Form is the division on the Categorias screen: the Receita mensal as typed and in cents, the Mode the limits
// are typed in ("value" or "percent"), each category's limit as the text of its value and percent fields and in
// cents and hundredths of a percent, and an error message per field name ("income", "limit_<id>", and "form" for a
// division that does not close the Receita mensal).
type Form struct {
	Income     string
	income     int64
	Mode       string
	Categories []store.Category
	Values     map[string]string
	Percents   map[string]string
	Cents      map[string]int64
	Points     map[string]int64
	Errors     map[string]string
	// Welcome is the form of the tela inicial: it posts to /welcome as a plain form, always shows the limit fields
	// and leaves the categories without a sheet.
	Welcome bool
}

// The Form modes: limits typed in value or in percent of the Receita mensal.
const (
	InValue   = "value"
	InPercent = "percent"
)

// NewForm is an empty form in mode over categories.
func NewForm(categories []store.Category, mode string) Form {
	return Form{Mode: mode, Categories: categories, Values: map[string]string{}, Percents: map[string]string{},
		Cents: map[string]int64{}, Points: map[string]int64{}, Errors: map[string]string{}}
}

func (f Form) percent() bool { return f.Mode == InPercent }

// gap is what is missing to close the Receita mensal, negative when it exceeds, in cents and in hundredths of a
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

	return utils.IfElse(f.percent(), sign(points), sign(cents))
}

// closed tells whether the division adds up exactly to a Receita mensal, in the typed mode.
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
	budget := store.Budget{Percent: form.percent(), Limits: map[string]int64{}}

	var incomeOK bool
	if form.income, incomeOK = shared.ParseAmount(r.Context(), form.Income); !incomeOK || form.income <= 0 {
		form.Errors["income"] = i18n.T(r.Context(), "budget.error.income")
	}

	budget.Income = form.income
	for _, category := range categories {
		typed := r.PostFormValue("limit_" + category.ID)
		limit, ok := shared.ParseAmount(r.Context(), typed)
		if !ok || limit < 0 {
			form.Errors["limit_"+category.ID] = i18n.T(r.Context(), "budget.error.limit")
		}

		budget.Limits[category.ID] = limit
		if form.percent() {
			form.Cents[category.ID], form.Points[category.ID] = form.income*limit/store.HundredPercent, limit
			form.Values[category.ID], form.Percents[category.ID] = shared.Amount(r.Context(), form.Cents[category.ID]), typed
		} else {
			form.Cents[category.ID], form.Points[category.ID] = limit, store.BasisPoints(limit, form.income)
			form.Values[category.ID], form.Percents[category.ID] = typed, shared.Amount(r.Context(), form.Points[category.ID])
		}
	}

	return form, budget
}

// Stored fills the screen with the stored Receita mensal and division, typed in percent.
func Stored(r *http.Request, categories []store.Category) Form {
	form := NewForm(categories, InPercent)
	if form.income = shared.SessionFrom(r.Context()).MonthlyIncome; form.income > 0 {
		form.Income = shared.Amount(r.Context(), form.income)
	}

	division := store.Division(categories)
	for _, category := range categories {
		form.Cents[category.ID], form.Points[category.ID] = category.MonthlyLimit, division[category.ID]
		form.Values[category.ID], form.Percents[category.ID] = shared.Amount(r.Context(), category.MonthlyLimit), shared.Amount(r.Context(), division[category.ID])
	}

	return form
}
