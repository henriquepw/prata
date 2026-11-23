package web

import (
	"net/http"
	"net/url"

	"github.com/henriquepw/prata/server/internal/i18n"
	"github.com/henriquepw/prata/server/internal/store"
	"github.com/henriquepw/prata/server/internal/web/pages/authpage"
	"github.com/henriquepw/prata/server/internal/web/pages/settingpage"
	"github.com/henriquepw/prata/server/internal/web/shared"
)

// setupForm shows the configurações iniciais, with the Idioma of the browser and the default formats and Moeda, only
// while the Servidor has no user.
func (h handler) setupForm(w http.ResponseWriter, r *http.Request) {
	if !h.requireEmpty(w, r) {
		return
	}

	form := settingpage.Form{
		Preferences: store.Preferences{Language: i18n.Language(r.Context()), DateFormat: shared.VisitorSession.DateFormat, NumberFormat: shared.VisitorSession.NumberFormat},
		Currency:    shared.VisitorSession.Currency,
		Errors:      map[string]string{},
	}
	shared.Render(w, r, http.StatusOK, authpage.SetupPage(form))
}

// setup validates the configurações iniciais like the Configurações and, writing nothing, carries them to the
// signup in its URL, or answers 422 with the form when a value is not one of the options.
func (h handler) setup(w http.ResponseWriter, r *http.Request) {
	if !h.requireEmpty(w, r) {
		return
	}

	form := settingpage.FromRequest(r)
	form.Validate(r.Context())
	if len(form.Errors) > 0 {
		shared.Render(w, r, http.StatusUnprocessableEntity, authpage.SetupPage(form))
		return
	}

	query := url.Values{}
	for name, value := range form.Fields() {
		query.Set(name, value)
	}

	shared.Redirect(w, r, "/signup?"+query.Encode())
}

// requireEmpty reports whether the Servidor has no user, sending everyone else to /login.
func (h handler) requireEmpty(w http.ResponseWriter, r *http.Request) bool {
	empty, ok := h.isEmpty(w, r)
	if ok && !empty {
		shared.Redirect(w, r, "/login")
	}

	return ok && empty
}
