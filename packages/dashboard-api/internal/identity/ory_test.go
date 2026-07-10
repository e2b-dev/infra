package identity

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/google/uuid"
	ory "github.com/ory/client-go"

	sharedteamprovision "github.com/e2b-dev/infra/packages/shared/pkg/teamprovision"
)

func TestOryDirectory_SetExternalID(t *testing.T) {
	t.Parallel()

	subject := uuid.NewString()
	externalID := uuid.New()

	var gotPatch []ory.JsonPatch
	var gotPath, gotMethod string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotPatch)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"` + subject + `","schema_id":"default","schema_url":"","state":"active","traits":{}}`))
	}))
	defer server.Close()

	directory, err := NewOryDirectory(OryConfig{
		HTTPClient: server.Client(),
		SDKURL:     server.URL,
		Token:      "test-token",
	})
	if err != nil {
		t.Fatalf("failed to build ory directory: %v", err)
	}

	if err := directory.SetExternalID(t.Context(), subject, externalID); err != nil {
		t.Fatalf("SetExternalID returned error: %v", err)
	}

	if gotMethod != http.MethodPatch {
		t.Fatalf("expected PATCH, got %s", gotMethod)
	}
	if want := "/admin/identities/" + subject; gotPath != want {
		t.Fatalf("expected path %q, got %q", want, gotPath)
	}
	if len(gotPatch) != 1 {
		t.Fatalf("expected one json patch op, got %d", len(gotPatch))
	}
	if gotPatch[0].Op != "add" || gotPatch[0].Path != "/external_id" {
		t.Fatalf("unexpected patch op/path: %+v", gotPatch[0])
	}
	if value, _ := gotPatch[0].Value.(string); value != externalID.String() {
		t.Fatalf("expected external id %q, got %v", externalID.String(), gotPatch[0].Value)
	}
}

func TestOryDirectory_SetExternalIDValidatesInput(t *testing.T) {
	t.Parallel()

	directory := &oryDirectory{}

	if err := directory.SetExternalID(t.Context(), "  ", uuid.New()); err == nil {
		t.Fatal("expected error for blank subject")
	}
	if err := directory.SetExternalID(t.Context(), uuid.NewString(), uuid.Nil); err == nil {
		t.Fatal("expected error for nil external id")
	}
}

func TestIdentityFromOryProfileFields(t *testing.T) {
	t.Parallel()

	userID := uuid.New()

	tests := []struct {
		name           string
		traits         any
		metadataPublic map[string]any
		credentials    *map[string]ory.IdentityCredentials
		wantName       string
		wantEmail      string
		wantPicture    string
		wantProviders  []string
	}{
		{
			name: "email and name from traits, picture from metadata_public",
			traits: map[string]any{
				"email":   "ada@example.com",
				"name":    "ada lovelace",
				"picture": "https://example.com/trait-picture.jpg",
			},
			metadataPublic: map[string]any{
				"picture": "https://example.com/ada.jpg",
			},
			wantName:    "ada lovelace",
			wantEmail:   "ada@example.com",
			wantPicture: "https://example.com/ada.jpg",
		},
		{
			name: "providers from credentials and oidc config",
			traits: map[string]any{
				"email": "grace@example.com",
				"name":  "grace hopper",
			},
			credentials: &map[string]ory.IdentityCredentials{
				"password": {
					Config: map[string]any{"hashed_password": "$argon2id$v=19$m=65536,t=3,p=4$hash"},
				},
				"oidc": {
					Config: map[string]any{
						"providers": []any{
							map[string]any{"provider": " Google-project "},
							map[string]any{"provider": "github-main"},
							map[string]any{"provider": "apple"},
						},
					},
					Identifiers: []string{"google:111", "github:222"},
				},
			},
			wantName:      "grace hopper",
			wantEmail:     "grace@example.com",
			wantProviders: []string{"email", "google", "github"},
		},
		{
			name: "providers fallback from identifiers when config missing",
			credentials: &map[string]ory.IdentityCredentials{
				"password": {
					Config: map[string]any{"use_password_migration_hook": true},
				},
				"oidc": {
					Identifiers: []string{"github-gQUWnTuT:50748440", "GOOGLE-abC:111", "google:111", "apple:333"},
				},
			},
			wantProviders: []string{"email", "google", "github"},
		},
		{
			name: "empty password credential ignored",
			credentials: &map[string]ory.IdentityCredentials{
				"password": {Identifiers: []string{"ada@example.com"}},
			},
		},
		{
			name: "email provider from usable password credential",
			credentials: &map[string]ory.IdentityCredentials{
				"password": {
					Config: map[string]any{"hashed_password": "$argon2id$v=19$m=65536,t=3,p=4$hash"},
				},
			},
			wantProviders: []string{"email"},
		},
		{
			name:   "nil traits and credentials returns zero values",
			traits: nil,
		},
		{
			name: "non-string values are ignored",
			traits: map[string]any{
				"email": 42,
				"name":  map[string]any{"first": "barbara"},
			},
			metadataPublic: map[string]any{
				"picture": 42,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			oryIdentity := ory.Identity{Id: uuid.NewString(), Traits: tt.traits, MetadataPublic: tt.metadataPublic, Credentials: tt.credentials}
			id, err := identityFromOry(oryIdentity)
			if err != nil {
				t.Fatalf("identityFromOry returned error: %v", err)
			}

			got := ProfileFromIdentity(userID, id)
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

func TestIdentityFromOryCreatorContextUsesMetadataAdmin(t *testing.T) {
	t.Parallel()

	credentials := map[string]ory.IdentityCredentials{
		"oidc": {
			Config: map[string]any{
				"providers": []any{map[string]any{"provider": "github-main"}},
			},
		},
	}
	oryIdentity := ory.Identity{
		Id: uuid.NewString(),
		MetadataAdmin: map[string]any{
			"signup_ip":         "198.51.100.20",
			"signup_user_agent": "Dashboard/1.0",
			"ip_address":        "should-not-be-used",
			"user_agent":        "should-not-be-used",
		},
		MetadataPublic: map[string]any{
			"signup_ip":         "should-not-be-used",
			"signup_user_agent": "should-not-be-used",
		},
		Credentials: &credentials,
	}

	id, err := identityFromOry(oryIdentity)
	if err != nil {
		t.Fatalf("identityFromOry returned error: %v", err)
	}

	got := CreatorContextFromIdentity(id)
	if got.IPAddress != "198.51.100.20" {
		t.Fatalf("IPAddress = %q, want %q", got.IPAddress, "198.51.100.20")
	}
	if got.UserAgent != "Dashboard/1.0" {
		t.Fatalf("UserAgent = %q, want %q", got.UserAgent, "Dashboard/1.0")
	}
	if got.AuthMethod != sharedteamprovision.AuthMethodSocial {
		t.Fatalf("AuthMethod = %q, want %q", got.AuthMethod, sharedteamprovision.AuthMethodSocial)
	}
}

func TestOryDirectory_GetIdentityReturnsOrganizationID(t *testing.T) {
	t.Parallel()

	orgID := uuid.New()
	subject := uuid.NewString()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body := `{"id":"` + subject + `","schema_id":"default","schema_url":"","state":"active","traits":{},"organization_id":"` + orgID.String() + `"}`
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	directory, err := NewOryDirectory(OryConfig{
		HTTPClient: server.Client(),
		SDKURL:     server.URL,
		Token:      "test-token",
	})
	if err != nil {
		t.Fatalf("failed to build ory directory: %v", err)
	}

	got, err := directory.GetIdentity(t.Context(), subject)
	if err != nil {
		t.Fatalf("GetIdentity returned error: %v", err)
	}
	if got.OrganizationID != orgID {
		t.Fatalf("expected organization %s, got %s", orgID, got.OrganizationID)
	}
}

func TestIdentityFromOryRejectsMalformedOrganizationID(t *testing.T) {
	t.Parallel()

	badOrgID := "not-a-uuid"
	if _, err := identityFromOry(ory.Identity{Id: uuid.NewString(), OrganizationId: *ory.NewNullableString(&badOrgID)}); err == nil {
		t.Fatal("expected error for malformed organization_id")
	}
}
