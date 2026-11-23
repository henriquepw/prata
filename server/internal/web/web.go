// Package web serves the server-rendered web app: session, pages and static assets.
package web

import (
	"embed"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/a-h/templ"
	"github.com/henriquepw/prata/server/internal/i18n"
	"github.com/henriquepw/prata/server/internal/pluggy"
	"github.com/henriquepw/prata/server/internal/store"
	"github.com/henriquepw/prata/server/internal/web/components/shell"
	"github.com/henriquepw/prata/server/internal/web/pages/homepage"
	"github.com/henriquepw/prata/server/internal/web/shared"
	"github.com/oklog/ulid/v2"
)

//go:generate go tool templ generate -path ../..
//go:generate tailwindcss -i static/globals.css -o static/app.css --minify

//go:embed static/app.css static/htmx.min.js static/fonts/GeistMono-Variable.woff2
var static embed.FS

const sessionCookie = "session"

type handler struct {
	db              *store.Store
	isSignupAllowed bool
	openFinance     OpenFinance
	home            homepage.Page
}

// OpenFinance is the instance's Open Finance setup: Key encrypts the users' Pluggy credentials, and an empty Key
// turns the feature off.
type OpenFinance struct {
	Key    string
	Pluggy pluggy.Client
}

// Enabled reports whether Open Finance is on, that is, whether the instance has a Key.
func (o OpenFinance) Enabled() bool {
	return o.Key != ""
}

// New returns the web routes behind cross-origin protection. Every route that needs a logged-in user must be
// wrapped in requireSession, and every page without a session in visitor.
func New(db *store.Store, isSignupAllowed bool, openFinance OpenFinance) http.Handler {
	h := handler{db: db, isSignupAllowed: isSignupAllowed, openFinance: openFinance}
	h.home = homepage.New(db, h.shell)
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.FileServerFS(static))
	mux.HandleFunc("GET /login", visitor(h.loginForm))
	mux.HandleFunc("POST /login", visitor(h.login))
	mux.HandleFunc("GET /signup", visitor(h.signupForm))
	mux.HandleFunc("POST /signup", visitor(h.signup))
	mux.HandleFunc("GET /setup", visitor(h.setupForm))
	mux.HandleFunc("POST /setup", visitor(h.setup))
	mux.HandleFunc("POST /logout", h.logout)
	mux.HandleFunc("GET /", visitor(func(w http.ResponseWriter, r *http.Request) {
		shared.Render(w, r, http.StatusNotFound, notFoundPage(r.URL.Path))
	}))
	mux.Handle("GET /{$}", h.requireSession(h.home.Home))
	mux.Handle("GET /welcome", h.requireSession(h.welcome))
	mux.Handle("POST /welcome", h.requireSession(h.saveWelcome))
	mux.Handle("GET /launch", h.requireSession(h.home.LaunchPreview))
	mux.Handle("POST /launch", h.requireSession(h.home.Launch))
	mux.Handle("GET /transactions", h.requireSession(h.transactions))
	mux.Handle("GET /transactions/new", h.requireSession(h.newTransaction))
	mux.Handle("GET /transactions/{id}", h.requireSession(h.editTransaction))
	mux.Handle("POST /transactions", h.requireSession(h.saveTransaction))
	mux.Handle("POST /transactions/{id}", h.requireSession(h.saveTransaction))
	mux.Handle("DELETE /transactions/{id}", h.requireSession(h.deleteTransaction))
	mux.Handle("GET /categories", h.requireSession(h.categories))
	mux.Handle("GET /categories/new", h.requireSession(func(w http.ResponseWriter, r *http.Request) {
		shared.Render(w, r, http.StatusOK, categorySheet(categoryForm{Color: "GRAY"}))
	}))
	mux.Handle("GET /categories/{id}", h.requireSession(h.editCategory))
	mux.Handle("POST /categories", h.requireSession(h.saveCategory))
	mux.Handle("POST /categories/{id}", h.requireSession(h.saveCategory))
	mux.Handle("DELETE /categories/{id}", h.requireSession(h.deleteCategory))
	mux.Handle("POST /budget", h.requireSession(h.saveBudget))
	mux.Handle("GET /recurrences", h.requireSession(h.recurrences))
	mux.Handle("GET /recurrences/new", h.requireSession(h.newRecurrence))
	mux.Handle("GET /recurrences/{id}", h.requireSession(h.editRecurrence))
	mux.Handle("POST /recurrences", h.requireSession(h.saveRecurrence))
	mux.Handle("POST /recurrences/{id}", h.requireSession(h.saveRecurrence))
	mux.Handle("DELETE /recurrences/{id}", h.requireSession(h.deleteRecurrence))
	mux.Handle("GET /recurrences/{id}/occurrences/{date}", h.requireSession(h.occurrenceSheet))
	mux.Handle("POST /recurrences/{id}/occurrences/{date}", h.requireSession(h.confirmOccurrence))
	mux.Handle("GET /tags", h.requireSession(h.tags))
	mux.Handle("GET /tags/new", h.requireSession(func(w http.ResponseWriter, r *http.Request) {
		shared.Render(w, r, http.StatusOK, tagSheet(tagForm{}))
	}))
	mux.Handle("GET /tags/{id}", h.requireSession(h.editTag))
	mux.Handle("POST /tags", h.requireSession(h.saveTag))
	mux.Handle("POST /tags/{id}", h.requireSession(h.saveTag))
	mux.Handle("DELETE /tags/{id}", h.requireSession(h.deleteTag))
	mux.Handle("GET /movements", h.requireSession(h.movements))
	mux.Handle("POST /movements/import", h.requireSession(h.importMovements))
	mux.Handle("POST /movements/confirm", h.requireSession(h.confirmMovements))
	mux.Handle("POST /movements/ignore", h.requireSession(h.ignoreMovements))
	mux.Handle("POST /movements/{id}/confirm", h.requireSession(h.confirmMovement))
	mux.Handle("POST /movements/{id}/occurrence", h.requireSession(h.confirmMovementAsOccurrence))
	mux.Handle("POST /movements/{id}/ignore", h.requireSession(h.ignoreMovement))
	mux.Handle("GET /connections", h.requireSession(h.connections))
	mux.Handle("POST /connections", h.requireSession(h.saveConnection))
	mux.Handle("POST /connections/fetch", h.requireSession(h.fetchConnections))
	mux.Handle("DELETE /connections/{id}", h.requireSession(h.deleteConnection))
	mux.Handle("GET /devices", h.requireSession(h.devices))
	mux.Handle("DELETE /devices/{id}", h.requireSession(h.revokeDevice))
	mux.Handle("GET /settings", h.requireSession(h.settings))
	mux.Handle("POST /settings", h.requireSession(h.saveSettings))
	mux.Handle("DELETE /settings/pluggy", h.requireSession(h.deletePluggyCredentials))

	return http.NewCrossOriginProtection().Handler(mux)
}

// loginForm shows the login, or sends the request to /setup while the Servidor has no user.
func (h handler) loginForm(w http.ResponseWriter, r *http.Request) {
	empty, ok := h.isEmpty(w, r)
	if !ok {
		return
	}

	if empty {
		shared.Redirect(w, r, "/setup")
		return
	}

	h.renderEntrance(w, r, http.StatusOK, false, authForm{}, loginPage)
}

// isEmpty reports whether the Servidor has no user; ok is false once the failure was answered.
func (h handler) isEmpty(w http.ResponseWriter, r *http.Request) (empty, ok bool) {
	empty, err := h.db.IsEmpty(r.Context())
	if err != nil {
		shared.InternalError(w, "checking users", err)
		return false, false
	}

	return empty, true
}

// signupForm shows the signup in the Idioma of the configurações iniciais it carries. With no user, it needs them and
// otherwise goes back to /setup; with users, it goes to /login when signup is closed.
func (h handler) signupForm(w http.ResponseWriter, r *http.Request) {
	empty, ok := h.isEmpty(w, r)
	if !ok {
		return
	}

	choices, r := chosen(r)
	if empty && choices.Language == "" {
		shared.Redirect(w, r, "/setup")
		return
	}

	if !empty && !h.isSignupAllowed {
		shared.Redirect(w, r, "/login")
		return
	}

	h.renderEntrance(w, r, http.StatusOK, empty, authForm{Choices: choices.fields()}, signupPage)
}

// renderEntrance renders a login or signup page with the links that depend on whether the Servidor has users
// (empty) and accepts signup.
func (h handler) renderEntrance(w http.ResponseWriter, r *http.Request, status int, empty bool, form authForm, page func(authForm) templ.Component) {
	form.CanSignUp = empty || h.isSignupAllowed
	form.HasUsers = !empty
	shared.Render(w, r, status, page(form))
}

func (h handler) login(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.PostFormValue("email"))
	userID, err := h.db.Authenticate(r.Context(), email, r.PostFormValue("password"))
	if errors.Is(err, store.ErrInvalidCredentials) {
		empty, ok := h.isEmpty(w, r)
		if !ok {
			return
		}

		h.renderEntrance(w, r, http.StatusUnprocessableEntity, empty, authForm{Email: email, PasswordError: i18n.T(r.Context(), "auth.error.credentials")}, loginPage)
		return
	}

	if err != nil {
		shared.InternalError(w, "authenticating", err)
		return
	}

	h.startSession(w, r, userID, "/")
}

func (h handler) signup(w http.ResponseWriter, r *http.Request) {
	choices, r := chosen(r)
	email := strings.TrimSpace(r.PostFormValue("email"))
	password := r.PostFormValue("password")
	form := authForm{Email: email, Choices: choices.fields()}
	if !strings.Contains(email, "@") {
		form.EmailError = i18n.T(r.Context(), "auth.error.email")
	}

	if password == "" {
		form.PasswordError = i18n.T(r.Context(), "auth.error.password")
	}

	if form.EmailError == "" && form.PasswordError == "" {
		signup := store.Signup{Email: email, Password: password, AccountID: ulid.Make().String(), OwnerID: ulid.Make().String(), Language: i18n.Language(r.Context()), DateFormat: choices.DateFormat, NumberFormat: choices.NumberFormat, Currency: choices.Currency}
		_, err := h.db.CreateAccount(r.Context(), signup, h.isSignupAllowed)
		switch {
		case errors.Is(err, store.ErrSignupClosed):
			form.Error = i18n.T(r.Context(), "auth.error.closed")
		case errors.Is(err, store.ErrDuplicateID):
			form.EmailError = i18n.T(r.Context(), "auth.error.taken")
		case err != nil:
			shared.InternalError(w, "creating account", err)
			return
		default:
			h.startSession(w, r, signup.OwnerID, "/welcome")
			return
		}
	}

	empty, ok := h.isEmpty(w, r)
	if !ok {
		return
	}

	h.renderEntrance(w, r, http.StatusUnprocessableEntity, empty, form, signupPage)
}

func (h handler) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		if err := h.db.DeleteSession(r.Context(), cookie.Value); err != nil {
			shared.InternalError(w, "deleting session", err)
			return
		}
	}

	setSessionCookie(w, r, "", time.Time{})
	shared.Redirect(w, r, "/login")
}

// startSession opens the user's session and redirects to the page they land on.
func (h handler) startSession(w http.ResponseWriter, r *http.Request, userID, landing string) {
	now := time.Now()
	token, err := h.db.CreateSession(r.Context(), userID, now)
	if err != nil {
		shared.InternalError(w, "creating session", err)
		return
	}

	setSessionCookie(w, r, token, now.Add(store.SessionTTL))
	shared.Redirect(w, r, landing)
}

// requireSession derives the session only from the cookie, refreshes the cookie expiry and sends
// visitors without a live session to /setup or /login, per redirectToEntrance. The session and its Idioma ride in the request context.
func (h handler) requireSession(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil {
			h.redirectToEntrance(w, r)
			return
		}

		session, err := h.db.SessionByToken(r.Context(), cookie.Value, time.Now())
		if errors.Is(err, store.ErrInvalidSession) {
			h.redirectToEntrance(w, r)
			return
		}

		if err != nil {
			shared.InternalError(w, "finding session", err)
			return
		}

		setSessionCookie(w, r, cookie.Value, session.ExpiresAt)
		ctx := shared.WithSession(r.Context(), session)
		ctx = i18n.WithLanguage(ctx, session.Language)
		next(w, r.WithContext(ctx))
	})
}

// redirectToEntrance sends a visitor to /setup while the Servidor has no user, and to /login after.
func (h handler) redirectToEntrance(w http.ResponseWriter, r *http.Request) {
	empty, ok := h.isEmpty(w, r)
	if !ok {
		return
	}

	if empty {
		shared.Redirect(w, r, "/setup")
		return
	}

	shared.Redirect(w, r, "/login")
}

// visitor serves a page without a session in the browser's Idioma: en when its preferred language is English, and
// pt-BR otherwise.
func visitor(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		language := i18n.PortugueseBR
		if strings.HasPrefix(strings.ToLower(preferredLanguage(r.Header.Get("Accept-Language"))), i18n.English) {
			language = i18n.English
		}

		next(w, r.WithContext(i18n.WithLanguage(r.Context(), language)))
	}
}

// preferredLanguage is the tag of the Accept-Language entry with the highest q weight, the first one on a tie.
func preferredLanguage(header string) string {
	preferred, best := "", 0.0
	for entry := range strings.SplitSeq(header, ",") {
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

// shell reads what the console chrome shows on every authenticated page: the ticker's balance and the user, at
// now in the user's time zone, and takes the row to flash out of its cookie. title is the i18n key of the screen's
// name, as "nav.devices".
func (h handler) shell(w http.ResponseWriter, r *http.Request, title string) (shell.Shell, bool) {
	session, now := shared.SessionFrom(r.Context()), time.Now().In(shared.Location(r))
	balance, err := shell.ReadBalance(r.Context(), h.db, session.AccountID, now)
	if err != nil {
		shared.InternalError(w, "reading balance", err)
		return shell.Shell{}, false
	}

	s := shell.Shell{Title: i18n.T(r.Context(), title), Path: r.URL.Path, Email: session.Email, Now: now, Balance: balance, Connections: h.openFinance.Enabled()}
	if cookie, err := r.Cookie(shared.FlashCookie); err == nil {
		s.Flash = cookie.Value
		http.SetCookie(w, &http.Cookie{Name: shared.FlashCookie, Path: r.URL.Path, MaxAge: -1})
	}

	return s, true
}
