package web

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/henriquepw/prata/server/internal/i18n"
	"github.com/henriquepw/prata/server/internal/pluggy"
	"github.com/henriquepw/prata/server/internal/store"
	"github.com/henriquepw/prata/server/internal/web/shared"
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
	session := shared.SessionFrom(r.Context())
	form := settingsForm{Preferences: session.Preferences, Currency: session.Currency, Errors: map[string]string{}}
	if _, err := h.showSavedPluggy(r.Context(), &form); err != nil {
		shared.InternalError(w, "reading pluggy credentials", err)
		return
	}

	shared.Render(w, r, http.StatusOK, settingsSheet(form))
}

// showSavedPluggy turns the form's Pluggy fields on when Open Finance is, marking whether the user has credentials
// saved, showing their client id when none was typed, and returns the saved client id, empty when there is none.
func (h handler) showSavedPluggy(ctx context.Context, form *settingsForm) (string, error) {
	if !h.openFinance.Enabled() {
		return "", nil
	}

	form.OpenFinance = true
	credentials, err := h.db.PluggyCredentials(ctx, h.openFinance.Key, shared.SessionFrom(ctx).UserID)
	if errors.Is(err, store.ErrNotFound) {
		return "", nil
	}

	if err != nil {
		return "", err
	}

	form.HasPluggy = true
	if form.PluggyClientID == "" {
		form.PluggyClientID = credentials.ClientID
	}

	return credentials.ClientID, nil
}

// saveSettings stores the Preferences, Idioma included, the account's Moeda and, when a secret is typed, the
// Pluggy credentials after Pluggy accepts them, all at once, and reloads the page in them, or answers 422 with the
// form and stores nothing when a value is not one of the options, a new client id comes without its secret, or
// Pluggy refuses the credentials or is unavailable.
func (h handler) saveSettings(w http.ResponseWriter, r *http.Request) {
	session := shared.SessionFrom(r.Context())
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
	savedClientID, err := h.showSavedPluggy(r.Context(), &form)
	if err != nil {
		shared.InternalError(w, "reading pluggy credentials", err)
		return
	}

	form.validateChoices(r.Context())

	if _, ok := shared.Find(shared.DailyLimitModes, form.DailyLimitMode); !ok {
		form.Errors["daily_limit_mode"] = i18n.T(r.Context(), "settings.error.daily_limit_mode")
	}

	credentials := store.PluggyCredentials{ClientID: form.PluggyClientID, ClientSecret: strings.TrimSpace(r.PostFormValue("pluggy_client_secret"))}
	isSavingPluggy := form.OpenFinance && credentials.ClientSecret != ""
	if isSavingPluggy && credentials.ClientID == "" {
		form.Errors["pluggy_client_id"] = i18n.T(r.Context(), "settings.error.pluggy_client_id")
	}

	if form.OpenFinance && credentials.ClientSecret == "" && credentials.ClientID != savedClientID {
		form.Errors["pluggy_client_secret"] = i18n.T(r.Context(), "settings.error.pluggy_secret")
	}

	if isSavingPluggy && len(form.Errors) == 0 {
		_, err := h.openFinance.Pluggy.Authenticate(r.Context(), credentials.ClientID, credentials.ClientSecret)
		if errors.Is(err, pluggy.ErrInvalidCredentials) {
			form.Errors["pluggy_client_secret"] = i18n.T(r.Context(), "settings.error.pluggy_rejected")
		} else if err != nil {
			slog.Warn("authenticating on pluggy", "error", err)
			form.Errors["pluggy_client_secret"] = i18n.T(r.Context(), "settings.error.pluggy_offline")
		}
	}

	if len(form.Errors) > 0 {
		shared.Render(w, r, http.StatusUnprocessableEntity, settingsSheet(form))
		return
	}

	var sealed []byte
	if isSavingPluggy {
		sealed, err = store.SealPluggyCredentials(h.openFinance.Key, session.UserID, credentials)
		if err != nil {
			shared.InternalError(w, "sealing pluggy credentials", err)
			return
		}
	}

	if err := h.db.SaveSettings(r.Context(), session.UserID, session.AccountID, form.Preferences, form.Currency, sealed); err != nil {
		shared.InternalError(w, "saving settings", err)
		return
	}

	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusNoContent)
}

// deletePluggyCredentials removes the user's Pluggy credentials and answers the sheet without them.
func (h handler) deletePluggyCredentials(w http.ResponseWriter, r *http.Request) {
	if !h.openFinance.Enabled() {
		http.NotFound(w, r)
		return
	}

	if err := h.db.DeletePluggyCredentials(r.Context(), shared.SessionFrom(r.Context()).UserID); err != nil {
		shared.InternalError(w, "deleting pluggy credentials", err)
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

// validateChoices flags the Idioma, Formato de data, Formato numérico and Moeda that are not one of the options.
func (f settingsForm) validateChoices(ctx context.Context) {
	if _, ok := shared.Find(shared.Languages, f.Language); !ok {
		f.Errors["language"] = i18n.T(ctx, "settings.error.language")
	}

	if _, ok := shared.Find(shared.DateFormats, f.DateFormat); !ok {
		f.Errors["date_format"] = i18n.T(ctx, "settings.error.date_format")
	}

	if _, ok := shared.Find(shared.NumberFormats, f.NumberFormat); !ok {
		f.Errors["number_format"] = i18n.T(ctx, "settings.error.number_format")
	}

	if _, ok := shared.Find(shared.Currencies, f.Currency); !ok {
		f.Errors["currency"] = i18n.T(ctx, "settings.error.currency")
	}
}
