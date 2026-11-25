// Package l10n holds the fixed texts of the web in each language and picks them by the language in the context.
//
// A text is a short, stable key, grouped by where it shows ("nav.settings", "launcher.go_to"), in ptBR and en. Every
// key lives in ptBR; a key missing in en shows the pt-BR text. A text with a count has two keys, "key.one" and
// "key.other", read through Plural. User data (names, descriptions) never goes through here.
package l10n

import (
	"context"

	"github.com/henriquepw/prata/server/pkg/i18n"
)

// The languages, as the schema spells them. PortugueseBR is also the language of a context without one.
const (
	PortugueseBR = "pt-BR"
	English      = "en"
)

var catalog = i18n.New(PortugueseBR, map[string]map[string]string{PortugueseBR: ptBR, English: en})

// WithLanguage returns ctx carrying the language, "pt-BR" or "en", that T and Plural follow.
func WithLanguage(ctx context.Context, language string) context.Context {
	return i18n.WithLanguage(ctx, language)
}

// Language is the language in ctx, or PortugueseBR when it has none.
func Language(ctx context.Context) string {
	return catalog.Language(ctx)
}

// T is the text of key in the language of ctx, falling back to pt-BR and then to the key itself.
func T(ctx context.Context, key string, args ...any) string {
	return catalog.T(ctx, key, args...)
}

// Plural is T of "key.one" when n is 1 and of "key.other" otherwise, with n as its arg.
func Plural(ctx context.Context, key string, n int) string {
	return catalog.Plural(ctx, key, n)
}
