package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	pluggyBankAccountID = "aaaaaaaa-0000-0000-0000-000000000001"
	pluggyCardAccountID = "aaaaaaaa-0000-0000-0000-000000000002"
)

// pluggyAccount is the JSON of one entry of GET /accounts for the item.
func pluggyAccount(id, kind, name string) map[string]any {
	return map[string]any{"id": id, "type": kind, "currencyCode": "BRL", "name": name}
}

// pluggyTransaction is the JSON of one entry of GET /v2/transactions; amount is the literal Pluggy sends, in reais.
func pluggyTransaction(id, status, date, description string, amount any) map[string]any {
	return map[string]any{"id": id, "status": status, "date": date + "T03:00:00.000Z", "description": description, "amount": amount, "operationType": nil}
}

// serveAccounts makes the fake answer GET /accounts for pluggyItemID with these accounts.
func (f *fakePluggy) serveAccounts(accounts ...map[string]any) {
	f.handle("GET /accounts", func(r *http.Request) (int, any) {
		if r.URL.Query().Get("itemId") != pluggyItemID {
			return http.StatusOK, map[string]any{"total": 0, "results": []any{}}
		}

		return http.StatusOK, map[string]any{"total": len(accounts), "results": accounts}
	})
}

// serveTransactions makes the fake answer GET /v2/transactions for the account with these pages, linking each to the
// next by the cursor in `next`, and records the dateFrom of every request in dateFroms.
func (f *fakePluggy) serveTransactions(accountID string, pages ...[]map[string]any) {
	f.handle("GET /v2/transactions", func(r *http.Request) (int, any) {
		query := r.URL.Query()
		if query.Get("accountId") != accountID {
			return http.StatusOK, map[string]any{"next": nil, "results": []any{}}
		}

		f.recordDateFrom(query.Get("dateFrom"))

		page, _ := strconv.Atoi(query.Get("cursor"))

		var next any
		if page+1 < len(pages) {
			next = "?" + url.Values{"accountId": {accountID}, "dateFrom": {query.Get("dateFrom")}, "cursor": {strconv.Itoa(page + 1)}}.Encode()
		}

		return http.StatusOK, map[string]any{"next": next, "results": pages[page]}
	})
}

func (f *fakePluggy) recordDateFrom(date string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dates = append(f.dates, date)
}

// dateFroms are the dateFrom of every transactions request so far.
func (f *fakePluggy) dateFroms() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.dates)
}

// fetchNow presses "buscar agora" on the Conexões screen.
func (w *webClient) fetchNow() *http.Response {
	w.t.Helper()
	response := w.submit(http.MethodPost, "/connections/fetch", nil)

	return response.Result()
}

// connected is a web client with Pluggy credentials and a Conexão on its account, served by the returned fake.
func connected(t *testing.T) (*webClient, *fakePluggy) {
	t.Helper()
	fake := newFakePluggy(t)
	fake.serveItem(pluggyItem("Pluggy Bank", "UPDATED", "SUCCESS"))
	w := newWebClientWith(t, fake.config())
	w.withCredentials()

	return w, fake
}

func monthStart(now time.Time) string {
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).Format(time.DateOnly)
}

func TestFetch_FirstTimeReadsFromTheStartOfTheMonthAndListsPostedMovementsForReview(t *testing.T) {
	w, fake := connected(t)
	today := time.Now().Format(time.DateOnly)
	fake.serveAccounts(pluggyAccount(pluggyBankAccountID, "BANK", "Conta Corrente"))
	fake.serveTransactions(pluggyBankAccountID,
		[]map[string]any{pluggyTransaction("t1", "POSTED", today, "Padaria", -45.9)},
		[]map[string]any{pluggyTransaction("t2", "POSTED", today, "Salário", 1500)},
	)
	w.connect(pluggyItemID, w.accountID())

	if response := w.fetchNow(); response.StatusCode >= 300 {
		t.Fatalf("POST /connections/fetch = %d", response.StatusCode)
	}

	w.assertBody("/movements", "Padaria", "−R$ 45,90", "Salário", "R$ 1.500,00", "Conta Corrente", "Pluggy Bank")

	if got := fake.dateFroms(); len(got) != 2 || got[0] != monthStart(time.Now()) {
		t.Errorf("dateFrom of the requests = %v, want the start of the month %s on every page", got, monthStart(time.Now()))
	}

	if ops := w.newOps(); len(ops) != 0 {
		t.Errorf("fetching made %d domain ops, want none", len(ops))
	}
}

// movementDescriptions are the descriptions of the movements in the database, whatever their status.
func (w *webClient) movementDescriptions() []string {
	w.t.Helper()

	query := "SELECT description FROM movements ORDER BY description"

	var descriptions []string
	if err := w.db.SelectContext(context.Background(), &descriptions, query); err != nil {
		w.t.Fatal(err)
	}

	return descriptions
}

func TestFetch_PendingLinesAreNotMovements(t *testing.T) {
	w, fake := connected(t)
	today := time.Now().Format(time.DateOnly)
	fake.serveAccounts(pluggyAccount(pluggyBankAccountID, "BANK", "Conta Corrente"))
	fake.serveTransactions(pluggyBankAccountID, []map[string]any{
		pluggyTransaction("t1", "POSTED", today, "Efetivada", -10),
		pluggyTransaction("t2", "PENDING", today, "Em análise", -20),
	})
	w.connect(pluggyItemID, w.accountID())
	w.fetchNow()

	if got := w.movementDescriptions(); len(got) != 1 || got[0] != "Efetivada" {
		t.Errorf("movements = %v, want only Efetivada", got)
	}
}

func TestFetch_CardBillPaymentIsDiscardedAndTheSignOfTheAmountTellsTheTipo(t *testing.T) {
	w, fake := connected(t)
	today := time.Now().Format(time.DateOnly)
	payment := pluggyTransaction("c3", "POSTED", today, "Pagamento recebido", 1234.56)
	payment["operationType"] = "PAGAMENTO_FATURA"
	purchase := pluggyTransaction("c1", "POSTED", today, "Assinatura", -89.9)
	purchase["type"] = "CREDIT" // the sandbox sends card purchases as CREDIT with a negative amount
	fake.serveAccounts(pluggyAccount(pluggyCardAccountID, "CREDIT", "Cartão Gold"))
	fake.serveTransactions(pluggyCardAccountID, []map[string]any{purchase, pluggyTransaction("c2", "POSTED", today, "Estorno", 19.99), payment})
	w.connect(pluggyItemID, w.accountID())
	w.fetchNow()

	if got := w.movementDescriptions(); len(got) != 2 || got[0] != "Assinatura" || got[1] != "Estorno" {
		t.Errorf("movements = %v, want Assinatura and Estorno without the bill payment", got)
	}

	w.assertBody("/movements", "−R$ 89,90", "R$ 19,99", "Cartão Gold")
}

func TestFetch_TwiceNeverDuplicatesMovements(t *testing.T) {
	w, fake := connected(t)
	today := time.Now().Format(time.DateOnly)
	fake.serveAccounts(pluggyAccount(pluggyBankAccountID, "BANK", "Conta Corrente"))
	fake.serveTransactions(pluggyBankAccountID, []map[string]any{pluggyTransaction("t1", "POSTED", today, "Padaria", -45.9)})
	w.connect(pluggyItemID, w.accountID())
	w.fetchNow()
	w.fetchNow()

	if got := w.movementDescriptions(); len(got) != 1 {
		t.Errorf("movements after two fetches = %v, want one", got)
	}
}

func TestFetch_CentsAreExactAndNeverGoThroughAFloat(t *testing.T) {
	w, fake := connected(t)
	today := time.Now().Format(time.DateOnly)
	fake.serveAccounts(pluggyAccount(pluggyBankAccountID, "BANK", "Conta Corrente"))
	fake.serveTransactions(pluggyBankAccountID, []map[string]any{
		pluggyTransaction("t1", "POSTED", today, "Fração", -1.15),
		pluggyTransaction("t2", "POSTED", today, "Grande", json.Number("90071992547409.93")),
	})
	w.connect(pluggyItemID, w.accountID())
	w.fetchNow()

	query := "SELECT amount FROM movements ORDER BY description"

	var cents []int64
	if err := w.db.SelectContext(context.Background(), &cents, query); err != nil {
		t.Fatal(err)
	}

	if len(cents) != 2 || cents[0] != 115 || cents[1] != 9007199254740993 {
		t.Errorf("cents = %v, want [115 9007199254740993]", cents)
	}
}

func TestFetch_UpdatesTheStateAndLastUpdateOfTheConnection(t *testing.T) {
	w, fake := connected(t)
	fake.serveAccounts()
	w.connect(pluggyItemID, w.accountID())
	w.assertBody("/connections", "10/11/2026")

	item := pluggyItem("Pluggy Bank", "LOGIN_ERROR", "INVALID_CREDENTIALS")
	item["lastUpdatedAt"] = "2026-11-20T13:30:00.000Z"
	fake.serveItem(item)
	w.fetchNow()

	w.assertBody("/connections", "20/11/2026", "reconecte em meu.pluggy.ai")
}

func TestFetch_LaterReadsFromAWeekBeforeTheLastFetch(t *testing.T) {
	w, fake := connected(t)
	fake.serveAccounts(pluggyAccount(pluggyBankAccountID, "BANK", "Conta Corrente"))
	fake.serveTransactions(pluggyBankAccountID, []map[string]any{})
	w.connect(pluggyItemID, w.accountID())
	w.fetchNow()
	w.fetchNow()

	want := time.Now().AddDate(0, 0, -7).Format(time.DateOnly)
	if got := fake.dateFroms(); len(got) != 2 || got[0] != monthStart(time.Now()) || got[1] != want {
		t.Errorf("dateFrom of the requests = %v, want [%s %s]", got, monthStart(time.Now()), want)
	}
}

func TestFetch_ABrokenConnectionDoesNotStopTheOthers(t *testing.T) {
	w, fake := connected(t)
	today := time.Now().Format(time.DateOnly)
	fake.serveAccounts(pluggyAccount(pluggyBankAccountID, "BANK", "Conta Corrente"))
	fake.serveTransactions(pluggyBankAccountID, []map[string]any{pluggyTransaction("t1", "POSTED", today, "Padaria", -45.9)})
	w.connect(pluggyItemID, w.accountID())

	query := `
		INSERT INTO connections (id, user_id, account_id, item_id, institution, status, execution_status, created_at)
		SELECT 'a-broken', user_id, account_id, 'item-que-some', 'Banco X', 'UPDATED', 'SUCCESS', '2000-01-01T00:00:00Z'
		FROM connections
	`
	if _, err := w.db.ExecContext(context.Background(), query); err != nil {
		t.Fatal(err)
	}

	w.fetchNow()

	if got := w.movementDescriptions(); len(got) != 1 {
		t.Errorf("movements = %v, want the healthy connection's one", got)
	}
}

func TestFetch_OnlyTheUsersOwnConnectionsAndBalancesStayPut(t *testing.T) {
	w, fake := connected(t)
	today := time.Now().Format(time.DateOnly)
	fake.serveAccounts(pluggyAccount(pluggyBankAccountID, "BANK", "Conta Corrente"))
	fake.serveTransactions(pluggyBankAccountID, []map[string]any{pluggyTransaction("t1", "POSTED", today, "Padaria", -45.9)})
	w.connect(pluggyItemID, w.accountID())

	mate := w.newAccountMate()
	before := get(w.handler, "/", w.cookie).Body.String()
	mate.fetchNow()

	if got := w.movementDescriptions(); len(got) != 0 {
		t.Errorf("another user's fetch brought %v", got)
	}

	w.fetchNow()

	if after := get(w.handler, "/", w.cookie).Body.String(); after != before {
		t.Errorf("pending movements changed the month screen:\n%s\nwas:\n%s", after, before)
	}

	if body := get(mate.handler, "/connections", mate.cookie).Body.String(); strings.Contains(body, "Pluggy Bank") {
		t.Errorf("mate sees the connection: %s", body)
	}
}

func TestFetch_RemovingTheConnectionDropsTheFetchedPendingMovements(t *testing.T) {
	w, fake := connected(t)
	today := time.Now().Format(time.DateOnly)
	fake.serveAccounts(pluggyAccount(pluggyBankAccountID, "BANK", "Conta Corrente"))
	fake.serveTransactions(pluggyBankAccountID, []map[string]any{pluggyTransaction("t1", "POSTED", today, "Padaria", -45.9)})
	w.connect(pluggyItemID, w.accountID())
	w.fetchNow()

	id := w.connectionID()

	w.submit(http.MethodDelete, "/connections/"+id, nil)

	if got := w.movementDescriptions(); len(got) != 0 {
		t.Errorf("movements after removing the connection = %v, want none", got)
	}
}

func TestFetch_WithoutTheEncryptionKeyIsNotFound(t *testing.T) {
	w := newWebClientWith(t, config{pluggyURL: newFakePluggy(t).URL})

	if response := w.fetchNow(); response.StatusCode != http.StatusNotFound {
		t.Errorf("POST /connections/fetch without the key = %d, want %d", response.StatusCode, http.StatusNotFound)
	}
}

func TestFetch_ConnectionsScreenOffersFetchNowOnlyWithAConnection(t *testing.T) {
	w, _ := connected(t)

	if body := get(w.handler, "/connections", w.cookie).Body.String(); strings.Contains(body, `hx-post="/connections/fetch"`) {
		t.Errorf("screen without Conexões offers the fetch: %s", body)
	}

	w.connect(pluggyItemID, w.accountID())
	w.assertBody("/connections", `hx-post="/connections/fetch"`, "buscar agora")
}

func TestFetch_AFailedFetchKeepsTheItemStateAndWarns(t *testing.T) {
	w, fake := connected(t)
	w.connect(pluggyItemID, w.accountID())
	fake.serveItem(pluggyItem("Pluggy Bank", "LOGIN_ERROR", "INVALID_CREDENTIALS"))

	response := w.submit(http.MethodPost, "/connections/fetch", nil)
	if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), "A busca falhou") {
		t.Errorf("failed fetch = %d, want %d with the warning: %s", response.Code, http.StatusBadGateway, response.Body)
	}

	w.assertBody("/connections", "reconecte em meu.pluggy.ai")
}

func TestFetch_ARenamedBankAccountDoesNotBringItsMovementsBack(t *testing.T) {
	w, fake := connected(t)
	today := time.Now().Format(time.DateOnly)
	name := "Conta Corrente"
	fake.handle("GET /accounts", func(r *http.Request) (int, any) {
		return http.StatusOK, map[string]any{"results": []any{pluggyAccount(pluggyBankAccountID, "BANK", name)}}
	})
	fake.serveTransactions(pluggyBankAccountID, []map[string]any{pluggyTransaction("t1", "POSTED", today, "Padaria", -45.9)})
	w.connect(pluggyItemID, w.accountID())
	w.fetchNow()
	w.submit(http.MethodPost, "/movements/"+w.movementID("Padaria")+"/ignore", url.Values{})

	name = "Conta Corrente Renomeada"
	w.fetchNow()

	if got := w.movementDescriptions(); len(got) != 1 {
		t.Errorf("movements after renaming the bank account = %v, want only the ignored one", got)
	}
}

func TestFetch_AccountMatesDoNotSeeOrReviewTheConnectionsMovements(t *testing.T) {
	w, fake := connected(t)
	today := time.Now().Format(time.DateOnly)
	fake.serveAccounts(pluggyAccount(pluggyBankAccountID, "BANK", "Conta Corrente"))
	fake.serveTransactions(pluggyBankAccountID, []map[string]any{
		pluggyTransaction("t1", "POSTED", today, "Padaria", -45.9),
		pluggyTransaction("t2", "POSTED", today, "Farmácia", -20),
	})
	w.connect(pluggyItemID, w.accountID())
	w.fetchNow()

	mate := w.newAccountMate()
	if body := get(mate.handler, "/movements", mate.cookie).Body.String(); strings.Contains(body, "Padaria") {
		t.Errorf("mate sees the connection's movements: %s", body)
	}

	mate.submit(http.MethodPost, "/movements/"+w.movementID("Padaria")+"/confirm", url.Values{})
	mate.submit(http.MethodPost, "/movements/"+w.movementID("Farmácia")+"/ignore", url.Values{})
	mate.submit(http.MethodPost, "/movements/confirm", url.Values{"id": {w.movementID("Padaria")}})
	mate.submit(http.MethodPost, "/movements/ignore", url.Values{"id": {w.movementID("Farmácia")}})

	w.assertBody("/movements", "Padaria", "Farmácia")
}
