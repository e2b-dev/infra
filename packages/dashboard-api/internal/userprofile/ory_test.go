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
		name        string
		traits      any
		wantName    string
		wantEmail   string
		wantPicture string
	}{
		{
			name: "all three standardized traits",
			traits: map[string]any{
				"email":   "ada@example.com",
				"name":    "ada lovelace",
				"picture": "https://example.com/ada.jpg",
			},
			wantName:    "ada lovelace",
			wantEmail:   "ada@example.com",
			wantPicture: "https://example.com/ada.jpg",
		},
		{
			name: "missing picture is empty",
			traits: map[string]any{
				"email": "grace@example.com",
				"name":  "grace hopper",
			},
			wantName:  "grace hopper",
			wantEmail: "grace@example.com",
		},
		{
			name:   "nil traits returns zero values",
			traits: nil,
		},
		{
			name: "non-string trait values are ignored",
			traits: map[string]any{
				"email":   42,
				"name":    map[string]any{"first": "barbara"},
				"picture": nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			identity := ory.Identity{Id: uuid.NewString(), Traits: tt.traits}
			got := profileFromOryIdentity(userID, identity)
			if got.UserID != userID {
				t.Fatalf("UserID = %s, want %s", got.UserID, userID)
			}
			if got.Name != tt.wantName {
				t.Fatalf("Name = %q, want %q", got.Name, tt.wantName)
			}
			if got.Email != tt.wantEmail {
				t.Fatalf("Email = %q, want %q", got.Email, tt.wantEmail)
			}
			if got.ProfilePictureURL != tt.wantPicture {
				t.Fatalf("ProfilePictureURL = %q, want %q", got.ProfilePictureURL, tt.wantPicture)
			}
		})
	}
}
