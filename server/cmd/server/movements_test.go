package main

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/henriquepw/prata/server/internal/store"
	"github.com/oklog/ulid/v2"
	"golang.org/x/text/encoding/charmap"
)

const ofxBank = "OFXHEADER:100\r\nDATA:OFXSGML\r\nVERSION:102\r\nENCODING:USASCII\r\nCHARSET:1252\r\n\r\n" +
	"<OFX><BANKMSGSRSV1><STMTTRNRS><STMTRS><CURDEF>BRL\r\n" +
	"<BANKACCTFROM><BANKID>0260<ACCTID>12345-6<ACCTTYPE>CHECKING</BANKACCTFROM>\r\n" +
	"<BANKTRANLIST>\r\n" +
	"<STMTTRN><TRNTYPE>DEBIT<DTPOSTED>20240105120000[-3:BRT]<TRNAMT>-45.90<FITID>A1<MEMO>Padaria Pão Quente\r\n" +
	"<STMTTRN><TRNTYPE>CREDIT<DTPOSTED>20240106<TRNAMT>1500.00<FITID>A2<MEMO>Salário\r\n" +
	"</BANKTRANLIST></STMTRS></STMTTRNRS></BANKMSGSRSV1></OFX>"

const ofxCard = `<?xml version="1.0" encoding="UTF-8"?><OFX><SIGNONMSGSRSV1><SONRS><FI><ORG>Banco Inter</ORG></FI></SONRS></SIGNONMSGSRSV1><CREDITCARDMSGSRSV1><CCSTMTTRNRS><CCSTMTRS><CURDEF>BRL</CURDEF>
<CCACCTFROM><ACCTID>9999</ACCTID></CCACCTFROM><BANKTRANLIST>
<STMTTRN><TRNTYPE>DEBIT</TRNTYPE><DTPOSTED>20240110</DTPOSTED><TRNAMT>-80.00</TRNAMT><FITID>C1</FITID><MEMO>Mercado &amp; Cia</MEMO></STMTTRN>
<STMTTRN><TRNTYPE>CREDIT</TRNTYPE><DTPOSTED>20240111</DTPOSTED><TRNAMT>20.00</TRNAMT><FITID>C2</FITID><MEMO>Estorno</MEMO></STMTTRN>
</BANKTRANLIST></CCSTMTRS></CCSTMTTRNRS></CREDITCARDMSGSRSV1></OFX>`

// windows1252 is s as the bytes of a Windows-1252 (CHARSET:1252) file.
func windows1252(s string) string {
	encoded, err := charmap.Windows1252.NewEncoder().String(s)
	if err != nil {
		panic(err)
	}

	return encoded
}

// upload sends the file like the browser does, as a multipart form with the destination account.
func (w *webClient) upload(accountID, content string) *httptest.ResponseRecorder {
	w.t.Helper()

	return w.uploadFile(accountID, "extrato.ofx", content)
}

// uploadFile is upload with the name the browser sends for the file.
func (w *webClient) uploadFile(accountID, name, content string) *httptest.ResponseRecorder {
	return w.uploadConfirmed(accountID, name, content, false)
}

// uploadConfirmed is uploadFile, with the user's answer to the currency dialog when confirmed.
func (w *webClient) uploadConfirmed(accountID, name, content string, confirmed bool) *httptest.ResponseRecorder {
	w.t.Helper()

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if err := form.WriteField("account_id", accountID); err != nil {
		w.t.Fatal(err)
	}

	if confirmed {
		if err := form.WriteField("confirm_currency", "1"); err != nil {
			w.t.Fatal(err)
		}
	}

	file, err := form.CreateFormFile("file", name)
	if err != nil {
		w.t.Fatal(err)
	}

	if _, err := file.Write([]byte(content)); err != nil {
		w.t.Fatal(err)
	}

	if err := form.Close(); err != nil {
		w.t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/movements/import", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.AddCookie(w.cookie)

	response := httptest.NewRecorder()
	w.handler.ServeHTTP(response, req)

	return response
}

func (w *webClient) accountID() string {
	w.t.Helper()

	query := "SELECT id FROM accounts"

	var id string
	if err := w.db.GetContext(context.Background(), &id, query); err != nil {
		w.t.Fatal(err)
	}

	return id
}

func assertContains(t *testing.T, body string, texts ...string) {
	t.Helper()
	for _, text := range texts {
		if !strings.Contains(body, text) {
			t.Errorf("body lacks %q: %s", text, body)
		}
	}
}

func TestMovements_ImportOFXBankStatement(t *testing.T) {
	w := newWebClient(t)

	response := w.upload(w.accountID(), windows1252(ofxBank))
	if response.Code != http.StatusOK {
		t.Fatalf("import = %d: %s", response.Code, response.Body)
	}

	assertContains(t, response.Body.String(), "2 novos, 0 já importados", "Padaria Pão Quente", "Salário",
		"05/01/2024", "−R$ 45,90", "R$ 1.500,00", "0260", "12345-6")

	if ops := w.newOps(); len(ops) != 0 {
		for _, o := range ops {
			if o.Entity == "TRANSACTION" {
				t.Errorf("pending movements reached the pull: %+v", o)
			}
		}
	}
}

func TestMovements_ImportOFXKeepsWindows1252Punctuation(t *testing.T) {
	w := newWebClient(t)

	ofx := strings.Replace(ofxBank, "Padaria Pão Quente", "Padaria “Pão” – Quente", 1)
	response := w.upload(w.accountID(), windows1252(ofx))
	if response.Code != http.StatusOK {
		t.Fatalf("import = %d: %s", response.Code, response.Body)
	}

	assertContains(t, response.Body.String(), "Padaria “Pão” – Quente")
}

func TestMovements_CardPurchaseIsAnExpenseAndCreditIsIncome(t *testing.T) {
	w := newWebClient(t)

	response := w.upload(w.accountID(), ofxCard)
	if response.Code != http.StatusOK {
		t.Fatalf("import = %d: %s", response.Code, response.Body)
	}

	assertContains(t, response.Body.String(), "2 novos", "Mercado &amp; Cia", "−R$ 80,00", "R$ 20,00", "Banco Inter", "9999")
}

func TestMovements_ReimportingAddsNothingAndAnotherAccountIsNew(t *testing.T) {
	w := newWebClient(t)
	w.upload(w.accountID(), ofxCard)

	assertContains(t, w.upload(w.accountID(), ofxCard).Body.String(), "0 novos, 2 já importados")

	id := w.addAccount("Casa", "BRL")

	assertContains(t, w.upload(id, ofxCard).Body.String(), "2 novos, 0 já importados")
}

func TestMovements_UnrecognizedFileIsRefusedWhole(t *testing.T) {
	w := newWebClient(t)

	for _, content := range []string{"id,name\n1,x", strings.Repeat("a", 1<<20+1)} {
		response := w.upload(w.accountID(), content)
		if response.Code != http.StatusUnprocessableEntity {
			t.Errorf("import = %d, want %d", response.Code, http.StatusUnprocessableEntity)
		}

		assertContains(t, response.Body.String(), "OFX")
	}

	assertContains(t, get(w.handler, "/movements", w.cookie).Body.String(), "nenhum movimento")
}

const csvCard = "date,title,amount\n2024-02-01,Uber *Trip,23.50\n2024-02-01,Uber *Trip,23.50\n2024-02-03,Pagamento recebido,-100.00\n"

const csvAccount = "Data,Valor,Identificador,Descrição\n05/02/2024,-35.40,id-1,Compra no débito - Padaria\n06/02/2024,2000.00,id-2,Transferência recebida - Empresa\n"

func TestMovements_NubankCardCSVPurchaseIsExpenseAndCreditIsIncome(t *testing.T) {
	w := newWebClient(t)

	response := w.uploadFile(w.accountID(), "nubank.csv", csvCard)
	if response.Code != http.StatusOK {
		t.Fatalf("import = %d: %s", response.Code, response.Body)
	}

	assertContains(t, response.Body.String(), "3 novos, 0 já importados", "Uber *Trip", "01/02/2024", "−R$ 23,50",
		"Pagamento recebido", "R$ 100,00", "Nubank")

	assertContains(t, w.uploadFile(w.accountID(), "nubank.csv", csvCard).Body.String(), "0 novos, 3 já importados")
}

func TestMovements_NubankAccountCSVNegativeIsExpenseAndDedupsByIdentifier(t *testing.T) {
	w := newWebClient(t)

	response := w.uploadFile(w.accountID(), "NU_conta.csv", csvAccount)
	if response.Code != http.StatusOK {
		t.Fatalf("import = %d: %s", response.Code, response.Body)
	}

	assertContains(t, response.Body.String(), "2 novos, 0 já importados", "Compra no débito - Padaria", "05/02/2024",
		"−R$ 35,40", "R$ 2.000,00", "Nubank")

	overlap := csvAccount + "07/02/2024,-10.00,id-3,Pix\n"
	assertContains(t, w.uploadFile(w.accountID(), "NU_conta.csv", overlap).Body.String(), "1 novos, 2 já importados")
}

func TestMovements_NotAMemberCannotImportNorSee(t *testing.T) {
	w := newWebClient(t)
	w.upload(w.accountID(), ofxCard)

	signup := store.Signup{Email: "bia@example.com", Password: "another secret", AccountID: ulid.Make().String(), OwnerID: ulid.Make().String()}
	if _, err := w.db.CreateAccount(context.Background(), signup, true); err != nil {
		t.Fatal(err)
	}

	stranger := *w
	stranger.cookie = sessionCookie(t, postForm(w.handler, "/login", url.Values{"email": {signup.Email}, "password": {signup.Password}}))

	if body := get(w.handler, "/movements", stranger.cookie).Body.String(); strings.Contains(body, "Mercado") {
		t.Errorf("stranger sees the movements: %s", body)
	}

	if response := stranger.upload(w.accountID(), ofxBank); response.Code < 400 {
		t.Errorf("stranger import = %d, want an error", response.Code)
	}

	assertContains(t, w.upload(w.accountID(), ofxBank).Body.String(), "2 novos")
}

func TestMovements_RoutesRequireSession(t *testing.T) {
	w := newWebClient(t)

	assertRedirectsToLogin(t, get(w.handler, "/movements"))
}

// movementID is the id of the movement with the description, whatever its status.
func (w *webClient) movementID(description string) string {
	w.t.Helper()

	query := "SELECT id FROM movements WHERE description = ?"

	var id string
	if err := w.db.GetContext(context.Background(), &id, query, description); err != nil {
		w.t.Fatal(err)
	}

	return id
}

func TestMovements_ConfirmCreatesTheTransactionOnce(t *testing.T) {
	w := newWebClient(t)
	w.upload(w.accountID(), windows1252(ofxBank))
	w.newOps()
	id := w.movementID("Padaria Pão Quente")

	form := url.Values{"description": {" Café da manhã "}, "category_id": {w.categories["Meta"]}}
	if response := w.submit(http.MethodPost, "/movements/"+id+"/confirm", form); response.Code >= 400 {
		t.Fatalf("confirm = %d: %s", response.Code, response.Body)
	}

	ops := w.newOps()
	if len(ops) != 1 {
		t.Fatalf("ops after confirm = %+v, want one", ops)
	}

	created := ops[0]
	if created.Entity != "TRANSACTION" || created.Kind != "CREATE" || created.DeviceID == w.deviceID || created.Payload["description"] != "Café da manhã" ||
		created.Payload["kind"] != "OUTCOME" || created.Payload["amount"] != float64(4590) || created.Payload["category_id"] != w.categories["Meta"] ||
		!strings.HasPrefix(created.Payload["occurred_at"].(string), "2024-01-05") {
		t.Errorf("confirm op = %+v, want the movement's amount, kind and date with the edited description and category", created)
	}

	w.submit(http.MethodPost, "/movements/"+id+"/confirm", form)
	if ops := w.newOps(); len(ops) != 0 {
		t.Errorf("second confirm made ops %+v, want none", ops)
	}

	body := get(w.handler, "/movements", w.cookie).Body.String()
	if strings.Contains(body, "Padaria") || !strings.Contains(body, "Salário") {
		t.Errorf("pending list after confirm = %s, want only Salário", body)
	}

	assertContains(t, w.upload(w.accountID(), windows1252(ofxBank)).Body.String(), "0 novos, 2 já importados")

	if body := get(w.handler, "/movements", w.cookie).Body.String(); strings.Contains(body, "Padaria") {
		t.Errorf("reimport brought a confirmed movement back: %s", body)
	}
}

func TestMovements_ConfirmedExpenseWithoutCategoryGoesToTheDefaultAndIncomeKeepsNone(t *testing.T) {
	w := newWebClient(t)
	w.upload(w.accountID(), windows1252(ofxBank))
	w.newOps()

	w.submit(http.MethodPost, "/movements/"+w.movementID("Padaria Pão Quente")+"/confirm", url.Values{"description": {"Padaria"}})
	w.submit(http.MethodPost, "/movements/"+w.movementID("Salário")+"/confirm", url.Values{"description": {"Salário"}, "category_id": {w.categories["Meta"]}})

	ops := w.newOps()
	if len(ops) != 2 || ops[0].Payload["category_id"] != w.defaultID || ops[1].Payload["category_id"] != nil {
		t.Errorf("ops = %+v, want the expense in the default category and the income in none", ops)
	}
}

func TestMovements_IgnoreHidesItForGood(t *testing.T) {
	w := newWebClient(t)
	w.upload(w.accountID(), windows1252(ofxBank))
	w.newOps()

	if response := w.submit(http.MethodPost, "/movements/"+w.movementID("Padaria Pão Quente")+"/ignore", url.Values{}); response.Code >= 400 {
		t.Fatalf("ignore = %d: %s", response.Code, response.Body)
	}

	assertContains(t, w.upload(w.accountID(), windows1252(ofxBank)).Body.String(), "0 novos, 2 já importados")

	if body := get(w.handler, "/movements", w.cookie).Body.String(); strings.Contains(body, "Padaria") {
		t.Errorf("ignored movement is back: %s", body)
	}

	if ops := w.newOps(); len(ops) != 0 {
		t.Errorf("ignore made ops %+v, want none", ops)
	}
}

func TestMovements_ReviewRoutesRequireSessionAndMembership(t *testing.T) {
	w := newWebClient(t)
	w.upload(w.accountID(), windows1252(ofxBank))
	id := w.movementID("Padaria Pão Quente")

	for _, action := range []string{"confirm", "ignore"} {
		req := httptest.NewRequest(http.MethodPost, "/movements/"+id+"/"+action, nil)
		response := httptest.NewRecorder()
		w.handler.ServeHTTP(response, req)
		assertRedirectsToLogin(t, response)
	}

	signup := store.Signup{Email: "bia@example.com", Password: "another secret", AccountID: ulid.Make().String(), OwnerID: ulid.Make().String()}
	if _, err := w.db.CreateAccount(context.Background(), signup, true); err != nil {
		t.Fatal(err)
	}

	stranger := *w
	stranger.cookie = sessionCookie(t, postForm(w.handler, "/login", url.Values{"email": {signup.Email}, "password": {signup.Password}}))
	stranger.submit(http.MethodPost, "/movements/"+id+"/confirm", url.Values{})
	stranger.submit(http.MethodPost, "/movements/"+id+"/ignore", url.Values{})

	if body := get(w.handler, "/movements", w.cookie).Body.String(); !strings.Contains(body, "Padaria") {
		t.Errorf("stranger reviewed the movement: %s", body)
	}
}

func TestMovements_ForeignCurrencyFileAsksConfirmationAndImportsAsIs(t *testing.T) {
	w := newWebClient(t)
	usd := strings.Replace(ofxCard, "<CURDEF>BRL", "<CURDEF>USD", 1)

	response := w.upload(w.accountID(), usd)
	assertContains(t, response.Body.String(), "USD", "BRL", "sem conversão")
	if response.Header().Get("HX-Retarget") != "#sheet" {
		t.Errorf("HX-Retarget = %q, want #sheet", response.Header().Get("HX-Retarget"))
	}

	query := "SELECT COUNT(*) FROM movements"

	var count int
	if err := w.db.GetContext(context.Background(), &count, query); err != nil || count != 0 {
		t.Errorf("movements before the confirmation = %d, %v", count, err)
	}

	response = w.uploadConfirmed(w.accountID(), "extrato.ofx", usd, true)
	assertContains(t, response.Body.String(), "2 novos", "Mercado")
}

func TestMovements_SameCurrencyFileImportsWithoutDialog(t *testing.T) {
	w := newWebClient(t)

	response := w.upload(w.accountID(), ofxCard)
	if strings.Contains(response.Body.String(), "sem conversão") || response.Header().Get("HX-Retarget") != "" {
		t.Errorf("unexpected dialog: %s", response.Body.String())
	}
	assertContains(t, response.Body.String(), "2 novos")
}

func TestMovements_NubankCSVIsBRLAndAsksConfirmationOnAnotherCurrencyAccount(t *testing.T) {
	w := newWebClient(t)

	query := "UPDATE accounts SET currency = 'USD'"

	if _, err := w.db.ExecContext(context.Background(), query); err != nil {
		t.Fatal(err)
	}

	assertContains(t, w.uploadFile(w.accountID(), "nubank.csv", csvCard).Body.String(), "sem conversão", "BRL", "USD")
}

// expenseRecurrences pushes two monthly expense recurrences on day 5 from 2024-01 (Padaria for the movement's amount,
// Internet for another) and an income one, returning their ids.
func expenseRecurrences(w *webClient) (padaria, internet, salary string) {
	padaria, internet, salary = ulid.Make().String(), ulid.Make().String(), ulid.Make().String()
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	w.push(
		monthlyRecurrence(w.client, padaria, "OUTCOME", 4590, "Padaria mensal", "5", start, ""),
		monthlyRecurrence(w.client, internet, "OUTCOME", 5000, "Internet", "5", start, ""),
		monthlyRecurrence(w.client, salary, "INCOME", 150000, "Salário fixo", "5", start, ""),
	)

	return padaria, internet, salary
}

func TestMovements_ConfirmAsOccurrenceListsPendingOfTheSameKindEqualAmountAndClosestDateFirst(t *testing.T) {
	w := newWebClient(t)
	w.upload(w.accountID(), windows1252(ofxBank))
	padaria, internet, salary := expenseRecurrences(w)

	w.submit(http.MethodPost, "/movements/"+w.movementID("Salário")+"/ignore", url.Values{})
	body := get(w.handler, "/movements", w.cookie).Body.String()

	assertInOrder(t, body, `"`+padaria+":2024-01-05", `"`+padaria+":2024-02-05", `"`+internet+":2024-01-05")
	if strings.Contains(body, salary+":") {
		t.Errorf("income occurrence offered for an expense: %s", body)
	}

	w.submit(http.MethodPost, "/recurrences/"+padaria+"/occurrences/2024-01-05", url.Values{"month": {"2024-01"}})
	if body := get(w.handler, "/movements", w.cookie).Body.String(); strings.Contains(body, padaria+":2024-01-05") {
		t.Errorf("confirmed occurrence still offered: %s", body)
	}
}

func TestMovements_ConfirmAsOccurrenceCreatesTheOccurrencesTransactionOnce(t *testing.T) {
	w := newWebClient(t)
	w.upload(w.accountID(), windows1252(ofxBank))
	padaria, _, _ := expenseRecurrences(w)
	w.newOps()
	id, second := w.movementID("Padaria Pão Quente"), w.movementID("Salário")

	form := url.Values{"occurrence": {padaria + ":2024-01-05"}}
	if response := w.submit(http.MethodPost, "/movements/"+id+"/occurrence", form); response.Code >= 400 {
		t.Fatalf("confirm as occurrence = %d: %s", response.Code, response.Body)
	}

	ops := w.newOps()
	if len(ops) != 1 {
		t.Fatalf("ops = %+v, want one", ops)
	}

	payload := ops[0].Payload
	if ops[0].Entity != "TRANSACTION" || payload["recurrence_id"] != padaria || payload["occurrence_date"] != "2024-01-05" ||
		payload["amount"] != float64(4590) || payload["kind"] != "OUTCOME" || !strings.HasPrefix(payload["occurred_at"].(string), "2024-01-05") {
		t.Errorf("op = %+v, want the occurrence confirmed with the movement's amount", ops[0])
	}

	if body := get(w.handler, "/movements", w.cookie).Body.String(); strings.Contains(body, "Padaria Pão") {
		t.Errorf("confirmed movement still pending: %s", body)
	}

	// another movement cannot confirm the same occurrence, nor an occurrence of another kind, nor a made-up one
	for _, occurrence := range []string{padaria + ":2024-01-05", padaria + ":2024-01-06", "nope:2024-01-05"} {
		w.submit(http.MethodPost, "/movements/"+second+"/occurrence", url.Values{"occurrence": {occurrence}})
	}

	if ops := w.newOps(); len(ops) != 0 {
		t.Errorf("refused confirmations made ops %+v, want none", ops)
	}

	// the manual confirmation of the same occurrence is refused too
	response := w.submit(http.MethodPost, "/recurrences/"+padaria+"/occurrences/2024-01-05", url.Values{"month": {"2024-01"}})
	if response.Code != http.StatusConflict {
		t.Errorf("manual confirm = %d, want %d", response.Code, http.StatusConflict)
	}
}

func TestMovements_ConfirmAsOccurrenceRefusesAnotherKind(t *testing.T) {
	w := newWebClient(t)
	w.upload(w.accountID(), windows1252(ofxBank))
	_, _, salary := expenseRecurrences(w)
	w.newOps()

	w.submit(http.MethodPost, "/movements/"+w.movementID("Padaria Pão Quente")+"/occurrence", url.Values{"occurrence": {salary + ":2024-01-05"}})

	if ops := w.newOps(); len(ops) != 0 {
		t.Errorf("ops = %+v, want none", ops)
	}
}

func TestMovements_BatchConfirmCreatesOneTransactionEachInOnePullAndSkipsReviewed(t *testing.T) {
	w := newWebClient(t)
	w.upload(w.accountID(), windows1252(ofxBank))
	w.newOps()

	form := url.Values{"id": {w.movementID("Padaria Pão Quente"), w.movementID("Salário")}, "category_id": {w.categories["Meta"]}}
	if response := w.submit(http.MethodPost, "/movements/confirm", form); response.Code >= 400 {
		t.Fatalf("batch confirm = %d: %s", response.Code, response.Body)
	}

	ops := w.newOps()
	if len(ops) != 2 {
		t.Fatalf("ops = %+v, want two", ops)
	}

	expense, income := ops[0], ops[1]
	if expense.Payload["description"] != "Padaria Pão Quente" || expense.Payload["category_id"] != w.categories["Meta"] || expense.Payload["amount"] != float64(4590) ||
		income.Payload["description"] != "Salário" || income.Payload["category_id"] != nil || income.Payload["kind"] != "INCOME" {
		t.Errorf("ops = %+v, want the bank descriptions, the batch category on the expense and none on the income", ops)
	}

	w.submit(http.MethodPost, "/movements/confirm", form)
	if ops := w.newOps(); len(ops) != 0 {
		t.Errorf("repeated batch made ops %+v, want none", ops)
	}

	if body := get(w.handler, "/movements", w.cookie).Body.String(); strings.Contains(body, "Padaria") || strings.Contains(body, "Salário") {
		t.Errorf("pending list after batch = %s, want empty", body)
	}
}

func TestMovements_BatchConfirmWithoutCategoryUsesTheDefault(t *testing.T) {
	w := newWebClient(t)
	w.upload(w.accountID(), windows1252(ofxBank))
	w.newOps()

	w.submit(http.MethodPost, "/movements/confirm", url.Values{"id": {w.movementID("Padaria Pão Quente")}})

	if ops := w.newOps(); len(ops) != 1 || ops[0].Payload["category_id"] != w.defaultID {
		t.Errorf("ops = %+v, want the default category", ops)
	}
}

func TestMovements_BatchIgnoreHidesAllForGoodAndSkipsReviewed(t *testing.T) {
	w := newWebClient(t)
	w.upload(w.accountID(), windows1252(ofxBank))
	w.newOps()

	form := url.Values{"id": {w.movementID("Padaria Pão Quente"), w.movementID("Salário")}}
	for range 2 {
		if response := w.submit(http.MethodPost, "/movements/ignore", form); response.Code >= 400 {
			t.Fatalf("batch ignore = %d: %s", response.Code, response.Body)
		}
	}

	assertContains(t, w.upload(w.accountID(), windows1252(ofxBank)).Body.String(), "0 novos, 2 já importados")

	if body := get(w.handler, "/movements", w.cookie).Body.String(); strings.Contains(body, "Padaria") || strings.Contains(body, "Salário") {
		t.Errorf("ignored movements are back: %s", body)
	}

	if ops := w.newOps(); len(ops) != 0 {
		t.Errorf("ignore made ops %+v, want none", ops)
	}
}

func TestMovements_BatchRoutesRequireSessionAndSelectAllIsOffered(t *testing.T) {
	w := newWebClient(t)
	body := w.upload(w.accountID(), windows1252(ofxBank)).Body.String()
	assertContains(t, body, `name="id"`, `form="batch"`, `hx-post="/movements/confirm"`, `hx-post="/movements/ignore"`)

	for _, action := range []string{"confirm", "ignore"} {
		response := httptest.NewRecorder()
		w.handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/movements/"+action, nil))
		assertRedirectsToLogin(t, response)
	}
}

// addAccount gives the user another account, created after the first, so the session stays on the first.
func (w *webClient) addAccount(name, currency string) string {
	w.t.Helper()

	id, now := ulid.Make().String(), time.Now().UTC().Add(time.Second).Format(time.RFC3339)
	for _, query := range []string{
		"INSERT INTO accounts (id, name, currency, created_by, created_at, updated_at) VALUES (?1, ?3, ?4, 'x', ?2, ?2)",
		"INSERT INTO account_users (id, account_id, user_id, role, created_at) SELECT ?1 || 'u', ?1, id, 'OWNER', ?2 FROM users",
	} {
		if _, err := w.db.ExecContext(context.Background(), query, id, now, name, currency); err != nil {
			w.t.Fatal(err)
		}
	}

	return id
}

func TestMovements_ReviewListsOnlyTheActiveAccountsMovements(t *testing.T) {
	w := newWebClient(t)
	casa := w.addAccount("Casa", "BRL")
	w.upload(casa, windows1252(ofxBank))
	w.upload(w.accountID(), ofxCard)

	body := get(w.handler, "/movements", w.cookie).Body.String()
	assertContains(t, body, "Mercado")

	if strings.Contains(body, "Padaria") {
		t.Errorf("movement of another account is listed, and could not be confirmed: %s", body)
	}
}

func TestMovements_ImportsIntoAnAccountOfAnotherCurrencyWithoutEncryptionKey(t *testing.T) {
	w := newWebClient(t)
	usd := w.addAccount("Dólar", "USD")

	response := w.upload(usd, strings.Replace(ofxCard, "<CURDEF>BRL", "<CURDEF>USD", 1))
	if response.Header().Get("HX-Retarget") != "" || !strings.Contains(response.Body.String(), "2 novos, 0 já importados") {
		t.Errorf("import into a USD account = %d: %s", response.Code, response.Body)
	}
}

func TestMovements_ImportedMovementsDoNotTouchTheScreensOfTheMoney(t *testing.T) {
	w := newWebClient(t)
	paths := []string{"/", "/transactions", "/categories"}
	before := make([]string, len(paths))
	for i, path := range paths {
		before[i] = get(w.handler, path, w.cookie).Body.String()
	}

	w.upload(w.accountID(), windows1252(ofxBank))

	for i, path := range paths {
		if after := get(w.handler, path, w.cookie).Body.String(); after != before[i] {
			t.Errorf("%s changed after an import", path)
		}
	}
}

func TestMovements_Latin1CSVKeepsItsAccents(t *testing.T) {
	w := newWebClient(t)

	assertContains(t, w.uploadFile(w.accountID(), "NU_conta.csv", windows1252(csvAccount)).Body.String(),
		"Compra no débito - Padaria", "Transferência recebida - Empresa")
}
