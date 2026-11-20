package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// browse sends a request from a browser whose Accept-Language is language, posting form when it is not nil.
func browse(handler http.Handler, method, path, language string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if language != "" {
		req.Header.Set("Accept-Language", language)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)

	return response
}

func TestAuthPages_FollowTheBrowserLanguage(t *testing.T) {
	handler := newHandler(openStore(t), config{})
	english := map[string][]string{
		"/login":  {`<html lang="en">`, "Sign in", "Email", "Password", "&gt; sign in", "Don&#39;t have an account yet?", "Create account"},
		"/signup": {`<html lang="en">`, "Create account", "&gt; create account", "Already use Prata in the app?", "Already have an account?", "Sign in"},
	}
	portuguese := map[string][]string{
		"/login":  {`<html lang="pt-BR">`, "Entrar", "Senha", "&gt; entrar", "Ainda não tem conta?"},
		"/signup": {`<html lang="pt-BR">`, "Criar conta", "&gt; criar conta", "Já usa o Prata no app?", "Já tem conta?"},
	}
	for language, pages := range map[string]map[string][]string{"en-US,en;q=0.9": english, "fr": portuguese, "": portuguese} {
		for path, texts := range pages {
			body := browse(handler, http.MethodGet, path, language, nil).Body.String()
			for _, text := range texts {
				if !strings.Contains(body, text) {
					t.Errorf("%s with Accept-Language %q lacks %q: %s", path, language, text, body)
				}
			}
		}
	}
}

func TestWebSignup_KeepsThePageLanguageWithTheDefaultFormats(t *testing.T) {
	handler := newHandler(openStore(t), config{})

	response := browse(handler, http.MethodPost, "/signup", "en-US,en;q=0.9", credentials())
	w := &webClient{client: &client{t: t, handler: handler}, cookie: sessionCookie(t, response)}

	w.assertBody("/", `<html lang="en">`, "Overview")
	w.assertSelected("en", "DD/MM/YYYY", "1.234,56", "BRL")
}

func TestAPISignup_IsInPortugueseWhateverTheBrowser(t *testing.T) {
	handler := newHandler(openStore(t), config{})
	signupKey(t, handler, newSignup())

	response := browse(handler, http.MethodPost, "/login", "en-US,en;q=0.9", credentials())
	w := &webClient{client: &client{t: t, handler: handler}, cookie: sessionCookie(t, response)}

	w.assertBody("/", `<html lang="pt-BR">`, "Visão geral")
	w.assertSelected("pt-BR")
}

func TestAuthErrors_FollowTheBrowserLanguage(t *testing.T) {
	closed, open := newHandler(openStore(t), config{}), newHandler(openStore(t), config{isSignupAllowed: true})
	signupKey(t, closed, newSignup())
	signupKey(t, open, newSignup())

	for name, tt := range map[string]struct {
		handler http.Handler
		path    string
		form    url.Values
		texts   []string
	}{
		"wrong password": {closed, "/login", url.Values{"email": {newSignup().Email}, "password": {"wrong"}}, []string{"Invalid email or password."}},
		"missing fields": {closed, "/signup", url.Values{"email": {"bia"}, "password": {""}}, []string{"Enter an email.", "Enter a password."}},
		"signup closed":  {closed, "/signup", url.Values{"email": {"bia@example.com"}, "password": {"another secret"}}, []string{"Sign-up is closed on this server."}},
		"email taken":    {open, "/signup", credentials(), []string{"This email already has an account."}},
	} {
		t.Run(name, func(t *testing.T) {
			response := browse(tt.handler, http.MethodPost, tt.path, "en-US,en;q=0.9", tt.form)
			for _, text := range append(tt.texts, `<html lang="en">`) {
				if body := response.Body.String(); response.Code != http.StatusUnprocessableEntity || !strings.Contains(body, text) {
					t.Errorf("POST %s = %d, want %d with %q: %s", tt.path, response.Code, http.StatusUnprocessableEntity, text, body)
				}
			}
		})
	}
}
