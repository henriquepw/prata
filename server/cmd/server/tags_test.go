package main

import (
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
)

func tagForm(name, startAt, endAt string) url.Values {
	return url.Values{"name": {name}, "start_at": {startAt}, "end_at": {endAt}}
}

func TestWebTags_WritesReachDevicePull(t *testing.T) {
	w := newWebClient(t)
	tz := &http.Cookie{Name: "tz", Value: "America/Sao_Paulo"}

	w.mustSubmit(http.MethodPost, "/tags", tagForm("  Viagem ", "2026-07-01", ""), tz)
	ops := w.newOps()
	if len(ops) != 1 {
		t.Fatalf("ops after create = %+v, want one", ops)
	}

	created := ops[0]
	if created.Entity != "TAG" || created.Kind != "CREATE" || created.DeviceID == w.deviceID ||
		created.Payload["name"] != "Viagem" || created.Payload["start_at"] != "2026-07-01T03:00:00Z" || created.Payload["end_at"] != nil {
		t.Errorf("create op = %+v, want a TAG CREATE with the trimmed name, local midnight in UTC and no end", created)
	}

	page := get(w.handler, "/tags", w.cookie, tz)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Viagem") || !strings.Contains(page.Body.String(), "01/07/2026") {
		t.Errorf("list = %d, want Viagem from 01/07/2026: %s", page.Code, page.Body)
	}

	edit := get(w.handler, "/tags/"+created.EntityID, w.cookie, tz)
	if edit.Code != http.StatusOK || strings.Contains(edit.Body.String(), "<html") || !strings.Contains(edit.Body.String(), `value="2026-07-01"`) {
		t.Errorf("edit form = %d, want the bare form fragment with the local start date: %s", edit.Code, edit.Body)
	}

	w.mustSubmit(http.MethodPost, "/tags/"+created.EntityID, tagForm("Viagem", "2026-07-01", "2026-07-31"), tz)
	ops = w.newOps()
	if len(ops) != 1 || ops[0].Kind != "UPDATE" || ops[0].EntityID != created.EntityID ||
		!slices.Equal(slices.Sorted(maps.Keys(ops[0].Payload)), []string{"end_at", "updated_at"}) || ops[0].Payload["end_at"] != "2026-07-31T03:00:00Z" {
		t.Errorf("ops after edit = %+v, want one UPDATE with only end_at and updated_at", ops)
	}

	w.mustSubmit(http.MethodPost, "/tags/"+created.EntityID, tagForm("Viagem", "2026-07-01", "2026-07-31"), tz)
	if ops = w.newOps(); len(ops) != 0 {
		t.Errorf("ops after saving unchanged = %+v, want none", ops)
	}

	w.mustSubmit(http.MethodDelete, "/tags/"+created.EntityID, nil, tz)
	if ops = w.newOps(); len(ops) != 1 || ops[0].Kind != "DELETE" || ops[0].EntityID != created.EntityID || len(ops[0].Payload) != 0 {
		t.Errorf("ops after delete = %+v, want one DELETE tombstone", ops)
	}

	if body := get(w.handler, "/tags", w.cookie).Body.String(); strings.Contains(body, "Viagem") {
		t.Errorf("list after delete still shows the tag: %s", body)
	}

	if code := get(w.handler, "/tags/"+created.EntityID, w.cookie).Code; code != http.StatusNotFound {
		t.Errorf("edit form of deleted tag = %d, want %d", code, http.StatusNotFound)
	}
}

func TestWebTags_InvalidFormIs422AndWritesNothing(t *testing.T) {
	tests := []struct {
		name     string
		form     url.Values
		messages []string
	}{
		{"blank name and no start", tagForm("   ", "", ""), []string{"Informe um nome.", "Informe o início."}},
		{"end before start", tagForm("Viagem", "2026-07-10", "2026-07-09"), []string{"Informe um fim igual ou depois do início."}},
		{"malformed end", tagForm("Viagem", "2026-07-10", "amanhã"), []string{"Informe um fim igual ou depois do início."}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newWebClient(t)

			response := w.submit(http.MethodPost, "/tags", tt.form)
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

	w := newWebClient(t)
	w.mustSubmit(http.MethodPost, "/tags", tagForm("Viagem", "2026-07-10", "2026-07-10"))
}
