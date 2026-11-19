package main

import (
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

const tagID = "01JXQ8Z9000000000000000000"

func transactionForm(description, kind, amount, occurredAt, category, tag string) url.Values {
	return url.Values{"description": {description}, "kind": {kind}, "amount": {amount}, "occurred_at": {occurredAt},
		"category_id": {category}, "tag_id": {tag}}
}

func newTag(c *client) op {
	return c.op("TAG", tagID, "CREATE", map[string]any{
		"name": "Viagem", "start_at": "2026-07-01T03:00:00Z",
		"created_at": "2026-08-23T14:02:11Z", "updated_at": "2026-08-23T14:02:11Z",
	})
}

func TestWebTransactions_WritesReachDevicePull(t *testing.T) {
	w := newWebClient(t)
	tz := &http.Cookie{Name: "tz", Value: "America/Sao_Paulo"}
	w.push(newCategory(w.client), newTag(w.client))
	w.newOps()

	w.mustSubmit(http.MethodPost, "/transactions", transactionForm(" Feira ", "OUTCOME", "1.234,56", "2026-07-15", categoryID, tagID), tz)
	ops := w.newOps()
	if len(ops) != 1 {
		t.Fatalf("ops after create = %+v, want one", ops)
	}

	created := ops[0]
	want := map[string]any{"description": "Feira", "kind": "OUTCOME", "amount": float64(123456), "occurred_at": "2026-07-15T03:00:00Z",
		"category_id": categoryID, "tag_id": tagID, "created_at": created.CreatedAt, "updated_at": created.CreatedAt}
	if created.Entity != "TRANSACTION" || created.Kind != "CREATE" || created.DeviceID == w.deviceID || !maps.Equal(created.Payload, want) {
		t.Errorf("create op = %+v, want a TRANSACTION CREATE from the web device with payload %v", created, want)
	}

	page := get(w.handler, "/transactions?month=2026-07", w.cookie, tz)
	if body := page.Body.String(); page.Code != http.StatusOK || !strings.Contains(body, "Feira") || !strings.Contains(body, "15/07/2026") ||
		!strings.Contains(body, "1.234,56") || !strings.Contains(body, "Mercado") {
		t.Errorf("list = %d, want Feira on 15/07/2026 with its amount and category: %s", page.Code, body)
	}

	edit := get(w.handler, "/transactions/"+created.EntityID, w.cookie, tz)
	if body := edit.Body.String(); edit.Code != http.StatusOK || strings.Contains(body, "<html") ||
		!strings.Contains(body, `value="1.234,56"`) || !strings.Contains(body, `value="2026-07-15"`) || !strings.Contains(body, "Viagem") {
		t.Errorf("edit form = %d, want the bare form fragment with amount, local date and tag: %s", edit.Code, body)
	}

	w.mustSubmit(http.MethodPost, "/transactions/"+created.EntityID, transactionForm("Feira", "INCOME", "1234,56", "2026-07-15", categoryID, tagID), tz)
	ops = w.newOps()
	if len(ops) != 1 || ops[0].Kind != "UPDATE" || !slices.Equal(slices.Sorted(maps.Keys(ops[0].Payload)), []string{"kind", "updated_at"}) {
		t.Errorf("ops after edit = %+v, want one UPDATE with only kind and updated_at", ops)
	}

	w.mustSubmit(http.MethodPost, "/transactions/"+created.EntityID, transactionForm("Feira", "INCOME", "1.234,56", "2026-07-15", categoryID, tagID), tz)
	if ops = w.newOps(); len(ops) != 0 {
		t.Errorf("ops after saving unchanged = %+v, want none", ops)
	}

	w.mustSubmit(http.MethodDelete, "/transactions/"+created.EntityID, nil, tz)
	if ops = w.newOps(); len(ops) != 1 || ops[0].Kind != "DELETE" || ops[0].EntityID != created.EntityID || len(ops[0].Payload) != 0 {
		t.Errorf("ops after delete = %+v, want one DELETE tombstone", ops)
	}

	if body := get(w.handler, "/transactions?month=2026-07", w.cookie, tz).Body.String(); strings.Contains(body, "Feira") {
		t.Errorf("list after delete still shows the transaction: %s", body)
	}

	if code := get(w.handler, "/transactions/"+created.EntityID, w.cookie).Code; code != http.StatusNotFound {
		t.Errorf("edit form of deleted transaction = %d, want %d", code, http.StatusNotFound)
	}
}

func TestWebTransactions_DateIsLocalMidnightInUTC(t *testing.T) {
	tests := []struct {
		tz, wantOccurredAt, inMonth, outMonth string
	}{
		{"Asia/Tokyo", "2026-06-30T15:00:00Z", "2026-07", "2026-06"},
		{"America/Sao_Paulo", "2026-07-01T03:00:00Z", "2026-07", "2026-06"},
		{"Pacific/Honolulu", "2026-07-01T10:00:00Z", "2026-07", "2026-08"},
	}
	for _, tt := range tests {
		t.Run(tt.tz, func(t *testing.T) {
			w := newWebClient(t)
			tz := &http.Cookie{Name: "tz", Value: tt.tz}

			w.mustSubmit(http.MethodPost, "/transactions", transactionForm("Feira", "OUTCOME", "10", "2026-07-01", "", ""), tz)
			ops := w.newOps()
			if len(ops) != 1 || ops[0].Payload["occurred_at"] != tt.wantOccurredAt {
				t.Fatalf("create ops = %+v, want occurred_at %s", ops, tt.wantOccurredAt)
			}

			if body := get(w.handler, "/transactions?month="+tt.inMonth, w.cookie, tz).Body.String(); !strings.Contains(body, "01/07/2026") {
				t.Errorf("month %s lacks the transaction on 01/07/2026: %s", tt.inMonth, body)
			}

			if body := get(w.handler, "/transactions?month="+tt.outMonth, w.cookie, tz).Body.String(); strings.Contains(body, "Feira") {
				t.Errorf("month %s shows the transaction: %s", tt.outMonth, body)
			}

			if body := get(w.handler, "/transactions/"+ops[0].EntityID, w.cookie, tz).Body.String(); !strings.Contains(body, `value="2026-07-01"`) {
				t.Errorf("edit form lacks the local date: %s", body)
			}
		})
	}
}

func TestWebTransactions_MonthNavigation(t *testing.T) {
	w := newWebClient(t)
	tz := &http.Cookie{Name: "tz", Value: "America/Sao_Paulo"}
	w.mustSubmit(http.MethodPost, "/transactions", transactionForm("Antiga", "OUTCOME", "10", "2026-07-01", "", ""), tz)
	w.mustSubmit(http.MethodPost, "/transactions", transactionForm("Nova", "INCOME", "20", "2026-07-31", "", ""), tz)

	body := get(w.handler, "/transactions?month=2026-07", w.cookie, tz).Body.String()
	if !strings.Contains(body, "julho de 2026") || !strings.Contains(body, `href="/transactions?month=2026-06"`) || !strings.Contains(body, `href="/transactions?month=2026-08"`) {
		t.Errorf("July page lacks its label or the arrows to June and August: %s", body)
	}

	if newer, older := strings.Index(body, "Nova"), strings.Index(body, "Antiga"); newer < 0 || older < 0 || newer > older {
		t.Errorf("July page = Nova at %d, Antiga at %d, want both, newest first", newer, older)
	}

	body = get(w.handler, "/transactions?month=2026-12", w.cookie, tz).Body.String()
	if !strings.Contains(body, `href="/transactions?month=2026-11"`) || !strings.Contains(body, `href="/transactions?month=2027-01"`) {
		t.Errorf("December page lacks the arrows to November and next January: %s", body)
	}

	today := time.Now().UTC().Format(time.DateOnly)
	w.mustSubmit(http.MethodPost, "/transactions", transactionForm("Hoje", "OUTCOME", "10", today, "", ""))
	if body := get(w.handler, "/transactions", w.cookie).Body.String(); !strings.Contains(body, "Hoje") {
		t.Errorf("default page lacks today's transaction: %s", body)
	}
}

func TestWebTransactions_InvalidFormIs422AndWritesNothing(t *testing.T) {
	tests := []struct {
		name     string
		form     url.Values
		messages []string
	}{
		{"zero amount, no kind and no date", transactionForm("", "", "0", "", "", ""), []string{"Informe um valor maior que zero.", "Escolha o tipo.", "Informe a data."}},
		{"malformed amount", transactionForm("", "OUTCOME", "12,345", "2026-07-01", "", ""), []string{"Informe um valor maior que zero."}},
		{"negative amount", transactionForm("", "OUTCOME", "-5", "2026-07-01", "", ""), []string{"Informe um valor maior que zero."}},
		{"unknown category and tag", transactionForm("", "INCOME", "5", "2026-07-01", transactionID, transactionID), []string{"Escolha uma categoria.", "Escolha uma tag."}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newWebClient(t)

			response := w.submit(http.MethodPost, "/transactions", tt.form)
			if response.Code != http.StatusUnprocessableEntity {
				t.Fatalf("invalid create = %d, want %d: %s", response.Code, http.StatusUnprocessableEntity, response.Body)
			}

			body := response.Body.String()
			for _, message := range tt.messages {
				if !strings.Contains(body, message) {
					t.Errorf("422 body lacks %q: %s", message, body)
				}
			}

			if got := strings.Count(body, `aria-invalid="true"`); got != len(tt.messages) {
				t.Errorf("422 body has %d invalid controls, want one per failing field", got)
			}

			if ops := w.newOps(); len(ops) != 0 {
				t.Errorf("ops after invalid create = %+v, want none", ops)
			}
		})
	}
}

func TestWebTransactions_DeletedCategoryAndTag(t *testing.T) {
	w := newWebClient(t)
	w.push(newCategory(w.client), newTag(w.client))
	w.mustSubmit(http.MethodPost, "/transactions", transactionForm("Feira", "OUTCOME", "10", "2026-07-01", categoryID, tagID))
	w.mustSubmit(http.MethodDelete, "/categories/"+categoryID, nil)
	w.mustSubmit(http.MethodDelete, "/tags/"+tagID, nil)
	w.newOps()

	if body := get(w.handler, "/transactions?month=2026-07", w.cookie).Body.String(); !strings.Contains(body, "Sem categoria") || strings.Contains(body, "Mercado") {
		t.Errorf("list shows the deleted category, want Sem categoria: %s", body)
	}

	if body := get(w.handler, "/transactions/new", w.cookie).Body.String(); strings.Contains(body, "Mercado") || strings.Contains(body, "Viagem") {
		t.Errorf("new form offers the deleted category or tag: %s", body)
	}

	response := w.submit(http.MethodPost, "/transactions", transactionForm("Feira", "OUTCOME", "10", "2026-07-01", categoryID, tagID))
	if response.Code != http.StatusUnprocessableEntity {
		t.Errorf("create with deleted category and tag = %d, want %d", response.Code, http.StatusUnprocessableEntity)
	}

	if ops := w.newOps(); len(ops) != 0 {
		t.Errorf("ops after referencing deleted rows = %+v, want none", ops)
	}
}

func TestWebTransactions_EditKeepsDeletedCategoryAndTag(t *testing.T) {
	w := newWebClient(t)
	w.push(newCategory(w.client), newTag(w.client))
	w.mustSubmit(http.MethodPost, "/transactions", transactionForm("Feira", "OUTCOME", "10", "2026-07-01", categoryID, tagID))
	created := w.newOps()[2]
	w.mustSubmit(http.MethodDelete, "/categories/"+categoryID, nil)
	w.mustSubmit(http.MethodDelete, "/tags/"+tagID, nil)
	w.newOps()

	w.mustSubmit(http.MethodPost, "/transactions/"+created.EntityID, transactionForm("Feira livre", "OUTCOME", "10", "2026-07-01", "", ""))
	ops := w.newOps()
	if len(ops) != 1 || !slices.Equal(slices.Sorted(maps.Keys(ops[0].Payload)), []string{"description", "updated_at"}) {
		t.Errorf("ops after editing the description = %+v, want one UPDATE with only description and updated_at", ops)
	}
}
