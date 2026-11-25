// Package i18n picks fixed texts by the language carried in the context.
//
// A Catalog holds one dictionary of key → text per language. A key missing in the context's language shows the text
// of the fallback language, and a key missing there too shows the key itself. A text with a count has two keys,
// "key.one" and "key.other", read through Plural.
package i18n

import (
	"context"
	"fmt"
)

type languageKey struct{}

// WithLanguage returns ctx carrying the language that a Catalog follows.
func WithLanguage(ctx context.Context, language string) context.Context {
	return context.WithValue(ctx, languageKey{}, language)
}

// Catalog is the texts of every language, with the language used when the context has none or a key is missing.
type Catalog struct {
	fallback     string
	dictionaries map[string]map[string]string
}

// New returns a Catalog of dictionaries, keyed by language, that falls back to the fallback language.
func New(fallback string, dictionaries map[string]map[string]string) Catalog {
	return Catalog{fallback: fallback, dictionaries: dictionaries}
}

// Language is the language in ctx, or the fallback when it has none.
func (c Catalog) Language(ctx context.Context) string {
	if language, ok := ctx.Value(languageKey{}).(string); ok {
		return language
	}

	return c.fallback
}

// T is the text of key in the language of ctx, falling back to the fallback language and then to the key itself, with
// args filled in as by fmt.Sprintf.
func (c Catalog) T(ctx context.Context, key string, args ...any) string {
	text, ok := c.dictionaries[c.Language(ctx)][key]
	if !ok {
		text, ok = c.dictionaries[c.fallback][key]
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
func (c Catalog) Plural(ctx context.Context, key string, n int) string {
	if n == 1 {
		return c.T(ctx, key+".one", n)
	}

	return c.T(ctx, key+".other", n)
}
