package main

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

// fetchedPadaria is a web client whose Conexão already fetched one pending Movimento, "Padaria".
func fetchedPadaria(t *testing.T) (*webClient, *fakePluggy) {
	t.Helper()
	w, fake := connected(t)
	today := time.Now().Format(time.DateOnly)
	fake.serveAccounts(pluggyAccount(pluggyBankAccountID, "BANK", "Conta Corrente"))
	fake.serveTransactions(pluggyBankAccountID, []map[string]any{
		pluggyTransaction("t1", "POSTED", today, "Padaria", -45.9),
		pluggyTransaction("t2", "POSTED", today, "Salário", 1500),
	})
	w.connect(pluggyItemID, w.accountID())
	w.fetchNow()
	w.newOps()

	return w, fake
}

func TestReview_ConfirmingAConnectionMovementCreatesTheTransactionOnce(t *testing.T) {
	w, _ := fetchedPadaria(t)
	id := w.movementID("Padaria")

	form := url.Values{"description": {"Café"}, "category_id": {w.categories["Meta"]}}
	if response := w.submit(http.MethodPost, "/movements/"+id+"/confirm", form); response.Code >= 400 {
		t.Fatalf("confirm = %d: %s", response.Code, response.Body)
	}

	w.submit(http.MethodPost, "/movements/"+id+"/confirm", form)

	ops := w.newOps()
	if len(ops) != 1 || ops[0].Entity != "TRANSACTION" || ops[0].Kind != "CREATE" || ops[0].Payload["description"] != "Café" ||
		ops[0].Payload["amount"] != float64(4590) || ops[0].Payload["kind"] != "OUTCOME" {
		t.Errorf("ops after confirming twice = %+v, want one transaction create for 45,90", ops)
	}
}

func TestReview_IgnoredConnectionMovementNeverComesBackInLaterFetches(t *testing.T) {
	w, _ := fetchedPadaria(t)
	w.submit(http.MethodPost, "/movements/"+w.movementID("Padaria")+"/ignore", url.Values{})
	w.fetchNow()
	w.fetchNow()

	if body := get(w.handler, "/movements", w.cookie).Body.String(); strings.Contains(body, "Padaria") || !strings.Contains(body, "Salário") {
		t.Errorf("review after ignoring and fetching again = %s, want only Salário", body)
	}

	if ops := w.newOps(); len(ops) != 0 {
		t.Errorf("ignore and fetch made ops %+v, want none", ops)
	}
}

func TestReview_BatchConfirmAndIgnoreMixConnectionAndFileMovements(t *testing.T) {
	w, _ := fetchedPadaria(t)
	w.upload(w.accountID(), windows1252(ofxBank))
	w.newOps()

	confirm := url.Values{"id": {w.movementID("Padaria"), w.movementID("Padaria Pão Quente")}}
	if response := w.submit(http.MethodPost, "/movements/confirm", confirm); response.Code >= 400 {
		t.Fatalf("batch confirm = %d: %s", response.Code, response.Body)
	}

	var created []string
	for _, op := range w.newOps() {
		created = append(created, op.Payload["description"].(string))
	}

	slices.Sort(created)
	if !slices.Equal(created, []string{"Padaria", "Padaria Pão Quente"}) {
		t.Errorf("transactions after the mixed batch confirm = %v, want the Conexão's and the file's", created)
	}

	var salaries []string
	if err := w.db.SelectContext(context.Background(), &salaries, "SELECT id FROM movements WHERE description = 'Salário'"); err != nil || len(salaries) != 2 {
		t.Fatalf("salary movements = %v, %v, want the Conexão's and the file's", salaries, err)
	}

	ignore := url.Values{"id": salaries}
	if response := w.submit(http.MethodPost, "/movements/ignore", ignore); response.Code >= 400 {
		t.Fatalf("batch ignore = %d: %s", response.Code, response.Body)
	}

	if body := get(w.handler, "/movements", w.cookie).Body.String(); strings.Contains(body, "Padaria") || strings.Contains(body, "Salário") {
		t.Errorf("review after the batches = %s, want it empty", body)
	}
}

func TestReview_RemovingTheConnectionKeepsConfirmedTransactionsIgnoredAndFileMovementsAndReviewNeedsNoKey(t *testing.T) {
	w, _ := fetchedPadaria(t)
	w.upload(w.accountID(), windows1252(ofxBank))
	w.submit(http.MethodPost, "/movements/"+w.movementID("Padaria")+"/confirm", url.Values{"description": {"Padaria"}})
	w.submit(http.MethodPost, "/movements/"+w.movementID("Padaria Pão Quente")+"/ignore", url.Values{})
	w.newOps()

	var id string
	if err := w.db.GetContext(context.Background(), &id, "SELECT id FROM connections"); err != nil {
		t.Fatal(err)
	}

	w.submit(http.MethodDelete, "/connections/"+id, nil)

	if ops := w.newOps(); len(ops) != 0 {
		t.Errorf("removing the Conexão made ops %+v, want the confirmed transaction untouched", ops)
	}

	var statuses []string
	if err := w.db.SelectContext(context.Background(), &statuses, "SELECT status FROM movements ORDER BY description, status"); err != nil {
		t.Fatal(err)
	}

	// Padaria CONFIRMED, Padaria Pão Quente IGNORED, Salário (Conexão) dropped as pending, Salário (file) PENDING.
	if want := []string{"CONFIRMED", "IGNORED", "PENDING"}; !slices.Equal(statuses, want) {
		t.Errorf("movement statuses after removing the Conexão = %v, want %v", statuses, want)
	}

	withoutKey := *w
	withoutKey.handler = newHandler(w.db, config{})

	salary := w.movementID("Salário")
	if response := withoutKey.submit(http.MethodPost, "/movements/"+salary+"/confirm", url.Values{"description": {"Salário"}}); response.Code >= 400 {
		t.Fatalf("confirm without the key = %d: %s", response.Code, response.Body)
	}

	if ops := w.newOps(); len(ops) != 1 || ops[0].Payload["description"] != "Salário" {
		t.Errorf("ops after confirming the file movement without the key = %+v, want its transaction", ops)
	}
}

func TestReview_ConfirmingAConnectionMovementAsAnOccurrenceCreatesTheOccurrencesTransactionWithTheMovementsAmount(t *testing.T) {
	w, _ := fetchedPadaria(t)
	padaria, _, _ := expenseRecurrences(w)
	w.newOps()

	form := url.Values{"occurrence": {padaria + ":2024-01-05"}}
	if response := w.submit(http.MethodPost, "/movements/"+w.movementID("Padaria")+"/occurrence", form); response.Code >= 400 {
		t.Fatalf("confirm as occurrence = %d: %s", response.Code, response.Body)
	}

	ops := w.newOps()
	if len(ops) != 1 {
		t.Fatalf("ops = %+v, want one", ops)
	}

	payload := ops[0].Payload
	if ops[0].Entity != "TRANSACTION" || payload["recurrence_id"] != padaria || payload["occurrence_date"] != "2024-01-05" ||
		payload["amount"] != float64(4590) || payload["kind"] != "OUTCOME" {
		t.Errorf("op = %+v, want the occurrence confirmed with the movement's amount", ops[0])
	}

	if body := get(w.handler, "/movements", w.cookie).Body.String(); strings.Contains(body, "Padaria") {
		t.Errorf("confirmed movement still pending: %s", body)
	}
}

// confirmSameOccurrence has the Movimento of the first description confirm the Padaria occurrence, then the one of the
// second try the same, and checks that only the first made an op.
func confirmSameOccurrence(t *testing.T, first, second string) {
	t.Helper()
	w, _ := fetchedPadaria(t)
	padaria, _, _ := expenseRecurrences(w)
	w.upload(w.accountID(), windows1252(ofxBank))
	w.newOps()
	form := url.Values{"occurrence": {padaria + ":2024-01-05"}}

	if response := w.submit(http.MethodPost, "/movements/"+w.movementID(first)+"/occurrence", form); response.Code >= 400 {
		t.Fatalf("%s confirm = %d", first, response.Code)
	}

	if response := w.submit(http.MethodPost, "/movements/"+w.movementID(second)+"/occurrence", form); response.Code < 400 {
		t.Errorf("%s confirmed the same occurrence: %d", second, response.Code)
	}

	if ops := w.newOps(); len(ops) != 1 {
		t.Errorf("ops = %+v, want only the first confirmation", ops)
	}
}

func TestReview_AFileMovementCannotConfirmAnOccurrenceAConnectionMovementConfirmed(t *testing.T) {
	confirmSameOccurrence(t, "Padaria", "Padaria Pão Quente")
}

func TestReview_AConnectionMovementCannotConfirmAnOccurrenceAFileMovementConfirmed(t *testing.T) {
	confirmSameOccurrence(t, "Padaria Pão Quente", "Padaria")
}
