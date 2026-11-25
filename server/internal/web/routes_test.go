package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/henriquepw/prata/server/internal/openfinance"
	"github.com/henriquepw/prata/server/internal/store"
)

// emptyRoutes is the web routes over a fresh database with no user.
func emptyRoutes(t *testing.T) http.Handler {
	t.Helper()

	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "prata.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	return New(db, true, openfinance.Config{})
}

// anonymous is a request without a session cookie.
func anonymous(routes http.Handler, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, httptest.NewRequest(method, target, nil))

	return rec
}

// idIn is the id of the first row the page links to under prefix, as in `/tags/<id>`.
func idIn(t *testing.T, body, prefix string) string {
	t.Helper()

	match := regexp.MustCompile(regexp.QuoteMeta(prefix) + `/([0-9A-Z]{26})`).FindStringSubmatch(body)
	if match == nil {
		t.Fatalf("no %s/<id> in %s", prefix, body)
	}

	return match[1]
}

// authenticatedPages are the routes that need a session.
var authenticatedPages = []string{"/", "/welcome", "/categories", "/tags", "/transactions", "/recurrences", "/movements", "/connections", "/devices", "/settings"}

func TestAuthenticatedRoutesRedirectWithoutSession(t *testing.T) {
	routes, _ := signedIn(t)

	for _, target := range authenticatedPages {
		t.Run(target, func(t *testing.T) {
			rec := anonymous(routes, http.MethodGet, target)
			if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
				t.Errorf("status %d, location %q", rec.Code, rec.Header().Get("Location"))
			}
		})
	}
}

func TestEmptyServerSendsEveryoneToSetup(t *testing.T) {
	routes := emptyRoutes(t)

	rec := anonymous(routes, http.MethodGet, "/categories")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/setup" {
		t.Errorf("status %d, location %q", rec.Code, rec.Header().Get("Location"))
	}

	rec = anonymous(routes, http.MethodGet, "/setup")
	if rec.Code != http.StatusOK {
		t.Errorf("setup status %d", rec.Code)
	}
}

func TestVisitorPages(t *testing.T) {
	routes, _ := signedIn(t)

	for _, tt := range []struct {
		target string
		status int
		want   string
	}{
		{"/login", http.StatusOK, `action="/login"`},
		{"/no/such/page", http.StatusNotFound, "Página não encontrada"},
		{"/signup", http.StatusOK, `action="/signup"`},
	} {
		t.Run(tt.target, func(t *testing.T) {
			rec := anonymous(routes, http.MethodGet, tt.target)
			if rec.Code != tt.status || !strings.Contains(rec.Body.String(), tt.want) {
				t.Errorf("status %d, location %q, body %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
			}
		})
	}
}

func TestLoginRejectsBadCredentials(t *testing.T) {
	routes, _ := signedIn(t)
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(url.Values{"email": {"ana@example.com"}, "password": {"wrong"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status %d", rec.Code)
	}
}

func TestPages(t *testing.T) {
	routes, cookie := signedInWith(t, openfinance.Config{Key: "key"})

	for _, target := range []string{"/", "/welcome", "/categories", "/tags", "/recurrences", "/movements", "/connections", "/devices"} {
		t.Run(target, func(t *testing.T) {
			rec := do(routes, cookie, http.MethodGet, target, false, nil)
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<html") {
				t.Errorf("status %d, body %s", rec.Code, rec.Body.String())
			}
		})
	}

	for _, target := range []string{"/settings", "/transactions"} {
		t.Run(target+" is a sheet", func(t *testing.T) {
			rec := do(routes, cookie, http.MethodGet, target, false, nil)
			if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
				t.Errorf("status %d, location %q", rec.Code, rec.Header().Get("Location"))
			}
		})
	}
}

func TestWelcomeRejectsAnInvalidMode(t *testing.T) {
	routes, cookie := signedIn(t)

	rec := do(routes, cookie, http.MethodPost, "/welcome", false, url.Values{"daily_limit_mode": {"nope"}})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("invalid form: status %d", rec.Code)
	}
}

func TestOpenFinanceRoutesAreHiddenWhenOff(t *testing.T) {
	routes, cookie := signedIn(t)

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		if rec := do(routes, cookie, method, "/connections", false, nil); rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d", method, rec.Code)
		}
	}
}

func TestRevokingAnUnknownDeviceIs404(t *testing.T) {
	routes, cookie := signedIn(t)

	if rec := do(routes, cookie, http.MethodDelete, "/devices/01ARZ3NDEKTSV4RRFFQ69G5FAV", true, nil); rec.Code != http.StatusNotFound {
		t.Errorf("status %d", rec.Code)
	}
}

func TestSheetLifecycle(t *testing.T) {
	routes, cookie := signedIn(t)
	today := time.Now().UTC().Format(time.DateOnly)

	tests := []struct {
		base, list string
		form       url.Values
		invalid    url.Values
	}{
		{"/categories", "/categories", url.Values{"name": {"Mercado"}, "color": {"BLUE"}}, url.Values{"name": {""}, "color": {"BLUE"}}},
		{"/tags", "/tags", url.Values{"name": {"Viagem"}, "start_at": {today}}, url.Values{"name": {"Viagem"}, "start_at": {"x"}}},
		{"/recurrences", "/recurrences", url.Values{"description": {"Aluguel"}, "kind": {"OUTCOME"}, "amount": {"10,00"}, "day": {"5"}, "start_at": {today}}, url.Values{"kind": {"OUTCOME"}, "amount": {"0"}, "day": {"5"}, "start_at": {today}}},
		{"/transactions", "/", url.Values{"description": {"Pão"}, "kind": {"OUTCOME"}, "amount": {"3,50"}, "occurred_at": {today}}, url.Values{"kind": {"OUTCOME"}, "amount": {"abc"}, "occurred_at": {today}}},
	}
	for _, tt := range tests {
		t.Run(tt.base, func(t *testing.T) {
			if rec := do(routes, cookie, http.MethodGet, tt.base+"/new", true, nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `hx-post="`+tt.base+`"`) {
				t.Fatalf("open: status %d, body %s", rec.Code, rec.Body.String())
			}

			if rec := do(routes, cookie, http.MethodPost, tt.base, true, tt.invalid); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `hx-post="`+tt.base+`"`) {
				t.Fatalf("invalid save: status %d, body %s", rec.Code, rec.Body.String())
			}

			if rec := do(routes, cookie, http.MethodPost, tt.base, true, tt.form); rec.Code != http.StatusNoContent || rec.Header().Get("HX-Refresh") != "true" {
				t.Fatalf("save: status %d, headers %v, body %s", rec.Code, rec.Header(), rec.Body.String())
			}

			id := idIn(t, do(routes, cookie, http.MethodGet, tt.list, false, nil).Body.String(), tt.base)
			edit := tt.base + "/" + id
			if rec := do(routes, cookie, http.MethodGet, edit, true, nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `hx-post="`+edit+`"`) {
				t.Fatalf("edit: status %d, body %s", rec.Code, rec.Body.String())
			}

			if rec := do(routes, cookie, http.MethodDelete, edit, true, nil); rec.Code != http.StatusNoContent || rec.Header().Get("HX-Refresh") != "true" {
				t.Fatalf("delete: status %d, headers %v", rec.Code, rec.Header())
			}

			if rec := do(routes, cookie, http.MethodGet, edit, true, nil); rec.Code != http.StatusNotFound {
				t.Errorf("after delete: status %d", rec.Code)
			}
		})
	}
}
