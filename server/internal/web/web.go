// Package web serves the server-rendered web app: session, pages and static assets.
package web

import (
	"context"
	"embed"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/a-h/templ"
	"github.com/henriquepw/prata/server/internal/store"
	"github.com/oklog/ulid/v2"
)

//go:generate go tool templ generate -path ../..
//go:generate tailwindcss -i static/globals.css -o static/app.css --minify

//go:embed static/app.css static/htmx.min.js
var static embed.FS

const sessionCookie = "session"

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
	mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) { render(w, r, http.StatusOK, loginPage(authForm{})) })
	mux.HandleFunc("POST /login", h.login)
	mux.HandleFunc("GET /signup", func(w http.ResponseWriter, r *http.Request) { render(w, r, http.StatusOK, signupPage(authForm{})) })
	mux.HandleFunc("POST /signup", h.signup)
	mux.HandleFunc("POST /logout", h.logout)
	mux.Handle("GET /{$}", h.requireSession(func(w http.ResponseWriter, r *http.Request) {
		render(w, r, http.StatusOK, homePage(newShell(r, "Visão geral")))
	}))
	mux.Handle("GET /categories", h.requireSession(h.categories))
	mux.Handle("GET /categories/new", h.requireSession(func(w http.ResponseWriter, r *http.Request) {
		render(w, r, http.StatusOK, categorySheet(categoryForm{Color: "GRAY"}))
	}))
	mux.Handle("GET /categories/{id}", h.requireSession(h.editCategory))
	mux.Handle("POST /categories", h.requireSession(h.saveCategory))
	mux.Handle("POST /categories/{id}", h.requireSession(h.saveCategory))
	mux.Handle("DELETE /categories/{id}", h.requireSession(h.deleteCategory))
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

	return http.NewCrossOriginProtection().Handler(mux)
}

func (h handler) login(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.PostFormValue("email"))
	userID, err := h.db.Authenticate(r.Context(), email, r.PostFormValue("password"))
	if errors.Is(err, store.ErrInvalidCredentials) {
		render(w, r, http.StatusUnprocessableEntity, loginPage(authForm{Email: email, Error: "E-mail ou senha inválidos."}))
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
	fail := func(message string) {
		render(w, r, http.StatusUnprocessableEntity, signupPage(authForm{Email: email, Error: message}))
	}
	if !strings.Contains(email, "@") || password == "" {
		fail("Informe um e-mail e uma senha.")
		return
	}

	signup := store.Signup{Email: email, Password: password, AccountID: ulid.Make().String(), OwnerID: ulid.Make().String()}
	_, err := h.db.CreateAccount(r.Context(), signup, h.isSignupAllowed)
	switch {
	case errors.Is(err, store.ErrSignupClosed):
		fail("Cadastro fechado nesta instalação.")
	case errors.Is(err, store.ErrDuplicateID):
		fail("Este e-mail já tem conta.")
	case err != nil:
		internalError(w, "creating account", err)
	default:
		h.startSession(w, r, signup.OwnerID)
	}
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
// visitors without a live session to /login.
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
		next(w, r.WithContext(context.WithValue(r.Context(), sessionKey{}, session)))
	})
}

func sessionFrom(ctx context.Context) store.Session {
	return ctx.Value(sessionKey{}).(store.Session)
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

func newShell(r *http.Request, title string) shell {
	theme := "system"
	if cookie, err := r.Cookie("theme"); err == nil {
		theme = cookie.Value
	}

	return shell{Title: title, Path: r.URL.Path, Theme: theme}
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
