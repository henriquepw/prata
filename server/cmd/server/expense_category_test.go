package main

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

const recurrenceID = "01JXQ8ZA000000000000000000"

func TestWebTransactions_ExpenseAlwaysHasACategory(t *testing.T) {
	w := newWebClient(t)
	w.push(newCategory(w.client))
	w.newOps()

	sheet := get(w.handler, "/transactions/new", w.cookie).Body.String()
	if want := `value="` + w.defaultID + `" selected`; !strings.Contains(sheet, want) || strings.Contains(sheet, `<option value="" `) {
		t.Errorf("new sheet = %s, want the default category selected and no empty option", sheet)
	}

	w.mustSubmit(http.MethodPost, "/transactions", transactionForm("Feira", "OUTCOME", "10", "2026-07-01", "", ""))
	w.mustSubmit(http.MethodPost, "/transactions", transactionForm("Sorvete", "OUTCOME", "10", "2026-07-01", categoryID, ""))
	ops := w.newOps()
	if len(ops) != 2 || ops[0].Payload["category_id"] != w.defaultID || ops[1].Payload["category_id"] != categoryID {
		t.Errorf("ops = %+v, want the blank category saved as the default and the chosen one kept", ops)
	}
}

func TestWebTransactions_IncomeIsSavedWithoutCategory(t *testing.T) {
	w := newWebClient(t)
	w.push(newCategory(w.client))
	w.newOps()

	w.mustSubmit(http.MethodPost, "/transactions", transactionForm("Salário", "INCOME", "1000", "2026-07-01", categoryID, ""))
	ops := w.newOps()
	if len(ops) != 1 {
		t.Fatalf("ops = %+v, want one", ops)
	}

	if category, ok := ops[0].Payload["category_id"]; !ok || category != nil {
		t.Errorf("income payload = %v, want category_id null", ops[0].Payload)
	}

	w.mustSubmit(http.MethodPost, "/transactions/"+ops[0].EntityID, transactionForm("Salário", "OUTCOME", "1000", "2026-07-01", "", ""))
	if ops = w.newOps(); len(ops) != 1 || ops[0].Payload["category_id"] != w.defaultID {
		t.Errorf("ops after turning it into an expense = %+v, want the default category", ops)
	}
}

func TestWebRecurrences_ExpenseAlwaysHasACategoryAndIncomeNone(t *testing.T) {
	w := newWebClient(t)
	w.push(newCategory(w.client))
	w.newOps()

	sheet := get(w.handler, "/recurrences/new", w.cookie).Body.String()
	if want := `value="` + w.defaultID + `" selected`; !strings.Contains(sheet, want) || strings.Contains(sheet, `<option value="" `) {
		t.Errorf("new sheet = %s, want the default category selected and no empty option", sheet)
	}

	w.mustSubmit(http.MethodPost, "/recurrences", recurrenceForm("Aluguel", "OUTCOME", "1500", "", "5", "2026-01-01", ""))
	w.mustSubmit(http.MethodPost, "/recurrences", recurrenceForm("Salário", "INCOME", "5000", categoryID, "5", "2026-01-01", ""))
	ops := w.newOps()
	if len(ops) != 2 || ops[0].Payload["category_id"] != w.defaultID {
		t.Fatalf("ops = %+v, want the expense saved in the default category", ops)
	}

	if category, ok := ops[1].Payload["category_id"]; !ok || category != nil {
		t.Errorf("income payload = %v, want category_id null", ops[1].Payload)
	}
}

func TestWebLaunch_ExpenseGoesToTheDefaultCategory(t *testing.T) {
	w := newWebClient(t)
	w.newOps()

	for _, line := range []string{"-10 padaria", "+10 bônus"} {
		form := url.Values{"line": {line}, "month": {"2026-11"}}
		if response := w.submit(http.MethodPost, "/launch", form); response.Code >= 300 {
			t.Fatalf("launch %q = %d: %s", line, response.Code, response.Body)
		}
	}

	ops := w.newOps()
	if len(ops) != 2 || ops[0].Payload["category_id"] != w.defaultID || ops[1].Payload["category_id"] != nil {
		t.Errorf("ops = %+v, want the expense in the default category and the income in none", ops)
	}
}

func TestPush_ExpenseWithoutCategoryGoesToTheDefault(t *testing.T) {
	c := newClient(t, newHandler(openStore(t), config{}), newSignup())

	var defaultID string
	for _, o := range c.pull(0, 500).Ops {
		if o.Payload["is_default"] == float64(1) {
			defaultID = o.EntityID
		}
	}

	fields := func(kind string, category any) map[string]any {
		return map[string]any{"category_id": category, "kind": kind, "amount": 4590, "occurred_at": "2026-08-23",
			"created_at": "2026-08-23T14:02:11Z", "updated_at": "2026-08-23T14:02:11Z"}
	}
	absent := fields("OUTCOME", nil)
	delete(absent, "category_id")
	recurrence := fields("OUTCOME", nil)
	recurrence["rule"], recurrence["start_at"] = "MONTHLY:5", "2026-01-01"
	pushed := c.push(
		c.op("TRANSACTION", transactionID, "CREATE", fields("OUTCOME", nil)),
		c.op("TRANSACTION", "01JXQ8Z8100000000000000000", "CREATE", absent),
		c.op("TRANSACTION", "01JXQ8Z8200000000000000000", "CREATE", fields("INCOME", nil)),
		c.op("RECURRENCE", recurrenceID, "CREATE", recurrence),
	)
	if len(pushed.Rejected) != 0 {
		t.Fatalf("push = %+v, want everything accepted", pushed)
	}

	got := map[string]any{}
	for _, o := range c.pull(seededOps, 500).Ops {
		got[o.EntityID] = o.Payload["category_id"]
	}

	want := map[string]any{transactionID: defaultID, "01JXQ8Z8100000000000000000": defaultID, "01JXQ8Z8200000000000000000": nil, recurrenceID: defaultID}
	for id, category := range want {
		if got[id] != category {
			t.Errorf("category of %s = %v, want %v", id, got[id], category)
		}
	}
}

func TestWeb_NoScreenSaysSemCategoria(t *testing.T) {
	w := newWebClient(t)
	w.mustSubmit(http.MethodPost, "/transactions", transactionForm("Salário", "INCOME", "1000", "2026-11-01", "", ""))

	for _, path := range []string{"/?month=2026-11", "/transactions/new", "/recurrences/new", "/launch?month=2026-11&line=%2B150"} {
		if body := get(w.handler, path, w.cookie).Body.String(); strings.Contains(strings.ToLower(body), "sem categoria") {
			t.Errorf("%s = %s, want no \"sem categoria\"", path, body)
		}
	}
}

func TestPush_UpdateTurningAnIncomeIntoAnExpenseGoesToTheDefault(t *testing.T) {
	c := newClient(t, newHandler(openStore(t), config{}), newSignup())

	var defaultID string
	for _, o := range c.pull(0, 500).Ops {
		if o.Payload["is_default"] == float64(1) {
			defaultID = o.EntityID
		}
	}

	income := newTransaction(c)
	income.Payload["kind"], income.Payload["category_id"] = "INCOME", nil
	c.push(income)
	c.push(c.op("TRANSACTION", transactionID, "UPDATE", map[string]any{"kind": "OUTCOME", "updated_at": "2026-08-24T10:00:00Z"}))

	ops := c.pull(seededOps+1, 500).Ops
	if len(ops) != 1 || ops[0].Payload["category_id"] != defaultID {
		t.Errorf("ops = %+v, want the UPDATE carrying the default category", ops)
	}
}

func TestPush_IncomeIsStoredWithoutCategory(t *testing.T) {
	w := newWebClient(t)
	w.push(newCategory(w.client))

	income := newTransaction(w.client)
	income.Payload["kind"] = "INCOME"
	w.push(income)
	w.mustSubmit(http.MethodPost, "/transactions", transactionForm("Feira", "OUTCOME", "10", "2026-07-01", categoryID, ""))
	created := w.newOps()
	expenseID := created[len(created)-1].EntityID
	w.push(w.op("TRANSACTION", expenseID, "UPDATE", map[string]any{"kind": "INCOME", "updated_at": "2026-08-24T10:00:00Z"}))

	ops := w.pull(seededOps, 500).Ops
	pushed, turned := ops[1], ops[len(ops)-1]
	if category, ok := pushed.Payload["category_id"]; pushed.EntityID != transactionID || !ok || category != nil {
		t.Errorf("pulled income = %+v, want category_id null", pushed)
	}

	if category, ok := turned.Payload["category_id"]; turned.EntityID != expenseID || !ok || category != nil {
		t.Errorf("pulled expense turned income = %+v, want category_id null", turned)
	}
}
