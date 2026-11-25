package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/henriquepw/prata/server/pkg/id"
)

// spend is a device expense of the named category at the given instant.
func (w *webClient) spend(category string, cents int, at time.Time) op {
	return w.op("TRANSACTION", id.New().String(), "CREATE", map[string]any{
		"category_id": w.categories[category], "kind": "OUTCOME", "amount": cents, "occurred_at": at.UTC().Format(time.RFC3339),
		"created_at": "2026-08-23T14:02:11Z", "updated_at": "2026-08-23T14:02:11Z",
	})
}

// setDailyMode saves the user's daily limit mode through the Settings sheet.
func (w *webClient) setDailyMode(mode string) {
	w.t.Helper()
	form := settingsForm("DD/MM/YYYY", "1.234,56")
	form.Set("daily_limit_mode", mode)
	w.mustSubmit(http.MethodPost, "/settings", form)
}

// dayLimit is the limit shown in the day cell of date, "" when there is no such cell.
func dayLimit(body, date string) string {
	_, rest, found := strings.Cut(body, `data-date="`+date+`"`)
	if !found {
		return ""
	}

	_, rest, _ = strings.Cut(rest, `text-muted-foreground">`)
	limit, _, _ := strings.Cut(rest, "<")

	return limit
}

// dailyBudget is a monthly income of R$ 3.000,00, all of it on Livre, the only daily category, in mode; DYNAMIC is the
// default and is left untouched.
func dailyBudget(t *testing.T, mode string) *webClient {
	t.Helper()
	w := newWebClient(t)
	if mode != "DYNAMIC" {
		w.setDailyMode(mode)
	}

	w.mustSubmit(http.MethodPost, "/budget", w.budgetForm("3.000,00", "value", map[string]string{"Livre": "3.000,00"}))

	return w
}

// dayTone is the color of the day cell of date on the Month of month, "" when there is no such cell.
func dayTone(body, date string) string {
	_, rest, found := strings.Cut(body, `data-date="`+date+`" data-tone="`)
	if !found {
		return ""
	}

	tone, _, _ := strings.Cut(rest, `"`)

	return tone
}

func TestWebMonth_DailyLimitPanelColorsTheDaysByTheShareOfTheLimit(t *testing.T) {
	w := dailyBudget(t, "FIXED")
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

func TestWebMonth_DailyLimitTooltipTellsWhatIsLeftOrOverAndHowTheLimitIsMade(t *testing.T) {
	w := dailyBudget(t, "FIXED")
	w.push(
		w.spend("Livre", 8000, time.Date(2020, 4, 1, 12, 0, 0, 0, time.UTC)),
		w.spend("Livre", 15000, time.Date(2020, 4, 2, 12, 0, 0, 0, time.UTC)),
	)

	body := get(w.handler, "/?month=2020-04", w.cookie).Body.String()
	assertInOrder(t, body,
		`data-date="2020-04-01"`, `role="tooltip"`, "sobrou R$ 20,00 (80%)", "dividido igualmente pelos dias do mês",
		`data-date="2020-04-02"`, "estourou R$ 50,00 (150%)",
	)
}

func TestWebMonth_DailyLimitPanelGraysAFutureDayEvenWithSpending(t *testing.T) {
	w := dailyBudget(t, "FIXED")
	future := time.Now().UTC().AddDate(0, 1, 0)
	first := time.Date(future.Year(), future.Month(), 1, 12, 0, 0, 0, time.UTC)
	w.push(w.spend("Livre", 99999, first))

	body := get(w.handler, "/?month="+first.Format("2006-01"), w.cookie).Body.String()
	if got := dayTone(body, first.Format(time.DateOnly)); got != "gray" {
		t.Errorf("tone of a future day with spending = %q, want gray", got)
	}
}

func TestWebMonth_DailyLimitPanelCutsTheDaysInTheUsersTimeZone(t *testing.T) {
	w := dailyBudget(t, "FIXED")
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
	w.setDailyMode("FIXED")
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

func TestWebMonth_DailyLimitDynamicSpreadsTheBalanceOverTheRemainingDays(t *testing.T) {
	w := dailyBudget(t, "DYNAMIC")
	w.assertSelectedDailyMode("DYNAMIC")
	w.push(
		w.spend("Livre", 20000, time.Date(2020, 4, 1, 12, 0, 0, 0, time.UTC)),
		w.spend("Livre", 5000, time.Date(2020, 4, 2, 12, 0, 0, 0, time.UTC)),
		w.spend("Gastos fixos", 99999, time.Date(2020, 4, 2, 12, 0, 0, 0, time.UTC)),
	)

	body := get(w.handler, "/?month=2020-04", w.cookie).Body.String()
	// R$ 3.000,00 over 30 days, then R$ 2.800,00 over 29, then R$ 2.750,00 over 28 (the 2nd's spending is on the 3rd)
	for date, want := range map[string]string{"2020-04-01": "R$ 100,00", "2020-04-02": "R$ 96,55", "2020-04-03": "R$ 98,21", "2020-04-30": "R$ 2.750,00"} {
		if got := dayLimit(body, date); got != want {
			t.Errorf("limit of %s = %q, want %q", date, got, want)
		}
	}

	if got := dayTone(body, "2020-04-01"); got != "red" {
		t.Errorf("tone of the 1st = %q, want red", got)
	}

	if got := dayTone(body, "2020-04-02"); got != "green" {
		t.Errorf("tone of the 2nd = %q, want green", got)
	}
}

func TestWebMonth_DailyLimitDynamicFutureDaysUseTheLimitOfToday(t *testing.T) {
	w := dailyBudget(t, "DYNAMIC")
	now := time.Now().UTC()
	w.push(w.spend("Livre", 50000, now))

	body := get(w.handler, "/?month="+now.Format("2006-01"), w.cookie).Body.String()
	today := dayLimit(body, now.Format(time.DateOnly))
	last := now.AddDate(0, 1, -now.Day()).Format(time.DateOnly)
	if got := dayLimit(body, last); got != today {
		t.Errorf("limit of the last day = %q, want the limit of today %q", got, today)
	}
}

func TestWebMonth_DailyLimitDynamicNegativeBalanceIsALimitOfZero(t *testing.T) {
	w := dailyBudget(t, "DYNAMIC")
	w.push(
		w.spend("Livre", 310000, time.Date(2020, 4, 1, 12, 0, 0, 0, time.UTC)),
		w.spend("Livre", 1, time.Date(2020, 4, 2, 12, 0, 0, 0, time.UTC)),
	)

	body := get(w.handler, "/?month=2020-04", w.cookie).Body.String()
	if got := dayLimit(body, "2020-04-02"); got != "R$ 0,00" {
		t.Errorf("limit with a negative balance = %q, want R$ 0,00", got)
	}

	if got := dayTone(body, "2020-04-02"); got != "red" {
		t.Errorf("tone of any spending over a zero limit = %q, want red", got)
	}
}

func TestWebSettings_DailyLimitModeIsOnlyTheUsersWhoChangedIt(t *testing.T) {
	w := newWebClient(t)
	w.assertSelectedDailyMode("DYNAMIC")

	w.setDailyMode("FIXED")
	w.assertSelectedDailyMode("FIXED")

	other := w.newAccountMate()
	other.assertSelectedDailyMode("DYNAMIC")

	form := settingsForm("DD/MM/YYYY", "1.234,56")
	form.Set("daily_limit_mode", "WEEKLY")
	if response := w.submit(http.MethodPost, "/settings", form); response.Code != http.StatusUnprocessableEntity {
		t.Errorf("unknown mode = %d, want %d", response.Code, http.StatusUnprocessableEntity)
	}

	w.assertSelectedDailyMode("FIXED")
}

// assertSelectedDailyMode checks /settings shows mode as the selected daily limit mode.
func (w *webClient) assertSelectedDailyMode(mode string) {
	w.t.Helper()
	body := w.submit(http.MethodGet, "/settings", nil).Body.String()
	if !strings.Contains(body, `value="`+mode+`" selected`) {
		w.t.Errorf("settings lacks the daily limit mode %q selected: %s", mode, body)
	}
}

// newAccountMate is another user of the account, signed in on its own session.
func (w *webClient) newAccountMate() *webClient {
	w.t.Helper()
	ctx := context.Background()
	userID, now := id.New(), time.Now().UTC().Format(time.RFC3339)

	query := "SELECT id FROM accounts"

	var accountID string
	if err := w.db.GetContext(ctx, &accountID, query); err != nil {
		w.t.Fatal(err)
	}

	query = `
		INSERT INTO users (id, email, secret, created_at, updated_at)
		VALUES (?, 'bia@example.com', '', ?, ?)
	`
	if _, err := w.db.ExecContext(ctx, query, userID, now, now); err != nil {
		w.t.Fatal(err)
	}

	query = `
		INSERT INTO account_users (id, account_id, user_id, role, created_at)
		VALUES (?, ?, ?, 'MEMBER', '2999-01-01T00:00:00Z')
	`
	if _, err := w.db.ExecContext(ctx, query, id.New().String(), accountID, userID); err != nil {
		w.t.Fatal(err)
	}

	token, err := w.db.CreateSession(ctx, userID, time.Now())
	if err != nil {
		w.t.Fatal(err)
	}

	mate := *w
	mate.cookie = &http.Cookie{Name: "session", Value: token}

	return &mate
}
