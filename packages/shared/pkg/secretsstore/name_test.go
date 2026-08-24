package secretsstore

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeNameCanonicalizesAllowedName(t *testing.T) {
	t.Parallel()

	got, err := NormalizeName("  API-Key_1  ")
	if err != nil {
		t.Fatalf("NormalizeName() error = %v", err)
	}
	if got != "api-key_1" {
		t.Fatalf("NormalizeName() = %q, want %q", got, "api-key_1")
	}
}

func TestNormalizeNameRejectsEachInvalidBranchWithAFixedError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{name: "empty", input: "   "},
		{name: "too long", input: strings.Repeat("a", maxNameBytes+1)},
		{name: "reserved ID prefix", input: "  SEC_private  "},
		{name: "non-ASCII case alias", input: "KEY"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := NormalizeName(tt.input)
			if got != "" {
				t.Fatalf("NormalizeName() = %q, want empty", got)
			}
			if !errors.Is(err, ErrInvalidName) {
				t.Fatalf("NormalizeName() error = %v, want ErrInvalidName", err)
			}
			if err.Error() != "secret name is invalid" {
				t.Fatalf("NormalizeName() error = %q, want fixed message", err)
			}
			if strings.Contains(err.Error(), tt.input) {
				t.Fatalf("NormalizeName() error contains rejected input")
			}
		})
	}
}
