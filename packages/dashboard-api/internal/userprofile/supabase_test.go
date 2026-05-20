package userprofile

import "testing"

func TestEscapePostgresLikePattern(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{input: "plain@example.com", want: "plain@example.com"},
		{input: "a_b@example.com", want: `a\_b@example.com`},
		{input: "a%b@example.com", want: `a\%b@example.com`},
		{input: `a\b@example.com`, want: `a\\b@example.com`},
		{input: `a\_%@example.com`, want: `a\\\_\%@example.com`},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()

			if got := escapePostgresLikePattern(tt.input); got != tt.want {
				t.Errorf("escapePostgresLikePattern(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
