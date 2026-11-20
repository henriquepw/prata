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

	if prompt := fragment[strings.Index(fragment, `id="launch"`):]; !strings.Contains(prompt[:strings.Index(prompt, ">")], "autofocus") {
		t.Errorf("launch answers the prompt without autofocus: %s", fragment)
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

func TestWebLaunch_PreviewsTheLine(t *testing.T) {
	w := newWebClient(t)
	w.push(newCategory(w.client), newTag(w.client))
	tests := []struct {
		line string
		want []string
	}{
		{"", []string{"-45,90 despesa, +150 receita, @categoria, #tag, ontem ou dd/mm"}},
		{"-45,90 padaria 18/11 @merc #viag", []string{"[despesa]", "−R$ 45,90", `"padaria"`, "Mercado", "#Viagem", "18/11/2026"}},
		{"+150", []string{"[receita]", "R$ 150,00", "sem descrição", "sem categoria"}},
		{"padaria @xyz", []string{"! falta o valor", "! @xyz não existe"}},
		{"10 @m", []string{"tab", "@mercado"}},
	}
	for _, tt := range tests {
		response := get(w.handler, "/launch?month=2026-11&line="+url.QueryEscape(tt.line), w.cookie)
		body := response.Body.String()
		for _, text := range tt.want {
			if response.Code != http.StatusOK || !strings.Contains(body, text) || strings.Contains(body, "<html") {
				t.Errorf("preview of %q = %d, want the fragment with %q: %s", tt.line, response.Code, text, body)
			}
		}
	}
}

func TestWebLaunch_MonthHasThePromptAndItsKey(t *testing.T) {
	w := newWebClient(t)
	body := get(w.handler, "/", w.cookie).Body.String()
	if strings.Contains(body, "autofocus") {
		t.Errorf("month opens with the prompt focused: %s", body)
	}

	for _, text := range []string{`id="launch"`, `aria-describedby="launch-preview"`, `aria-live="polite"`, "Lançar", "focar", "<kbd>/</kbd> lançar"} {
		if !strings.Contains(body, text) {
			t.Errorf("month lacks %q: %s", text, body)
		}
	}

	if body := get(w.handler, "/categories", w.cookie).Body.String(); strings.Contains(body, "<kbd>/</kbd> lançar") {
		t.Errorf("categories shows the prompt's key: %s", body)
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
