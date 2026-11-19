package main

import (
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
)

func recurrenceForm(description, kind, amount, category, day, startAt, endAt string) url.Values {
	return url.Values{"description": {description}, "kind": {kind}, "amount": {amount}, "category_id": {category},
		"day": {day}, "start_at": {startAt}, "end_at": {endAt}}
}

func TestWebRecurrences_WritesReachDevicePull(t *testing.T) {
	w := newWebClient(t)
	w.push(newCategory(w.client))
	w.newOps()

	w.mustSubmit(http.MethodPost, "/recurrences", recurrenceForm(" Aluguel ", "OUTCOME", "1.500,00", categoryID, "5", "2026-01-01", ""))
	ops := w.newOps()
	if len(ops) != 1 {
		t.Fatalf("ops after create = %+v, want one", ops)
	}

	created := ops[0]
	want := map[string]any{"description": "Aluguel", "kind": "OUTCOME", "amount": float64(150000), "category_id": categoryID,
		"rule": "MONTHLY:5", "start_at": "2026-01-01", "end_at": nil, "created_at": created.CreatedAt, "updated_at": created.CreatedAt}
	if created.Entity != "RECURRENCE" || created.Kind != "CREATE" || created.DeviceID == w.deviceID || !maps.Equal(created.Payload, want) {
		t.Errorf("create op = %+v, want a RECURRENCE CREATE from the web device with payload %v", created, want)
	}

	page := get(w.handler, "/recurrences", w.cookie)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Aluguel") || !strings.Contains(page.Body.String(), "1.500,00") {
		t.Errorf("list = %d, want Aluguel with its amount: %s", page.Code, page.Body)
	}

	edit := get(w.handler, "/recurrences/"+created.EntityID, w.cookie)
	if body := edit.Body.String(); edit.Code != http.StatusOK || strings.Contains(body, "<html") ||
		!strings.Contains(body, `value="1.500,00"`) || !strings.Contains(body, `value="5"`) || !strings.Contains(body, "Mercado") {
		t.Errorf("edit form = %d, want the bare form fragment with amount, day and category: %s", edit.Code, edit.Body)
	}

	w.mustSubmit(http.MethodPost, "/recurrences/"+created.EntityID, recurrenceForm("Aluguel", "OUTCOME", "1500", categoryID, "10", "2026-01-01", ""))
	ops = w.newOps()
	if len(ops) != 1 || ops[0].Kind != "UPDATE" || !slices.Equal(slices.Sorted(maps.Keys(ops[0].Payload)), []string{"rule", "updated_at"}) ||
		ops[0].Payload["rule"] != "MONTHLY:10" {
		t.Errorf("ops after edit = %+v, want one UPDATE with only rule and updated_at", ops)
	}

	w.mustSubmit(http.MethodPost, "/recurrences/"+created.EntityID, recurrenceForm("Aluguel", "OUTCOME", "1.500", categoryID, "10", "2026-01-01", ""))
	if ops = w.newOps(); len(ops) != 0 {
		t.Errorf("ops after saving unchanged = %+v, want none", ops)
	}

	confirmed := newTransaction(w.client)
	confirmed.Payload["recurrence_id"], confirmed.Payload["occurrence_date"] = created.EntityID, "2026-01-10"
	w.push(confirmed)
	w.newOps()

	w.mustSubmit(http.MethodDelete, "/recurrences/"+created.EntityID, nil)
	if ops = w.newOps(); len(ops) != 1 || ops[0].Entity != "RECURRENCE" || ops[0].Kind != "DELETE" || ops[0].EntityID != created.EntityID {
		t.Errorf("ops after delete = %+v, want one RECURRENCE DELETE", ops)
	}

	transactions := w.snapshot().Transactions
	if len(transactions) != 1 || transactions[0]["deleted_at"] != nil || transactions[0]["recurrence_id"] != created.EntityID {
		t.Errorf("transactions after deleting the recurrence = %v, want the confirmed one intact", transactions)
	}

	if body := get(w.handler, "/recurrences", w.cookie).Body.String(); strings.Contains(body, "Aluguel") {
		t.Errorf("list after delete still shows the recurrence: %s", body)
	}

	if code := get(w.handler, "/recurrences/"+created.EntityID, w.cookie).Code; code != http.StatusNotFound {
		t.Errorf("edit form of deleted recurrence = %d, want %d", code, http.StatusNotFound)
	}
}

func TestWebRecurrences_InvalidFormIs422AndWritesNothing(t *testing.T) {
	w := newWebClient(t)
	deleted := newCategory(w.client)
	w.push(deleted, w.op("CATEGORY", categoryID, "DELETE", nil))
	w.newOps()

	tests := []struct {
		name   string
		form   url.Values
		errors []string
	}{
		{"everything wrong", recurrenceForm("", "SAVING", "0", categoryID, "32", "2026-02-01", "2026-01-31"),
			[]string{"Informe um valor maior que zero.", "Escolha o tipo.", "Escolha uma categoria.", "Informe um dia de 1 a 31.", "Informe um fim igual ou depois do início."}},
		{"day zero", recurrenceForm("", "INCOME", "10", "", "0", "2026-01-01", ""), []string{"Informe um dia de 1 a 31."}},
		{"day not a number", recurrenceForm("", "INCOME", "10", "", "5º", "2026-01-01", ""), []string{"Informe um dia de 1 a 31."}},
		{"missing start", recurrenceForm("", "INCOME", "10", "", "5", "", ""), []string{"Informe o início."}},
		{"bad end", recurrenceForm("", "INCOME", "10", "", "5", "2026-01-01", "31/01/2026"), []string{"Informe um fim igual ou depois do início."}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := w.submit(http.MethodPost, "/recurrences", tt.form)
			if response.Code != http.StatusUnprocessableEntity {
				t.Fatalf("invalid create = %d, want %d: %s", response.Code, http.StatusUnprocessableEntity, response.Body)
			}

			body := response.Body.String()
			for _, message := range tt.errors {
				if !strings.Contains(body, message) {
					t.Errorf("422 body lacks %q: %s", message, body)
				}
			}

			if got := strings.Count(body, `aria-invalid="true"`); got != len(tt.errors) {
				t.Errorf("422 body has %d invalid controls, want %d", got, len(tt.errors))
			}

			if ops := w.newOps(); len(ops) != 0 {
				t.Errorf("ops after invalid create = %+v, want none", ops)
			}
		})
	}
}

func TestWebRecurrences_EditKeepsDeletedCategory(t *testing.T) {
	w := newWebClient(t)
	w.push(newCategory(w.client))
	w.mustSubmit(http.MethodPost, "/recurrences", recurrenceForm("Aluguel", "OUTCOME", "1500", categoryID, "5", "2026-01-01", ""))
	created := w.newOps()[1]
	w.mustSubmit(http.MethodDelete, "/categories/"+categoryID, nil)
	w.newOps()

	w.mustSubmit(http.MethodPost, "/recurrences/"+created.EntityID, recurrenceForm("Aluguel", "OUTCOME", "1500", "", "10", "2026-01-01", ""))
	ops := w.newOps()
	if len(ops) != 1 || !slices.Equal(slices.Sorted(maps.Keys(ops[0].Payload)), []string{"rule", "updated_at"}) {
		t.Errorf("ops after editing the day = %+v, want one UPDATE with only rule and updated_at", ops)
	}
}
