package web

import (
	"net/http"
	"net/url"

	"github.com/henriquepw/prata/server/internal/i18n"
	"github.com/henriquepw/prata/server/internal/web/components/settingform"
	"github.com/henriquepw/prata/server/internal/web/pages/authpage"
	"github.com/henriquepw/prata/server/internal/web/session"
)

// setupForm shows the configurações iniciais, with the Idioma of the browser and the default formats and Moeda, only
// while the Servidor has no user.
func (h handler) setupForm(w http.ResponseWriter, r *http.Request) {
	if !h.requireEmpty(w, r) {
		return
	}

	form := settingform.Form{
		Language:     i18n.Language(r.Context()),
		DateFormat:   session.Visitor.DateFormat,
		NumberFormat: session.Visitor.NumberFormat,
		Currency:     session.Visitor.Currency,
		Errors:       map[string]string{},
	}
	session.Render(w, r, http.StatusOK, authpage.SetupPage(form))
}

// setup validates the configurações iniciais like the Configurações and, writing nothing, carries them to the
// signup in its URL, or answers 422 with the form when a value is not one of the options.
func (h handler) setup(w http.ResponseWriter, r *http.Request) {
	if !h.requireEmpty(w, r) {
		return
	}

	form := settingform.FromRequest(r)
	form.Validate(r.Context())
	if len(form.Errors) > 0 {
		session.Render(w, r, http.StatusUnprocessableEntity, authpage.SetupPage(form))
		return
	}

	query := url.Values{}
	for name, value := range form.Fields() {
		query.Set(name, value)
	}

	session.Redirect(w, r, "/signup?"+query.Encode())
}

// requireEmpty reports whether the Servidor has no user, sending everyone else to /login.
func (h handler) requireEmpty(w http.ResponseWriter, r *http.Request) bool {
	empty, ok := h.isEmpty(w, r)
	if ok && !empty {
		session.Redirect(w, r, "/login")
	}

	return ok && empty
}
