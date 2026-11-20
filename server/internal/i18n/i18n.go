// Package i18n holds the fixed texts of the web in each Idioma and picks them by the Idioma in the context.
//
// A text is a short, stable key, grouped by where it shows ("nav.settings", "launcher.go_to"), in ptBR and en. Every
// key lives in ptBR; a key missing in en shows the pt-BR text. A text with a count has two keys, "key.one" and
// "key.other", read through Plural. User data (names, descriptions) never goes through here.
package i18n

import (
	"context"
	"fmt"
)

// The Idiomas, as the schema spells them. PortugueseBR is also the Idioma of a context without one.
const (
	PortugueseBR = "pt-BR"
	English      = "en"
)

var dictionaries = map[string]map[string]string{PortugueseBR: ptBR, English: en}

type languageKey struct{}

// WithLanguage returns ctx carrying the Idioma, "pt-BR" or "en", that T and Plural follow.
func WithLanguage(ctx context.Context, language string) context.Context {
	return context.WithValue(ctx, languageKey{}, language)
}

// Language is the Idioma in ctx, or PortugueseBR when it has none.
func Language(ctx context.Context) string {
	if language, ok := ctx.Value(languageKey{}).(string); ok {
		return language
	}

	return PortugueseBR
}

// T is the text of key in the Idioma of ctx, falling back to pt-BR and then to the key itself, with args filled in
// as by fmt.Sprintf.
func T(ctx context.Context, key string, args ...any) string {
	text, ok := dictionaries[Language(ctx)][key]
	if !ok {
		text, ok = ptBR[key]
	}

	if !ok {
		return key
	}

	if len(args) == 0 {
		return text
	}

	return fmt.Sprintf(text, args...)
}

// Plural is T of "key.one" when n is 1 and of "key.other" otherwise, with n as its arg.
func Plural(ctx context.Context, key string, n int) string {
	if n == 1 {
		return T(ctx, key+".one", n)
	}

	return T(ctx, key+".other", n)
}
