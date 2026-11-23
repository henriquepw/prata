package web

import (
	"net/http"
	"net/url"

	"github.com/henriquepw/prata/server/internal/i18n"
	"github.com/henriquepw/prata/server/internal/store"
	"github.com/henriquepw/prata/server/internal/web/shared"
)

// setupForm shows the configurações iniciais, with the Idioma of the browser and the default formats and Moeda, only
// while the Servidor has no user.
func (h handler) setupForm(w http.ResponseWriter, r *http.Request) {
	if !h.requireEmpty(w, r) {
		return
	}

	form := settingsForm{
		Preferences: store.Preferences{Language: i18n.Language(r.Context()), DateFormat: shared.VisitorSession.DateFormat, NumberFormat: shared.VisitorSession.NumberFormat},
		Currency:    shared.VisitorSession.Currency,
		Errors:      map[string]string{},
	}
	shared.Render(w, r, http.StatusOK, setupPage(form))
}

// setup validates the configurações iniciais like the Configurações and, writing nothing, carries them to the
// signup in its URL, or answers 422 with the form when a value is not one of the options.
func (h handler) setup(w http.ResponseWriter, r *http.Request) {
	if !h.requireEmpty(w, r) {
		return
	}

	form := choicesFrom(r)
	form.validateChoices(r.Context())
	if len(form.Errors) > 0 {
		shared.Render(w, r, http.StatusUnprocessableEntity, setupPage(form))
		return
	}

	query := url.Values{}
	for name, value := range form.fields() {
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

// choicesFrom reads the configurações iniciais from the request's form or query.
func choicesFrom(r *http.Request) settingsForm {
	return settingsForm{
		Preferences: store.Preferences{Language: r.FormValue("language"), DateFormat: r.FormValue("date_format"), NumberFormat: r.FormValue("number_format")},
		Currency:    r.FormValue("currency"),
		Errors:      map[string]string{},
	}
}

// fields are the choices by field name, nil when there are none.
func (f settingsForm) fields() map[string]string {
	if f.Language == "" {
		return nil
	}

	return map[string]string{"language": f.Language, "date_format": f.DateFormat, "number_format": f.NumberFormat, "currency": f.Currency}
}

// chosen is the configurações iniciais the signup request carries, none when they are missing or not all valid, and
// the request in the chosen Idioma.
func chosen(r *http.Request) (settingsForm, *http.Request) {
	form := choicesFrom(r)
	form.validateChoices(r.Context())
	if len(form.Errors) > 0 {
		return settingsForm{}, r
	}

	return form, r.WithContext(i18n.WithLanguage(r.Context(), form.Language))
}
