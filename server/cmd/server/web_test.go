package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func postForm(handler http.Handler, path string, form url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)

	return response
}

func get(handler http.Handler, path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)

	return response
}

func sessionCookie(t *testing.T, response *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "session" {
			return cookie
		}
	}

	t.Fatalf("no session cookie in response %d: %s", response.Code, response.Body)

	return nil
}

func credentials() url.Values {
	return url.Values{"email": {newSignup().Email}, "password": {newSignup().Password}}
}

func login(t *testing.T, handler http.Handler) *http.Cookie {
	t.Helper()
	response := postForm(handler, "/login", credentials())
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/" {
		t.Fatalf("login = %d to %q, want %d to /", response.Code, response.Header().Get("Location"), http.StatusSeeOther)
	}

	return sessionCookie(t, response)
}

func assertRedirectsToLogin(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/login" {
		t.Errorf("response = %d to %q, want %d to /login", response.Code, response.Header().Get("Location"), http.StatusSeeOther)
	}
}

func TestLogin_SetsSessionCookie(t *testing.T) {
	handler := newHandler(openStore(t), config{})
	signupKey(t, handler, newSignup())

	cookie := login(t, handler)
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" || cookie.Secure {
		t.Errorf("cookie = %+v, want HttpOnly, SameSite=Lax, Path=/ and not Secure over plain http", cookie)
	}

	if len(cookie.Value) < 43 {
		t.Errorf("token %q is shorter than 256 bits", cookie.Value)
	}

	if got := get(handler, "/", cookie).Code; got != http.StatusOK {
		t.Errorf("home with session status = %d, want %d", got, http.StatusOK)
	}
}

func TestLogin_SecureBehindTLSProxy(t *testing.T) {
	handler := newHandler(openStore(t), config{})
	signupKey(t, handler, newSignup())

	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(credentials().Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Forwarded-Proto", "https")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)

	if cookie := sessionCookie(t, response); !cookie.Secure {
		t.Errorf("cookie behind https proxy = %+v, want Secure", cookie)
	}
}

func TestLogin_WrongPasswordRerendersForm(t *testing.T) {
	handler := newHandler(openStore(t), config{})
	signupKey(t, handler, newSignup())

	response := postForm(handler, "/login", url.Values{"email": {newSignup().Email}, "password": {"wrong"}})
	if response.Code != http.StatusUnprocessableEntity {
		t.Errorf("wrong password status = %d, want %d", response.Code, http.StatusUnprocessableEntity)
	}

	if len(response.Result().Cookies()) != 0 {
		t.Errorf("wrong password set cookies %v", response.Result().Cookies())
	}

	if !strings.Contains(response.Body.String(), "E-mail ou senha inválidos") {
		t.Errorf("wrong password body lacks the error: %s", response.Body)
	}
}

func TestAuthenticatedRoute_WithoutSessionRedirectsToLogin(t *testing.T) {
	handler := newHandler(openStore(t), config{})

	assertRedirectsToLogin(t, get(handler, "/"))
	assertRedirectsToLogin(t, get(handler, "/", &http.Cookie{Name: "session", Value: "forged"}))
}

func TestLogout_EndsOnlyCurrentSession(t *testing.T) {
	handler := newHandler(openStore(t), config{})
	signupKey(t, handler, newSignup())
	first, second := login(t, handler), login(t, handler)

	response := postForm(handler, "/logout", nil, first)
	assertRedirectsToLogin(t, response)
	if cleared := sessionCookie(t, response); cleared.MaxAge >= 0 {
		t.Errorf("logout cookie = %+v, want it cleared", cleared)
	}

	assertRedirectsToLogin(t, get(handler, "/", first))
	if got := get(handler, "/", second).Code; got != http.StatusOK {
		t.Errorf("other session after logout status = %d, want %d", got, http.StatusOK)
	}
}

func TestCrossOriginMutationIsBlocked(t *testing.T) {
	handler := newHandler(openStore(t), config{})
	signupKey(t, handler, newSignup())
	cookie := login(t, handler)

	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)

	if response.Code != http.StatusForbidden {
		t.Errorf("cross-origin logout status = %d, want %d", response.Code, http.StatusForbidden)
	}

	if got := get(handler, "/", cookie).Code; got != http.StatusOK {
		t.Errorf("session after blocked logout status = %d, want %d", got, http.StatusOK)
	}
}

func TestWebSignup(t *testing.T) {
	handler := newHandler(openStore(t), config{})

	response := postForm(handler, "/signup", credentials())
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/" {
		t.Fatalf("signup = %d to %q, want %d to /", response.Code, response.Header().Get("Location"), http.StatusSeeOther)
	}

	if got := get(handler, "/", sessionCookie(t, response)).Code; got != http.StatusOK {
		t.Errorf("home after signup status = %d, want %d", got, http.StatusOK)
	}

	login(t, handler)

	closed := postForm(handler, "/signup", url.Values{"email": {"bia@example.com"}, "password": {"another secret"}})
	if closed.Code != http.StatusUnprocessableEntity || !strings.Contains(closed.Body.String(), "Cadastro fechado") {
		t.Errorf("second signup = %d, want %d with the closed message: %s", closed.Code, http.StatusUnprocessableEntity, closed.Body)
	}
}

func TestAuthPages_ShowTheConsoleEntrance(t *testing.T) {
	handler := newHandler(openStore(t), config{})

	for path, action := range map[string]string{"/login": "&gt; entrar", "/signup": "&gt; criar conta"} {
		body := get(handler, path).Body.String()
		for _, text := range []string{`role="img" aria-label="Prata"`, "██████╗", action, "prata v1", "[online]"} {
			if !strings.Contains(body, text) {
				t.Errorf("%s lacks %q", path, text)
			}
		}

		if strings.Contains(body, `aria-invalid="`) {
			t.Errorf("%s marks a field before any error: %s", path, body)
		}
	}
}

func TestWebSignup_FlagsEachMissingField(t *testing.T) {
	handler := newHandler(openStore(t), config{})

	response := postForm(handler, "/signup", url.Values{"email": {"bia"}, "password": {""}})
	body := response.Body.String()
	if response.Code != http.StatusUnprocessableEntity || strings.Count(body, `aria-invalid="true"`) != 2 {
		t.Errorf("signup without email or password = %d, want %d with both fields invalid: %s", response.Code, http.StatusUnprocessableEntity, body)
	}

	for _, text := range []string{"Informe um e-mail.", "Informe uma senha."} {
		if !strings.Contains(body, text) {
			t.Errorf("signup body lacks %q", text)
		}
	}
}

func TestWebLauncher_ListsTheCommandsOnEveryPage(t *testing.T) {
	w := newWebClient(t)
	for _, path := range []string{"/", "/devices"} {
		body := get(w.handler, path, w.cookie).Body.String()
		for _, text := range []string{
			`<dialog id="launcher"`, "Ir para mês", "Ir para dispositivos", "Nova transação", "Nova categoria",
			"Nova tag", "Nova recorrência", "Alternar tema", "Sair",
		} {
			if !strings.Contains(body, text) {
				t.Errorf("%s launcher lacks %q: %s", path, text, body)
			}
		}
	}
}

func TestWebKeybar_BindsNewOnlyWhereThereIsOne(t *testing.T) {
	w := newWebClient(t)
	for path, want := range map[string]string{"/": "/transactions/new", "/tags": "/tags/new", "/devices": ""} {
		body := get(w.handler, path, w.cookie).Body.String()
		bound := `data-key="n" hx-get="` + want + `"`
		if want == "" && strings.Contains(body, `data-key="n"`) || want != "" && !strings.Contains(body, bound) {
			t.Errorf("%s keybar binds n to %q? want %q: %s", path, bound, want, body)
		}
	}
}

func TestWebChrome_NamesCommandsAlikeAndClosesOnlyWithSomethingOpen(t *testing.T) {
	w := newWebClient(t)
	body := get(w.handler, "/tags", w.cookie).Body.String()
	for _, text := range []string{"[ctrl k] comandos", "<kbd>ctrl k</kbd> comandos", "<kbd>ctrl enter</kbd> salvar", "Categorias</a>"} {
		if !strings.Contains(body, text) {
			t.Errorf("tags chrome lacks %q: %s", text, body)
		}
	}

	if save, esc := strings.Index(body, "<kbd>ctrl enter</kbd>"), strings.Index(body, "<kbd>esc</kbd> fechar"); esc < save {
		t.Errorf("keybar shows esc fechar outside the open sheet: %s", body)
	}

	if strings.Contains(body, "]Cat") {
		t.Errorf("mobile nav still abbreviates: %s", body)
	}
}

func TestStaticAssets(t *testing.T) {
	handler := newHandler(openStore(t), config{})

	for _, path := range []string{"/static/app.css", "/static/htmx.min.js", "/static/fonts/GeistMono-Variable.woff2"} {
		if got := get(handler, path).Code; got != http.StatusOK {
			t.Errorf("%s status = %d, want %d", path, got, http.StatusOK)
		}
	}
}
