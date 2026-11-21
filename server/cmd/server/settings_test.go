package main

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func settingsForm(dateFormat, numberFormat string) url.Values {
	return url.Values{"language": {"pt-BR"}, "date_format": {dateFormat}, "number_format": {numberFormat}, "currency": {"BRL"}, "daily_limit_mode": {"DYNAMIC"}}
}

// withCurrency sets the form's Moeda.
func withCurrency(form url.Values, currency string) url.Values {
	form.Set("currency", currency)

	return form
}

// withLanguage sets the form's Idioma.
func withLanguage(form url.Values, language string) url.Values {
	form.Set("language", language)

	return form
}

// assertSelected checks /settings shows each value as the selected option.
func (w *webClient) assertSelected(values ...string) {
	w.t.Helper()
	body := w.submit(http.MethodGet, "/settings", nil).Body.String()
	for _, value := range values {
		if !strings.Contains(body, `value="`+value+`" selected`) {
			w.t.Errorf("settings lacks %q selected: %s", value, body)
		}
	}
}

func TestWebSettings_ShowsAndSavesTheFormats(t *testing.T) {
	w := newWebClient(t)

	page := w.submit(http.MethodGet, "/settings", nil)
	if body := page.Body.String(); page.Code != http.StatusOK || !strings.Contains(body, "Formato de data") || !strings.Contains(body, "Formato numérico") ||
		!strings.Contains(body, "DD/MM/AAAA") || !strings.Contains(body, "1,234.56") {
		t.Errorf("settings = %d, want both formats with their options: %s", page.Code, body)
	}

	w.assertSelected("DD/MM/YYYY", "1.234,56")

	w.mustSubmit(http.MethodPost, "/settings", settingsForm("YYYY-MM-DD", "1,234.56"))
	w.assertSelected("YYYY-MM-DD", "1,234.56")
}

func TestWebSettings_InvalidValueIs422AndSavesNothing(t *testing.T) {
	w := newWebClient(t)
	for name, form := range map[string]url.Values{
		"unknown date format":   settingsForm("DD.MM.YYYY", "1,234.56"),
		"unknown number format": settingsForm("MM/DD/YYYY", "1 234,56"),
		"unknown currency":      withCurrency(settingsForm("MM/DD/YYYY", "1,234.56"), "JPY"),
		"unknown language":      withLanguage(settingsForm("MM/DD/YYYY", "1,234.56"), "fr"),
	} {
		t.Run(name, func(t *testing.T) {
			response := w.submit(http.MethodPost, "/settings", form)
			if body := response.Body.String(); response.Code != http.StatusUnprocessableEntity || !strings.Contains(body, `hx-post="/settings"`) ||
				!strings.Contains(body, `aria-invalid="true"`) || strings.Contains(body, "<html") {
				t.Errorf("POST /settings = %d, want %d with the form flagging the field: %s", response.Code, http.StatusUnprocessableEntity, body)
			}

			w.assertSelected("pt-BR", "DD/MM/YYYY", "1.234,56", "BRL")
		})
	}
}

// assertBody checks the page at path is 200 and has every text.
func (w *webClient) assertBody(path string, texts ...string) {
	w.t.Helper()
	tz := &http.Cookie{Name: "tz", Value: "America/Sao_Paulo"}
	page := get(w.handler, path, w.cookie, tz)
	for _, text := range texts {
		if body := page.Body.String(); page.Code != http.StatusOK || !strings.Contains(body, text) {
			w.t.Errorf("%s = %d, want %q in: %s", path, page.Code, text, body)
		}
	}
}

// assertSettings checks the Configurações sheet, opened as htmx does, is 200 and has every text.
func (w *webClient) assertSettings(texts ...string) {
	w.t.Helper()
	sheet := w.submit(http.MethodGet, "/settings", nil)
	for _, text := range texts {
		if body := sheet.Body.String(); sheet.Code != http.StatusOK || !strings.Contains(body, text) {
			w.t.Errorf("/settings = %d, want %q in: %s", sheet.Code, text, body)
		}
	}
}

func TestWebSettings_AmountsAreTypedAndShownInTheNumberFormat(t *testing.T) {
	w := newWebClient(t)
	tz := &http.Cookie{Name: "tz", Value: "America/Sao_Paulo"}
	w.mustSubmit(http.MethodPost, "/settings", settingsForm("DD/MM/YYYY", "1,234.56"))

	w.mustSubmit(http.MethodPost, "/categories", categoryForm("Mercado", "GREEN", "1,234.56"))
	category := w.newOps()[0]
	w.mustSubmit(http.MethodPost, "/transactions", transactionForm("Feira", "OUTCOME", "1,234.56", "2026-07-15", "", ""), tz)
	transaction := w.newOps()[0]
	w.mustSubmit(http.MethodPost, "/recurrences", recurrenceForm("Aluguel", "OUTCOME", "1,500", "", "5", "2026-01-01", ""))
	recurrence := w.newOps()[0]

	if category.Payload["monthly_limit"] != float64(123456) || transaction.Payload["amount"] != float64(123456) || recurrence.Payload["amount"] != float64(150000) {
		t.Errorf("ops = %v, %v and %v, want 123456, 123456 and 150000 cents", category.Payload, transaction.Payload, recurrence.Payload)
	}

	w.assertBody("/categories", "R$ 1,234.56")
	w.assertBody("/categories/"+category.EntityID, `value="1,234.56"`, `placeholder="0.00"`)
	w.assertBody("/?month=2026-07", "−R$ 1,234.56")
	w.assertBody("/transactions/"+transaction.EntityID, `value="1,234.56"`)
	w.assertBody("/recurrences", "−R$ 1,500.00")
	w.assertBody("/recurrences/"+recurrence.EntityID, `value="1,500.00"`)

	for _, typed := range []string{"1.234,56", "1,50", "12.345"} {
		response := w.submit(http.MethodPost, "/transactions", transactionForm("Feira", "OUTCOME", typed, "2026-07-15", "", ""), tz)
		if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "Informe um valor maior que zero.") {
			t.Errorf("amount %q = %d, want %d with the amount error: %s", typed, response.Code, http.StatusUnprocessableEntity, response.Body)
		}
	}

	if ops := w.newOps(); len(ops) != 0 {
		t.Errorf("amounts in the other format wrote %+v", ops)
	}
}

func TestWebSettings_DatesFollowTheDateFormat(t *testing.T) {
	w := newWebClient(t)
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().In(loc)
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
	w.push(newTag(w.client))
	w.mustSubmit(http.MethodPost, "/recurrences", recurrenceForm("Aluguel", "OUTCOME", "10", "", "1", month.Format(time.DateOnly), ""))
	w.mustSubmit(http.MethodPost, "/transactions", transactionForm("Feira", "OUTCOME", "10", "2026-07-15", "", ""),
		&http.Cookie{Name: "tz", Value: "America/Sao_Paulo"})

	for format, layouts := range map[string][2]string{"MM/DD/YYYY": {"01/02/2006", "01/02"}, "YYYY-MM-DD": {"2006-01-02", "01-02"}} {
		layout, day := layouts[0], layouts[1]
		w.mustSubmit(http.MethodPost, "/settings", settingsForm(format, "1.234,56"))

		w.assertBody("/", `data-clock="`+format+`"`, now.Format(layout), month.Format(day))
		w.assertBody("/?month=2026-07", "qua "+time.Date(2026, 7, 15, 0, 0, 0, 0, loc).Format(day))
		w.assertBody("/tags", time.Date(2026, 7, 1, 0, 0, 0, 0, loc).Format(layout))
		w.assertBody("/devices", now.Format(layout)+" ")
	}
}

func TestWebSettings_CurrencySymbolFollowsTheAccountCurrency(t *testing.T) {
	w := newWebClient(t)
	tz := &http.Cookie{Name: "tz", Value: "America/Sao_Paulo"}

	w.assertSettings("Moeda", "não serão convertidos")
	w.assertSelected("BRL")
	w.mustSubmit(http.MethodPost, "/transactions", transactionForm("Salário", "INCOME", "1.234,56", time.Now().Format(time.DateOnly), "", ""), tz)
	w.assertBody("/", "R$ 1.234,56")

	w.mustSubmit(http.MethodPost, "/settings", withCurrency(settingsForm("DD/MM/YYYY", "1,234.56"), "USD"))
	w.assertSelected("USD")
	w.mustSubmit(http.MethodPost, "/transactions", transactionForm("Feira", "OUTCOME", "1,234.56", "2026-07-15", "", ""), tz)

	w.assertBody("/?month=2026-07", "−$ 1,234.56")
	for _, path := range []string{"/transactions/new", "/recurrences/new", "/categories/new"} {
		w.assertBody(path, ">$</span>")
	}

	for _, path := range []string{"/", "/?month=2026-07", "/transactions/new"} {
		if body := get(w.handler, path, w.cookie, tz).Body.String(); strings.Contains(body, "R$") {
			t.Errorf("%s still shows R$ after switching to USD: %s", path, body)
		}
	}
}

func TestWebSettings_InLauncherAndNav(t *testing.T) {
	w := newWebClient(t)

	body := get(w.handler, "/", w.cookie).Body.String()
	for _, text := range []string{`hx-get="/settings"`, "Abrir configurações", "<kbd>,</kbd> configurações"} {
		if !strings.Contains(body, text) {
			t.Errorf("home lacks %q: %s", text, body)
		}
	}
}

func TestWebSettings_EnglishShowsTheCommonPartsInEnglish(t *testing.T) {
	w := newWebClient(t)
	w.assertBody("/", `<html lang="pt-BR">`)
	w.mustSubmit(http.MethodPost, "/tags", tagForm("Despesa", "2026-07-01", ""))

	w.mustSubmit(http.MethodPost, "/settings", withLanguage(settingsForm("DD/MM/YYYY", "1.234,56"), "en"))
	w.assertSelected("en")
	w.assertBody("/",
		`<html lang="en">`, "Month", "Realized", "Projected", "Pending", "User", "[theme]", "[sign out]",
		"Go to month", "Open settings", "New transaction", "Toggle theme", "Sign out", "type a command", "no command",
		"</kbd> navigate", "<kbd>ctrl k</kbd> commands", "<kbd>esc</kbd> close", "Statement")
	w.assertBody("/?month=2026-11", "November 2026", "<kbd>n</kbd> new")
	w.assertBody("/transactions/new", "[expense]", "[income]")
	w.assertSettings("Settings", "Language", "Date format", "Number format", "DD/MM/YYYY",
		"Currency", "recorded amounts are not converted", "&gt; Save")
	w.assertBody("/tags", "Despesa")
}
