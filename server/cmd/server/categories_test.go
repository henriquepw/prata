package main

import (
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/henriquepw/prata/server/internal/store"
)

// webClient pairs a logged-in web session with a device of the same account that pulls what the web writes.
type webClient struct {
	*client
	cookie *http.Cookie
	db     *store.Store
	cursor int64
	// defaultID is the default category the account was born with.
	defaultID string
	// categories maps the names of the categories the account was born with to their ids.
	categories map[string]string
}

func newWebClient(t *testing.T) *webClient {
	t.Helper()
	db := openStore(t)
	handler := newHandler(db, config{})
	device := newClient(t, handler, newSignup())

	w := &webClient{client: device, cookie: login(t, handler), db: db, categories: map[string]string{}}
	for _, o := range w.newOps() { // the categories every account is born with
		w.categories[o.Payload["name"].(string)] = o.EntityID
		if o.Payload["is_default"] == float64(1) {
			w.defaultID = o.EntityID
		}
	}

	return w
}

// submit sends the form like htmx does, with extra cookies such as tz, and returns the response.
func (w *webClient) submit(method, path string, form url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	for _, cookie := range append(cookies, w.cookie) {
		req.AddCookie(cookie)
	}

	response := httptest.NewRecorder()
	w.handler.ServeHTTP(response, req)

	return response
}

// mustSubmit asserts the write succeeded with HX-Refresh.
func (w *webClient) mustSubmit(method, path string, form url.Values, cookies ...*http.Cookie) {
	w.t.Helper()
	response := w.submit(method, path, form, cookies...)
	if response.Code >= 300 || response.Header().Get("HX-Refresh") != "true" {
		w.t.Fatalf("%s %s = %d (HX-Refresh %q), want success with HX-Refresh: %s", method, path, response.Code, response.Header().Get("HX-Refresh"), response.Body)
	}
}

// newOps pulls the ops the device has not seen yet.
func (w *webClient) newOps() []op {
	w.t.Helper()
	page := w.pull(w.cursor, 500)
	w.cursor = page.NextCursor

	return page.Ops
}

func categoryForm(name, color, limit string) url.Values {
	return url.Values{"name": {name}, "color": {color}, "monthly_limit": {limit}}
}

func TestWebCategories_WritesReachDevicePull(t *testing.T) {
	w := newWebClient(t)

	w.mustSubmit(http.MethodPost, "/categories", categoryForm("  Mercado ", "GREEN", "1.234,56"))
	ops := w.newOps()
	if len(ops) != 1 {
		t.Fatalf("ops after create = %+v, want one", ops)
	}

	created := ops[0]
	if created.Entity != "CATEGORY" || created.Kind != "CREATE" || created.OwnerID != w.ownerID || created.DeviceID == w.deviceID ||
		created.ProtocolVersion != 1 || created.Payload["name"] != "Mercado" || created.Payload["color"] != "GREEN" ||
		created.Payload["monthly_limit"] != float64(123456) || created.Payload["created_at"] != created.CreatedAt {
		t.Errorf("create op = %+v, want a CATEGORY CREATE from the web device with the trimmed name and limit in cents", created)
	}

	page := get(w.handler, "/categories", w.cookie)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Mercado") || !strings.Contains(page.Body.String(), `"text-cat-green" aria-hidden="true">■`) {
		t.Errorf("list = %d, want Mercado with a green dot: %s", page.Code, page.Body)
	}

	edit := get(w.handler, "/categories/"+created.EntityID, w.cookie)
	if edit.Code != http.StatusOK || strings.Contains(edit.Body.String(), "<html") || !strings.Contains(edit.Body.String(), `value="1.234,56"`) {
		t.Errorf("edit form = %d, want the bare form fragment with the limit: %s", edit.Code, edit.Body)
	}

	w.mustSubmit(http.MethodPost, "/categories/"+created.EntityID, categoryForm("Feira", "GREEN", "1.234,56"))
	ops = w.newOps()
	if len(ops) != 1 || ops[0].Kind != "UPDATE" || ops[0].EntityID != created.EntityID ||
		!slices.Equal(slices.Sorted(maps.Keys(ops[0].Payload)), []string{"name", "updated_at"}) || ops[0].Payload["name"] != "Feira" {
		t.Errorf("ops after edit = %+v, want one UPDATE with only name and updated_at", ops)
	}

	w.mustSubmit(http.MethodPost, "/categories/"+created.EntityID, categoryForm("Feira", "GREEN", "1234,56"))
	if ops = w.newOps(); len(ops) != 0 {
		t.Errorf("ops after saving unchanged = %+v, want none", ops)
	}

	w.mustSubmit(http.MethodDelete, "/categories/"+created.EntityID, nil)
	ops = w.newOps() // the limit of the deleted category moves to the default one first
	if len(ops) != 2 || ops[0].EntityID != w.defaultID || ops[0].Payload["monthly_limit"] != float64(123456) ||
		ops[1].Kind != "DELETE" || ops[1].EntityID != created.EntityID {
		t.Errorf("ops after delete = %+v, want the limit moved to the default category and one DELETE", ops)
	}

	if body := get(w.handler, "/categories", w.cookie).Body.String(); strings.Contains(body, "Feira") {
		t.Errorf("list after delete still shows the category: %s", body)
	}

	if code := get(w.handler, "/categories/"+created.EntityID, w.cookie).Code; code != http.StatusNotFound {
		t.Errorf("edit form of deleted category = %d, want %d", code, http.StatusNotFound)
	}
}

func TestWebCategories_InvalidFormIs422AndWritesNothing(t *testing.T) {
	w := newWebClient(t)

	response := w.submit(http.MethodPost, "/categories", categoryForm("   ", "PINK", "-1"))
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid create = %d, want %d: %s", response.Code, http.StatusUnprocessableEntity, response.Body)
	}

	body := response.Body.String()
	for _, message := range []string{"Informe um nome.", "Escolha uma cor.", "Informe um limite maior ou igual a zero."} {
		if !strings.Contains(body, message) {
			t.Errorf("422 body lacks %q: %s", message, body)
		}
	}

	if got := strings.Count(body, `aria-invalid="true"`); got != 3 {
		t.Errorf("422 body has %d invalid controls, want 3", got)
	}

	if ops := w.newOps(); len(ops) != 0 {
		t.Errorf("ops after invalid create = %+v, want none", ops)
	}
}

func TestWebCategories_SigninWithWebDeviceIDIsRefused(t *testing.T) {
	w := newWebClient(t)
	w.mustSubmit(http.MethodPost, "/categories", categoryForm("Mercado", "GREEN", "0"))
	webDeviceID := w.newOps()[0].DeviceID

	body := newSignin()
	body.DeviceID = webDeviceID
	if code := postSignin(t, w.handler, body).Code; code != http.StatusConflict {
		t.Fatalf("signin with the web device id = %d, want %d", code, http.StatusConflict)
	}

	w.mustSubmit(http.MethodPost, "/categories", categoryForm("Feira", "GREEN", "0"))
}

func TestSignup_SeedsTheInitialCategoriesInThePull(t *testing.T) {
	handler := newHandler(openStore(t), config{})
	device := newClient(t, handler, newSignup())

	type marks struct{ daily, isDefault float64 }
	got := map[string]marks{}
	for _, o := range device.pull(0, 500).Ops {
		if o.Entity != "CATEGORY" || o.Kind != "CREATE" || o.Payload["monthly_limit"] != float64(0) {
			t.Errorf("signup op = %+v, want a CATEGORY CREATE with limit 0", o)
		}

		got[o.Payload["name"].(string)] = marks{o.Payload["is_daily"].(float64), o.Payload["is_default"].(float64)}
	}

	want := map[string]marks{"Gastos fixos": {0, 0}, "Investimento": {0, 0}, "Meta": {0, 0}, "Livre": {1, 1}}
	if !maps.Equal(got, want) {
		t.Errorf("initial categories = %v, want %v", got, want)
	}
}

func TestWebCategories_DefaultCategoryIsNotDeletable(t *testing.T) {
	w := newWebClient(t)

	response := w.submit(http.MethodDelete, "/categories/"+w.defaultID, nil)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("delete default = %d, want %d: %s", response.Code, http.StatusUnprocessableEntity, response.Body)
	}

	if ops := w.newOps(); len(ops) != 0 {
		t.Errorf("ops after refused delete = %+v, want none", ops)
	}

	edit := get(w.handler, "/categories/"+w.defaultID, w.cookie).Body.String()
	if !strings.Contains(edit, "Livre") || strings.Contains(edit, `hx-delete`) {
		t.Errorf("default category sheet = %s, want it alive without a delete action", edit)
	}

	w.mustSubmit(http.MethodPost, "/categories/"+w.defaultID, categoryForm("Geral", "GRAY", "0"))
	if page := get(w.handler, "/categories", w.cookie).Body.String(); !strings.Contains(page, "Geral") {
		t.Errorf("list after renaming the default = %s, want Geral", page)
	}
}

func TestWebCategories_DeleteMovesTransactionsAndRecurrencesToTheDefault(t *testing.T) {
	w := newWebClient(t)
	w.push(newCategory(w.client))
	w.mustSubmit(http.MethodPost, "/transactions", transactionForm("Feira", "OUTCOME", "10", "2026-07-01", categoryID, ""))
	w.mustSubmit(http.MethodPost, "/recurrences", recurrenceForm("Aluguel", "OUTCOME", "1500", categoryID, "5", "2026-01-01", ""))
	created := w.newOps()
	transactionID, recurrenceID := created[1].EntityID, created[2].EntityID

	w.mustSubmit(http.MethodDelete, "/categories/"+categoryID, nil)

	moved := map[string]bool{}
	ops := w.newOps()
	for _, o := range ops[1 : len(ops)-1] { // the first op moves the category's limit
		if o.Kind != "UPDATE" || o.Payload["category_id"] != w.defaultID {
			t.Errorf("move op = %+v, want an UPDATE to the default category", o)
		}

		moved[o.Entity+"/"+o.EntityID] = true
	}

	if last := ops[len(ops)-1]; len(ops) != 4 || ops[0].EntityID != w.defaultID || ops[0].Payload["monthly_limit"] != float64(50000) || last.Entity != "CATEGORY" || last.Kind != "DELETE" || last.EntityID != categoryID ||
		!moved["TRANSACTION/"+transactionID] || !moved["RECURRENCE/"+recurrenceID] {
		t.Errorf("ops after delete = %+v, want both moves then the DELETE", ops)
	}
}

func TestWebCategories_DailyMarkIsEditedInTheForm(t *testing.T) {
	w := newWebClient(t)
	w.mustSubmit(http.MethodPost, "/categories", url.Values{"name": {"Mercado"}, "color": {"GREEN"}, "monthly_limit": {"0"}, "is_daily": {"1"}})
	created := w.newOps()[0]
	if created.Payload["is_daily"] != float64(1) || created.Payload["is_default"] != nil {
		t.Errorf("create payload = %v, want daily and no default mark", created.Payload)
	}

	edit := get(w.handler, "/categories/"+created.EntityID, w.cookie).Body.String()
	if !strings.Contains(edit, `name="is_daily" value="1" checked`) {
		t.Errorf("edit form = %s, want the daily choice on", edit)
	}

	w.mustSubmit(http.MethodPost, "/categories/"+created.EntityID, url.Values{"name": {"Mercado"}, "color": {"GREEN"}, "monthly_limit": {"0"}, "is_daily": {"0"}})
	if ops := w.newOps(); len(ops) != 1 || ops[0].Payload["is_daily"] != float64(0) {
		t.Errorf("ops after turning daily off = %+v, want one UPDATE with is_daily 0", ops)
	}
}

func TestPush_DefaultCategoryIsNotDeletableAndMarksSync(t *testing.T) {
	c := newClient(t, newHandler(openStore(t), config{}), newSignup())

	var defaultID string
	for _, o := range c.pull(0, 500).Ops {
		if o.Payload["is_default"] == float64(1) {
			defaultID = o.EntityID
		}
	}

	pushed := c.push(c.op("CATEGORY", defaultID, "DELETE", nil))
	if len(pushed.Rejected) != 1 || pushed.Rejected[0].Reason != "INVALID_OPERATION" {
		t.Fatalf("push deleting the default = %+v, want INVALID_OPERATION", pushed)
	}

	category := newCategory(c)
	category.Payload["is_daily"] = 1
	c.push(category, c.op("CATEGORY", categoryID, "UPDATE", map[string]any{"is_daily": 0, "updated_at": "2026-08-24T10:00:00Z"}))

	var created, updated op
	for _, o := range c.pull(seededOps, 500).Ops {
		if o.Kind == "CREATE" {
			created = o
		} else {
			updated = o
		}
	}

	if created.Payload["is_daily"] != float64(1) || updated.Payload["is_daily"] != float64(0) {
		t.Errorf("pulled marks = %v then %v, want is_daily 1 then 0", created.Payload, updated.Payload)
	}
}

func TestWebCategories_MarkingAnotherDefaultTakesTheMarkFromTheCurrentOne(t *testing.T) {
	w := newWebClient(t)
	meta := w.categories["Meta"]

	if sheet := get(w.handler, "/categories/"+w.defaultID, w.cookie).Body.String(); strings.Contains(sheet, `name="is_default"`) {
		t.Errorf("default category sheet = %s, want no default choice", sheet)
	}

	w.mustSubmit(http.MethodPost, "/categories/"+meta, url.Values{"name": {"Meta"}, "color": {"YELLOW"}, "is_daily": {"0"}, "is_default": {"1"}})
	ops := w.newOps()
	if len(ops) != 2 || ops[0].EntityID != w.defaultID || ops[0].Payload["is_default"] != float64(0) ||
		ops[1].EntityID != meta || ops[1].Payload["is_default"] != float64(1) {
		t.Fatalf("ops after marking Meta = %+v, want Livre unmarked then Meta marked", ops)
	}

	w.mustSubmit(http.MethodDelete, "/categories/"+w.defaultID, nil)
	if response := w.submit(http.MethodDelete, "/categories/"+meta, nil); response.Code != http.StatusUnprocessableEntity {
		t.Errorf("delete new default = %d, want %d", response.Code, http.StatusUnprocessableEntity)
	}
}

func TestPush_KeepsExactlyOneDefaultCategory(t *testing.T) {
	w := newWebClient(t)

	unmark := w.op("CATEGORY", w.defaultID, "UPDATE", map[string]any{"is_default": 0, "updated_at": "2026-08-24T10:00:00Z"})
	trash := w.op("CATEGORY", w.defaultID, "UPDATE", map[string]any{"deleted_at": "2026-08-24T10:00:00Z", "updated_at": "2026-08-24T10:00:00Z"})
	if pushed := w.push(unmark, trash); len(pushed.Rejected) != 2 {
		t.Fatalf("push unmarking and trashing the default = %+v, want both rejected", pushed)
	}

	category := newCategory(w.client)
	category.Payload["is_default"] = 1
	if pushed := w.push(category); len(pushed.Rejected) != 0 {
		t.Fatalf("push creating a default = %+v, want accepted", pushed)
	}

	ops := w.newOps()
	if len(ops) != 2 || ops[0].EntityID != w.defaultID || ops[0].Payload["is_default"] != float64(0) || ops[1].EntityID != categoryID {
		t.Errorf("pulled ops = %+v, want Livre unmarked then the new default", ops)
	}
}
