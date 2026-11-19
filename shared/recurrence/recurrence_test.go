package recurrence_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/henriquepw/prata/shared/recurrence"
)

type fixture struct {
	Name      string  `json:"name"`
	Rule      string  `json:"rule"`
	StartAt   string  `json:"start_at"`
	EndAt     *string `json:"end_at"`
	DeletedAt *string `json:"deleted_at"`
	Window    struct {
		From string `json:"from"`
		To   string `json:"to"`
	} `json:"window"`
	Expected []string `json:"expected"`
}

func TestExpandRuleFixtures(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("testdata", "*.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no fixtures found: %v", err)
	}

	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			var fx fixture
			if err := json.Unmarshal(data, &fx); err != nil {
				t.Fatal(err)
			}

			got := []string{}
			// A tombstone never projects: callers only expand recurrences with deleted_at NULL.
			if fx.DeletedAt == nil {
				got, err = recurrence.ExpandRule(fx.Rule, fx.StartAt, fx.EndAt, fx.Window.From, fx.Window.To)
				if err != nil {
					t.Fatal(err)
				}
			}

			if !slices.Equal(got, fx.Expected) {
				t.Errorf("%s: got %v, want %v", fx.Name, got, fx.Expected)
			}
		})
	}
}
