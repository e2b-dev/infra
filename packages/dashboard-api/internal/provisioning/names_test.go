package provisioning

import "testing"

func TestDefaultTeamNameFromOIDCUserName(t *testing.T) {
	t.Parallel()

	stringPtr := func(value string) *string { return &value }

	tests := []struct {
		name     string
		userName *string
		want     string
	}{
		{
			name:     "nil name falls back",
			userName: nil,
			want:     "Personal Project",
		},
		{
			name:     "empty name falls back",
			userName: stringPtr(""),
			want:     "Personal Project",
		},
		{
			name:     "whitespace-only name falls back",
			userName: stringPtr("   "),
			want:     "Personal Project",
		},
		{
			name:     "multi-word name uses capitalized first word",
			userName: stringPtr("jakub kracina"),
			want:     "Jakub's Project",
		},
		{
			name:     "single word is capitalized",
			userName: stringPtr("ada"),
			want:     "Ada's Project",
		},
		{
			name:     "unicode first letter is capitalized",
			userName: stringPtr("žofia novak"),
			want:     "Žofia's Project",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := defaultTeamNameFromOIDCUserName(tt.userName); got != tt.want {
				t.Fatalf("expected generated name %q, got %q", tt.want, got)
			}
		})
	}
}
