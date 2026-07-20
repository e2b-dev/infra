package identity

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"
	ory "github.com/ory/client-go"
)

// Ory's admin ListIdentities rejects an `ids` filter with more than 500 entries
// (400 Bad Request) and does not paginate id-filtered results, so we look
// identities up in batches of 500 and rely on a single request per batch.
// https://www.ory.com/docs/kratos/reference/api (listIdentities, ids filter)
const oryListIDsBatchSize = 500

type oryDirectory struct {
	identities ory.IdentityAPI
	token      string
}

var _ Directory = (*oryDirectory)(nil)

type OryConfig struct {
	HTTPClient *http.Client
	SDKURL     string
	Token      string
}

func NewOryDirectory(config OryConfig) (Directory, error) {
	sdkURL := strings.TrimRight(strings.TrimSpace(config.SDKURL), "/")
	token := strings.TrimSpace(config.Token)

	switch {
	case config.HTTPClient == nil:
		return nil, errors.New("ory http client is required")
	case sdkURL == "":
		return nil, errors.New("ory sdk url is required")
	case token == "":
		return nil, errors.New("ory api token is required")
	}

	return &oryDirectory{
		identities: newOryIdentityAPI(config.HTTPClient, sdkURL),
		token:      token,
	}, nil
}

func newOryIdentityAPI(httpClient *http.Client, sdkURL string) ory.IdentityAPI {
	// shallow-copy so the SDK can't mutate the caller's shared client; the token
	// rides per-request via ContextAccessToken (see authCtx), never on the client.
	clientCopy := *httpClient

	cfg := ory.NewConfiguration()
	cfg.Servers = ory.ServerConfigurations{{URL: sdkURL}}
	cfg.HTTPClient = &clientCopy

	return ory.NewAPIClient(cfg).IdentityAPI
}

func (d *oryDirectory) authCtx(ctx context.Context) context.Context {
	return context.WithValue(ctx, ory.ContextAccessToken, d.token)
}

func (d *oryDirectory) GetIdentity(ctx context.Context, subject string) (Identity, error) {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return Identity{}, errors.New("ory identity subject is required")
	}

	oryIdentity, resp, err := d.identities.GetIdentityExecute(
		d.identities.GetIdentity(d.authCtx(ctx), subject).
			IncludeCredential(oryProfileCredentialTypes),
	)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return Identity{}, ErrIdentityNotFound
		}
		return Identity{}, fmt.Errorf("ory get identity: %w", err)
	}

	return identityFromOry(*oryIdentity)
}

func (d *oryDirectory) ListIdentities(ctx context.Context, subjects []string) ([]Identity, error) {
	identities := make([]Identity, 0, len(subjects))
	for batchSubjects := range slices.Chunk(subjects, oryListIDsBatchSize) {
		batch, resp, err := d.identities.ListIdentitiesExecute(
			d.identities.ListIdentities(d.authCtx(ctx)).
				Ids(batchSubjects).
				IncludeCredential(oryProfileCredentialTypes),
		)
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		if err != nil {
			if resp != nil && resp.StatusCode == http.StatusNotFound {
				return nil, nil
			}
			return nil, fmt.Errorf("ory list identities: %w", err)
		}

		for _, oryIdentity := range batch {
			id, err := identityFromOry(oryIdentity)
			if err != nil {
				return nil, err
			}
			identities = append(identities, id)
		}
	}

	return identities, nil
}

func (d *oryDirectory) SearchByEmail(ctx context.Context, email string) ([]Identity, error) {
	normalized := strings.TrimSpace(email)
	if normalized == "" {
		return []Identity{}, nil
	}

	oryIdentities, resp, err := d.identities.ListIdentitiesExecute(
		d.identities.ListIdentities(d.authCtx(ctx)).
			CredentialsIdentifier(normalized).
			IncludeCredential(oryProfileCredentialTypes),
	)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return []Identity{}, nil
		}
		return nil, fmt.Errorf("ory list identities by credentials identifier: %w", err)
	}

	identities := make([]Identity, 0, len(oryIdentities))
	for _, oryIdentity := range oryIdentities {
		id, err := identityFromOry(oryIdentity)
		if err != nil {
			return nil, err
		}
		identities = append(identities, id)
	}

	return identities, nil
}

func (d *oryDirectory) SetExternalID(ctx context.Context, subject string, externalID uuid.UUID) error {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return errors.New("ory identity subject is required")
	}
	if externalID == uuid.Nil {
		return errors.New("external id is required")
	}

	// "add" (not "replace") so the patch succeeds even when the identity has no
	// external_id yet: Ory serializes external_id with omitempty, so an unset
	// value is absent from the document and RFC 6902 "replace" would fail with a
	// path-not-found error. "add" creates the member if missing and replaces it
	// if present, making the operation idempotent across re-bootstraps.
	patch := []ory.JsonPatch{{Op: "add", Path: "/external_id", Value: externalID.String()}}
	_, resp, err := d.identities.PatchIdentityExecute(
		d.identities.PatchIdentity(d.authCtx(ctx), subject).JsonPatch(patch),
	)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return ErrIdentityNotFound
		}
		return fmt.Errorf("ory patch identity external id: %w", err)
	}

	return nil
}

func (d *oryDirectory) DeleteIdentity(ctx context.Context, subject string) error {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return errors.New("ory identity subject is required")
	}

	resp, err := d.identities.DeleteIdentityExecute(
		d.identities.DeleteIdentity(d.authCtx(ctx), subject),
	)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return fmt.Errorf("delete ory identity %s: %w", subject, err)
	}

	return nil
}

func identityFromOry(oryIdentity ory.Identity) (Identity, error) {
	traits, _ := oryIdentity.Traits.(map[string]any)

	organizationID := uuid.Nil
	if rawOrgID := strings.TrimSpace(oryIdentity.GetOrganizationId()); rawOrgID != "" {
		parsed, err := uuid.Parse(rawOrgID)
		if err != nil {
			return Identity{}, fmt.Errorf("parse ory organization_id %q: %w", rawOrgID, err)
		}
		organizationID = parsed
	}

	return Identity{
		Subject:           oryIdentity.Id,
		Email:             metadataString(traits, "email"),
		Name:              metadataString(traits, "name"),
		ProfilePictureURL: metadataString(oryIdentity.MetadataPublic, "picture"),
		Providers:         oryLinkedProviders(oryIdentity),
		OrganizationID:    organizationID,
		SignupIP:          metadataString(oryIdentity.MetadataAdmin, signupIPMetadataKey),
		SignupUserAgent:   metadataString(oryIdentity.MetadataAdmin, signupUserAgentMetadataKey),
		AuthMethod:        authMethodFromProviderNames(providerNamesFromOryIdentity(oryIdentity)),
	}, nil
}

func providerNamesFromOryIdentity(identity ory.Identity) []string {
	if identity.Credentials == nil {
		return nil
	}

	credentials := *identity.Credentials
	providers := make([]string, 0, 3)
	if _, ok := credentials[oryCredentialPassword]; ok {
		providers = append(providers, authProviderEmail)
	}

	oidc, ok := credentials[oryCredentialOIDC]
	if !ok {
		return providers
	}
	oidcProviderCount := 0

	if entries, ok := oidc.Config["providers"].([]any); ok {
		for _, entry := range entries {
			obj, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			if name, ok := obj["provider"].(string); ok {
				providers = append(providers, name)
				oidcProviderCount++
			}
		}
	}

	for _, identifier := range oidc.Identifiers {
		if provider, _, found := strings.Cut(identifier, ":"); found {
			providers = append(providers, provider)
			oidcProviderCount++
		}
	}

	if oidcProviderCount == 0 {
		providers = append(providers, oryCredentialOIDC)
	}

	return providers
}

// OIDC provider names can appear either in config.providers or as the
// "provider:subject" prefix of identifiers, depending on the response shape.
func oryLinkedProviders(identity ory.Identity) []string {
	if identity.Credentials == nil {
		return nil
	}

	credentials := *identity.Credentials
	candidates := make([]string, 0, 3)
	if password, ok := credentials[oryCredentialPassword]; ok && hasUsablePasswordCredential(password) {
		candidates = append(candidates, authProviderEmail)
	}

	oidc, ok := credentials[oryCredentialOIDC]
	if !ok {
		return normalizeAuthProviders(candidates)
	}

	if entries, ok := oidc.Config["providers"].([]any); ok {
		for _, entry := range entries {
			obj, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			if name, ok := obj["provider"].(string); ok {
				candidates = append(candidates, name)
			}
		}
	}

	for _, identifier := range oidc.Identifiers {
		if provider, _, found := strings.Cut(identifier, ":"); found {
			candidates = append(candidates, provider)
		}
	}

	return normalizeAuthProviders(candidates)
}

func hasUsablePasswordCredential(credential ory.IdentityCredentials) bool {
	if hashedPassword, ok := credential.Config["hashed_password"].(string); ok && hashedPassword != "" {
		return true
	}

	useMigrationHook, _ := credential.Config["use_password_migration_hook"].(bool)

	return useMigrationHook
}

func metadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}

	value, ok := metadata[key].(string)
	if !ok {
		return ""
	}

	return strings.TrimSpace(value)
}
