// Package settingform is the settings form the Configurações sheet and the initial setup share.
package settingform

import (
	"context"
	"net/http"

	"github.com/henriquepw/prata/server/internal/i18n"
	"github.com/henriquepw/prata/server/internal/store"
	"github.com/henriquepw/prata/server/internal/web/shared"
)

// Validate flags the Idioma, Formato de data, Formato numérico and Moeda that are not one of the options.
func (f Form) Validate(ctx context.Context) {
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

// FromRequest reads the configurações iniciais from the request's form or query.
func FromRequest(r *http.Request) Form {
	return Form{
		Preferences: store.Preferences{Language: r.FormValue("language"), DateFormat: r.FormValue("date_format"), NumberFormat: r.FormValue("number_format")},
		Currency:    r.FormValue("currency"),
		Errors:      map[string]string{},
	}
}

// Fields are the choices by field name, nil when there are none.
func (f Form) Fields() map[string]string {
	if f.Language == "" {
		return nil
	}

	return map[string]string{"language": f.Language, "date_format": f.DateFormat, "number_format": f.NumberFormat, "currency": f.Currency}
}

// Chosen is the configurações iniciais the signup request carries, none when they are missing or not all valid, and
// the request in the chosen Idioma.
func Chosen(r *http.Request) (Form, *http.Request) {
	form := FromRequest(r)
	form.Validate(r.Context())
	if len(form.Errors) > 0 {
		return Form{}, r
	}

	return form, r.WithContext(i18n.WithLanguage(r.Context(), form.Language))
}
