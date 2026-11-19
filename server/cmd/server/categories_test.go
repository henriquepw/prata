package main

import (
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
)

// webClient pairs a logged-in web session with a device of the same account that pulls what the web writes.
type webClient struct {
	*client
	cookie *http.Cookie
	cursor int64
}

func newWebClient(t *testing.T) *webClient {
	t.Helper()
	handler := newHandler(openStore(t), config{})
	device := newClient(t, handler, newSignup())

	return &webClient{client: device, cookie: login(t, handler)}
}

// submit sends the form like htmx does and returns the response.
func (w *webClient) submit(method, path string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.AddCookie(w.cookie)
	response := httptest.NewRecorder()
	w.handler.ServeHTTP(response, req)

	return response
}

// mustSubmit asserts the write succeeded with HX-Refresh.
func (w *webClient) mustSubmit(method, path string, form url.Values) {
	w.t.Helper()
	response := w.submit(method, path, form)
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
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Mercado") || !strings.Contains(page.Body.String(), "bg-cat-green") {
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
	if ops = w.newOps(); len(ops) != 1 || ops[0].Kind != "DELETE" || ops[0].EntityID != created.EntityID {
		t.Errorf("ops after delete = %+v, want one DELETE", ops)
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
