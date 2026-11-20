package i18n

import (
	"context"
	"testing"
)

func TestT_KeyMissingInEnglishFallsBackToPortuguese(t *testing.T) {
	ptBR["test.only_pt"] = "só em português %d"
	t.Cleanup(func() { delete(ptBR, "test.only_pt") })

	if got := T(WithLanguage(context.Background(), "en"), "test.only_pt", 1); got != "só em português 1" {
		t.Errorf("T = %q, want the pt-BR text", got)
	}
}

func TestPlural_PicksOneOrOther(t *testing.T) {
	en["test.late.one"], en["test.late.other"] = "%d late", "%d late ones"
	t.Cleanup(func() { delete(en, "test.late.one"); delete(en, "test.late.other") })

	ctx := WithLanguage(context.Background(), "en")
	for n, want := range map[int]string{0: "0 late ones", 1: "1 late", 2: "2 late ones"} {
		if got := Plural(ctx, "test.late", n); got != want {
			t.Errorf("Plural(%d) = %q, want %q", n, got, want)
		}
	}
}
