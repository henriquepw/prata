// Package session holds the request's session and location, and the responses every handler writes: rendering,
// redirects, errors and the sheet commit.
package session

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/a-h/templ"
	"github.com/henriquepw/prata/server/internal/importfile"
	"github.com/henriquepw/prata/server/internal/store"
)

// FlashCookie holds the id of the row a sheet just saved, scoped to the path of the list that shows it for that page
// to flash once.
const FlashCookie = "flash"

type sessionKey struct{}

// With carries the session in ctx for From.
func With(ctx context.Context, session store.Session) context.Context {
	return context.WithValue(ctx, sessionKey{}, session)
}

// From is the request's session, or Visitor on a page without one. It never fails, so a route
// that needs a logged-in user relies on requireSession to turn visitors away.
func From(ctx context.Context) store.Session {
	if session, ok := ctx.Value(sessionKey{}).(store.Session); ok {
		return session
	}

	return Visitor
}

// Location is the browser time zone from the tz cookie, or UTC when it is missing or unknown.
func Location(r *http.Request) *time.Location {
	cookie, err := r.Cookie("tz")
	if err != nil {
		return time.UTC
	}

	loc, err := time.LoadLocation(cookie.Value)
	if err != nil {
		return time.UTC
	}

	return loc
}

// Redirect sends htmx requests a full-page HX-Redirect, since fetch would follow a 303 and swap the
// target page under the old URL, and everyone else a 303.
func Redirect(w http.ResponseWriter, r *http.Request, url string) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", url)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	http.Redirect(w, r, url, http.StatusSeeOther)
}

// Render writes page as an HTML response with status.
func Render(w http.ResponseWriter, r *http.Request, status int, page templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := page.Render(r.Context(), w); err != nil {
		slog.Error("rendering page", "error", err)
	}
}

// InternalError logs err and answers 500.
func InternalError(w http.ResponseWriter, msg string, err error) {
	slog.Error(msg, "error", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

// Visitor holds the date format, number format and currency that pages without a session follow; their
// language comes from visitor.
var Visitor = store.Session{DateFormat: "DD/MM/YYYY", NumberFormat: "1.234,56", Currency: importfile.CurrencyBRL}
