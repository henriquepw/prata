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
	"github.com/henriquepw/prata/server/internal/store"
	"github.com/henriquepw/prata/server/internal/web/components/settingform"
	"github.com/henriquepw/prata/server/internal/web/components/shell"
	"github.com/henriquepw/prata/server/internal/web/instance"
	"github.com/henriquepw/prata/server/internal/web/pages/authpage"
	"github.com/henriquepw/prata/server/internal/web/pages/categorypage"
	"github.com/henriquepw/prata/server/internal/web/pages/connectionpage"
	"github.com/henriquepw/prata/server/internal/web/pages/devicepage"
	"github.com/henriquepw/prata/server/internal/web/pages/homepage"
	"github.com/henriquepw/prata/server/internal/web/pages/movementpage"
	"github.com/henriquepw/prata/server/internal/web/pages/recurrencepage"
	"github.com/henriquepw/prata/server/internal/web/pages/settingpage"
	"github.com/henriquepw/prata/server/internal/web/pages/tagpage"
	"github.com/henriquepw/prata/server/internal/web/pages/transactionpage"
	"github.com/henriquepw/prata/server/internal/web/pages/welcomepage"
	"github.com/henriquepw/prata/server/internal/web/session"
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
	categories      categorypage.Page
	tags            tagpage.Page
	transactions    transactionpage.Page
	recurrences     recurrencepage.Page
	movements       movementpage.Page
	welcome         welcomepage.Page
	connections     connectionpage.Page
	devices         devicepage.Page
	settings        settingpage.Page
}

// OpenFinance is the instance's Open Finance setup: Key encrypts the users' Pluggy credentials, and an empty Key
// turns the feature off.
type OpenFinance = instance.OpenFinance

// New returns the web routes behind cross-origin protection. Every route that needs a logged-in user must be
// wrapped in requireSession, and every page without a session in visitor.
func New(db *store.Store, isSignupAllowed bool, openFinance OpenFinance) http.Handler {
	h := handler{db: db, isSignupAllowed: isSignupAllowed, openFinance: openFinance}
	h.home = homepage.New(db, h.shell)
	h.categories = categorypage.New(db, h.shell)
	h.tags = tagpage.New(db, h.shell)
	h.transactions = transactionpage.New(db)
	h.recurrences = recurrencepage.New(db, h.shell, h.home)
	h.movements = movementpage.New(db, h.shell)
	h.welcome = welcomepage.New(db)
	h.connections = connectionpage.New(db, h.shell, openFinance)
	h.devices = devicepage.New(db, h.shell)
	h.settings = settingpage.New(db, openFinance)
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
		session.Render(w, r, http.StatusNotFound, authpage.NotFoundPage(r.URL.Path))
	}))
	mux.Handle("GET /{$}", h.requireSession(h.home.Home))
	mux.Handle("GET /welcome", h.requireSession(h.welcome.Welcome))
	mux.Handle("POST /welcome", h.requireSession(h.welcome.Save))
	mux.Handle("GET /launch", h.requireSession(h.home.LaunchPreview))
	mux.Handle("POST /launch", h.requireSession(h.home.Launch))
	mux.Handle("GET /transactions", h.requireSession(h.transactions.Redirect))
	mux.Handle("GET /transactions/new", h.requireSession(h.transactions.NewSheet))
	mux.Handle("GET /transactions/{id}", h.requireSession(h.transactions.Edit))
	mux.Handle("POST /transactions", h.requireSession(h.transactions.Save))
	mux.Handle("POST /transactions/{id}", h.requireSession(h.transactions.Save))
	mux.Handle("DELETE /transactions/{id}", h.requireSession(h.transactions.Delete))
	mux.Handle("GET /categories", h.requireSession(h.categories.Categories))
	mux.Handle("GET /categories/new", h.requireSession(h.categories.NewSheet))
	mux.Handle("GET /categories/{id}", h.requireSession(h.categories.Edit))
	mux.Handle("POST /categories", h.requireSession(h.categories.Save))
	mux.Handle("POST /categories/{id}", h.requireSession(h.categories.Save))
	mux.Handle("DELETE /categories/{id}", h.requireSession(h.categories.Delete))
	mux.Handle("POST /budget", h.requireSession(h.categories.SaveBudget))
	mux.Handle("GET /recurrences", h.requireSession(h.recurrences.Recurrences))
	mux.Handle("GET /recurrences/new", h.requireSession(h.recurrences.NewSheet))
	mux.Handle("GET /recurrences/{id}", h.requireSession(h.recurrences.Edit))
	mux.Handle("POST /recurrences", h.requireSession(h.recurrences.Save))
	mux.Handle("POST /recurrences/{id}", h.requireSession(h.recurrences.Save))
	mux.Handle("DELETE /recurrences/{id}", h.requireSession(h.recurrences.Delete))
	mux.Handle("GET /recurrences/{id}/occurrences/{date}", h.requireSession(h.recurrences.OccurrenceSheet))
	mux.Handle("POST /recurrences/{id}/occurrences/{date}", h.requireSession(h.recurrences.ConfirmOccurrence))
	mux.Handle("GET /tags", h.requireSession(h.tags.Tags))
	mux.Handle("GET /tags/new", h.requireSession(h.tags.NewSheet))
	mux.Handle("GET /tags/{id}", h.requireSession(h.tags.Edit))
	mux.Handle("POST /tags", h.requireSession(h.tags.Save))
	mux.Handle("POST /tags/{id}", h.requireSession(h.tags.Save))
	mux.Handle("DELETE /tags/{id}", h.requireSession(h.tags.Delete))
	mux.Handle("GET /movements", h.requireSession(h.movements.Movements))
	mux.Handle("POST /movements/import", h.requireSession(h.movements.Import))
	mux.Handle("POST /movements/confirm", h.requireSession(h.movements.ConfirmAll))
	mux.Handle("POST /movements/ignore", h.requireSession(h.movements.IgnoreAll))
	mux.Handle("POST /movements/{id}/confirm", h.requireSession(h.movements.Confirm))
	mux.Handle("POST /movements/{id}/occurrence", h.requireSession(h.movements.ConfirmAsOccurrence))
	mux.Handle("POST /movements/{id}/ignore", h.requireSession(h.movements.Ignore))
	mux.Handle("GET /connections", h.requireSession(h.connections.Connections))
	mux.Handle("POST /connections", h.requireSession(h.connections.Save))
	mux.Handle("POST /connections/fetch", h.requireSession(h.connections.Fetch))
	mux.Handle("DELETE /connections/{id}", h.requireSession(h.connections.Delete))
	mux.Handle("GET /devices", h.requireSession(h.devices.Devices))
	mux.Handle("DELETE /devices/{id}", h.requireSession(h.devices.Revoke))
	mux.Handle("GET /settings", h.requireSession(h.settings.Settings))
	mux.Handle("POST /settings", h.requireSession(h.settings.Save))
	mux.Handle("DELETE /settings/pluggy", h.requireSession(h.settings.DeletePluggy))

	return http.NewCrossOriginProtection().Handler(mux)
}

// loginForm shows the login, or sends the request to /setup while the Servidor has no user.
func (h handler) loginForm(w http.ResponseWriter, r *http.Request) {
	empty, ok := h.isEmpty(w, r)
	if !ok {
		return
	}

	if empty {
		session.Redirect(w, r, "/setup")
		return
	}

	h.renderEntrance(w, r, http.StatusOK, false, authpage.Form{}, authpage.LoginPage)
}

// isEmpty reports whether the Servidor has no user; ok is false once the failure was answered.
func (h handler) isEmpty(w http.ResponseWriter, r *http.Request) (empty, ok bool) {
	empty, err := h.db.IsEmpty(r.Context())
	if err != nil {
		session.InternalError(w, "checking users", err)
		return false, false
	}

	return empty, true
}

// signupForm shows the signup in the language of the initial settings it carries. With no user, it needs them and
// otherwise goes back to /setup; with users, it goes to /login when signup is closed.
func (h handler) signupForm(w http.ResponseWriter, r *http.Request) {
	empty, ok := h.isEmpty(w, r)
	if !ok {
		return
	}

	choices, r := settingform.Chosen(r)
	if empty && choices.Language == "" {
		session.Redirect(w, r, "/setup")
		return
	}

	if !empty && !h.isSignupAllowed {
		session.Redirect(w, r, "/login")
		return
	}

	h.renderEntrance(w, r, http.StatusOK, empty, authpage.Form{Choices: choices.Fields()}, authpage.SignupPage)
}

// renderEntrance renders a login or signup page with the links that depend on whether the Servidor has users
// (empty) and accepts signup.
func (h handler) renderEntrance(w http.ResponseWriter, r *http.Request, status int, empty bool, fm authpage.Form, page func(authpage.Form) templ.Component) {
	fm.CanSignUp = empty || h.isSignupAllowed
	fm.HasUsers = !empty
	session.Render(w, r, status, page(fm))
}

func (h handler) login(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.PostFormValue("email"))
	userID, err := h.db.Authenticate(r.Context(), email, r.PostFormValue("password"))
	if errors.Is(err, store.ErrInvalidCredentials) {
		empty, ok := h.isEmpty(w, r)
		if !ok {
			return
		}

		h.renderEntrance(w, r, http.StatusUnprocessableEntity, empty, authpage.Form{Email: email, PasswordError: i18n.T(r.Context(), "auth.error.credentials")}, authpage.LoginPage)
		return
	}

	if err != nil {
		session.InternalError(w, "authenticating", err)
		return
	}

	h.startSession(w, r, userID, "/")
}

func (h handler) signup(w http.ResponseWriter, r *http.Request) {
	choices, r := settingform.Chosen(r)
	email := strings.TrimSpace(r.PostFormValue("email"))
	password := r.PostFormValue("password")
	fm := authpage.Form{Email: email, Choices: choices.Fields()}
	if !strings.Contains(email, "@") {
		fm.EmailError = i18n.T(r.Context(), "auth.error.email")
	}

	if password == "" {
		fm.PasswordError = i18n.T(r.Context(), "auth.error.password")
	}

	if fm.EmailError == "" && fm.PasswordError == "" {
		signup := store.Signup{Email: email, Password: password, AccountID: ulid.Make().String(), OwnerID: ulid.Make().String(), Language: i18n.Language(r.Context()), DateFormat: choices.DateFormat, NumberFormat: choices.NumberFormat, Currency: choices.Currency}
		_, err := h.db.CreateAccount(r.Context(), signup, h.isSignupAllowed)
		switch {
		case errors.Is(err, store.ErrSignupClosed):
			fm.Error = i18n.T(r.Context(), "auth.error.closed")
		case errors.Is(err, store.ErrDuplicateID):
			fm.EmailError = i18n.T(r.Context(), "auth.error.taken")
		case err != nil:
			session.InternalError(w, "creating account", err)
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

	h.renderEntrance(w, r, http.StatusUnprocessableEntity, empty, fm, authpage.SignupPage)
}

func (h handler) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		if err := h.db.DeleteSession(r.Context(), cookie.Value); err != nil {
			session.InternalError(w, "deleting session", err)
			return
		}
	}

	setSessionCookie(w, r, "", time.Time{})
	session.Redirect(w, r, "/login")
}

// startSession opens the user's session and redirects to the page they land on.
func (h handler) startSession(w http.ResponseWriter, r *http.Request, userID, landing string) {
	now := time.Now()
	token, err := h.db.CreateSession(r.Context(), userID, now)
	if err != nil {
		session.InternalError(w, "creating session", err)
		return
	}

	setSessionCookie(w, r, token, now.Add(store.SessionTTL))
	session.Redirect(w, r, landing)
}

// requireSession derives the session only from the cookie, refreshes the cookie expiry and sends
// visitors without a live session to /setup or /login, per redirectToEntrance. The session and its language ride in the request context.
func (h handler) requireSession(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil {
			h.redirectToEntrance(w, r)
			return
		}

		sess, err := h.db.SessionByToken(r.Context(), cookie.Value, time.Now())
		if errors.Is(err, store.ErrInvalidSession) {
			h.redirectToEntrance(w, r)
			return
		}

		if err != nil {
			session.InternalError(w, "finding session", err)
			return
		}

		setSessionCookie(w, r, cookie.Value, sess.ExpiresAt)
		ctx := session.With(r.Context(), sess)
		ctx = i18n.WithLanguage(ctx, sess.Language)
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
		session.Redirect(w, r, "/setup")
		return
	}

	session.Redirect(w, r, "/login")
}

// visitor serves a page without a session in the browser's language: en when its preferred language is English, and
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
	sess, now := session.From(r.Context()), time.Now().In(session.Location(r))
	balance, err := shell.ReadBalance(r.Context(), h.db, sess.AccountID, now)
	if err != nil {
		session.InternalError(w, "reading balance", err)
		return shell.Shell{}, false
	}

	s := shell.Shell{Title: i18n.T(r.Context(), title), Path: r.URL.Path, Email: sess.Email, Now: now, Balance: balance, Connections: h.openFinance.Enabled()}
	if cookie, err := r.Cookie(session.FlashCookie); err == nil {
		s.Flash = cookie.Value
		http.SetCookie(w, &http.Cookie{Name: session.FlashCookie, Path: r.URL.Path, MaxAge: -1})
	}

	return s, true
}
