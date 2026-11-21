package main

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestWebLists_AreConsolePanelsWithEmptyStates(t *testing.T) {
	w := newWebClient(t)
	w.mustSubmit(http.MethodDelete, "/devices/"+w.deviceID, nil)

	for path, empty := range map[string]string{
		"/tags":        "nenhuma tag ainda",
		"/recurrences": "nenhuma recorrência ainda",
		"/devices":     "nenhum dispositivo conectado",
	} {
		body := get(w.handler, path, w.cookie).Body.String()
		if !strings.Contains(body, `class="panel`) || !strings.Contains(body, `¯\_(ツ)_/¯`) || !strings.Contains(body, empty) {
			t.Errorf("%s lacks a panel with the empty state %q: %s", path, empty, body)
		}

		if hasNew := strings.Contains(body, "+ nova [n]"); hasNew == (path == "/devices") {
			t.Errorf("%s shows + nova [n]? %v", path, hasNew)
		}
	}
}

func TestWebLists_RowsOpenTheSheetAndFlashOnce(t *testing.T) {
	for _, list := range []struct {
		path string
		form url.Values
	}{
		{"/categories", categoryForm("Mercado", "GREEN", "0")},
		{"/tags", tagForm("Viagem", "2026-07-01", "")},
		{"/recurrences", recurrenceForm("Aluguel", "OUTCOME", "1.500,00", "", "5", "2026-01-01", "")},
	} {
		w := newWebClient(t)
		flash := flashCookie(w.submit(http.MethodPost, list.path, list.form))
		id := w.newOps()[0].EntityID
		if flash == nil || flash.Value != id || flash.Path != list.path {
			t.Fatalf("%s create sets flash cookie %v, want the saved row's id for that list only", list.path, flash)
		}

		body := get(w.handler, list.path, w.cookie, flash).Body.String()
		row := `hx-get="` + list.path + "/" + id + `" hx-target="#sheet" tabindex="0"`
		if !strings.Contains(body, row) || strings.Count(body, "data-flash") != 1 {
			t.Errorf("%s lacks the focusable row %q flashed once: %s", list.path, row, body)
		}
	}
}
