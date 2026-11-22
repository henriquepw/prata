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

	w.assertBody("/categories", "Receita mensal", `value="1.000,00"`, "fechado em 100%",
		`value="50,00"`, "R$ 500,00", `value="20,00"`, "R$ 200,00", `value="10,00"`, "R$ 100,00")
}

func TestWebBudget_WithoutIncomeTheScreenAsksForItAndHasNoLimitFields(t *testing.T) {
	w := newWebClient(t)

	body := get(w.handler, "/categories", w.cookie).Body.String()
	if !strings.Contains(body, `name="income"`) || !strings.Contains(body, "Gastos fixos") {
		t.Errorf("screen without income = %s, want the income field and the categories", body)
	}

	if strings.Contains(body, `type="range"`) || strings.Contains(body, "Gastos fixos em %") || strings.Contains(body, "Gastos fixos em valor") {
		t.Errorf("screen without income = %s, want no limit slider nor field", body)
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
		"short in value":                    w.budgetForm("1.000,00", "value", map[string]string{"Gastos fixos": "500,00", "Investimento": "200,00", "Meta": "100,00", "Livre": "199,99"}),
		"over in value":                     w.budgetForm("1.000,00", "value", map[string]string{"Gastos fixos": "500,00", "Investimento": "200,00", "Meta": "100,00", "Livre": "200,01"}),
		"short in %":                        w.budgetForm("1.000,00", "percent", map[string]string{"Gastos fixos": "50,00", "Investimento": "20,00", "Meta": "10,00", "Livre": "19,99"}),
		"% without pay":                     w.budgetForm("", "percent", map[string]string{"Gastos fixos": "100,00"}),
		"zero pay":                          w.budgetForm("0,00", "percent", map[string]string{"Gastos fixos": "100,00"}),
		"first pay in value over no limits": w.budgetForm("1.000,00", "value", map[string]string{}),
	} {
		response := w.submit(http.MethodPost, "/budget", form)
		body := response.Body.String()
		if response.Code != http.StatusUnprocessableEntity || !strings.Contains(body, `role="alert"`) || !strings.Contains(body, "Receita mensal") {
			t.Errorf("%s = %d, want 422 with both panels and the error: %s", name, response.Code, body)
		}

		if typed := form.Get("limit_" + w.categories["Gastos fixos"]); typed != "" && !strings.Contains(body, `value="`+typed+`"`) {
			t.Errorf("%s = %s, want what was typed back", name, body)
		}
	}

	if ops := w.newOps(); len(ops) != 0 {
		t.Errorf("ops after rejected divisions = %+v, want none", ops)
	}

	if body := get(w.handler, "/categories", w.cookie).Body.String(); strings.Contains(body, `value="1.000,00"`) {
		t.Errorf("screen after rejected divisions shows a saved Receita mensal: %s", body)
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

func TestWebBudget_ChangingTheIncomeInPercentKeepsThePercents(t *testing.T) {
	w := definedBudget(t)

	w.mustSubmit(http.MethodPost, "/budget", w.budgetForm("2.000,00", "percent",
		map[string]string{"Gastos fixos": "50,00", "Investimento": "20,00", "Meta": "10,00", "Livre": "20,00"}))

	got := w.limitUpdates()
	if got["Gastos fixos"] != 100000 || got["Investimento"] != 40000 || got["Meta"] != 20000 || got["Livre"] != 40000 {
		t.Errorf("limits after doubling the income = %v, want every limit doubled", got)
	}
}

func TestWebBudget_RescaleRemainderInPercentGoesToTheDefaultCategory(t *testing.T) {
	w := definedBudget(t)

	w.mustSubmit(http.MethodPost, "/budget", w.budgetForm("10,01", "percent",
		map[string]string{"Gastos fixos": "50,00", "Investimento": "20,00", "Meta": "10,00", "Livre": "20,00"}))

	got := w.limitUpdates()
	if got["Gastos fixos"] != 500 || got["Investimento"] != 200 || got["Meta"] != 100 || got["Livre"] != 201 {
		t.Errorf("limits after rescaling = %v, want the rounding cent on Livre", got)
	}
}

func TestWebBudget_ChangingTheIncomeInValueKeepsTheValuesAndMustClose(t *testing.T) {
	w := definedBudget(t)

	response := w.submit(http.MethodPost, "/budget", w.budgetForm("2.000,00", "value",
		map[string]string{"Gastos fixos": "500,00", "Investimento": "200,00", "Meta": "100,00", "Livre": "200,00"}))
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "R$ 1.000,00") {
		t.Errorf("new income in value = %d, want 422 with R$ 1.000,00 missing: %s", response.Code, response.Body)
	}

	if ops := w.newOps(); len(ops) != 0 {
		t.Errorf("ops after a new income in value = %+v, want none", ops)
	}
}

func TestWebBudget_FirstIncomeInPercentOverNoLimitsGoesToTheDefaultCategory(t *testing.T) {
	w := newWebClient(t)

	w.mustSubmit(http.MethodPost, "/budget", w.budgetForm("1.000,00", "percent", map[string]string{}))

	if got := w.limitUpdates(); len(got) != 1 || got["Livre"] != 100000 {
		t.Errorf("limits after the first income = %v, want all of it on Livre", got)
	}
}

func TestWebBudget_CategorySheetHasNoLimitAndNeverWritesIt(t *testing.T) {
	for name, w := range map[string]*webClient{"without income": newWebClient(t), "with income": definedBudget(t)} {
		if body := get(w.handler, "/categories/"+w.categories["Meta"], w.cookie).Body.String(); strings.Contains(body, "monthly_limit") || strings.Contains(body, "Limite mensal") {
			t.Errorf("%s: category sheet = %s, want no monthly limit", name, body)
		}

		w.mustSubmit(http.MethodPost, "/categories", categoryForm("Mercado", "GREEN", "50,00"))
		ops := w.newOps()
		if len(ops) != 1 || ops[0].Payload["monthly_limit"] != float64(0) {
			t.Errorf("%s: ops after creating = %+v, want one CREATE with limit 0", name, ops)
		}

		w.mustSubmit(http.MethodPost, "/categories/"+w.categories["Meta"], categoryForm("Metas", "YELLOW", "999,00"))
		ops = w.newOps()
		if len(ops) != 1 || ops[0].Payload["name"] != "Metas" || ops[0].Payload["monthly_limit"] != nil {
			t.Errorf("%s: ops after editing = %+v, want the name changed and the limit left to the division", name, ops)
		}
	}
}

func TestWebBudget_NewCategoryShowsUpInTheDivision(t *testing.T) {
	w := definedBudget(t)

	w.mustSubmit(http.MethodPost, "/categories", categoryForm("Mercado", "GREEN", ""))

	w.assertBody("/categories", "Mercado em %", "fechado em 100%")
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

func TestWebBudget_TheDivisionSheetIsGone(t *testing.T) {
	w := definedBudget(t)

	if code := get(w.handler, "/budget", w.cookie).Code; code == http.StatusOK {
		t.Errorf("GET /budget = %d, want it gone", code)
	}

	if body := get(w.handler, "/categories", w.cookie).Body.String(); strings.Contains(body, "dividir receita") {
		t.Errorf("screen = %s, want no dividir receita action", body)
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

	w.assertBody("/categories", "fechado em 100%")
}
