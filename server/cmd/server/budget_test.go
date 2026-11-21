package main

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// budgetForm is the division sheet's form: the Receita mensal, the input mode and a limit per category name.
func (w *webClient) budgetForm(income, mode string, limits map[string]string) url.Values {
	form := url.Values{"income": {income}, "mode": {mode}}
	for name, limit := range limits {
		form.Set("limit_"+w.categories[name], limit)
	}

	return form
}

// limits are the monthly limits in cents the UPDATE ops carry, by category name.
func (w *webClient) limitUpdates() map[string]float64 {
	w.t.Helper()
	names := map[string]string{}
	for name, id := range w.categories {
		names[id] = name
	}

	limits := map[string]float64{}
	for _, o := range w.newOps() {
		if limit, ok := o.Payload["monthly_limit"].(float64); ok && o.Entity == "CATEGORY" {
			limits[names[o.EntityID]] = limit
		}
	}

	return limits
}

func TestWebBudget_SavesTheDivisionTypedInValue(t *testing.T) {
	w := newWebClient(t)

	w.mustSubmit(http.MethodPost, "/budget", w.budgetForm("1.000,00", "value",
		map[string]string{"Gastos fixos": "500,00", "Investimento": "200,00", "Meta": "100,00", "Livre": "200,00"}))

	got := w.limitUpdates()
	if got["Gastos fixos"] != 50000 || got["Investimento"] != 20000 || got["Meta"] != 10000 || got["Livre"] != 20000 {
		t.Errorf("limits after save = %v, want the typed values in cents", got)
	}

	sheet := get(w.handler, "/budget", w.cookie)
	if sheet.Code != http.StatusOK || !strings.Contains(sheet.Body.String(), `value="1.000,00"`) {
		t.Errorf("sheet = %d, want the saved Receita mensal: %s", sheet.Code, sheet.Body)
	}
}

func TestWebBudget_SavesTheDivisionTypedInPercent(t *testing.T) {
	w := newWebClient(t)

	w.mustSubmit(http.MethodPost, "/budget", w.budgetForm("1.000,00", "percent",
		map[string]string{"Gastos fixos": "50,00", "Investimento": "20,00", "Meta": "10,00", "Livre": "20,00"}))

	got := w.limitUpdates()
	if got["Gastos fixos"] != 50000 || got["Investimento"] != 20000 || got["Meta"] != 10000 || got["Livre"] != 20000 {
		t.Errorf("limits after save = %v, want the percentages of the Receita mensal in cents", got)
	}
}

func TestWebBudget_PercentRemainderGoesToTheDefaultCategory(t *testing.T) {
	w := newWebClient(t)

	w.mustSubmit(http.MethodPost, "/budget", w.budgetForm("1,00", "percent",
		map[string]string{"Gastos fixos": "33,33", "Investimento": "33,33", "Meta": "33,34", "Livre": "0,00"}))

	got := w.limitUpdates()
	if got["Gastos fixos"] != 33 || got["Investimento"] != 33 || got["Meta"] != 33 || got["Livre"] != 1 {
		t.Errorf("limits after save = %v, want the cent lost to rounding on Livre", got)
	}
}

func TestWebBudget_DivisionThatDoesNotCloseIs422AndSavesNothing(t *testing.T) {
	w := newWebClient(t)

	for name, form := range map[string]url.Values{
		"short in value": w.budgetForm("1.000,00", "value", map[string]string{"Gastos fixos": "500,00", "Investimento": "200,00", "Meta": "100,00", "Livre": "199,99"}),
		"over in value":  w.budgetForm("1.000,00", "value", map[string]string{"Gastos fixos": "500,00", "Investimento": "200,00", "Meta": "100,00", "Livre": "200,01"}),
		"short in %":     w.budgetForm("1.000,00", "percent", map[string]string{"Gastos fixos": "50,00", "Investimento": "20,00", "Meta": "10,00", "Livre": "19,99"}),
		"% without pay":  w.budgetForm("", "percent", map[string]string{"Gastos fixos": "100,00", "Investimento": "0,00", "Meta": "0,00", "Livre": "0,00"}),
	} {
		response := w.submit(http.MethodPost, "/budget", form)
		if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), `aria-invalid`) && !strings.Contains(response.Body.String(), "role=\"alert\"") {
			t.Errorf("%s = %d, want 422 with the error in the form: %s", name, response.Code, response.Body)
		}
	}

	if ops := w.newOps(); len(ops) != 0 {
		t.Errorf("ops after rejected divisions = %+v, want none", ops)
	}

	if body := get(w.handler, "/budget", w.cookie).Body.String(); strings.Contains(body, `value="1.000,00"`) {
		t.Errorf("sheet after rejected divisions shows a saved Receita mensal: %s", body)
	}
}

// definedBudget saves a Receita mensal of R$ 1.000,00 divided 500, 200, 100 and 200, and drains the ops.
func definedBudget(t *testing.T) *webClient {
	t.Helper()
	w := newWebClient(t)
	w.mustSubmit(http.MethodPost, "/budget", w.budgetForm("1.000,00", "value",
		map[string]string{"Gastos fixos": "500,00", "Investimento": "200,00", "Meta": "100,00", "Livre": "200,00"}))
	w.newOps()

	return w
}

func TestWebBudget_ChangingTheIncomeRescalesTheLimitsInProportion(t *testing.T) {
	w := definedBudget(t)

	w.mustSubmit(http.MethodPost, "/budget", w.budgetForm("2.000,00", "value",
		map[string]string{"Gastos fixos": "500,00", "Investimento": "200,00", "Meta": "100,00", "Livre": "200,00"}))

	got := w.limitUpdates()
	if got["Gastos fixos"] != 100000 || got["Investimento"] != 40000 || got["Meta"] != 20000 || got["Livre"] != 40000 {
		t.Errorf("limits after doubling the income = %v, want every limit doubled", got)
	}
}

func TestWebBudget_RescaleRemainderGoesToTheDefaultCategory(t *testing.T) {
	w := definedBudget(t)

	w.mustSubmit(http.MethodPost, "/budget", w.budgetForm("10,01", "value",
		map[string]string{"Gastos fixos": "500,00", "Investimento": "200,00", "Meta": "100,00", "Livre": "200,00"}))

	got := w.limitUpdates()
	if got["Gastos fixos"] != 500 || got["Investimento"] != 200 || got["Meta"] != 100 || got["Livre"] != 201 {
		t.Errorf("limits after rescaling = %v, want the rounding cent on Livre", got)
	}
}

func TestWebBudget_FirstIncomeOverNoLimitsGoesToTheDefaultCategory(t *testing.T) {
	w := newWebClient(t)

	w.mustSubmit(http.MethodPost, "/budget", w.budgetForm("1.000,00", "value",
		map[string]string{"Gastos fixos": "0,00", "Investimento": "0,00", "Meta": "0,00", "Livre": "0,00"}))

	if got := w.limitUpdates(); len(got) != 1 || got["Livre"] != 100000 {
		t.Errorf("limits after the first income = %v, want all of it on Livre", got)
	}
}

func TestWebBudget_WithoutIncomeTheLimitsAreLoose(t *testing.T) {
	w := newWebClient(t)

	w.mustSubmit(http.MethodPost, "/budget", w.budgetForm("", "value",
		map[string]string{"Gastos fixos": "123,00", "Investimento": "0,00", "Meta": "0,00", "Livre": "7,00"}))

	if got := w.limitUpdates(); got["Gastos fixos"] != 12300 || got["Livre"] != 700 {
		t.Errorf("loose limits = %v, want them saved as typed", got)
	}

	w.mustSubmit(http.MethodPost, "/categories/"+w.categories["Meta"], categoryForm("Meta", "YELLOW", "9,00"))
	if got := w.limitUpdates(); got["Meta"] != 900 {
		t.Errorf("category form without income = %v, want its limit saved", got)
	}
}

func TestWebBudget_NewCategoryEntersWithZeroAndEditingKeepsTheLimit(t *testing.T) {
	w := definedBudget(t)

	w.mustSubmit(http.MethodPost, "/categories", categoryForm("Mercado", "GREEN", "50,00"))
	ops := w.newOps()
	if len(ops) != 1 || ops[0].Payload["monthly_limit"] != float64(0) {
		t.Fatalf("ops after creating = %+v, want one CREATE with limit 0", ops)
	}

	w.mustSubmit(http.MethodPost, "/categories/"+w.categories["Meta"], categoryForm("Metas", "YELLOW", "999,00"))
	ops = w.newOps()
	if len(ops) != 1 || ops[0].Payload["name"] != "Metas" || ops[0].Payload["monthly_limit"] != nil {
		t.Errorf("ops after editing = %+v, want the name changed and the limit left to the division", ops)
	}
}

func TestWebBudget_DeletedCategoryLimitGoesToTheDefaultCategory(t *testing.T) {
	w := definedBudget(t)

	w.mustSubmit(http.MethodDelete, "/categories/"+w.categories["Meta"], nil)

	updates := map[string]float64{}
	for _, o := range w.newOps() {
		if o.Entity == "CATEGORY" && o.EntityID == w.defaultID && o.Kind == "UPDATE" {
			updates["Livre"] = o.Payload["monthly_limit"].(float64)
		}
	}

	if updates["Livre"] != 30000 {
		t.Errorf("Livre limit after deleting Meta = %v, want 200 + 100 in cents", updates)
	}
}

func TestPush_KeepsTheDivisionOfADefinedIncome(t *testing.T) {
	w := definedBudget(t)

	raise := w.op("CATEGORY", w.categories["Meta"], "UPDATE", map[string]any{"monthly_limit": 99900, "updated_at": "2026-08-24T10:00:00Z"})
	if pushed := w.push(newCategory(w.client), raise); len(pushed.Rejected) != 0 {
		t.Fatalf("push = %+v, want both accepted", pushed)
	}

	ops := w.newOps()
	if len(ops) != 2 || ops[0].Payload["monthly_limit"] != float64(0) || ops[1].Payload["monthly_limit"] != float64(10000) {
		t.Errorf("pulled ops = %+v, want the new category with 0 and Meta kept at 100 in cents", ops)
	}

	if body := get(w.handler, "/budget", w.cookie).Body.String(); !strings.Contains(body, "fechado em 100%") {
		t.Errorf("sheet after push = %s, want the division still closed", body)
	}
}

func TestWebBudget_SwitchingTheModeKeepsWhatWasTyped(t *testing.T) {
	w := newWebClient(t)
	form := w.budgetForm("1.000,00", "percent", map[string]string{"Gastos fixos": "500,00", "Investimento": "200,00", "Meta": "100,00", "Livre": "100,00"})

	sheet := get(w.handler, "/budget?"+form.Encode(), w.cookie).Body.String()
	for _, want := range []string{`value="1.000,00"`, `value="50,00"`, `value="20,00"`, `value="10,00"`} {
		if !strings.Contains(sheet, want) {
			t.Errorf("sheet in %% = %s, want %s", sheet, want)
		}
	}

	if !strings.Contains(sheet, "10,00%") {
		t.Errorf("sheet in %% = %s, want the 10%% still missing", sheet)
	}
}
