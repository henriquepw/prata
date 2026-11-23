// Package settingpage is the Configurações sheet.
package settingpage

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/henriquepw/prata/server/internal/i18n"
	"github.com/henriquepw/prata/server/internal/pluggy"
	"github.com/henriquepw/prata/server/internal/store"
	"github.com/henriquepw/prata/server/internal/web/components/settingform"
	"github.com/henriquepw/prata/server/internal/web/shared"
)

// Page is the Configurações handlers.
type Page struct {
	db          *store.Store
	openFinance shared.OpenFinance
}

// New returns the settings page over db.
func New(db *store.Store, openFinance shared.OpenFinance) Page {
	return Page{db: db, openFinance: openFinance}
}

// Settings answers the Configurações sheet; a full-page request goes home.
func (p Page) Settings(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("HX-Request") != "true" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	p.renderSettings(w, r)
}

// renderSettings answers the Configurações sheet with the saved values.
func (p Page) renderSettings(w http.ResponseWriter, r *http.Request) {
	session := shared.SessionFrom(r.Context())
	form := settingform.Form{Preferences: session.Preferences, Currency: session.Currency, Errors: map[string]string{}}
	if _, err := p.showSavedPluggy(r.Context(), &form); err != nil {
		shared.InternalError(w, "reading pluggy credentials", err)
		return
	}

	shared.Render(w, r, http.StatusOK, sheet(form))
}

// showSavedPluggy turns the form's Pluggy fields on when Open Finance is, marking whether the user has credentials
// saved, showing their client id when none was typed, and returns the saved client id, empty when there is none.
func (p Page) showSavedPluggy(ctx context.Context, form *settingform.Form) (string, error) {
	if !p.openFinance.Enabled() {
		return "", nil
	}

	form.OpenFinance = true
	credentials, err := p.db.PluggyCredentials(ctx, p.openFinance.Key, shared.SessionFrom(ctx).UserID)
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

// Save stores the Preferences, Idioma included, the account's Moeda and, when a secret is typed, the
// Pluggy credentials after Pluggy accepts them, all at once, and reloads the page in them, or answers 422 with the
// form and stores nothing when a value is not one of the options, a new client id comes without its secret, or
// Pluggy refuses the credentials or is unavailable.
func (p Page) Save(w http.ResponseWriter, r *http.Request) {
	session := shared.SessionFrom(r.Context())
	form := settingform.Form{
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
	savedClientID, err := p.showSavedPluggy(r.Context(), &form)
	if err != nil {
		shared.InternalError(w, "reading pluggy credentials", err)
		return
	}

	form.Validate(r.Context())

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
		_, err := p.openFinance.Pluggy.Authenticate(r.Context(), credentials.ClientID, credentials.ClientSecret)
		if errors.Is(err, pluggy.ErrInvalidCredentials) {
			form.Errors["pluggy_client_secret"] = i18n.T(r.Context(), "settings.error.pluggy_rejected")
		} else if err != nil {
			slog.Warn("authenticating on pluggy", "error", err)
			form.Errors["pluggy_client_secret"] = i18n.T(r.Context(), "settings.error.pluggy_offline")
		}
	}

	if len(form.Errors) > 0 {
		shared.Render(w, r, http.StatusUnprocessableEntity, sheet(form))
		return
	}

	var sealed []byte
	if isSavingPluggy {
		sealed, err = store.SealPluggyCredentials(p.openFinance.Key, session.UserID, credentials)
		if err != nil {
			shared.InternalError(w, "sealing pluggy credentials", err)
			return
		}
	}

	if err := p.db.SaveSettings(r.Context(), session.UserID, session.AccountID, form.Preferences, form.Currency, sealed); err != nil {
		shared.InternalError(w, "saving settings", err)
		return
	}

	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusNoContent)
}

// DeletePluggy removes the user's Pluggy credentials and answers the sheet without them.
func (p Page) DeletePluggy(w http.ResponseWriter, r *http.Request) {
	if !p.openFinance.Enabled() {
		http.NotFound(w, r)
		return
	}

	if err := p.db.DeletePluggyCredentials(r.Context(), shared.SessionFrom(r.Context()).UserID); err != nil {
		shared.InternalError(w, "deleting pluggy credentials", err)
		return
	}

	p.renderSettings(w, r)
}

// secretHint is the hint under the Pluggy secret: keeping the saved one when it is left empty.
func secretHint(ctx context.Context, hasPluggy bool) string {
	if !hasPluggy {
		return ""
	}

	return i18n.T(ctx, "settings.pluggy_keep")
}
