package main

import (
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
)

// transactionAt is a device transaction of Mercado happening at the given instant.
func transactionAt(c *client, kind string, amount int, at time.Time) op {
	return c.op("TRANSACTION", ulid.Make().String(), "CREATE", map[string]any{
		"category_id": categoryID, "kind": kind, "amount": amount, "occurred_at": at.UTC().Format(time.RFC3339),
		"created_at": "2026-08-23T14:02:11Z", "updated_at": "2026-08-23T14:02:11Z",
	})
}

// monthlyRecurrence is a device recurrence on day of every month from start, up to end when it is not empty.
func monthlyRecurrence(c *client, id, kind string, amount int, description, day string, start time.Time, end string) op {
	fields := map[string]any{
		"kind": kind, "amount": amount, "description": description, "rule": "MONTHLY:" + day, "start_at": start.Format(time.DateOnly),
		"created_at": "2026-08-23T14:02:11Z", "updated_at": "2026-08-23T14:02:11Z",
	}
	if end != "" {
		fields["end_at"] = end
	}

	return c.op("RECURRENCE", id, "CREATE", fields)
}

// assertInOrder checks body has every text, each one after the previous.
func assertInOrder(t *testing.T, body string, texts ...string) {
	t.Helper()
	rest := body
	for _, text := range texts {
		i := strings.Index(rest, text)
		if i < 0 {
			t.Errorf("page lacks %q after %v: %s", text, texts, body)
			return
		}

		rest = rest[i+len(text):]
	}
}

// boxes is the Mês's six boxes in order, each value before its label.
func boxes(previous, income, outcome, realized, pending, projected string) []string {
	return []string{previous, "Saldo anterior", income, "+ Entradas", outcome, "− Saídas", realized, "= Realizado", pending, "± A confirmar", projected, "= Projetado"}
}

func TestWebMonth_AddsUpThePastTheCurrentAndAFutureMonth(t *testing.T) {
	w := newWebClient(t)
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Fatal(err)
	}

	tz := &http.Cookie{Name: "tz", Value: loc.String()}
	now := time.Now().In(loc)
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
	w.push(newCategory(w.client),
		transactionAt(w.client, "INCOME", 500000, month.AddDate(0, -2, 9)),
		transactionAt(w.client, "OUTCOME", 10000, month.AddDate(0, -1, 4)),
		transactionAt(w.client, "INCOME", 100000, month),
		transactionAt(w.client, "OUTCOME", 3000, month.AddDate(0, 1, 0)),
		monthlyRecurrence(w.client, ulid.Make().String(), "OUTCOME", 5000, "Internet", "1", month.AddDate(0, -1, 0), ""),
	)

	tests := []struct {
		name  string
		month time.Time
		boxes []string
	}{
		{"past", month.AddDate(0, -1, 0), boxes("R$ 5.000,00", "R$ 0,00", "−R$ 100,00", "R$ 4.900,00", "−R$ 50,00", "R$ 4.850,00")},
		{"current, with the overdue of the past month", month, boxes("R$ 4.900,00", "R$ 1.000,00", "R$ 0,00", "R$ 5.900,00", "−R$ 100,00", "R$ 5.800,00")},
		{"future", month.AddDate(0, 1, 0), boxes("R$ 5.900,00", "R$ 0,00", "−R$ 30,00", "R$ 5.870,00", "−R$ 50,00", "R$ 5.820,00")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page := get(w.handler, "/?month="+tt.month.Format("2006-01"), w.cookie, tz)
			if page.Code != http.StatusOK {
				t.Fatalf("month = %d, want %d: %s", page.Code, http.StatusOK, page.Body)
			}

			assertInOrder(t, page.Body.String(), tt.boxes...)
		})
	}
}

func TestWebMonth_ReadsTheMonthInTheUserTimeZone(t *testing.T) {
	for _, zone := range []string{"Asia/Tokyo", "America/Sao_Paulo"} {
		t.Run(zone, func(t *testing.T) {
			loc, err := time.LoadLocation(zone)
			if err != nil {
				t.Fatal(err)
			}

			w := newWebClient(t)
			now := time.Now().In(loc)
			month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
			w.push(newCategory(w.client),
				transactionAt(w.client, "OUTCOME", 1000, month),
				transactionAt(w.client, "OUTCOME", 2000, month.AddDate(0, 1, -1).Add(23*time.Hour)),
				transactionAt(w.client, "OUTCOME", 4000, month.AddDate(0, 1, 0)),
				transactionAt(w.client, "OUTCOME", 8000, month.Add(-time.Minute)),
				transactionAt(w.client, "INCOME", 500000, month.AddDate(0, -1, 0)),
			)

			body := get(w.handler, "/", w.cookie, &http.Cookie{Name: "tz", Value: zone}).Body.String()
			assertInOrder(t, body, boxes("R$ 4.920,00", "R$ 0,00", "−R$ 30,00", "R$ 4.890,00", "R$ 0,00", "R$ 4.890,00")...)
			for _, text := range []string{"Mercado", "6%"} {
				if !strings.Contains(body, text) {
					t.Errorf("month lacks %q: %s", text, body)
				}
			}
		})
	}
}

func TestWebMonth_StatementPutsPendingOnTheirDayAndOverdueUnderToday(t *testing.T) {
	w := newWebClient(t)
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Fatal(err)
	}

	tz := &http.Cookie{Name: "tz", Value: loc.String()}
	now := time.Now().In(loc)
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
	lastDay, overdue := month.AddDate(0, 1, -1), month.AddDate(0, -1, 0)
	rent, salary := ulid.Make().String(), ulid.Make().String()
	w.push(newCategory(w.client),
		monthlyRecurrence(w.client, rent, "OUTCOME", 10000, "Aluguel", "1", overdue, month.AddDate(0, 0, -1).Format(time.DateOnly)),
		monthlyRecurrence(w.client, salary, "INCOME", 100000, "Salário", "31", month, ""),
	)
	w.newOps()

	body := get(w.handler, "/", w.cookie, tz).Body.String()
	assertInOrder(t, body, lastDay.Format("02/01"), "Salário", "[pendente]", "── hoje", overdue.Format("02/01"), "Aluguel", "[atrasada]")

	response := w.submit(http.MethodPost, "/recurrences/"+salary+"/occurrences/"+lastDay.Format(time.DateOnly), url.Values{"month": {month.Format("2006-01")}}, tz)
	fragment := response.Body.String()
	if response.Code != http.StatusOK || strings.Contains(fragment, "<html") || strings.Count(fragment, "data-flash") != 1 ||
		strings.Contains(fragment, "[pendente]") || !strings.Contains(fragment, "= Projetado") {
		t.Errorf("confirm on the row = %d, want the boxes and the statement with the new row flashing: %s", response.Code, fragment)
	}

	ops := w.newOps()
	want := map[string]any{"kind": "INCOME", "amount": float64(100000), "description": "Salário", "category_id": nil,
		"recurrence_id": salary, "occurrence_date": lastDay.Format(time.DateOnly), "occurred_at": lastDay.UTC().Format(time.RFC3339)}
	if len(ops) != 1 || ops[0].Entity != "TRANSACTION" || ops[0].Kind != "CREATE" {
		t.Fatalf("ops after confirm = %+v, want one TRANSACTION CREATE", ops)
	}

	maps.DeleteFunc(ops[0].Payload, func(field string, _ any) bool { return field == "created_at" || field == "updated_at" })
	if !maps.Equal(ops[0].Payload, want) {
		t.Errorf("confirm payload = %v, want %v", ops[0].Payload, want)
	}

	date := overdue.Format(time.DateOnly)
	sheet := get(w.handler, "/recurrences/"+rent+"/occurrences/"+date, w.cookie, tz).Body.String()
	for _, text := range []string{"Confirmar ocorrência", `value="100,00"`, `value="` + date + `"`, "Aluguel", "veio diferente? ajuste antes de confirmar", "confirmando"} {
		if !strings.Contains(sheet, text) {
			t.Errorf("occurrence sheet lacks %q: %s", text, sheet)
		}
	}

	today := now.Format(time.DateOnly)
	response = w.submit(http.MethodPost, "/recurrences/"+rent+"/occurrences/"+date, transactionForm("Aluguel", "OUTCOME", "120,00", today, categoryID, ""), tz)
	if flash := flashCookie(response); response.Header().Get("HX-Refresh") != "true" || flash == nil || flash.Path != "/" {
		t.Fatalf("confirm on the sheet = %d with flash cookie %v, want HX-Refresh and the flash for /: %s", response.Code, flash, response.Body)
	}

	ops = w.newOps()
	midnight, _ := time.ParseInLocation(time.DateOnly, today, loc)
	want = map[string]any{"kind": "OUTCOME", "amount": float64(12000), "description": "Aluguel", "category_id": categoryID,
		"recurrence_id": rent, "occurrence_date": date, "occurred_at": midnight.UTC().Format(time.RFC3339)}
	if len(ops) != 1 {
		t.Fatalf("ops after confirm on the sheet = %+v, want one", ops)
	}

	maps.DeleteFunc(ops[0].Payload, func(field string, _ any) bool { return field == "created_at" || field == "updated_at" || field == "tag_id" })
	if !maps.Equal(ops[0].Payload, want) {
		t.Errorf("confirm on the sheet payload = %v, want %v", ops[0].Payload, want)
	}

	body = get(w.handler, "/", w.cookie, tz).Body.String()
	if strings.Contains(body, "[atrasada]") || strings.Contains(body, "[pendente]") {
		t.Errorf("month still lists a confirmed occurrence: %s", body)
	}
}

func TestWebMonth_InvalidOccurrenceSheetIs422(t *testing.T) {
	w := newWebClient(t)
	id := ulid.Make().String()
	w.push(monthlyRecurrence(w.client, id, "OUTCOME", 10000, "Aluguel", "5", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), ""))
	w.newOps()

	response := w.submit(http.MethodPost, "/recurrences/"+id+"/occurrences/2026-01-05", transactionForm("Aluguel", "OUTCOME", "0", "", "", ""))
	if body := response.Body.String(); response.Code != http.StatusUnprocessableEntity || !strings.Contains(body, "Informe um valor maior que zero.") ||
		!strings.Contains(body, "Informe a data.") {
		t.Errorf("invalid confirm on the sheet = %d, want %d with the field errors: %s", response.Code, http.StatusUnprocessableEntity, body)
	}

	if ops := w.newOps(); len(ops) != 0 {
		t.Errorf("ops after an invalid confirm = %+v, want none", ops)
	}
}

func TestWebMonth_TransactionsRedirectsKeepingTheMonth(t *testing.T) {
	w := newWebClient(t)
	for path, want := range map[string]string{"/transactions?month=2026-10": "/?month=2026-10", "/transactions": "/"} {
		response := get(w.handler, path, w.cookie)
		if response.Code != http.StatusSeeOther || response.Header().Get("Location") != want {
			t.Errorf("GET %s = %d to %q, want %d to %q", path, response.Code, response.Header().Get("Location"), http.StatusSeeOther, want)
		}
	}
}

func TestWebMonth_MonthBar(t *testing.T) {
	w := newWebClient(t)
	body := get(w.handler, "/?month=2026-12", w.cookie).Body.String()
	for _, text := range []string{"dezembro de 2026", `href="/?month=2026-11"`, `href="/?month=2027-01"`, "[mês atual]", `aria-label="Mês anterior"`, `data-key="["`, `data-key="]"`} {
		if !strings.Contains(body, text) {
			t.Errorf("December lacks %q: %s", text, body)
		}
	}

	if body := get(w.handler, "/", w.cookie).Body.String(); strings.Contains(body, "[mês atual]") {
		t.Errorf("the current month links to itself: %s", body)
	}
}

func TestWebMonth_CategoryFilterNarrowsTheStatementAndKeepsTheBoxes(t *testing.T) {
	w := newWebClient(t)
	now := time.Now().UTC()
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	leisure := ulid.Make().String()
	spent := func(category, description string, amount int) op {
		return w.op("TRANSACTION", ulid.Make().String(), "CREATE", map[string]any{
			"category_id": category, "kind": "OUTCOME", "amount": amount, "description": description, "occurred_at": month.Format(time.RFC3339),
			"created_at": "2026-08-23T14:02:11Z", "updated_at": "2026-08-23T14:02:11Z",
		})
	}

	w.push(newCategory(w.client),
		w.op("CATEGORY", leisure, "CREATE", map[string]any{"name": "Lazer", "color": "BLUE", "created_at": "2026-08-23T14:02:11Z", "updated_at": "2026-08-23T14:02:11Z"}),
		spent(categoryID, "padaria", 4590),
		spent(leisure, "cinema", 3000),
		monthlyRecurrence(w.client, ulid.Make().String(), "OUTCOME", 5000, "Internet", "28", month, ""),
	)

	shown := "/?month=" + month.Format("2006-01")
	body := get(w.handler, shown+"&category="+categoryID, w.cookie).Body.String()
	assertInOrder(t, body, boxes("R$ 0,00", "R$ 0,00", "−R$ 75,90", "−R$ 75,90", "−R$ 50,00", "−R$ 125,90")...)
	selected := `href="` + shown + "&amp;category=" + categoryID + `" class="usage" aria-current="true"`
	for _, text := range []string{"padaria", "Mercado ✕", "[todas]", selected, "∞"} {
		if !strings.Contains(body, text) {
			t.Errorf("filtered month lacks %q: %s", text, body)
		}
	}

	if strings.Contains(body, "cinema") || strings.Contains(body, "Internet") || strings.Count(body, `aria-current="true"`) != 1 {
		t.Errorf("filtered month shows another category or marks more than one: %s", body)
	}

	if body := get(w.handler, shown, w.cookie).Body.String(); !strings.Contains(body, "cinema") || strings.Contains(body, "[todas]") || strings.Contains(body, `aria-current="true"`) {
		t.Errorf("unfiltered month hides a category or shows a filter: %s", body)
	}

	if body := get(w.handler, "/?month="+month.AddDate(0, 1, 0).Format("2006-01")+"&category="+leisure, w.cookie).Body.String(); !strings.Contains(body, "nenhuma transação nesta categoria") {
		t.Errorf("empty filtered month lacks its empty state: %s", body)
	}

	req := httptest.NewRequest(http.MethodGet, "/transactions/new", nil)
	req.AddCookie(w.cookie)
	req.Header.Set("HX-Current-URL", "http://example.com"+shown+"&category="+leisure)
	sheet := httptest.NewRecorder()
	w.handler.ServeHTTP(sheet, req)
	if !strings.Contains(sheet.Body.String(), `value="`+leisure+`" selected`) {
		t.Errorf("new transaction from a filtered month lacks its category: %s", sheet.Body)
	}
}

func TestWebMonth_ConfirmDropsDeletedCategory(t *testing.T) {
	w := newWebClient(t)
	recurrenceID := ulid.Make().String()
	w.push(newCategory(w.client), w.op("RECURRENCE", recurrenceID, "CREATE", map[string]any{
		"category_id": categoryID, "kind": "OUTCOME", "amount": 10000, "rule": "MONTHLY:5", "start_at": "2026-01-01",
		"created_at": "2026-08-23T14:02:11Z", "updated_at": "2026-08-23T14:02:11Z",
	}), w.op("CATEGORY", categoryID, "DELETE", nil))
	w.newOps()

	if response := w.submit(http.MethodPost, "/recurrences/"+recurrenceID+"/occurrences/2026-01-05", nil); response.Code >= 300 {
		t.Fatalf("confirm = %d, want the month: %s", response.Code, response.Body)
	}

	ops := w.newOps()
	if len(ops) != 1 || ops[0].Payload["category_id"] != nil {
		t.Errorf("ops after confirm = %+v, want one TRANSACTION CREATE without the deleted category", ops)
	}
}

func TestWebMonth_ConfirmRejectedForOtherReasonIsServerError(t *testing.T) {
	db := openStore(t)
	handler := newHandler(db, config{})
	w := &webClient{client: newClient(t, handler, newSignup()), cookie: login(t, handler)}
	recurrenceID := ulid.Make().String()
	w.push(w.op("RECURRENCE", recurrenceID, "CREATE", map[string]any{
		"kind": "OUTCOME", "amount": 10000, "rule": "MONTHLY:5", "start_at": "2026-01-01",
		"created_at": "2026-08-23T14:02:11Z", "updated_at": "2026-08-23T14:02:11Z",
	}))
	w.newOps()

	if _, err := db.Exec("CREATE TRIGGER reject_transactions BEFORE INSERT ON transactions BEGIN SELECT RAISE(ABORT, 'rejected'); END"); err != nil {
		t.Fatal(err)
	}

	response := w.submit(http.MethodPost, "/recurrences/"+recurrenceID+"/occurrences/2026-01-05", nil)
	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "já confirmada") {
		t.Errorf("confirm rejected by another constraint = %d, want %d without \"já confirmada\": %s", response.Code, http.StatusInternalServerError, response.Body)
	}
}

func TestWebMonth_ConfirmAlreadyConfirmed(t *testing.T) {
	w := newWebClient(t)
	recurrenceID := ulid.Make().String()
	confirmed := newTransaction(w.client)
	confirmed.Payload["recurrence_id"], confirmed.Payload["occurrence_date"] = recurrenceID, "2026-01-05"
	w.push(w.op("RECURRENCE", recurrenceID, "CREATE", map[string]any{
		"kind": "OUTCOME", "amount": 10000, "rule": "MONTHLY:5", "start_at": "2026-01-01",
		"created_at": "2026-08-23T14:02:11Z", "updated_at": "2026-08-23T14:02:11Z",
	}), confirmed)
	w.newOps()

	response := w.submit(http.MethodPost, "/recurrences/"+recurrenceID+"/occurrences/2026-01-05", nil)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "já confirmada") || !strings.Contains(response.Body.String(), "Extrato") {
		t.Errorf("confirm twice = %d, want %d with the month and \"já confirmada\": %s", response.Code, http.StatusConflict, response.Body)
	}

	if ops := w.newOps(); len(ops) != 0 {
		t.Errorf("ops after confirming twice = %+v, want none", ops)
	}

	for _, path := range []string{"/recurrences/" + recurrenceID + "/occurrences/2026-01-06", "/recurrences/" + transactionID + "/occurrences/2026-01-05"} {
		if code := w.submit(http.MethodPost, path, nil).Code; code != http.StatusNotFound {
			t.Errorf("confirm %s = %d, want %d", path, code, http.StatusNotFound)
		}

		if code := get(w.handler, path, w.cookie).Code; code != http.StatusNotFound {
			t.Errorf("sheet of %s = %d, want %d", path, code, http.StatusNotFound)
		}
	}
}

func TestWebTicker_ConfirmAndLaunchSwapTheTickerAndTheBalanceLine(t *testing.T) {
	w := newWebClient(t)
	now := time.Now().UTC()
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	salary := ulid.Make().String()
	w.push(monthlyRecurrence(w.client, salary, "INCOME", 100000, "Salário", "1", month, ""))
	shown := url.Values{"month": {month.Format("2006-01")}}

	fragment := w.submit(http.MethodPost, "/recurrences/"+salary+"/occurrences/"+month.Format(time.DateOnly), shown).Body.String()
	for _, text := range []string{`id="ticker" hx-swap-oob="true"`, `id="mbal" hx-swap-oob="true"`, "Realizado</dt><dd>R$ 1.000,00", `<a href="/" class="lnk">0</a>`, "Pendentes</dt><dd>0"} {
		if !strings.Contains(fragment, text) {
			t.Errorf("confirm on the row lacks %q: %s", text, fragment)
		}
	}

	shown.Set("line", "+50")
	fragment = w.submit(http.MethodPost, "/launch", shown).Body.String()
	if strings.Count(fragment, "hx-swap-oob") != 2 || strings.Count(fragment, "Realizado</dt><dd>R$ 1.050,00") != 2 {
		t.Errorf("launch lacks the ticker and the balance line at R$ 1.050,00: %s", fragment)
	}
}

func TestWebTicker_ShowsBalancesAndPendingOnEveryPage(t *testing.T) {
	w := newWebClient(t)
	now := time.Now().UTC()
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	w.push(newCategory(w.client),
		transactionAt(w.client, "INCOME", 500000, month.AddDate(0, -1, 0)),
		w.op("RECURRENCE", ulid.Make().String(), "CREATE", map[string]any{
			"kind": "OUTCOME", "amount": 10000, "rule": "MONTHLY:1", "start_at": month.AddDate(0, -2, 0).Format(time.DateOnly),
			"end_at": month.AddDate(0, 0, -1).Format(time.DateOnly), "created_at": "2026-08-23T14:02:11Z", "updated_at": "2026-08-23T14:02:11Z",
		}),
		w.op("RECURRENCE", ulid.Make().String(), "CREATE", map[string]any{
			"kind": "INCOME", "amount": 100000, "rule": "MONTHLY:31", "start_at": month.Format(time.DateOnly),
			"created_at": "2026-08-23T14:02:11Z", "updated_at": "2026-08-23T14:02:11Z",
		}),
	)

	body := get(w.handler, "/categories", w.cookie).Body.String()
	for _, text := range []string{"R$ 5.000,00", "R$ 5.800,00", `<a href="/" class="lnk">3</a>`, "[2 atrasadas]", newSignup().Email} {
		if !strings.Contains(body, text) {
			t.Errorf("categories ticker lacks %q: %s", text, body)
		}
	}

	start := strings.Index(body, `aria-label="Saldo"`)
	line := body[max(start, 0):]
	line = line[:strings.Index(line, "</dl>")+1]
	for _, text := range []string{"Realizado", "R$ 5.000,00", "Projetado", "R$ 5.800,00", "Pendentes</dt><dd>3", "[2]"} {
		if start < 0 || !strings.Contains(line, text) {
			t.Errorf("categories balance line lacks %q: %s", text, line)
		}
	}

	page := get(w.handler, "/", w.cookie).Body.String()
	for _, status := range []string{"[atrasada]", "[pendente]"} {
		i := strings.Index(page, status)
		if cell := page[strings.LastIndex(page[:max(i, 0)], "<td"):max(i, 0)]; i < 0 || strings.Contains(cell, "hide-m") {
			t.Errorf("month hides %s on mobile: %s", status, page)
		}
	}
}

func TestWebDecode_ReadsTheRealValueAndHidesTheScramble(t *testing.T) {
	w := newWebClient(t)
	body := get(w.handler, "/", w.cookie).Body.String()
	for _, value := range []string{"Extrato", "Categorias", "R$ 0,00"} {
		if want := `<span class="sr-only">` + value + `</span><span aria-hidden="true" data-decode>` + value + `</span>`; !strings.Contains(body, want) {
			t.Errorf("month lacks %q: %s", want, body)
		}
	}

	if strings.Contains(body, "data-decode aria-label") {
		t.Errorf("month still decodes into aria-label: %s", body)
	}
}
