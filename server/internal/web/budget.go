package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/henriquepw/prata/server/internal/i18n"
	"github.com/henriquepw/prata/server/internal/store"
)

// budgetForm is the division sheet: the Receita mensal and a limit per category id as typed, in Mode "value" or
// "percent", and an error message per field name ("income", also for a division that does not close the
// Receita mensal, and "limit_<id>"). Gap is what is missing to close, negative when it exceeds, in cents or hundredths of a percent.
type budgetForm struct {
	Income     string
	Mode       string
	Categories []store.Category
	Limits     map[string]string
	Gap        int64
	Errors     map[string]string
}

// The budgetForm modes: limits typed in value or in percent of the Receita mensal.
const (
	budgetInValue   = "value"
	budgetInPercent = "percent"
)

func (f budgetForm) percent() bool { return f.Mode == budgetInPercent }

func (h handler) budget(w http.ResponseWriter, r *http.Request) {
	categories, ok := h.liveCategories(w, r)
	if !ok {
		return
	}

	query := r.URL.Query()
	form := storedBudgetForm(r.Context(), categories, query.Get("mode"))
	if query.Has("income") {
		form = typedBudgetForm(r.Context(), form, query)
	}

	render(w, r, http.StatusOK, budgetSheet(form))
}

// saveBudget stores the Receita mensal and the division, or answers 422 with the form and stores nothing when a value
// is not an amount or the division does not close the Receita mensal.
func (h handler) saveBudget(w http.ResponseWriter, r *http.Request) {
	categories, ok := h.liveCategories(w, r)
	if !ok {
		return
	}

	form := budgetForm{Income: r.PostFormValue("income"), Mode: r.PostFormValue("mode"), Categories: categories, Limits: map[string]string{}, Errors: map[string]string{}}
	budget := store.Budget{Percent: form.percent(), Limits: map[string]int64{}}

	var incomeOK bool
	if budget.Income, incomeOK = parseAmount(r.Context(), form.Income); !incomeOK {
		form.Errors["income"] = i18n.T(r.Context(), "budget.error.income")
	}

	if form.percent() && budget.Income == 0 && form.Errors["income"] == "" {
		form.Errors["income"] = i18n.T(r.Context(), "budget.error.income_percent")
	}

	var typed int64
	for _, category := range categories {
		form.Limits[category.ID] = r.PostFormValue("limit_" + category.ID)

		limit, ok := parseAmount(r.Context(), form.Limits[category.ID])
		if !ok || limit < 0 {
			form.Errors["limit_"+category.ID] = i18n.T(r.Context(), "categories.error.monthly_limit")
		}

		budget.Limits[category.ID] = limit
		typed += limit
	}

	form.Gap = budget.Income - typed
	if form.percent() {
		form.Gap = store.HundredPercent - typed
	}

	if len(form.Errors) == 0 {
		err := h.db.SaveBudget(r.Context(), sessionFrom(r.Context()), budget)
		if err != nil && !errors.Is(err, store.ErrDivisionNotClosed) {
			internalError(w, "saving budget", err)
			return
		}

		if err == nil {
			w.Header().Set("HX-Refresh", "true")
			w.WriteHeader(http.StatusNoContent)

			return
		}

		form.Errors["income"] = i18n.T(r.Context(), "budget.error.open")
	}

	render(w, r, http.StatusUnprocessableEntity, budgetSheet(form))
}

// storedBudgetForm fills the sheet with the stored Receita mensal and limits, the limits as percentages of what they add
// up to when mode is "percent".
func storedBudgetForm(ctx context.Context, categories []store.Category, mode string) budgetForm {
	form := budgetForm{Mode: budgetInValue, Categories: categories, Limits: map[string]string{}, Errors: map[string]string{}}
	if mode == budgetInPercent {
		form.Mode = mode
	}

	income := sessionFrom(ctx).MonthlyIncome
	if income > 0 {
		form.Income = amount(ctx, income)
	}

	form.Gap = income
	if form.percent() {
		form.Gap = store.HundredPercent
	}

	division := store.Division(categories, form.percent())
	for _, category := range categories {
		form.Limits[category.ID] = amount(ctx, division[category.ID])
		form.Gap -= division[category.ID]
	}

	return form
}

// typedBudgetForm carries into form's mode the Receita mensal and the limits typed in the other mode: a limit in value
// becomes its percentage of the typed Receita mensal and a percentage its value; one that is not an amount stays as typed.
func typedBudgetForm(ctx context.Context, form budgetForm, typed url.Values) budgetForm {
	form.Income = typed.Get("income")
	income, _ := parseAmount(ctx, form.Income)

	form.Gap = income
	if form.percent() {
		form.Gap = store.HundredPercent
	}

	for _, category := range form.Categories {
		raw := typed.Get("limit_" + category.ID)
		limit, ok := parseAmount(ctx, raw)
		if !ok {
			form.Limits[category.ID] = raw
			continue
		}

		if form.percent() {
			limit = store.BasisPoints(limit, income)
		} else {
			limit = income * limit / store.HundredPercent
		}

		form.Limits[category.ID] = amount(ctx, limit)
		form.Gap -= limit
	}

	return form
}

// budgetAmount writes cents as money, or hundredths of a percent as a percentage, by the mode of the form.
func budgetAmount(ctx context.Context, form budgetForm, value int64) string {
	if form.percent() {
		if value < 0 {
			return "−" + amount(ctx, -value) + "%"
		}

		return amount(ctx, value) + "%"
	}

	return money(ctx, value)
}
