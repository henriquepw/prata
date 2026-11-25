package i18n

import (
	"context"
	"testing"
)

func TestT_KeyMissingInLanguageFallsBack(t *testing.T) {
	catalog := New("pt-BR", map[string]map[string]string{"pt-BR": {"only_pt": "só em português %d"}, "en": {}})

	if got := catalog.T(WithLanguage(context.Background(), "en"), "only_pt", 1); got != "só em português 1" {
		t.Errorf("T = %q, want the fallback text", got)
	}

	if got := catalog.T(context.Background(), "missing"); got != "missing" {
		t.Errorf("T = %q, want the key itself", got)
	}
}

func TestPlural_PicksOneOrOther(t *testing.T) {
	catalog := New("pt-BR", map[string]map[string]string{"en": {"late.one": "%d late", "late.other": "%d late ones"}})

	ctx := WithLanguage(context.Background(), "en")
	for n, want := range map[int]string{0: "0 late ones", 1: "1 late", 2: "2 late ones"} {
		if got := catalog.Plural(ctx, "late", n); got != want {
			t.Errorf("Plural(%d) = %q, want %q", n, got, want)
		}
	}
}
