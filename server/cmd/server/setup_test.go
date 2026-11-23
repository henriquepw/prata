package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func englishChoices() url.Values {
	return url.Values{"language": {"en"}, "date_format": {"MM/DD/YYYY"}, "number_format": {"1,234.56"}, "currency": {"USD"}}
}

func assertRedirect(t *testing.T, got *httptest.ResponseRecorder, location string) {
	t.Helper()
	if got.Code != http.StatusSeeOther || got.Header().Get("Location") != location {
		t.Errorf("response = %d to %q, want %d to %q", got.Code, got.Header().Get("Location"), http.StatusSeeOther, location)
	}
}

func TestWebSetup_EmptyDatabaseSendsTheEntrancesToSetup(t *testing.T) {
	handler := newHandler(openStore(t), config{})

	for _, path := range []string{"/", "/login", "/signup"} {
		assertRedirect(t, get(handler, path), "/setup")
	}

	if response := get(handler, "/setup"); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `action="/setup"`) {
		t.Errorf("GET /setup = %d, want %d with the form: %s", response.Code, http.StatusOK, response.Body)
	}
}

func TestWebSetup_InvalidValueIs422AndWritesNothing(t *testing.T) {
	handler := newHandler(openStore(t), config{})
	form := englishChoices()
	form.Set("currency", "JPY")

	response := postForm(handler, "/setup", form)
	if body := response.Body.String(); response.Code != http.StatusUnprocessableEntity || !strings.Contains(body, `aria-invalid="true"`) ||
		!strings.Contains(body, `value="MM/DD/YYYY" selected`) {
		t.Errorf("POST /setup = %d, want %d with the filled form flagging the field: %s", response.Code, http.StatusUnprocessableEntity, body)
	}

	assertRedirect(t, get(handler, "/login"), "/setup")
}

func TestWebSetup_CarriesTheChoicesThroughTheSignupIntoTheSettings(t *testing.T) {
	handler := newHandler(openStore(t), config{})

	response := browse(handler, http.MethodPost, "/setup", "pt-BR", englishChoices())
	location := response.Header().Get("Location")
	if response.Code != http.StatusSeeOther || !strings.HasPrefix(location, "/signup?") {
		t.Fatalf("POST /setup = %d to %q, want %d to /signup with the choices", response.Code, location, http.StatusSeeOther)
	}

	page := browse(handler, http.MethodGet, location, "pt-BR", nil)
	body := page.Body.String()
	for _, text := range []string{`<html lang="en"`, "Create account", `name="currency" value="USD"`} {
		if page.Code != http.StatusOK || !strings.Contains(body, text) {
			t.Fatalf("GET %s = %d, want %q in: %s", location, page.Code, text, body)
		}
	}

	if strings.Contains(body, `href="/login"`) {
		t.Errorf("first signup links to login: %s", body)
	}

	form := credentials()
	for name, values := range englishChoices() {
		form[name] = values
	}

	signedUp := browse(handler, http.MethodPost, "/signup", "pt-BR", form)
	assertRedirect(t, signedUp, "/welcome")

	w := &webClient{client: &client{t: t, handler: handler}, cookie: sessionCookie(t, signedUp)}
	w.assertSelected("en", "MM/DD/YYYY", "1,234.56", "USD")
}

func TestWebSetup_WithUsersRedirectsToLogin(t *testing.T) {
	handler := newHandler(openStore(t), config{})
	signupKey(t, handler, newSignup())

	assertRedirect(t, get(handler, "/setup"), "/login")
	assertRedirect(t, postForm(handler, "/setup", englishChoices()), "/login")
}

func TestWebSetup_SecondSignupSkipsSetupWithTheDefaults(t *testing.T) {
	handler := newHandler(openStore(t), config{isSignupAllowed: true})
	signupKey(t, handler, newSignup())

	if page := get(handler, "/signup"); page.Code != http.StatusOK || strings.Contains(page.Body.String(), `type="hidden"`) {
		t.Fatalf("GET /signup = %d, want the plain form: %s", page.Code, page.Body)
	}

	signedUp := postForm(handler, "/signup", url.Values{"email": {"bia@example.com"}, "password": {"another secret"}})
	assertRedirect(t, signedUp, "/welcome")

	w := &webClient{client: &client{t: t, handler: handler}, cookie: sessionCookie(t, signedUp)}
	w.assertSelected("pt-BR", "DD/MM/YYYY", "1.234,56", "BRL")
}
