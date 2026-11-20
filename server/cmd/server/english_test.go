package main

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
)

// newEnglishClient is a web client whose user picked the Idioma en.
func newEnglishClient(t *testing.T) *webClient {
	t.Helper()
	w := newWebClient(t)
	w.mustSubmit(http.MethodPost, "/settings", withLanguage(settingsForm("DD/MM/YYYY", "1.234,56"), "en"))

	return w
}

func TestWebEnglish_Categories(t *testing.T) {
	w := newEnglishClient(t)
	w.assertBody("/categories", "Categories", "+ new [n]", "no categories yet")

	w.mustSubmit(http.MethodPost, "/categories", categoryForm("Mercado", "GREEN", "0"))
	id := w.newOps()[0].EntityID
	w.assertBody("/categories", "Mercado", "Name", "Monthly limit", "no limit")
	w.assertBody("/categories/"+id, "Edit category", "Color", "green", "Monthly limit", "empty = no limit", "Cancel", "Delete",
		"delete this category? Its transactions will show as no category.", "yes, delete", "no", "&gt; Save", `data-busy="saving"`, `aria-label="Close"`)
	w.assertBody("/categories/new", "New category")
}

func TestWebEnglish_Tags(t *testing.T) {
	w := newEnglishClient(t)
	tz := &http.Cookie{Name: "tz", Value: "America/Sao_Paulo"}
	w.assertBody("/tags", "Tags", "no tags yet")

	w.mustSubmit(http.MethodPost, "/tags", tagForm("Viagem", "2026-07-01", ""), tz)
	id := w.newOps()[0].EntityID
	w.mustSubmit(http.MethodPost, "/tags", tagForm("Natal", "2026-12-01", "2026-12-31"), tz)
	w.assertBody("/tags", "#Viagem", "Name", "Period", "since 01/07/2026", "01/12/2026 to 31/12/2026")
	w.assertBody("/tags/"+id, "Edit tag", "Start", "[today]", "[yesterday]", "End", "empty = no end",
		"delete this tag? Its transactions will show without a tag.")
	w.assertBody("/tags/new", "New tag")
}

func TestWebEnglish_Recurrences(t *testing.T) {
	w := newEnglishClient(t)
	w.assertBody("/recurrences", "Recurrences", "no recurrences yet")

	w.mustSubmit(http.MethodPost, "/recurrences", recurrenceForm("Aluguel", "OUTCOME", "1.500", "", "5", "2026-01-01", ""))
	id := w.newOps()[0].EntityID
	w.mustSubmit(http.MethodPost, "/recurrences", recurrenceForm("", "INCOME", "10", "", "31", "2026-01-01", ""))
	w.assertBody("/recurrences", "Aluguel", "no description", "Description", "Day", "Amount", "day 5 of every month")
	w.assertBody("/recurrences/"+id, "Edit recurrence", "Description", "Kind", "Amount", "Category", "No category",
		"1 to 31; 31 falls on the last day", "Start", "End", "empty = no end",
		"delete this recurrence? It stops generating pending occurrences; the confirmed transactions stay.")
	w.assertBody("/recurrences/new", "New recurrence")
}

func TestWebEnglish_Month(t *testing.T) {
	w := newEnglishClient(t)
	tz := &http.Cookie{Name: "tz", Value: "America/Sao_Paulo"}
	w.assertBody("/?month=2026-07", "July 2026", "Previous balance", "+ Income", "− Expenses", "= Realized", "± To confirm",
		"= Projected", `aria-label="Previous month"`, `aria-label="Next month"`, "[this month]", "switch month", "Statement",
		"no transactions this month", "no categories yet")

	w.mustSubmit(http.MethodPost, "/categories", categoryForm("Mercado", "GREEN", "0"))
	w.mustSubmit(http.MethodPost, "/transactions", transactionForm("Feira", "OUTCOME", "10", "2026-07-15", "", ""), tz)
	id := w.newOps()[1].EntityID
	w.assertBody("/?month=2026-07", "Feira", "wed 15/07", "Mercado", "R$ 0,00 · no limit")
	w.assertBody("/transactions/"+id, "Edit transaction", "Description", "Amount", "Date", "Category", "No category", "Tag", "No tag", "[today]", "[yesterday]",
		"delete this transaction? It disappears from the lists and balances on every device.")
	w.assertBody("/transactions/new", "New transaction")

	now := time.Now().UTC()
	first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	recurrenceID := ulid.Make().String()
	w.push(w.overdueRecurrence(recurrenceID, first), w.op("RECURRENCE", ulid.Make().String(), "CREATE", map[string]any{
		"kind": "INCOME", "amount": 10000, "rule": "MONTHLY:31", "start_at": first.Format(time.DateOnly),
		"created_at": "2026-08-23T14:02:11Z", "updated_at": "2026-08-23T14:02:11Z",
	}))
	w.assertBody("/", "── today", "[overdue]", "[pending]", "confirm", `data-busy="confirming"`)

	date := first.AddDate(0, -1, 0).Format(time.DateOnly)
	w.assertBody("/recurrences/"+recurrenceID+"/occurrences/"+date, "Confirm occurrence", "came out different? adjust it before confirming")
	w.submit(http.MethodPost, "/recurrences/"+recurrenceID+"/occurrences/"+date, nil)
	response := w.submit(http.MethodPost, "/recurrences/"+recurrenceID+"/occurrences/"+date, nil)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "Occurrence already confirmed.") {
		t.Errorf("confirm twice = %d, want %d with \"Occurrence already confirmed.\": %s", response.Code, http.StatusConflict, response.Body)
	}
}

// overdueRecurrence is a recurrence whose only occurrence is the 1st of the month before first.
func (w *webClient) overdueRecurrence(id string, first time.Time) op {
	return w.op("RECURRENCE", id, "CREATE", map[string]any{
		"kind": "OUTCOME", "amount": 10000, "rule": "MONTHLY:1", "start_at": first.AddDate(0, -1, 0).Format(time.DateOnly),
		"end_at": first.AddDate(0, 0, -1).Format(time.DateOnly), "created_at": "2026-08-23T14:02:11Z", "updated_at": "2026-08-23T14:02:11Z",
	})
}

func TestWebTicker_OverdueCountIsPluralInEachLanguage(t *testing.T) {
	w := newWebClient(t)
	now := time.Now().UTC()
	first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	w.push(w.overdueRecurrence(ulid.Make().String(), first))
	w.assertBody("/tags", "[1 atrasada]")
	w.mustSubmit(http.MethodPost, "/settings", withLanguage(settingsForm("DD/MM/YYYY", "1.234,56"), "en"))
	w.assertBody("/tags", "[1 overdue]")

	w.push(w.overdueRecurrence(ulid.Make().String(), first))
	w.assertBody("/tags", "[2 overdue]")
	w.mustSubmit(http.MethodPost, "/settings", settingsForm("DD/MM/YYYY", "1.234,56"))
	w.assertBody("/tags", "[2 atrasadas]")
}

func TestWebEnglish_Devices(t *testing.T) {
	w := newEnglishClient(t)
	w.assertBody("/devices", "Devices", "Name", "Last seen", "Created at", "Revoke",
		"revoke laptop?", "yes, revoke", "Activity log", "no operations yet")
}

func TestWebEnglish_ValidationErrorsAre422InEnglish(t *testing.T) {
	w := newEnglishClient(t)
	for name, c := range map[string]struct {
		path string
		form url.Values
		want []string
	}{
		"category": {"/categories", categoryForm("", "PINK", "-1"),
			[]string{"Enter a name.", "Choose a color.", "Enter a limit of zero or more."}},
		"tag": {"/tags", tagForm("", "2026-07-02", "2026-07-01"),
			[]string{"Enter a name.", "Enter an end on or after the start."}},
		"tag without start": {"/tags", tagForm("Viagem", "", ""), []string{"Enter the start."}},
		"transaction": {"/transactions", transactionForm("Feira", "LOAN", "0", "", "missing", "missing"),
			[]string{"Enter an amount greater than zero.", "Choose the kind.", "Enter the date.", "Choose a category.", "Choose a tag."}},
		"recurrence": {"/recurrences", recurrenceForm("Aluguel", "OUTCOME", "10", "", "32", "2026-07-02", "2026-07-01"),
			[]string{"Enter a day from 1 to 31.", "Enter an end on or after the start."}},
	} {
		t.Run(name, func(t *testing.T) {
			response := w.submit(http.MethodPost, c.path, c.form)
			for _, text := range c.want {
				if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), text) {
					t.Errorf("POST %s = %d, want %d with %q: %s", c.path, response.Code, http.StatusUnprocessableEntity, text, response.Body)
				}
			}
		})
	}
}
