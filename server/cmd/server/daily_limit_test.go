package main

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
)

// spend is a device expense of the named category at the given instant.
func (w *webClient) spend(category string, cents int, at time.Time) op {
	return w.op("TRANSACTION", ulid.Make().String(), "CREATE", map[string]any{
		"category_id": w.categories[category], "kind": "OUTCOME", "amount": cents, "occurred_at": at.UTC().Format(time.RFC3339),
		"created_at": "2026-08-23T14:02:11Z", "updated_at": "2026-08-23T14:02:11Z",
	})
}

// dailyBudget is a Receita mensal of R$ 3.000,00, all of it on Livre, the only daily category.
func dailyBudget(t *testing.T) *webClient {
	t.Helper()
	w := newWebClient(t)
	w.mustSubmit(http.MethodPost, "/budget", w.budgetForm("3.000,00", "value", map[string]string{"Livre": "3.000,00"}))

	return w
}

// dayTone is the color of the day cell of date on the Mês of month, "" when there is no such cell.
func dayTone(body, date string) string {
	_, rest, found := strings.Cut(body, `data-date="`+date+`" data-tone="`)
	if !found {
		return ""
	}

	tone, _, _ := strings.Cut(rest, `"`)

	return tone
}

func TestWebMonth_DailyLimitPanelColorsTheDaysByTheShareOfTheLimit(t *testing.T) {
	w := dailyBudget(t)
	loc := time.FixedZone("tz", 0)
	day := func(d int) time.Time { return time.Date(2020, 4, d, 12, 0, 0, 0, loc) }
	w.push(
		w.spend("Livre", 8000, day(1)),
		w.spend("Livre", 8001, day(2)),
		w.spend("Livre", 10000, day(3)),
		w.spend("Livre", 10001, day(4)),
		w.spend("Livre", 5000, day(5)),
		w.spend("Livre", 3001, day(5)),
		w.spend("Gastos fixos", 99999, day(6)),
	)

	body := get(w.handler, "/?month=2020-04", w.cookie).Body.String()
	want := map[string]string{
		"2020-04-01": "green", "2020-04-02": "yellow", "2020-04-03": "yellow", "2020-04-04": "red",
		"2020-04-05": "yellow", "2020-04-06": "gray", "2020-04-07": "gray", "2020-04-30": "gray",
	}
	for date, tone := range want {
		if got := dayTone(body, date); got != tone {
			t.Errorf("tone of %s = %q, want %q", date, got, tone)
		}
	}

	if got := dayTone(body, "2020-05-01"); got != "" {
		t.Errorf("tone of a day of the next month = %q, want no cell", got)
	}

	assertInOrder(t, body, `data-date="2020-04-01"`, "R$ 80,00", "R$ 100,00", `data-date="2020-04-02"`)
}

func TestWebMonth_DailyLimitPanelGraysAFutureDayEvenWithSpending(t *testing.T) {
	w := dailyBudget(t)
	future := time.Now().UTC().AddDate(0, 1, 0)
	first := time.Date(future.Year(), future.Month(), 1, 12, 0, 0, 0, time.UTC)
	w.push(w.spend("Livre", 99999, first))

	body := get(w.handler, "/?month="+first.Format("2006-01"), w.cookie).Body.String()
	if got := dayTone(body, first.Format(time.DateOnly)); got != "gray" {
		t.Errorf("tone of a future day with spending = %q, want gray", got)
	}
}

func TestWebMonth_DailyLimitPanelCutsTheDaysInTheUsersTimeZone(t *testing.T) {
	w := dailyBudget(t)
	w.push(w.spend("Livre", 5000, time.Date(2020, 4, 8, 1, 0, 0, 0, time.UTC))) // 22:00 of the 7th in São Paulo
	tz := &http.Cookie{Name: "tz", Value: "America/Sao_Paulo"}

	body := get(w.handler, "/?month=2020-04", w.cookie, tz).Body.String()
	if got := dayTone(body, "2020-04-07"); got != "green" {
		t.Errorf("tone of the 7th = %q, want green", got)
	}

	if got := dayTone(body, "2020-04-08"); got != "gray" {
		t.Errorf("tone of the 8th = %q, want gray", got)
	}
}

func TestWebMonth_DailyLimitPanelInvitesToDefineTheIncomeWhenThereIsNone(t *testing.T) {
	w := newWebClient(t)
	w.push(w.spend("Livre", 5000, time.Date(2020, 4, 1, 12, 0, 0, 0, time.UTC)))

	body := get(w.handler, "/?month=2020-04", w.cookie).Body.String()
	if got := dayTone(body, "2020-04-01"); got != "" {
		t.Errorf("tone of a day without income = %q, want no cell", got)
	}

	assertInOrder(t, body, "Limite diário", "defina a receita mensal")
}

func TestWebMonth_DailyLimitPanelTruncatesTheLimitToTheCentAndRedsAnyDayOverAZeroLimit(t *testing.T) {
	w := newWebClient(t)
	w.mustSubmit(http.MethodPost, "/budget", w.budgetForm("3.000,00", "value", map[string]string{"Gastos fixos": "1.000,00", "Livre": "2.000,00"}))
	w.push(w.spend("Livre", 100, time.Date(2020, 4, 1, 12, 0, 0, 0, time.UTC)))

	if body := get(w.handler, "/?month=2020-04", w.cookie).Body.String(); !strings.Contains(body, "R$ 66,66") {
		t.Errorf("page lacks the limit R$ 66,66 of R$ 2.000,00 over 30 days: %s", body)
	}

	w.mustSubmit(http.MethodPost, "/budget", w.budgetForm("3.000,00", "value", map[string]string{"Gastos fixos": "3.000,00", "Livre": "0,00"}))

	if got := dayTone(get(w.handler, "/?month=2020-04", w.cookie).Body.String(), "2020-04-01"); got != "red" {
		t.Errorf("tone of spending over a zero limit = %q, want red", got)
	}
}
