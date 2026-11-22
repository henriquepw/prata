package main

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

const pluggyItemID = "11111111-2222-3333-4444-555555555555"

// pluggyItem is the JSON of GET /items/{id} for an item of the institution in status and executionStatus.
func pluggyItem(institution, status, executionStatus string) map[string]any {
	return map[string]any{
		"id":              pluggyItemID,
		"status":          status,
		"executionStatus": executionStatus,
		"lastUpdatedAt":   "2026-11-10T13:30:00.000Z",
		"connector":       map[string]any{"id": 2, "name": institution},
	}
}

// serveItem makes the fake answer GET /items/{id} for pluggyItemID with item, and 404 for any other; calling it again
// changes what the fake answers.
func (f *fakePluggy) serveItem(item map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.item = item
	if f.itemServed {
		return
	}

	f.itemServed = true
	f.handle("GET /items/{id}", func(r *http.Request) (int, any) {
		f.mu.Lock()
		defer f.mu.Unlock()

		if r.PathValue("id") != pluggyItemID {
			return http.StatusNotFound, map[string]any{"code": 404, "message": "Item not found"}
		}

		return http.StatusOK, f.item
	})
}

// withCredentials saves the Pluggy credentials of the fake for the web user.
func (w *webClient) withCredentials() {
	w.t.Helper()
	w.mustSubmit(http.MethodPost, "/settings", withPluggy(settingsForm("DD/MM/YYYY", "1.234,56"), pluggyClientID, pluggyClientSecret))
}

// connect registers the item on the account, as the Conexões form does.
func (w *webClient) connect(itemID, accountID string) bool {
	w.t.Helper()

	return w.submit(http.MethodPost, "/connections", url.Values{"item_id": {itemID}, "account_id": {accountID}}).Header().Get("HX-Refresh") == "true"
}

func (w *webClient) connectionRows() int {
	w.t.Helper()

	query := "SELECT COUNT(*) FROM connections"

	var n int
	if err := w.db.GetContext(context.Background(), &n, query); err != nil {
		w.t.Fatal(err)
	}

	return n
}

// connectionID is the id of the only Conexão in the database.
func (w *webClient) connectionID() string {
	w.t.Helper()

	query := "SELECT id FROM connections"

	var id string
	if err := w.db.GetContext(context.Background(), &id, query); err != nil {
		w.t.Fatal(err)
	}

	return id
}

func TestConnections_HiddenWithoutTheEncryptionKey(t *testing.T) {
	w := newWebClientWith(t, config{pluggyURL: newFakePluggy(t).URL})

	if response := get(w.handler, "/connections", w.cookie); response.Code != http.StatusNotFound {
		t.Errorf("GET /connections without the key = %d, want %d", response.Code, http.StatusNotFound)
	}

	if body := get(w.handler, "/devices", w.cookie).Body.String(); strings.Contains(body, "/connections") {
		t.Errorf("nav shows Conexões without the key: %s", body)
	}
}

func TestConnections_RegisteredByItemIDAndListedWithInstitutionAndLastUpdate(t *testing.T) {
	fake := newFakePluggy(t)
	fake.serveItem(pluggyItem("Pluggy Bank", "UPDATED", "SUCCESS"))
	w := newWebClientWith(t, fake.config())
	w.withCredentials()

	if !w.connect(pluggyItemID, w.accountID()) {
		t.Fatal("POST /connections did not succeed")
	}

	w.assertBody("/connections", "Pluggy Bank", "10/11/2026")
	assertContains(t, get(w.handler, "/devices", w.cookie).Body.String(), `href="/connections"`, "Ir para conexões")
	if body := get(w.handler, "/connections", w.cookie).Body.String(); strings.Contains(body, "reconecte") {
		t.Errorf("healthy connection shows the reconnect warning: %s", body)
	}
}

func TestConnections_WithoutCredentialsTheScreenLeadsToSettings(t *testing.T) {
	w := newWebClientWith(t, newFakePluggy(t).config())

	body := get(w.handler, "/connections", w.cookie).Body.String()
	assertContains(t, body, `hx-get="/settings"`, "credenciais da Pluggy")

	if strings.Contains(body, `name="item_id"`) {
		t.Errorf("screen without credentials shows the form: %s", body)
	}
}

func TestConnections_UnknownItemShowsTheErrorAndSavesNothing(t *testing.T) {
	fake := newFakePluggy(t)
	fake.serveItem(pluggyItem("Pluggy Bank", "UPDATED", "SUCCESS"))
	w := newWebClientWith(t, fake.config())
	w.withCredentials()

	response := w.submit(http.MethodPost, "/connections", url.Values{"item_id": {"00000000-0000-0000-0000-000000000000"}, "account_id": {w.accountID()}})
	if body := response.Body.String(); response.Code != http.StatusUnprocessableEntity || !strings.Contains(body, "A Pluggy não achou esse Item ID.") || !strings.Contains(body, `aria-invalid="true"`) {
		t.Errorf("POST /connections with an unknown item = %d, want %d with the error: %s", response.Code, http.StatusUnprocessableEntity, body)
	}

	if n := w.connectionRows(); n != 0 {
		t.Errorf("connections = %d, want none saved", n)
	}
}

func TestConnections_OnlyBRLAccountsAreOffered(t *testing.T) {
	fake := newFakePluggy(t)
	fake.serveItem(pluggyItem("Pluggy Bank", "UPDATED", "SUCCESS"))
	w := newWebClientWith(t, fake.config())
	w.withCredentials()
	usd := w.addAccount("Viagem", "USD")

	assertContains(t, get(w.handler, "/connections", w.cookie).Body.String(), "disabled", "Viagem (USD): só BRL")

	response := w.submit(http.MethodPost, "/connections", url.Values{"item_id": {pluggyItemID}, "account_id": {usd}})
	if body := response.Body.String(); response.Code != http.StatusUnprocessableEntity || !strings.Contains(body, "Só contas em BRL aceitam conexões.") {
		t.Errorf("POST /connections on a USD account = %d, want %d with the error: %s", response.Code, http.StatusUnprocessableEntity, body)
	}

	if n := w.connectionRows(); n != 0 {
		t.Errorf("connections = %d, want none saved", n)
	}
}

func TestConnections_ErrorStateAsksToReconnect(t *testing.T) {
	for name, item := range map[string]map[string]any{
		"login error":    pluggyItem("Banco A", "LOGIN_ERROR", "INVALID_CREDENTIALS"),
		"waiting mfa":    pluggyItem("Banco A", "WAITING_USER_INPUT", "WAITING_USER_INPUT"),
		"account locked": pluggyItem("Banco A", "OUTDATED", "ACCOUNT_LOCKED"),
		"authorization":  pluggyItem("Banco A", "OUTDATED", "USER_AUTHORIZATION_REVOKED"),
	} {
		t.Run(name, func(t *testing.T) {
			fake := newFakePluggy(t)
			fake.serveItem(item)
			w := newWebClientWith(t, fake.config())
			w.withCredentials()
			w.connect(pluggyItemID, w.accountID())

			w.assertBody("/connections", "reconecte em meu.pluggy.ai")
		})
	}

	fake := newFakePluggy(t)
	fake.serveItem(pluggyItem("Banco A", "OUTDATED", "SITE_NOT_AVAILABLE"))
	w := newWebClientWith(t, fake.config())
	w.withCredentials()
	w.connect(pluggyItemID, w.accountID())

	if body := get(w.handler, "/connections", w.cookie).Body.String(); strings.Contains(body, "reconecte") {
		t.Errorf("a transient failure asks to reconnect: %s", body)
	}
}

func TestConnections_AreOnlyTheirOwnersEvenInTheSameAccount(t *testing.T) {
	fake := newFakePluggy(t)
	fake.serveItem(pluggyItem("Pluggy Bank", "UPDATED", "SUCCESS"))
	w := newWebClientWith(t, fake.config())
	w.withCredentials()
	w.connect(pluggyItemID, w.accountID())

	mate := w.newAccountMate()
	if body := get(mate.handler, "/connections", mate.cookie).Body.String(); strings.Contains(body, "Pluggy Bank") {
		t.Errorf("another user of the account sees the connection: %s", body)
	}

	id := w.connectionID()

	if response := mate.submit(http.MethodDelete, "/connections/"+id, nil); response.Code != http.StatusNotFound {
		t.Errorf("DELETE of another user's connection = %d, want %d", response.Code, http.StatusNotFound)
	}

	if n := w.connectionRows(); n != 1 {
		t.Errorf("connections = %d, want the owner's kept", n)
	}
}

func TestConnections_RemovedWithoutCallingPluggyDroppingOnlyPendingMovements(t *testing.T) {
	fake := newFakePluggy(t)
	fake.serveItem(pluggyItem("Pluggy Bank", "UPDATED", "SUCCESS"))
	w := newWebClientWith(t, fake.config())
	w.withCredentials()
	account := w.accountID()
	w.connect(pluggyItemID, account)

	id := w.connectionID()

	for status, external := range map[string]string{"PENDING": "p", "CONFIRMED": "c", "IGNORED": "i"} {
		query := `
			INSERT INTO movements (id, account_id, connection_id, institution, bank_account, bank_account_kind, external_id, description, kind, amount, occurred_at, status, created_at)
			VALUES (?1, ?2, ?3, 'Pluggy Bank', 'cc', 'CHECKING', ?1, ?4, 'OUTCOME', 100, '2026-11-10', ?5, '2026-11-10T00:00:00Z')
		`
		if _, err := w.db.ExecContext(context.Background(), query, external, account, id, "mov "+status, status); err != nil {
			t.Fatal(err)
		}
	}

	before := fake.requests.Load()

	if response := w.submit(http.MethodDelete, "/connections/"+id, nil); response.Code >= 300 || response.Header().Get("HX-Refresh") != "true" {
		t.Fatalf("DELETE /connections/%s = %d, want success with HX-Refresh: %s", id, response.Code, response.Body)
	}

	if after := fake.requests.Load(); after != before {
		t.Errorf("removing the connection made %d calls to Pluggy, want none", after-before)
	}

	if body := get(w.handler, "/connections", w.cookie).Body.String(); strings.Contains(body, "Pluggy Bank") {
		t.Errorf("removed connection is still listed: %s", body)
	}

	query := "SELECT status FROM movements ORDER BY status"

	var statuses []string
	if err := w.db.SelectContext(context.Background(), &statuses, query); err != nil {
		t.Fatal(err)
	}

	if got := strings.Join(statuses, ","); got != "CONFIRMED,IGNORED" {
		t.Errorf("movements left = %s, want CONFIRMED,IGNORED", got)
	}
}
