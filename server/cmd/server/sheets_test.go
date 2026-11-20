package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestWebDelete_ConfirmsInlineAndDeletesThroughHxDelete(t *testing.T) {
	w := newWebClient(t)
	phone := signinClient(t, w.client, newSignin().DeviceID)
	w.mustSubmit(http.MethodPost, "/categories", categoryForm("Mercado", "GREEN", "0"))
	w.mustSubmit(http.MethodPost, "/tags", tagForm("Viagem", "2026-07-01", ""))
	w.mustSubmit(http.MethodPost, "/recurrences", recurrenceForm("Aluguel", "OUTCOME", "1500", "", "5", "2026-01-01", ""))
	w.mustSubmit(http.MethodPost, "/transactions", transactionForm("Feira", "OUTCOME", "10", "2026-07-15", "", ""))
	ids := map[string]string{}
	for _, op := range w.newOps() {
		ids[op.Entity] = op.EntityID
	}

	sheets := []struct{ entity, path, question string }{
		{"TRANSACTION", "/transactions/", "excluir esta transação?"},
		{"RECURRENCE", "/recurrences/", "excluir esta recorrência?"},
		{"TAG", "/tags/", "excluir esta tag?"},
		{"CATEGORY", "/categories/", "excluir esta categoria?"},
	}
	for _, page := range []string{"/", "/transactions?month=2026-07", "/categories", "/tags", "/recurrences", "/devices"} {
		if body := get(w.handler, page, w.cookie).Body.String(); strings.Contains(body, "hx-confirm") {
			t.Errorf("%s renders hx-confirm: %s", page, body)
		}
	}

	devices := get(w.handler, "/devices", w.cookie).Body.String()
	if !strings.Contains(devices, "revogar phone?") || !strings.Contains(devices, "sim, revogar") {
		t.Errorf("devices lacks the inline revoke confirmation: %s", devices)
	}

	w.mustSubmit(http.MethodDelete, "/devices/"+phone.deviceID, nil)

	for _, sheet := range sheets {
		path := sheet.path + ids[sheet.entity]
		body := get(w.handler, path, w.cookie).Body.String()
		if strings.Contains(body, "hx-confirm") || !strings.Contains(body, sheet.question) || !strings.Contains(body, "sim, excluir") ||
			!strings.Contains(body, `hx-delete="`+path+`"`) {
			t.Errorf("%s sheet = %s, want %q confirmed inline with hx-delete and no hx-confirm", path, body, sheet.question)
		}

		w.mustSubmit(http.MethodDelete, path, nil)
		if code := get(w.handler, path, w.cookie).Code; code != http.StatusNotFound {
			t.Errorf("%s after delete = %d, want %d", path, code, http.StatusNotFound)
		}
	}
}
