package userprofile

import (
	"reflect"
	"testing"

	"github.com/google/uuid"
	ory "github.com/ory/client-go"
)

func TestProfileFromOryIdentity(t *testing.T) {
	t.Parallel()

	userID := uuid.New()

	tests := []struct {
		name          string
		traits        any
		credentials   *map[string]ory.IdentityCredentials
		wantName      string
		wantEmail     string
		wantPicture   string
		wantProviders []string
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
			name: "providers from oidc config",
			traits: map[string]any{
				"email": "grace@example.com",
				"name":  "grace hopper",
			},
			credentials: &map[string]ory.IdentityCredentials{
				"oidc": {
					Config: map[string]any{
						"providers": []any{
							map[string]any{"provider": "google"},
							map[string]any{"provider": "github"},
						},
					},
					Identifiers: []string{"google:111", "github:222"},
				},
			},
			wantName:      "grace hopper",
			wantEmail:     "grace@example.com",
			wantProviders: []string{"google", "github"},
		},
		{
			name: "providers fallback from identifiers when config missing",
			credentials: &map[string]ory.IdentityCredentials{
				"oidc": {
					Identifiers: []string{"google:111", "github:222", "google:111"},
				},
			},
			wantProviders: []string{"google", "github"},
		},
		{
			name: "providers ignored when only password credential",
			credentials: &map[string]ory.IdentityCredentials{
				"password": {Identifiers: []string{"ada@example.com"}},
			},
		},
		{
			name:   "nil traits and credentials returns zero values",
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

			identity := ory.Identity{Id: uuid.NewString(), Traits: tt.traits, Credentials: tt.credentials}
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
			if !reflect.DeepEqual(got.Providers, tt.wantProviders) {
				t.Fatalf("Providers = %v, want %v", got.Providers, tt.wantProviders)
			}
		})
	}
}
