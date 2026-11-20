package main

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func settingsForm(dateFormat, numberFormat string) url.Values {
	return url.Values{"date_format": {dateFormat}, "number_format": {numberFormat}}
}

// assertSelected checks /settings shows each value as the selected option.
func (w *webClient) assertSelected(values ...string) {
	w.t.Helper()
	body := get(w.handler, "/settings", w.cookie).Body.String()
	for _, value := range values {
		if !strings.Contains(body, `value="`+value+`" selected`) {
			w.t.Errorf("settings lacks %q selected: %s", value, body)
		}
	}
}

func TestWebSettings_ShowsAndSavesTheFormats(t *testing.T) {
	w := newWebClient(t)

	page := get(w.handler, "/settings", w.cookie)
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
	} {
		t.Run(name, func(t *testing.T) {
			response := w.submit(http.MethodPost, "/settings", form)
			if body := response.Body.String(); response.Code != http.StatusUnprocessableEntity || !strings.Contains(body, `hx-post="/settings"`) ||
				!strings.Contains(body, `aria-invalid="true"`) || strings.Contains(body, "<html") {
				t.Errorf("POST /settings = %d, want %d with the form flagging the field: %s", response.Code, http.StatusUnprocessableEntity, body)
			}

			w.assertSelected("DD/MM/YYYY", "1.234,56")
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
	w.assertBody("/transactions?month=2026-07", "−R$ 1,234.56")
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

	for format, layout := range map[string]string{"MM/DD/YYYY": "01/02/2006", "YYYY-MM-DD": "2006-01-02"} {
		w.mustSubmit(http.MethodPost, "/settings", settingsForm(format, "1.234,56"))

		w.assertBody("/", `data-clock="`+format+`"`, now.Format(layout), month.Format(layout))
		w.assertBody("/transactions?month=2026-07", time.Date(2026, 7, 15, 0, 0, 0, 0, loc).Format(layout))
		w.assertBody("/tags", time.Date(2026, 7, 1, 0, 0, 0, 0, loc).Format(layout))
		w.assertBody("/devices", now.Format(layout)+" ")
	}
}

func TestWebSettings_InLauncherAndNav(t *testing.T) {
	w := newWebClient(t)

	body := get(w.handler, "/", w.cookie).Body.String()
	for _, text := range []string{`href="/settings"`, "Ir para configurações"} {
		if !strings.Contains(body, text) {
			t.Errorf("home lacks %q: %s", text, body)
		}
	}
}
