package userprofile

import (
	"testing"

	"github.com/google/uuid"
	ory "github.com/ory/client-go"
)

func TestProfileFromOryIdentity(t *testing.T) {
	t.Parallel()

	userID := uuid.New()

	tests := []struct {
		name      string
		traits    any
		wantName  string
		wantEmail string
	}{
		{
			name: "flat name and email",
			traits: map[string]any{
				"email": "ada@example.com",
				"name":  "ada lovelace",
			},
			wantName:  "ada lovelace",
			wantEmail: "ada@example.com",
		},
		{
			name: "nested first and last name",
			traits: map[string]any{
				"email": "grace@example.com",
				"name": map[string]any{
					"first": "grace",
					"last":  "hopper",
				},
			},
			wantName:  "grace hopper",
			wantEmail: "grace@example.com",
		},
		{
			name: "first name only nested",
			traits: map[string]any{
				"name": map[string]any{
					"first": "barbara",
				},
			},
			wantName:  "barbara",
			wantEmail: "",
		},
		{
			name: "given/family fallback",
			traits: map[string]any{
				"given_name":  "marie",
				"family_name": "curie",
				"email":       "marie@example.com",
			},
			wantName:  "marie curie",
			wantEmail: "marie@example.com",
		},
		{
			name: "full name fallback when no flat name and no nested",
			traits: map[string]any{
				"full_name": "alan turing",
			},
			wantName:  "alan turing",
			wantEmail: "",
		},
		{
			name:      "missing traits returns zero values",
			traits:    nil,
			wantName:  "",
			wantEmail: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			identity := ory.Identity{Id: uuid.NewString(), Traits: tt.traits}
			got := profileFromOryIdentity(userID, identity)
			if got.UserID != userID {
				t.Fatalf("profileFromOryIdentity().UserID = %s, want %s", got.UserID, userID)
			}
			if got.Name != tt.wantName {
				t.Fatalf("profileFromOryIdentity().Name = %q, want %q", got.Name, tt.wantName)
			}
			if got.Email != tt.wantEmail {
				t.Fatalf("profileFromOryIdentity().Email = %q, want %q", got.Email, tt.wantEmail)
			}
		})
	}
}
