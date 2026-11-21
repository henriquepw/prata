package main

import (
	"fmt"
	"html"
	"net/http"
	"strings"
	"testing"

	"github.com/oklog/ulid/v2"
)

// at is the op with created_at moved to the given UTC instant.
func at(o op, instant string) op {
	o.CreatedAt = instant

	return o
}

func TestWebDevices_ActivityLogListsLatestOpsOfEveryDeviceByTime(t *testing.T) {
	w := newWebClient(t)
	phone := signinClient(t, w.client, newSignin().DeviceID)
	ops := []op{at(w.op("TAG", ulid.Make().String(), "CREATE", map[string]any{
		"name": "Viagem", "created_at": "2026-08-23T12:00:00Z", "updated_at": "2026-08-23T12:00:00Z",
	}), "2026-08-23T12:00:00Z")}
	for i := 1; i <= 10; i++ {
		created := fmt.Sprintf("2026-08-23T13:%02d:00Z", i)
		ops = append(ops, at(w.op("TRANSACTION", ulid.Make().String(), "CREATE", map[string]any{
			"kind": "OUTCOME", "amount": 1000, "description": fmt.Sprintf("Compra %d", i), "occurred_at": created,
			"created_at": created, "updated_at": created,
		}), created))
	}

	w.push(ops...)
	phone.push(
		at(phone.op("TRANSACTION", ops[1].EntityID, "UPDATE", map[string]any{"description": "Uber", "updated_at": "2026-08-23T17:32:00Z"}), "2026-08-23T17:32:00Z"),
		at(phone.op("TRANSACTION", ops[2].EntityID, "DELETE", nil), "2026-08-23T13:05:30Z"),
	)

	if body := get(w.handler, "/", w.cookie).Body.String(); strings.Contains(body, "Log de atividade") {
		t.Errorf("the month shows the activity log: %s", body)
	}

	body := get(w.handler, "/devices", w.cookie, &http.Cookie{Name: "tz", Value: "America/Sao_Paulo"}).Body.String()
	if got := strings.Count(body, "&gt; ["); got != 12 {
		t.Errorf("activity log has %d lines, want 12: %s", got, body)
	}

	if strings.Contains(body, "Viagem") || strings.Contains(body, `"Compra 1"`) {
		t.Errorf("activity log shows an op older than the 12th newest: %s", body)
	}

	rest := body
	// the four seeded categories of the signup are the newest ops
	for _, want := range []string{
		"[14:32]", `phone · transação "Uber" alterada`,
		"[10:10]", `laptop · transação "Compra 10" criada`,
		"[10:06]", `laptop · transação "Compra 6" criada`,
		"[10:05]", "phone · transação excluída",
		"[10:05]", `laptop · transação "Compra 5" criada`,
	} {
		i := strings.Index(rest, html.EscapeString(want))
		if i < 0 {
			t.Fatalf("activity log lacks %q after the previous line: %s", want, body)
		}

		rest = rest[i:]
	}
}
