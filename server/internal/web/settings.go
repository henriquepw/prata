package web

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/henriquepw/prata/server/internal/i18n"
	"github.com/henriquepw/prata/server/internal/pluggy"
	"github.com/henriquepw/prata/server/internal/store"
)

func (h handler) settings(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("HX-Request") != "true" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	h.renderSettings(w, r)
}

// renderSettings answers the Configurações sheet with the saved values.
func (h handler) renderSettings(w http.ResponseWriter, r *http.Request) {
	session := sessionFrom(r.Context())
	form := settingsForm{Preferences: session.Preferences, Currency: session.Currency, Errors: map[string]string{}}
	if err := h.withPluggy(r.Context(), &form); err != nil {
		internalError(w, "reading pluggy credentials", err)
		return
	}

	render(w, r, http.StatusOK, settingsSheet(form))
}

// withPluggy turns the form's Pluggy fields on when Open Finance is, marking whether the user has credentials saved
// and showing their client id when none was typed.
func (h handler) withPluggy(ctx context.Context, form *settingsForm) error {
	if h.openFinance.Key == "" {
		return nil
	}

	form.OpenFinance = true
	credentials, err := h.db.PluggyCredentials(ctx, h.openFinance.Key, sessionFrom(ctx).UserID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}

	if err != nil {
		return err
	}

	form.HasPluggy = true
	if form.PluggyClientID == "" {
		form.PluggyClientID = credentials.ClientID
	}

	return nil
}

// saveSettings stores the Preferences, Idioma included, the account's Moeda and, when a secret is typed, the
// Pluggy credentials after Pluggy accepts them, and reloads the page in them, or answers 422 with the form and
// stores nothing when a value is not one of the options or Pluggy refuses the credentials.
func (h handler) saveSettings(w http.ResponseWriter, r *http.Request) {
	session := sessionFrom(r.Context())
	form := settingsForm{
		Preferences: store.Preferences{
			Language:       r.PostFormValue("language"),
			DateFormat:     r.PostFormValue("date_format"),
			NumberFormat:   r.PostFormValue("number_format"),
			DailyLimitMode: r.PostFormValue("daily_limit_mode"),
		},
		Currency:       r.PostFormValue("currency"),
		PluggyClientID: strings.TrimSpace(r.PostFormValue("pluggy_client_id")),
		Errors:         map[string]string{},
	}
	if err := h.withPluggy(r.Context(), &form); err != nil {
		internalError(w, "reading pluggy credentials", err)
		return
	}

	if _, ok := find(languages, form.Language); !ok {
		form.Errors["language"] = i18n.T(r.Context(), "settings.error.language")
	}

	if _, ok := find(dateFormats, form.DateFormat); !ok {
		form.Errors["date_format"] = i18n.T(r.Context(), "settings.error.date_format")
	}

	if _, ok := find(numberFormats, form.NumberFormat); !ok {
		form.Errors["number_format"] = i18n.T(r.Context(), "settings.error.number_format")
	}

	if _, ok := find(currencies, form.Currency); !ok {
		form.Errors["currency"] = i18n.T(r.Context(), "settings.error.currency")
	}

	if _, ok := find(dailyLimitModes, form.DailyLimitMode); !ok {
		form.Errors["daily_limit_mode"] = i18n.T(r.Context(), "settings.error.daily_limit_mode")
	}

	credentials := store.PluggyCredentials{ClientID: form.PluggyClientID, ClientSecret: strings.TrimSpace(r.PostFormValue("pluggy_client_secret"))}
	isSavingPluggy := form.OpenFinance && credentials.ClientSecret != ""
	if isSavingPluggy && credentials.ClientID == "" {
		form.Errors["pluggy_client_id"] = i18n.T(r.Context(), "settings.error.pluggy_client_id")
	}

	if isSavingPluggy && len(form.Errors) == 0 {
		_, err := h.openFinance.Pluggy.Authenticate(r.Context(), credentials.ClientID, credentials.ClientSecret)
		if errors.Is(err, pluggy.ErrInvalidCredentials) {
			form.Errors["pluggy_client_secret"] = i18n.T(r.Context(), "settings.error.pluggy_rejected")
		} else if err != nil {
			internalError(w, "authenticating on pluggy", err)
			return
		}
	}

	if len(form.Errors) > 0 {
		render(w, r, http.StatusUnprocessableEntity, settingsSheet(form))
		return
	}

	if isSavingPluggy {
		if err := h.db.SavePluggyCredentials(r.Context(), h.openFinance.Key, session.UserID, credentials); err != nil {
			internalError(w, "saving pluggy credentials", err)
			return
		}
	}

	if err := h.db.SaveSettings(r.Context(), session.UserID, session.AccountID, form.Preferences, form.Currency); err != nil {
		internalError(w, "saving settings", err)
		return
	}

	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusNoContent)
}

// deletePluggyCredentials removes the user's Pluggy credentials and answers the sheet without them.
func (h handler) deletePluggyCredentials(w http.ResponseWriter, r *http.Request) {
	if h.openFinance.Key == "" {
		http.NotFound(w, r)
		return
	}

	if err := h.db.DeletePluggyCredentials(r.Context(), sessionFrom(r.Context()).UserID); err != nil {
		internalError(w, "deleting pluggy credentials", err)
		return
	}

	h.renderSettings(w, r)
}

// secretHint is the hint under the Pluggy secret: keeping the saved one when it is left empty.
func secretHint(ctx context.Context, hasPluggy bool) string {
	if !hasPluggy {
		return ""
	}

	return i18n.T(ctx, "settings.pluggy_keep")
}
