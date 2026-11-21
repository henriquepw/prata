package web

import (
	"context"
	"errors"
	"net/http"

	"github.com/henriquepw/prata/server/internal/i18n"
	"github.com/henriquepw/prata/server/internal/store"
)

// budgetForm is the division sheet: the Receita mensal and a limit per category id as typed, in Mode "value" or
// "percent", and an error message per field name ("income", "limit_<id>", "form" for a division that does not
// close). Gap is what is missing to close, negative when it exceeds, in cents or hundredths of a percent.
type budgetForm struct {
	Income     string
	Mode       string
	Categories []store.Category
	Limits     map[string]string
	Gap        int64
	Errors     map[string]string
}

func (f budgetForm) percent() bool { return f.Mode == "percent" }

func (h handler) budget(w http.ResponseWriter, r *http.Request) {
	categories, ok := h.liveCategories(w, r)
	if !ok {
		return
	}

	render(w, r, http.StatusOK, budgetSheet(storedBudgetForm(r.Context(), categories, r.URL.Query().Get("mode"))))
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
		form.Gap = 10000 - typed
	}

	if len(form.Errors) == 0 {
		err := h.db.SaveBudget(r.Context(), sessionFrom(r.Context()), budget)
		if err != nil && !errors.Is(err, store.ErrBudgetOpen) {
			internalError(w, "saving budget", err)
			return
		}

		if err == nil {
			w.Header().Set("HX-Refresh", "true")
			w.WriteHeader(http.StatusNoContent)

			return
		}

		form.Errors["form"] = i18n.T(r.Context(), "budget.error.open")
	}

	render(w, r, http.StatusUnprocessableEntity, budgetSheet(form))
}

// storedBudgetForm fills the sheet with the stored Receita mensal and limits, the limits as percentages of what they add
// up to when mode is "percent".
func storedBudgetForm(ctx context.Context, categories []store.Category, mode string) budgetForm {
	form := budgetForm{Mode: "value", Categories: categories, Limits: map[string]string{}, Errors: map[string]string{}}
	if mode == "percent" {
		form.Mode = mode
	}

	income := sessionFrom(ctx).MonthlyIncome
	if income > 0 {
		form.Income = amount(ctx, income)
	}

	var stored int64
	for _, category := range categories {
		stored += category.MonthlyLimit
	}

	form.Gap = income
	if form.percent() {
		form.Gap = 10000
	}

	for _, category := range categories {
		limit := category.MonthlyLimit
		if form.percent() {
			limit = store.BasisPoints(limit, stored)
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
