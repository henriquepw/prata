// Package settingform is the settings form the Settings sheet and the initial setup share.
package settingform

import (
	"context"
	"net/http"

	"github.com/henriquepw/prata/server/internal/l10n"
	"github.com/henriquepw/prata/server/internal/web/prefs"
)

// Validate flags the language, date format, number format and currency that are not one of the options.
func (f Form) Validate(ctx context.Context) {
	if _, ok := prefs.Find(prefs.Languages, f.Language); !ok {
		f.Errors["language"] = l10n.T(ctx, "settings.error.language")
	}

	if _, ok := prefs.Find(prefs.DateFormats, f.DateFormat); !ok {
		f.Errors["date_format"] = l10n.T(ctx, "settings.error.date_format")
	}

	if _, ok := prefs.Find(prefs.NumberFormats, f.NumberFormat); !ok {
		f.Errors["number_format"] = l10n.T(ctx, "settings.error.number_format")
	}

	if _, ok := prefs.Find(prefs.Currencies, f.Currency); !ok {
		f.Errors["currency"] = l10n.T(ctx, "settings.error.currency")
	}
}

// FromRequest reads the initial settings from the request's form or query.
func FromRequest(r *http.Request) Form {
	return Form{
		Language: r.FormValue("language"), DateFormat: r.FormValue("date_format"), NumberFormat: r.FormValue("number_format"),
		Currency: r.FormValue("currency"),
		Errors:   map[string]string{},
	}
}

// Fields are the choices by field name, nil when there are none.
func (f Form) Fields() map[string]string {
	if f.Language == "" {
		return nil
	}

	return map[string]string{"language": f.Language, "date_format": f.DateFormat, "number_format": f.NumberFormat, "currency": f.Currency}
}

// Chosen is the initial settings the signup request carries, none when they are missing or not all valid, and
// the request in the chosen language.
func Chosen(r *http.Request) (Form, *http.Request) {
	form := FromRequest(r)
	form.Validate(r.Context())
	if len(form.Errors) > 0 {
		return Form{}, r
	}

	return form, r.WithContext(l10n.WithLanguage(r.Context(), form.Language))
}
