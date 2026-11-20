package main

import (
	"maps"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestWebLaunch_CreatesTheTransactionAndSwapsTheMonth(t *testing.T) {
	w := newWebClient(t)
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Fatal(err)
	}

	tz := &http.Cookie{Name: "tz", Value: loc.String()}
	now := time.Now().In(loc)
	w.push(newCategory(w.client), newTag(w.client))
	w.newOps()

	form := url.Values{"line": {"-45,90 padaria ontem @merc #viag"}, "month": {now.Format("2006-01")}, "category": {categoryID}}
	response := w.submit(http.MethodPost, "/launch", form, tz)
	fragment := response.Body.String()
	if response.Code != http.StatusOK || strings.Contains(fragment, "<html") || strings.Count(fragment, "data-flash") != 1 ||
		!strings.Contains(fragment, "padaria") || !strings.Contains(fragment, "= Projetado") || !strings.Contains(fragment, "Mercado ✕") {
		t.Errorf("launch = %d, want the boxes and the statement, still filtered, with the new row flashing: %s", response.Code, fragment)
	}

	ops := w.newOps()
	if len(ops) != 1 || ops[0].Entity != "TRANSACTION" || ops[0].Kind != "CREATE" {
		t.Fatalf("ops after launch = %+v, want one TRANSACTION CREATE", ops)
	}

	yesterday := time.Date(now.Year(), now.Month(), now.Day()-1, 0, 0, 0, 0, loc)
	want := map[string]any{"kind": "OUTCOME", "amount": float64(4590), "description": "padaria", "category_id": categoryID,
		"tag_id": tagID, "occurred_at": yesterday.UTC().Format(time.RFC3339)}
	maps.DeleteFunc(ops[0].Payload, func(field string, _ any) bool { return field == "created_at" || field == "updated_at" })
	if !maps.Equal(ops[0].Payload, want) {
		t.Errorf("launch payload = %v, want %v", ops[0].Payload, want)
	}
}

func TestWebLaunch_InvalidLineIs422(t *testing.T) {
	w := newWebClient(t)
	w.push(newCategory(w.client))
	w.newOps()

	response := w.submit(http.MethodPost, "/launch", url.Values{"line": {"padaria @xyz"}})
	body := response.Body.String()
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(body, "! falta o valor") || !strings.Contains(body, "! @xyz não existe") {
		t.Errorf("invalid launch = %d, want %d with the preview's problems: %s", response.Code, http.StatusUnprocessableEntity, body)
	}

	if ops := w.newOps(); len(ops) != 0 {
		t.Errorf("ops after an invalid launch = %+v, want none", ops)
	}
}
