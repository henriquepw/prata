package main

import (
	"maps"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
)

// confirm posts the confirmation of a recurrence's occurrence like the boosted form does.
func (w *webClient) confirm(recurrenceID, date string, tz *http.Cookie) *http.Response {
	return w.submit(http.MethodPost, "/recurrences/"+recurrenceID+"/occurrences/"+date, nil, tz).Result()
}

// transactionAt is a device transaction of Mercado happening at the given instant.
func transactionAt(c *client, kind string, amount int, at time.Time) op {
	return c.op("TRANSACTION", ulid.Make().String(), "CREATE", map[string]any{
		"category_id": categoryID, "kind": kind, "amount": amount, "occurred_at": at.UTC().Format(time.RFC3339),
		"created_at": "2026-08-23T14:02:11Z", "updated_at": "2026-08-23T14:02:11Z",
	})
}

func TestWebOverview(t *testing.T) {
	for _, zone := range []string{"Asia/Tokyo", "America/Sao_Paulo"} {
		t.Run(zone, func(t *testing.T) {
			loc, err := time.LoadLocation(zone)
			if err != nil {
				t.Fatal(err)
			}

			w := newWebClient(t)
			tz := &http.Cookie{Name: "tz", Value: zone}
			now := time.Now().In(loc)
			month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
			nextMonth, lastDay := month.AddDate(0, 1, 0), month.AddDate(0, 1, -1)
			w.push(newCategory(w.client),
				transactionAt(w.client, "OUTCOME", 1000, month),
				transactionAt(w.client, "OUTCOME", 2000, lastDay.Add(23*time.Hour)),
				transactionAt(w.client, "OUTCOME", 4000, nextMonth),
				transactionAt(w.client, "OUTCOME", 8000, month.Add(-time.Minute)),
				transactionAt(w.client, "INCOME", 500000, month.AddDate(0, -1, 0)),
			)

			monthly := w.op("RECURRENCE", ulid.Make().String(), "CREATE", map[string]any{
				"category_id": categoryID, "kind": "OUTCOME", "amount": 10000, "description": "Aluguel", "rule": "MONTHLY:1",
				"start_at": month.AddDate(0, -2, 0).Format(time.DateOnly), "end_at": month.AddDate(0, 0, -1).Format(time.DateOnly),
				"created_at": "2026-08-23T14:02:11Z", "updated_at": "2026-08-23T14:02:11Z",
			})
			salary := w.op("RECURRENCE", ulid.Make().String(), "CREATE", map[string]any{
				"kind": "INCOME", "amount": 100000, "description": "Salário", "rule": "MONTHLY:31",
				"start_at": now.Format(time.DateOnly), "created_at": "2026-08-23T14:02:11Z", "updated_at": "2026-08-23T14:02:11Z",
			})
			w.push(monthly, salary)
			w.newOps()

			oldest := month.AddDate(0, -2, 0)
			if response := w.confirm(monthly.EntityID, oldest.Format(time.DateOnly), tz); response.StatusCode >= 300 || response.Header.Get("HX-Redirect") != "/" {
				t.Fatalf("confirm = %d (HX-Redirect %q), want a redirect to the overview", response.StatusCode, response.Header.Get("HX-Redirect"))
			}

			ops := w.newOps()
			want := map[string]any{"kind": "OUTCOME", "amount": float64(10000), "category_id": categoryID, "description": "Aluguel",
				"recurrence_id": monthly.EntityID, "occurrence_date": oldest.Format(time.DateOnly), "occurred_at": oldest.UTC().Format(time.RFC3339)}
			if len(ops) != 1 || ops[0].Entity != "TRANSACTION" || ops[0].Kind != "CREATE" {
				t.Fatalf("ops after confirm = %+v, want one TRANSACTION CREATE", ops)
			}

			maps.DeleteFunc(ops[0].Payload, func(field string, _ any) bool { return field == "created_at" || field == "updated_at" })
			if !maps.Equal(ops[0].Payload, want) {
				t.Errorf("confirm payload = %v, want %v", ops[0].Payload, want)
			}

			page := get(w.handler, "/", w.cookie, tz)
			body := page.Body.String()
			for _, text := range []string{"R$ 4.750,00", "R$ 5.650,00", "Mercado", "R$ 30,00 de R$ 500,00",
				month.AddDate(0, -1, 0).Format("02/01/2006"), lastDay.Format("02/01/2006")} {
				if !strings.Contains(body, text) {
					t.Errorf("overview lacks %q: %s", text, body)
				}
			}

			if strings.Contains(body, oldest.Format("02/01/2006")) {
				t.Errorf("overview still lists the confirmed occurrence %s", oldest.Format("02/01/2006"))
			}

			if got := strings.Count(body, ">atrasada<"); got != 1 {
				t.Errorf("overview has %d overdue badges, want 1: %s", got, body)
			}
		})
	}
}

func TestWebOverview_ConfirmDropsDeletedCategory(t *testing.T) {
	w := newWebClient(t)
	recurrenceID := ulid.Make().String()
	w.push(newCategory(w.client), w.op("RECURRENCE", recurrenceID, "CREATE", map[string]any{
		"category_id": categoryID, "kind": "OUTCOME", "amount": 10000, "rule": "MONTHLY:5", "start_at": "2026-01-01",
		"created_at": "2026-08-23T14:02:11Z", "updated_at": "2026-08-23T14:02:11Z",
	}), w.op("CATEGORY", categoryID, "DELETE", nil))
	w.newOps()

	if response := w.submit(http.MethodPost, "/recurrences/"+recurrenceID+"/occurrences/2026-01-05", nil); response.Code >= 300 {
		t.Fatalf("confirm = %d, want a redirect to the overview: %s", response.Code, response.Body)
	}

	ops := w.newOps()
	if len(ops) != 1 || ops[0].Payload["category_id"] != nil {
		t.Errorf("ops after confirm = %+v, want one TRANSACTION CREATE without the deleted category", ops)
	}
}

func TestWebOverview_ConfirmRejectedForOtherReasonIsServerError(t *testing.T) {
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

func TestWebOverview_ConfirmAlreadyConfirmed(t *testing.T) {
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
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "já confirmada") || !strings.Contains(response.Body.String(), "Visão geral") {
		t.Errorf("confirm twice = %d, want %d with the reloaded overview and \"já confirmada\": %s", response.Code, http.StatusConflict, response.Body)
	}

	if ops := w.newOps(); len(ops) != 0 {
		t.Errorf("ops after confirming twice = %+v, want none", ops)
	}

	for _, path := range []string{"/recurrences/" + recurrenceID + "/occurrences/2026-01-06", "/recurrences/" + transactionID + "/occurrences/2026-01-05"} {
		if code := w.submit(http.MethodPost, path, nil).Code; code != http.StatusNotFound {
			t.Errorf("confirm %s = %d, want %d", path, code, http.StatusNotFound)
		}
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
	for _, text := range []string{"R$ 5.000,00", "R$ 5.800,00", "Pendentes</dt><dd>3", "[2 atrasada]", newSignup().Email} {
		if !strings.Contains(body, text) {
			t.Errorf("categories ticker lacks %q: %s", text, body)
		}
	}
}
