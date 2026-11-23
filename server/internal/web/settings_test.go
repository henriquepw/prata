package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/henriquepw/prata/server/internal/store"
)

// signedIn is the web routes over a fresh database with one account, and the cookie of its owner's session.
func signedIn(t *testing.T) (http.Handler, *http.Cookie) {
	t.Helper()

	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "prata.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	routes := New(db, true, OpenFinance{})
	req := httptest.NewRequest(http.MethodPost, "/signup", strings.NewReader(url.Values{"email": {"ana@example.com"}, "password": {"correct horse"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, req)

	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			return routes, c
		}
	}
	t.Fatalf("signup set no session cookie, status %d", rec.Code)

	return nil, nil
}

func do(routes http.Handler, cookie *http.Cookie, method, target string, htmx bool, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, req)

	return rec
}

func TestSettingsSheet(t *testing.T) {
	routes, cookie := signedIn(t)

	t.Run("htmx gets the form without the layout", func(t *testing.T) {
		rec := do(routes, cookie, http.MethodGet, "/settings", true, nil)
		body := rec.Body.String()
		if rec.Code != http.StatusOK || !strings.Contains(body, `hx-post="/settings"`) || strings.Contains(body, `aria-label="Telas"`) {
			t.Errorf("status %d, body %s", rec.Code, body)
		}
	})

	t.Run("a plain request goes to the Mês", func(t *testing.T) {
		rec := do(routes, cookie, http.MethodGet, "/settings", false, nil)
		if rec.Code != http.StatusSeeOther && rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
			t.Errorf("status %d, location %q", rec.Code, rec.Header().Get("Location"))
		}
	})

	t.Run("an invalid value answers 422 with the error", func(t *testing.T) {
		rec := do(routes, cookie, http.MethodPost, "/settings", true, url.Values{"language": {"xx"}, "date_format": {"DD/MM/YYYY"}, "number_format": {"1.234,56"}, "currency": {"BRL"}})
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "hx-post=\"/settings\"") {
			t.Errorf("status %d, body %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("a valid form refreshes the page", func(t *testing.T) {
		rec := do(routes, cookie, http.MethodPost, "/settings", true, url.Values{"language": {"en"}, "date_format": {"DD/MM/YYYY"}, "number_format": {"1.234,56"}, "currency": {"BRL"}, "daily_limit_mode": {"FIXED"}})
		if rec.Code != http.StatusNoContent || rec.Header().Get("HX-Refresh") != "true" {
			t.Errorf("status %d, headers %v", rec.Code, rec.Header())
		}
	})
}

func TestHomeChrome(t *testing.T) {
	routes, cookie := signedIn(t)
	body := do(routes, cookie, http.MethodGet, "/", false, nil).Body.String()

	settings, theme := strings.Index(body, `hx-get="/settings"`), strings.Index(body, `onclick="cycleTheme()"`)
	if settings < 0 || theme < 0 || settings > theme {
		t.Errorf("settings link at %d, theme at %d, want the link before theme", settings, theme)
	}

	for _, want := range []string{`data-key=","`, "1-6"} {
		if !strings.Contains(body, want) {
			t.Errorf("home lacks %s", want)
		}
	}

	if strings.Contains(body, `<a href="/settings"`) {
		t.Error("rail still links to /settings")
	}
}
