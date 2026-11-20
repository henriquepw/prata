// Package web serves the server-rendered web app: session, pages and static assets.
package web

import (
	"context"
	"embed"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/a-h/templ"
	"github.com/henriquepw/prata/server/internal/i18n"
	"github.com/henriquepw/prata/server/internal/store"
	"github.com/oklog/ulid/v2"
)

//go:generate go tool templ generate -path ../..
//go:generate tailwindcss -i static/globals.css -o static/app.css --minify

//go:embed static/app.css static/htmx.min.js static/fonts/GeistMono-Variable.woff2
var static embed.FS

const sessionCookie = "session"

// flashCookie holds the id of the row a sheet just saved, scoped to its list's path for that page to flash once.
const flashCookie = "flash"

type sessionKey struct{}

type handler struct {
	db              *store.Store
	isSignupAllowed bool
}

// New returns the web routes behind cross-origin protection. Routes wrapped in requireSession see the
// session via sessionFrom.
func New(db *store.Store, isSignupAllowed bool) http.Handler {
	h := handler{db: db, isSignupAllowed: isSignupAllowed}
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.FileServerFS(static))
	mux.HandleFunc("GET /login", visitor(func(w http.ResponseWriter, r *http.Request) { render(w, r, http.StatusOK, loginPage(authForm{})) }))
	mux.HandleFunc("POST /login", visitor(h.login))
	mux.HandleFunc("GET /signup", visitor(func(w http.ResponseWriter, r *http.Request) { render(w, r, http.StatusOK, signupPage(authForm{})) }))
	mux.HandleFunc("POST /signup", visitor(h.signup))
	mux.HandleFunc("POST /logout", h.logout)
	mux.Handle("GET /{$}", h.requireSession(h.home))
	mux.Handle("GET /transactions", h.requireSession(h.transactions))
	mux.Handle("GET /transactions/new", h.requireSession(h.newTransaction))
	mux.Handle("GET /transactions/{id}", h.requireSession(h.editTransaction))
	mux.Handle("POST /transactions", h.requireSession(h.saveTransaction))
	mux.Handle("POST /transactions/{id}", h.requireSession(h.saveTransaction))
	mux.Handle("DELETE /transactions/{id}", h.requireSession(h.deleteTransaction))
	mux.Handle("GET /categories", h.requireSession(h.categories))
	mux.Handle("GET /categories/new", h.requireSession(func(w http.ResponseWriter, r *http.Request) {
		render(w, r, http.StatusOK, categorySheet(categoryForm{Color: "GRAY"}))
	}))
	mux.Handle("GET /categories/{id}", h.requireSession(h.editCategory))
	mux.Handle("POST /categories", h.requireSession(h.saveCategory))
	mux.Handle("POST /categories/{id}", h.requireSession(h.saveCategory))
	mux.Handle("DELETE /categories/{id}", h.requireSession(h.deleteCategory))
	mux.Handle("GET /recurrences", h.requireSession(h.recurrences))
	mux.Handle("GET /recurrences/new", h.requireSession(h.newRecurrence))
	mux.Handle("GET /recurrences/{id}", h.requireSession(h.editRecurrence))
	mux.Handle("POST /recurrences", h.requireSession(h.saveRecurrence))
	mux.Handle("POST /recurrences/{id}", h.requireSession(h.saveRecurrence))
	mux.Handle("DELETE /recurrences/{id}", h.requireSession(h.deleteRecurrence))
	mux.Handle("POST /recurrences/{id}/occurrences/{date}", h.requireSession(h.confirmOccurrence))
	mux.Handle("GET /tags", h.requireSession(h.tags))
	mux.Handle("GET /tags/new", h.requireSession(func(w http.ResponseWriter, r *http.Request) {
		render(w, r, http.StatusOK, tagSheet(tagForm{}))
	}))
	mux.Handle("GET /tags/{id}", h.requireSession(h.editTag))
	mux.Handle("POST /tags", h.requireSession(h.saveTag))
	mux.Handle("POST /tags/{id}", h.requireSession(h.saveTag))
	mux.Handle("DELETE /tags/{id}", h.requireSession(h.deleteTag))
	mux.Handle("GET /devices", h.requireSession(h.devices))
	mux.Handle("DELETE /devices/{id}", h.requireSession(h.revokeDevice))
	mux.Handle("GET /settings", h.requireSession(h.settings))
	mux.Handle("POST /settings", h.requireSession(h.saveSettings))

	return http.NewCrossOriginProtection().Handler(mux)
}

func (h handler) login(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.PostFormValue("email"))
	userID, err := h.db.Authenticate(r.Context(), email, r.PostFormValue("password"))
	if errors.Is(err, store.ErrInvalidCredentials) {
		render(w, r, http.StatusUnprocessableEntity, loginPage(authForm{Email: email, PasswordError: i18n.T(r.Context(), "auth.error.credentials")}))
		return
	}

	if err != nil {
		internalError(w, "authenticating", err)
		return
	}

	h.startSession(w, r, userID)
}

func (h handler) signup(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.PostFormValue("email"))
	password := r.PostFormValue("password")
	form := authForm{Email: email}
	if !strings.Contains(email, "@") {
		form.EmailError = i18n.T(r.Context(), "auth.error.email")
	}

	if password == "" {
		form.PasswordError = i18n.T(r.Context(), "auth.error.password")
	}

	if form.EmailError == "" && form.PasswordError == "" {
		signup := store.Signup{Email: email, Password: password, AccountID: ulid.Make().String(), OwnerID: ulid.Make().String(), Language: i18n.Language(r.Context())}
		_, err := h.db.CreateAccount(r.Context(), signup, h.isSignupAllowed)
		switch {
		case errors.Is(err, store.ErrSignupClosed):
			form.Error = i18n.T(r.Context(), "auth.error.closed")
		case errors.Is(err, store.ErrDuplicateID):
			form.EmailError = i18n.T(r.Context(), "auth.error.taken")
		case err != nil:
			internalError(w, "creating account", err)
			return
		default:
			h.startSession(w, r, signup.OwnerID)
			return
		}
	}

	render(w, r, http.StatusUnprocessableEntity, signupPage(form))
}

func (h handler) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		if err := h.db.DeleteSession(r.Context(), cookie.Value); err != nil {
			internalError(w, "deleting session", err)
			return
		}
	}

	setSessionCookie(w, r, "", time.Time{})
	redirect(w, r, "/login")
}

func (h handler) startSession(w http.ResponseWriter, r *http.Request, userID string) {
	now := time.Now()
	token, err := h.db.CreateSession(r.Context(), userID, now)
	if err != nil {
		internalError(w, "creating session", err)
		return
	}

	setSessionCookie(w, r, token, now.Add(store.SessionTTL))
	redirect(w, r, "/")
}

// requireSession derives the session only from the cookie, refreshes the cookie expiry and sends
// visitors without a live session to /login. The session and its Idioma ride in the request context.
func (h handler) requireSession(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil {
			redirect(w, r, "/login")
			return
		}

		session, err := h.db.SessionByToken(r.Context(), cookie.Value, time.Now())
		if errors.Is(err, store.ErrInvalidSession) {
			redirect(w, r, "/login")
			return
		}

		if err != nil {
			internalError(w, "finding session", err)
			return
		}

		setSessionCookie(w, r, cookie.Value, session.ExpiresAt)
		ctx := context.WithValue(r.Context(), sessionKey{}, session)
		ctx = i18n.WithLanguage(ctx, session.Language)
		next(w, r.WithContext(ctx))
	})
}

// visitor serves a page without a session in the browser's Idioma: en when its preferred language is English, and
// pt-BR otherwise.
func visitor(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		language := i18n.Default
		if strings.HasPrefix(strings.ToLower(preferredLanguage(r.Header.Get("Accept-Language"))), "en") {
			language = "en"
		}

		next(w, r.WithContext(i18n.WithLanguage(r.Context(), language)))
	}
}

// preferredLanguage is the tag of the Accept-Language entry with the highest q weight, the first one on a tie.
func preferredLanguage(header string) string {
	preferred, best := "", 0.0
	for _, entry := range strings.Split(header, ",") {
		tag, params, _ := strings.Cut(entry, ";")
		weight := 1.0
		if q, ok := strings.CutPrefix(strings.TrimSpace(params), "q="); ok {
			weight, _ = strconv.ParseFloat(q, 64)
		}

		if weight > best {
			preferred, best = strings.TrimSpace(tag), weight
		}
	}

	return preferred
}

// sessionFrom is the request's session, or visitorSession on a page without one.
func sessionFrom(ctx context.Context) store.Session {
	if session, ok := ctx.Value(sessionKey{}).(store.Session); ok {
		return session
	}

	return visitorSession
}

// setSessionCookie writes the session cookie, or clears it when token is empty.
func setSessionCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	cookie := &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
	}
	if token == "" {
		cookie.MaxAge = -1
	}

	http.SetCookie(w, cookie)
}

// redirect sends htmx requests a full-page HX-Redirect, since fetch would follow a 303 and swap the
// target page under the old URL, and everyone else a 303.
func redirect(w http.ResponseWriter, r *http.Request, url string) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", url)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	http.Redirect(w, r, url, http.StatusSeeOther)
}

// shell reads what the console chrome shows on every authenticated page: the ticker's balance and the user, at
// now in the user's time zone, and takes the row to flash out of its cookie. title is the i18n key of the screen's
// name, as "nav.settings".
func (h handler) shell(w http.ResponseWriter, r *http.Request, title string) (shell, bool) {
	session, now := sessionFrom(r.Context()), time.Now().In(location(r))
	balance, err := h.balance(r.Context(), session.AccountID, now)
	if err != nil {
		internalError(w, "reading balance", err)
		return shell{}, false
	}

	s := shell{Title: i18n.T(r.Context(), title), Path: r.URL.Path, Email: session.Email, Now: now, Balance: balance}
	if cookie, err := r.Cookie(flashCookie); err == nil {
		s.Flash = cookie.Value
		http.SetCookie(w, &http.Cookie{Name: flashCookie, Path: r.URL.Path, MaxAge: -1})
	}

	return s, true
}

// location is the browser time zone from the tz cookie, or UTC when it is missing or unknown.
func location(r *http.Request) *time.Location {
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

func render(w http.ResponseWriter, r *http.Request, status int, page templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := page.Render(r.Context(), w); err != nil {
		slog.Error("rendering page", "error", err)
	}
}

func internalError(w http.ResponseWriter, msg string, err error) {
	slog.Error(msg, "error", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}
