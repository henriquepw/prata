package web

import (
	"net/http"

	"github.com/henriquepw/prata/server/internal/i18n"
	"github.com/henriquepw/prata/server/internal/store"
)

func (h handler) settings(w http.ResponseWriter, r *http.Request) {
	s, ok := h.shell(w, r, "nav.settings")
	if !ok {
		return
	}

	p := prefsFrom(r.Context())
	render(w, r, http.StatusOK, settingsPage(s, settingsForm{Preferences: p.Preferences, Currency: p.Currency, Errors: map[string]string{}}))
}

// saveSettings stores the Preferences, Idioma included, and the account's Moeda and reloads the page in them, or
// answers 422 with the form and stores nothing when a value is not one of the options.
func (h handler) saveSettings(w http.ResponseWriter, r *http.Request) {
	session := sessionFrom(r.Context())
	form := settingsForm{
		Preferences: store.Preferences{
			Language:     r.PostFormValue("language"),
			DateFormat:   r.PostFormValue("date_format"),
			NumberFormat: r.PostFormValue("number_format"),
		},
		Currency: r.PostFormValue("currency"),
		Errors:   map[string]string{},
	}
	if !isLanguage(form.Language) {
		form.Errors["language"] = i18n.T(r.Context(), "settings.error.language")
	}

	if _, ok := findDateFormat(form.DateFormat); !ok {
		form.Errors["date_format"] = i18n.T(r.Context(), "settings.error.date_format")
	}

	if _, ok := findNumberFormat(form.NumberFormat); !ok {
		form.Errors["number_format"] = i18n.T(r.Context(), "settings.error.number_format")
	}

	if _, ok := findCurrency(form.Currency); !ok {
		form.Errors["currency"] = i18n.T(r.Context(), "settings.error.currency")
	}

	if len(form.Errors) > 0 {
		render(w, r, http.StatusUnprocessableEntity, settingsFields(form))
		return
	}

	if err := h.db.SavePreferences(r.Context(), session.UserID, form.Preferences); err != nil {
		internalError(w, "saving preferences", err)
		return
	}

	if err := h.db.SaveCurrency(r.Context(), session.AccountID, form.Currency); err != nil {
		internalError(w, "saving currency", err)
		return
	}

	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusNoContent)
}
