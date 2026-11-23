package homepage

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/henriquepw/prata/server/internal/store"
)

func TestParseLaunch(t *testing.T) {
	categories := []store.Category{{ID: "lazer", Name: "Lazer"}, {ID: "mercado", Name: "Mercado"}, {ID: "saude", Name: "Saúde"}}
	tags := []store.Tag{{ID: "casa", Name: "Casa"}, {ID: "ferias", Name: "Férias de verão"}}
	today := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name, line                       string
		year                             int
		kind                             string
		amount                           int64
		description, date, category, tag string
		unknown                          []string
	}{
		{"outcome with everything", "-45,90 padaria ontem @merc #casa", 2026, "OUTCOME", 4590, "padaria", "2026-11-18", "mercado", "casa", nil},
		{"no sign is an outcome", "almoço 32", 2026, "OUTCOME", 3200, "almoço", "2026-11-19", "", "", nil},
		{"unicode minus", "−10 pão", 2026, "OUTCOME", 1000, "pão", "2026-11-19", "", "", nil},
		{"plus is an income with thousands", "+1.234,56 salário hoje", 2026, "INCOME", 123456, "salário", "2026-11-19", "", "", nil},
		{"the first number is the amount", "10 2 cafés", 2026, "OUTCOME", 1000, "2 cafés", "2026-11-19", "", "", nil},
		{"prefixes without accents and case", "8 @SAU #ferias", 2026, "OUTCOME", 800, "", "2026-11-19", "saude", "ferias", nil},
		{"accented prefix", "8 @saú #FÉR", 2026, "OUTCOME", 800, "", "2026-11-19", "saude", "ferias", nil},
		{"dd/mm in the year shown", "5/1 7,5 feira", 2027, "OUTCOME", 750, "feira", "2027-01-05", "", "", nil},
		{"invalid dd/mm is description", "31/02 7", 2026, "OUTCOME", 700, "31/02", "2026-11-19", "", "", nil},
		{"missing amount", "padaria @merc", 2026, "OUTCOME", 0, "padaria", "2026-11-19", "mercado", "", nil},
		{"unknown category and tag", "9 @xyz #nada @", 2026, "OUTCOME", 900, "", "2026-11-19", "", "", []string{"@xyz", "#nada", "@"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseLaunch(context.Background(), tt.line, categories, tags, today, tt.year)
			if got.Kind != tt.kind || got.Amount != tt.amount || got.Description != tt.description || got.Date != tt.date ||
				got.Category.ID != tt.category || got.Tag.ID != tt.tag || !slices.Equal(got.Unknown, tt.unknown) {
				t.Errorf("parseLaunch(%q) = %s %d %q %s @%s #%s %v, want %s %d %q %s @%s #%s %v", tt.line,
					got.Kind, got.Amount, got.Description, got.Date, got.Category.ID, got.Tag.ID, got.Unknown,
					tt.kind, tt.amount, tt.description, tt.date, tt.category, tt.tag, tt.unknown)
			}
		})
	}
}

func TestParseLaunch_Suggestions(t *testing.T) {
	categories := []store.Category{{ID: "lazer", Name: "Lazer"}, {ID: "mercado", Name: "Mercado"}, {ID: "moradia", Name: "Moradia"}}
	tags := []store.Tag{{ID: "ferias", Name: "Férias de verão"}}
	tests := []struct {
		line string
		want []string
	}{
		{"10 @m", []string{"@mercado", "@moradia"}},
		{"10 @", []string{"@lazer", "@mercado", "@moradia"}},
		{"10 #fé", []string{"#feriasdeverao"}},
		{"10 @m ", nil},
		{"10 pão", nil},
	}
	for _, tt := range tests {
		got := parseLaunch(context.Background(), tt.line, categories, tags, time.Now(), 2026)
		if !slices.Equal(got.Suggestions, tt.want) {
			t.Errorf("suggestions of %q = %v, want %v", tt.line, got.Suggestions, tt.want)
		}
	}
}
