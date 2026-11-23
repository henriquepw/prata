package main

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// welcomeForm is the renda e categorias form: the Receita mensal, the percentages of the Divisão sugerida and the
// Modo do limite diário.
func (w *webClient) welcomeForm(income, mode string) url.Values {
	form := w.budgetForm(income, "percent", map[string]string{"Gastos fixos": "50,00", "Investimento": "20,00", "Meta": "15,00", "Livre": "15,00"})
	form.Set("daily_limit_mode", mode)

	return form
}

func TestWebWelcome_ShowsTheSuggestedDivisionAndDynamicModeWithoutTheAppNavigation(t *testing.T) {
	w := newWebClient(t)

	body := get(w.handler, "/welcome", w.cookie).Body.String()
	for _, text := range []string{"Gastos fixos", "Investimento", "Meta", "Livre", `value="50,00"`, `value="20,00"`, `value="15,00"`,
		`name="income"`, `name="daily_limit_mode"`, `value="DYNAMIC" checked`, `data-mode="percent"`, `href="/"`} {
		if !strings.Contains(body, text) {
			t.Errorf("welcome lacks %q: %s", text, body)
		}
	}

	if strings.Contains(body, `href="/devices"`) {
		t.Errorf("welcome has the app navigation: %s", body)
	}
}

func TestWebWelcome_WithoutSessionRedirectsToSetup(t *testing.T) {
	handler := newHandler(openStore(t), config{})

	assertRedirect(t, get(handler, "/welcome"), "/setup")
	assertRedirect(t, postForm(handler, "/welcome", url.Values{}), "/setup")
}

func TestWebWelcome_SavesTheDivisionThatClosesTheIncomeWithTheRemainderOnLivreAndTheMode(t *testing.T) {
	w := newWebClient(t)

	response := postForm(w.handler, "/welcome", w.welcomeForm("1.000,01", "FIXED"), w.cookie)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/" {
		t.Fatalf("welcome save = %d to %q, want %d to /: %s", response.Code, response.Header().Get("Location"), http.StatusSeeOther, response.Body)
	}

	w.assertBody("/categories", `value="1.000,01"`, "R$ 500,00", "R$ 200,00", "R$ 150,00", "R$ 150,01", "fechado em 100%")
	w.assertSelectedDailyMode("FIXED")
}

func TestWebWelcome_WithoutIncomeIs422AndSavesNothing(t *testing.T) {
	w := newWebClient(t)

	for _, income := range []string{"", "0,00"} {
		response := postForm(w.handler, "/welcome", w.welcomeForm(income, "FIXED"), w.cookie)
		if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "Informe uma receita maior que zero.") {
			t.Errorf("welcome save with income %q = %d, want %d with the income message: %s", income, response.Code, http.StatusUnprocessableEntity, response.Body)
		}
	}

	w.assertSelectedDailyMode("DYNAMIC")
	if body := get(w.handler, "/categories", w.cookie).Body.String(); !strings.Contains(body, "defina a receita mensal") {
		t.Errorf("categories after the refused saves = %s, want no income", body)
	}
}

func TestWebWelcome_UnknownModeIs422AndSavesNothing(t *testing.T) {
	w := newWebClient(t)

	if response := postForm(w.handler, "/welcome", w.welcomeForm("1.000,00", "WEEKLY"), w.cookie); response.Code != http.StatusUnprocessableEntity {
		t.Errorf("welcome save with unknown mode = %d, want %d", response.Code, http.StatusUnprocessableEntity)
	}

	if body := get(w.handler, "/categories", w.cookie).Body.String(); !strings.Contains(body, "defina a receita mensal") {
		t.Errorf("categories after the refused save = %s, want no income", body)
	}
}
