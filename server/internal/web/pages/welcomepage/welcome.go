// Package welcomepage is the tela inicial after a signup: the division form and the Modo do limite diário.
package welcomepage

import (
	"errors"
	"net/http"

	"github.com/henriquepw/prata/server/internal/i18n"
	"github.com/henriquepw/prata/server/internal/store"
	"github.com/henriquepw/prata/server/internal/web/components/budget"
	"github.com/henriquepw/prata/server/internal/web/form"
	"github.com/henriquepw/prata/server/internal/web/prefs"
	"github.com/henriquepw/prata/server/internal/web/session"
)

// Page is the welcome handlers.
type Page struct{ db *store.Store }

// New returns the welcome page over db.
func New(db *store.Store) Page { return Page{db: db} }

// suggestedDivision is the Divisão sugerida: the hundredths of a percent of the Receita mensal that go to each
// initial category. Nothing of it is stored as a percentage; the screen only fills the form with it.
var suggestedDivision = map[string]int64{"Gastos fixos": 5000, "Investimento": 2000, "Meta": 1500, "Livre": 1500}

// Welcome is the tela inicial after a signup: the division form in percent, filled with the Divisão sugerida, and the
// Modo do limite diário, dynamic.
func (p Page) Welcome(w http.ResponseWriter, r *http.Request) {
	categories, ok := form.LiveCategories(w, r, p.db)
	if !ok {
		return
	}

	form := budget.NewForm(categories, budget.InPercent)
	form.Welcome = true
	for _, category := range categories {
		form.Points[category.ID] = suggestedDivision[category.Name]
		form.Percents[category.ID] = prefs.Amount(r.Context(), form.Points[category.ID])
	}

	session.Render(w, r, http.StatusOK, welcomePage(form, prefs.DailyLimitDynamic))
}

// Save stores the Receita mensal, the division and the Modo do limite diário and goes home, or answers 422 with
// the form and stores nothing when the Receita mensal is not above zero, a value is invalid or the division does not
// close it.
func (p Page) Save(w http.ResponseWriter, r *http.Request) {
	categories, ok := form.LiveCategories(w, r, p.db)
	if !ok {
		return
	}

	form, division := budget.Parse(r, categories)
	form.Welcome = true
	mode := r.PostFormValue("daily_limit_mode")
	if _, ok := prefs.Find(prefs.DailyLimitModes, mode); !ok {
		form.Errors["daily_limit_mode"] = i18n.T(r.Context(), "settings.error.daily_limit_mode")
	}

	if len(form.Errors) == 0 {
		division.DailyLimitMode = mode
		err := p.db.SaveBudget(r.Context(), session.From(r.Context()), division)
		if err == nil {
			session.Redirect(w, r, "/")
			return
		}

		if !errors.Is(err, store.ErrDivisionNotClosed) {
			session.InternalError(w, "saving budget", err)
			return
		}

		form.Errors["form"] = i18n.T(r.Context(), "budget.error.open")
	}

	session.Render(w, r, http.StatusUnprocessableEntity, welcomePage(form, mode))
}
