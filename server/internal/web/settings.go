package web

import (
	"net/http"

	"github.com/henriquepw/prata/server/internal/store"
)

func (h handler) settings(w http.ResponseWriter, r *http.Request) {
	s, ok := h.shell(w, r, "Configurações")
	if !ok {
		return
	}

	render(w, r, http.StatusOK, settingsPage(s, settingsForm{Preferences: prefsFrom(r.Context()).Preferences, Errors: map[string]string{}}))
}

// saveSettings stores the Preferences and reloads the page in them, or answers 422 with the form and stores nothing
// when a value is not one of the options.
func (h handler) saveSettings(w http.ResponseWriter, r *http.Request) {
	session := sessionFrom(r.Context())
	form := settingsForm{
		Preferences: store.Preferences{
			Language:     session.Language,
			DateFormat:   r.PostFormValue("date_format"),
			NumberFormat: r.PostFormValue("number_format"),
		},
		Errors: map[string]string{},
	}
	if _, ok := findDateFormat(form.DateFormat); !ok {
		form.Errors["date_format"] = "Escolha um formato de data."
	}

	if _, ok := findNumberFormat(form.NumberFormat); !ok {
		form.Errors["number_format"] = "Escolha um formato numérico."
	}

	if len(form.Errors) > 0 {
		render(w, r, http.StatusUnprocessableEntity, settingsFields(form))
		return
	}

	if err := h.db.SavePreferences(r.Context(), session.UserID, form.Preferences); err != nil {
		internalError(w, "saving preferences", err)
		return
	}

	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusNoContent)
}
