package web

import (
	"errors"
	"net/http"

	"github.com/henriquepw/prata/server/internal/i18n"
	"github.com/henriquepw/prata/server/internal/store"
)

// suggestedDivision is the Divisão sugerida: the hundredths of a percent of the Receita mensal that go to each
// initial category. Nothing of it is stored as a percentage; the screen only fills the form with it.
var suggestedDivision = map[string]int64{"Gastos fixos": 5000, "Investimento": 2000, "Meta": 1500, "Livre": 1500}

// welcome is the tela inicial after a signup: the division form in percent, filled with the Divisão sugerida, and the
// Modo do limite diário, dynamic.
func (h handler) welcome(w http.ResponseWriter, r *http.Request) {
	categories, ok := h.liveCategories(w, r)
	if !ok {
		return
	}

	form := newBudgetForm(categories, budgetInPercent)
	form.Welcome = true
	for _, category := range categories {
		form.Points[category.ID] = suggestedDivision[category.Name]
		form.Percents[category.ID] = amount(r.Context(), form.Points[category.ID])
	}

	render(w, r, http.StatusOK, welcomePage(form, dailyLimitDynamic))
}

// saveWelcome stores the Receita mensal, the division and the Modo do limite diário and goes home, or answers 422 with
// the form and stores nothing when the Receita mensal is not above zero, a value is invalid or the division does not
// close it.
func (h handler) saveWelcome(w http.ResponseWriter, r *http.Request) {
	categories, ok := h.liveCategories(w, r)
	if !ok {
		return
	}

	form, budget := parseBudget(r, categories)
	form.Welcome = true
	mode := r.PostFormValue("daily_limit_mode")
	if _, ok := find(dailyLimitModes, mode); !ok {
		form.Errors["daily_limit_mode"] = i18n.T(r.Context(), "settings.error.daily_limit_mode")
	}

	if len(form.Errors) == 0 {
		session := sessionFrom(r.Context())
		err := h.db.SaveBudget(r.Context(), session, budget)
		if err != nil && !errors.Is(err, store.ErrDivisionNotClosed) {
			internalError(w, "saving budget", err)
			return
		}

		if err == nil {
			if err := h.db.SaveDailyLimitMode(r.Context(), session.UserID, mode); err != nil {
				internalError(w, "saving daily limit mode", err)
				return
			}

			redirect(w, r, "/")

			return
		}

		form.Errors["form"] = i18n.T(r.Context(), "budget.error.open")
	}

	render(w, r, http.StatusUnprocessableEntity, welcomePage(form, mode))
}
