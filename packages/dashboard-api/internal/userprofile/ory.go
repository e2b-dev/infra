package userprofile

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"
	ory "github.com/ory/client-go"

	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	sharedteamprovision "github.com/e2b-dev/infra/packages/shared/pkg/teamprovision"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// Ory's admin ListIdentities rejects an `ids` filter with more than 500 entries
// (400 Bad Request) and does not paginate id-filtered results, so we look
// identities up in batches of 500 and rely on a single request per batch.
// https://www.ory.com/docs/kratos/reference/api (listIdentities, ids filter)
const oryListIDsBatchSize = 500

type oryProvider struct {
	identities ory.IdentityAPI
	token      string
	resolver   identityResolver
	issuer     string
}

type identityResolver interface {
	GetUserIdentitiesByUserIDs(ctx context.Context, arg authqueries.GetUserIdentitiesByUserIDsParams) ([]authqueries.GetUserIdentitiesByUserIDsRow, error)
	GetUserIdentitiesBySubjects(ctx context.Context, arg authqueries.GetUserIdentitiesBySubjectsParams) ([]authqueries.GetUserIdentitiesBySubjectsRow, error)
}

var _ Provider = (*oryProvider)(nil)

type OryConfig struct {
	HTTPClient *http.Client
	SDKURL     string
	Token      string
	Issuer     string
	Resolver   identityResolver
}

func NewOryProvider(config OryConfig) (Provider, error) {
	sdkURL := strings.TrimRight(strings.TrimSpace(config.SDKURL), "/")
	token := strings.TrimSpace(config.Token)
	issuer := strings.TrimSpace(config.Issuer)

	switch {
	case config.HTTPClient == nil:
		return nil, errors.New("ory http client is required")
	case sdkURL == "":
		return nil, errors.New("ory sdk url is required")
	case token == "":
		return nil, errors.New("ory api token is required")
	case issuer == "":
		return nil, errors.New("ory issuer is required")
	case config.Resolver == nil:
		return nil, errors.New("ory identity resolver is required")
	}

	return &oryProvider{
		identities: newOryIdentityAPI(config.HTTPClient, sdkURL),
		token:      token,
		resolver:   config.Resolver,
		issuer:     issuer,
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

func (p *oryProvider) authCtx(ctx context.Context) context.Context {
	return context.WithValue(ctx, ory.ContextAccessToken, p.token)
}

func (p *oryProvider) GetProfilesByUserID(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]Profile, error) {
	unique := uniqueUUIDs(userIDs)
	if len(unique) == 0 {
		return map[uuid.UUID]Profile{}, nil
	}

	userIDBySubject, err := p.subjectsForUserIDs(ctx, unique)
	if err != nil {
		return nil, err
	}
	if len(userIDBySubject) == 0 {
		return map[uuid.UUID]Profile{}, nil
	}

	identities, err := p.listIdentitiesByIDs(ctx, slices.Collect(maps.Keys(userIDBySubject)))
	if err != nil {
		return nil, err
	}

	profiles := make(map[uuid.UUID]Profile, len(identities))
	for _, identity := range identities {
		if userID, ok := userIDBySubject[identity.Id]; ok {
			profiles[userID] = profileFromOryIdentity(userID, identity)
		}
	}

	return profiles, nil
}

func (p *oryProvider) FindProfilesByEmail(ctx context.Context, email string) ([]Profile, error) {
	normalized := strings.TrimSpace(email)
	if normalized == "" {
		return []Profile{}, nil
	}

	identities, resp, err := p.identities.ListIdentitiesExecute(
		p.identities.ListIdentities(p.authCtx(ctx)).
			CredentialsIdentifier(normalized).
			IncludeCredential(oryProfileCredentialTypes),
	)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return nil, fmt.Errorf("ory list identities by credentials identifier: %w", err)
	}
	if len(identities) == 0 {
		return []Profile{}, nil
	}

	subjects := utils.Map(identities, func(identity ory.Identity) string { return identity.Id })
	userIDBySubject, err := p.userIDsForSubjects(ctx, subjects)
	if err != nil {
		return nil, err
	}

	profiles := make([]Profile, 0, len(identities))
	for _, identity := range identities {
		userID, ok := userIDBySubject[identity.Id]
		if !ok {
			continue
		}
		profiles = append(profiles, profileFromOryIdentity(userID, identity))
	}

	return profiles, nil
}

func (p *oryProvider) GetTeamCreatorContext(ctx context.Context, userID uuid.UUID) (*sharedteamprovision.CreatorContextV1, error) {
	if userID == uuid.Nil {
		return nil, nil
	}

	userIDBySubject, err := p.subjectsForUserIDs(ctx, []uuid.UUID{userID})
	if err != nil {
		return nil, err
	}
	if len(userIDBySubject) == 0 {
		return nil, nil
	}

	identities, err := p.listIdentitiesByIDs(ctx, slices.Collect(maps.Keys(userIDBySubject)))
	if err != nil {
		return nil, err
	}
	if len(identities) == 0 {
		return nil, nil
	}

	return creatorContextFromOryIdentity(identities[0]), nil
}

func (p *oryProvider) SetIdentityExternalID(ctx context.Context, subject string, externalID uuid.UUID) error {
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
	_, resp, err := p.identities.PatchIdentityExecute(
		p.identities.PatchIdentity(p.authCtx(ctx), subject).JsonPatch(patch),
	)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return fmt.Errorf("ory patch identity external id: %w", err)
	}

	return nil
}

func (p *oryProvider) PrepareDeleteUser(ctx context.Context, userID uuid.UUID) (DeleteUserHandle, error) {
	if userID == uuid.Nil {
		return nil, errors.New("user id is required")
	}

	subjectsByUser, err := p.subjectsForUserIDs(ctx, []uuid.UUID{userID})
	if err != nil {
		return nil, fmt.Errorf("lookup ory subject for user: %w", err)
	}

	if len(subjectsByUser) == 0 {
		return nil, fmt.Errorf("%w: no identity mapping for user %s", ErrUserNotFound, userID)
	}

	subjects := make([]string, 0, len(subjectsByUser))
	for s := range subjectsByUser {
		subjects = append(subjects, s)
	}

	return &oryDeleteHandle{provider: p, subjects: subjects}, nil
}

type oryDeleteHandle struct {
	provider *oryProvider
	subjects []string
}

func (h *oryDeleteHandle) Execute(ctx context.Context) error {
	for _, subject := range h.subjects {
		resp, err := h.provider.identities.DeleteIdentityExecute(
			h.provider.identities.DeleteIdentity(h.provider.authCtx(ctx), subject),
		)
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		if err != nil {
			return fmt.Errorf("delete ory identity %s: %w", subject, err)
		}
	}

	return nil
}

func (p *oryProvider) subjectsForUserIDs(ctx context.Context, userIDs []uuid.UUID) (map[string]uuid.UUID, error) {
	rows, err := p.resolver.GetUserIdentitiesByUserIDs(ctx, authqueries.GetUserIdentitiesByUserIDsParams{
		OidcIss: p.issuer,
		UserIds: userIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("lookup ory subjects: %w", err)
	}

	userIDBySubject := make(map[string]uuid.UUID, len(rows))
	for _, row := range rows {
		userIDBySubject[row.OidcSub] = row.UserID
	}

	return userIDBySubject, nil
}

func (p *oryProvider) userIDsForSubjects(ctx context.Context, subjects []string) (map[string]uuid.UUID, error) {
	rows, err := p.resolver.GetUserIdentitiesBySubjects(ctx, authqueries.GetUserIdentitiesBySubjectsParams{
		OidcIss:  p.issuer,
		OidcSubs: subjects,
	})
	if err != nil {
		return nil, fmt.Errorf("lookup user ids by ory subjects: %w", err)
	}

	userIDBySubject := make(map[string]uuid.UUID, len(rows))
	for _, row := range rows {
		userIDBySubject[row.OidcSub] = row.UserID
	}

	return userIDBySubject, nil
}

func (p *oryProvider) listIdentitiesByIDs(ctx context.Context, ids []string) ([]ory.Identity, error) {
	identities := make([]ory.Identity, 0, len(ids))
	for batchIDs := range slices.Chunk(ids, oryListIDsBatchSize) {
		batch, resp, err := p.identities.ListIdentitiesExecute(
			p.identities.ListIdentities(p.authCtx(ctx)).
				Ids(batchIDs).
				IncludeCredential(oryProfileCredentialTypes),
		)
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		if err != nil {
			return nil, fmt.Errorf("ory list identities: %w", err)
		}
		identities = append(identities, batch...)
	}

	return identities, nil
}

// email and name are identity traits; picture lives in metadata_public rather
// than traits so it never renders on Ory's self-service registration form. The
// OIDC Jsonnet mapper still populates all of them from provider claims (Google
// profile scope, GitHub user scope, etc.). The canonical trait shape lives in
// fixtures/ory/identity.v1.schema.json.
func profileFromOryIdentity(userID uuid.UUID, identity ory.Identity) Profile {
	traits, _ := identity.Traits.(map[string]any)

	return Profile{
		UserID:            userID,
		Email:             metadataString(traits, "email"),
		Name:              metadataString(traits, "name"),
		ProfilePictureURL: metadataString(identity.MetadataPublic, "picture"),
		Providers:         oryLinkedProviders(identity),
	}
}

func creatorContextFromOryIdentity(identity ory.Identity) *sharedteamprovision.CreatorContextV1 {
	return creatorContextFromMetadata(identity.MetadataAdmin, providerNamesFromOryIdentity(identity))
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
