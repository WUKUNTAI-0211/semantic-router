package testcases

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadCacheCasesRequiresNegationAcceptanceControls(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		wantErr string
	}{
		{
			name:    "missing-positive-control",
			fixture: `[{"original_question":"Is caching enabled?","negation_questions":["Is caching disabled?"]}]`,
			wantErr: "similar_questions",
		},
		{
			name:    "missing-negation-control",
			fixture: `[{"original_question":"Is caching enabled?","similar_questions":["Is caching enabled in prod?"]}]`,
			wantErr: "negation_questions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cache_cases.json")
			if err := os.WriteFile(path, []byte(tt.fixture), 0o600); err != nil {
				t.Fatal(err)
			}

			_, err := loadCacheCases(path)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("loadCacheCases() error = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadCacheCasesAcceptsCheckedInAcceptanceFixture(t *testing.T) {
	cases, err := loadCacheCases(filepath.Join("testdata", "cache_cases.json"))
	if err != nil {
		t.Fatalf("loadCacheCases(checked-in fixture): %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("checked-in fixture must contain cache cases")
	}
}
