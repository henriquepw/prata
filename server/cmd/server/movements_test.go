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
)

const ofxBank = "OFXHEADER:100\r\nDATA:OFXSGML\r\nVERSION:102\r\nENCODING:USASCII\r\nCHARSET:1252\r\n\r\n" +
	"<OFX><BANKMSGSRSV1><STMTTRNRS><STMTRS><CURDEF>BRL\r\n" +
	"<BANKACCTFROM><BANKID>0260<ACCTID>12345-6<ACCTTYPE>CHECKING</BANKACCTFROM>\r\n" +
	"<BANKTRANLIST>\r\n" +
	"<STMTTRN><TRNTYPE>DEBIT<DTPOSTED>20240105120000[-3:BRT]<TRNAMT>-45.90<FITID>A1<MEMO>Padaria Pão Quente\r\n" +
	"<STMTTRN><TRNTYPE>CREDIT<DTPOSTED>20240106<TRNAMT>1500.00<FITID>A2<MEMO>Salário\r\n" +
	"</BANKTRANLIST></STMTRS></STMTTRNRS></BANKMSGSRSV1></OFX>"

const ofxCard = `<?xml version="1.0" encoding="UTF-8"?><OFX><CREDITCARDMSGSRSV1><CCSTMTTRNRS><CCSTMTRS><CURDEF>BRL</CURDEF>
<CCACCTFROM><ACCTID>9999</ACCTID></CCACCTFROM><BANKTRANLIST>
<STMTTRN><TRNTYPE>DEBIT</TRNTYPE><DTPOSTED>20240110</DTPOSTED><TRNAMT>-80.00</TRNAMT><FITID>C1</FITID><MEMO>Mercado &amp; Cia</MEMO></STMTTRN>
<STMTTRN><TRNTYPE>CREDIT</TRNTYPE><DTPOSTED>20240111</DTPOSTED><TRNAMT>20.00</TRNAMT><FITID>C2</FITID><MEMO>Estorno</MEMO></STMTTRN>
</BANKTRANLIST></CCSTMTRS></CCSTMTTRNRS></CREDITCARDMSGSRSV1></OFX>`

// latin1 is s as the bytes of a Latin-1 file.
func latin1(s string) string {
	var b strings.Builder
	for _, r := range s {
		b.WriteByte(byte(r))
	}

	return b.String()
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

	var id string
	if err := w.db.GetContext(context.Background(), &id, "SELECT id FROM accounts"); err != nil {
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

	response := w.upload(w.accountID(), latin1(ofxBank))
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

func TestMovements_CardPurchaseIsAnExpenseAndCreditIsIncome(t *testing.T) {
	w := newWebClient(t)

	response := w.upload(w.accountID(), ofxCard)
	if response.Code != http.StatusOK {
		t.Fatalf("import = %d: %s", response.Code, response.Body)
	}

	assertContains(t, response.Body.String(), "2 novos", "Mercado &amp; Cia", "−R$ 80,00", "R$ 20,00", "9999")
}

func TestMovements_ReimportingAddsNothingAndAnotherAccountIsNew(t *testing.T) {
	w := newWebClient(t)
	w.upload(w.accountID(), ofxCard)

	assertContains(t, w.upload(w.accountID(), ofxCard).Body.String(), "0 novos, 2 já importados")

	id, now := ulid.Make().String(), time.Now().UTC().Format(time.RFC3339)
	for _, query := range []string{
		"INSERT INTO accounts (id, name, created_by, created_at, updated_at) VALUES (?1, 'Casa', 'x', ?2, ?2)",
		"INSERT INTO account_users (id, account_id, user_id, role, created_at) SELECT ?1 || 'u', ?1, id, 'OWNER', ?2 FROM users",
	} {
		if _, err := w.db.ExecContext(context.Background(), query, id, now); err != nil {
			t.Fatal(err)
		}
	}

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

func TestMovements_ForeignCurrencyFileAsksConfirmationAndImportsAsIs(t *testing.T) {
	w := newWebClient(t)
	usd := strings.Replace(ofxCard, "<CURDEF>BRL", "<CURDEF>USD", 1)

	response := w.upload(w.accountID(), usd)
	assertContains(t, response.Body.String(), "USD", "BRL", "sem conversão")
	if response.Header().Get("HX-Retarget") != "#sheet" {
		t.Errorf("HX-Retarget = %q, want #sheet", response.Header().Get("HX-Retarget"))
	}

	var count int
	if err := w.db.GetContext(context.Background(), &count, "SELECT COUNT(*) FROM movements"); err != nil || count != 0 {
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

	if _, err := w.db.ExecContext(context.Background(), "UPDATE accounts SET currency = 'USD'"); err != nil {
		t.Fatal(err)
	}

	assertContains(t, w.uploadFile(w.accountID(), "nubank.csv", csvCard).Body.String(), "sem conversão", "BRL", "USD")
}
