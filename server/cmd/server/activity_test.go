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

func TestWebOverview_ActivityLogListsLatestOpsOfEveryDevice(t *testing.T) {
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
		at(phone.op("TRANSACTION", ops[2].EntityID, "DELETE", nil), "2026-08-23T17:35:00Z"),
	)

	body := get(w.handler, "/", w.cookie, &http.Cookie{Name: "tz", Value: "America/Sao_Paulo"}).Body.String()
	if got := strings.Count(body, "&gt; ["); got != 12 {
		t.Errorf("activity log has %d lines, want 12: %s", got, body)
	}

	if strings.Contains(body, "Viagem") {
		t.Errorf("activity log shows the 13th newest op: %s", body)
	}

	rest := body
	for _, want := range []string{
		"[14:35]", "phone · transação excluída",
		"[14:32]", `phone · transação "Uber" alterada`,
		"[10:10]", `laptop · transação "Compra 10" criada`,
		"[10:01]", `laptop · transação "Compra 1" criada`,
	} {
		i := strings.Index(rest, html.EscapeString(want))
		if i < 0 {
			t.Fatalf("activity log lacks %q after the previous line: %s", want, body)
		}

		rest = rest[i:]
	}
}
