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
)

// matches the page cap used by dashboard.full-stack's oryAuthAdmin.
const oryListPageSize = 1000

type oryProvider struct {
	identities ory.IdentityAPI
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
		identities: newOryIdentityAPI(config.HTTPClient, sdkURL, token),
		resolver:   config.Resolver,
		issuer:     issuer,
	}, nil
}

func newOryIdentityAPI(httpClient *http.Client, sdkURL, token string) ory.IdentityAPI {
	clientCopy := *httpClient
	base := clientCopy.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	clientCopy.Transport = &oryBearerTransport{token: token, base: base}

	cfg := ory.NewConfiguration()
	cfg.Servers = ory.ServerConfigurations{{URL: sdkURL}}
	cfg.HTTPClient = &clientCopy

	return ory.NewAPIClient(cfg).IdentityAPI
}

// injects the PAT instead of threading context.WithValue(ory.ContextAccessToken, ...) per call.
type oryBearerTransport struct {
	token string
	base  http.RoundTripper
}

func (t *oryBearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	cloned.Header.Set("Authorization", "Bearer "+t.token)

	return t.base.RoundTrip(cloned)
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

	identities, _, err := p.identities.ListIdentitiesExecute(
		p.identities.ListIdentities(ctx).CredentialsIdentifier(normalized),
	)
	if err != nil {
		return nil, fmt.Errorf("ory list identities by credentials identifier: %w", err)
	}
	if len(identities) == 0 {
		return []Profile{}, nil
	}

	userIDBySubject, err := p.userIDsForSubjects(ctx, identitySubjects(identities))
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
	for page := range slices.Chunk(ids, oryListPageSize) {
		batch, _, err := p.identities.ListIdentitiesExecute(
			p.identities.ListIdentities(ctx).Ids(page).PageSize(int64(len(page))),
		)
		if err != nil {
			return nil, fmt.Errorf("ory list identities: %w", err)
		}
		identities = append(identities, batch...)
	}

	return identities, nil
}

func identitySubjects(identities []ory.Identity) []string {
	subjects := make([]string, 0, len(identities))
	for _, identity := range identities {
		subjects = append(subjects, identity.Id)
	}

	return subjects
}

func profileFromOryIdentity(userID uuid.UUID, identity ory.Identity) Profile {
	traits, _ := identity.Traits.(map[string]any)

	return Profile{
		UserID: userID,
		Email:  metadataString(traits, "email"),
		Name:   oryDisplayName(traits),
	}
}

// mirrors dashboard.full-stack/src/core/server/auth/ory/identity.ts readDisplayName.
func oryDisplayName(traits map[string]any) string {
	if name := metadataString(traits, "name"); name != "" {
		return name
	}
	if name := nestedNameTrait(traits); name != "" {
		return name
	}
	if name := splitNameTraits(traits); name != "" {
		return name
	}

	return FirstNonEmpty(
		metadataString(traits, "full_name"),
		metadataString(traits, "fullName"),
	)
}

func nestedNameTrait(traits map[string]any) string {
	nested, ok := traits["name"].(map[string]any)
	if !ok {
		return ""
	}

	return strings.TrimSpace(metadataString(nested, "first") + " " + metadataString(nested, "last"))
}

func splitNameTraits(traits map[string]any) string {
	first := FirstNonEmpty(
		metadataString(traits, "first_name"),
		metadataString(traits, "firstName"),
		metadataString(traits, "given_name"),
		metadataString(traits, "givenName"),
	)
	last := FirstNonEmpty(
		metadataString(traits, "last_name"),
		metadataString(traits, "lastName"),
		metadataString(traits, "family_name"),
		metadataString(traits, "familyName"),
	)
	if first == "" && last == "" {
		return ""
	}

	return strings.TrimSpace(first + " " + last)
}
