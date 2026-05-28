package userprofile

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"
	ory "github.com/ory/client-go"

	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// Ory's admin ListIdentities rejects an `ids` filter with more than 500 entries
// (400 Bad Request) and does not paginate id-filtered results, so we look
// identities up in batches of 500 and rely on a single request per batch.
// https://www.ory.com/docs/kratos/reference/api (listIdentities, ids filter)
const oryListIDsBatchSize = 500

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

// ory's generated client returns the raw *http.Response alongside the parsed
// body, so callers must close it (even on error) to release the connection.
func closeOryResponse(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
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
		p.identities.ListIdentities(ctx).
			CredentialsIdentifier(normalized).
			IncludeCredential([]string{"oidc"}),
	)
	closeOryResponse(resp)
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

func (p *oryProvider) subjectsForUserIDs(ctx context.Context, userIDs []uuid.UUID) (map[string]uuid.UUID, error) {
	rows, err := p.resolver.GetUserIdentitiesByUserIDs(ctx, authqueries.GetUserIdentitiesByUserIDsParams{
		OidcIss: p.issuer,
		UserIds: userIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("lookup ory subjects: %w", err)
	}

	return userIDsBySubject(rows, func(r authqueries.GetUserIdentitiesByUserIDsRow) (string, uuid.UUID) {
		return r.OidcSub, r.UserID
	}), nil
}

func (p *oryProvider) userIDsForSubjects(ctx context.Context, subjects []string) (map[string]uuid.UUID, error) {
	rows, err := p.resolver.GetUserIdentitiesBySubjects(ctx, authqueries.GetUserIdentitiesBySubjectsParams{
		OidcIss:  p.issuer,
		OidcSubs: subjects,
	})
	if err != nil {
		return nil, fmt.Errorf("lookup user ids by ory subjects: %w", err)
	}

	return userIDsBySubject(rows, func(r authqueries.GetUserIdentitiesBySubjectsRow) (string, uuid.UUID) {
		return r.OidcSub, r.UserID
	}), nil
}

// userIDsBySubject is generic because the two resolver queries that feed it
// return distinct generated row types with the same (oidc_sub, user_id) shape.
func userIDsBySubject[Row any](rows []Row, sub func(Row) (string, uuid.UUID)) map[string]uuid.UUID {
	bySubject := make(map[string]uuid.UUID, len(rows))
	for _, row := range rows {
		oidcSub, userID := sub(row)
		bySubject[oidcSub] = userID
	}

	return bySubject
}

func (p *oryProvider) listIdentitiesByIDs(ctx context.Context, ids []string) ([]ory.Identity, error) {
	identities := make([]ory.Identity, 0, len(ids))
	for batchIDs := range slices.Chunk(ids, oryListIDsBatchSize) {
		batch, resp, err := p.identities.ListIdentitiesExecute(
			p.identities.ListIdentities(ctx).
				Ids(batchIDs).
				IncludeCredential([]string{"oidc"}),
		)
		closeOryResponse(resp)
		if err != nil {
			return nil, fmt.Errorf("ory list identities: %w", err)
		}
		identities = append(identities, batch...)
	}

	return identities, nil
}

// profileFromOryIdentity reads the standardized E2B identity schema traits:
// name, email, picture. The Ory project is configured to populate these from
// OIDC provider claims (e.g. Google profile scope, GitHub user scope) so the
// underlying upstream is transparent here. See
// packages/dashboard-api/fixtures/ory/identity.v1.schema.json for the canonical
// trait shape uploaded to Ory.
func profileFromOryIdentity(userID uuid.UUID, identity ory.Identity) Profile {
	traits, _ := identity.Traits.(map[string]any)

	return Profile{
		UserID:            userID,
		Email:             metadataString(traits, "email"),
		Name:              metadataString(traits, "name"),
		ProfilePictureURL: metadataString(traits, "picture"),
		Providers:         oryLinkedProviders(identity),
	}
}

// oryLinkedProviders extracts the upstream OIDC providers (e.g. "google",
// "github") linked to an Ory identity. The list lives at
// credentials.oidc.config.providers[].provider; if the Console didn't expose
// config (depends on include_credential), fall back to parsing the
// "provider:subject" prefix from credentials.oidc.identifiers.
func oryLinkedProviders(identity ory.Identity) []string {
	if identity.Credentials == nil {
		return nil
	}
	oidc, ok := (*identity.Credentials)["oidc"]
	if !ok {
		return nil
	}

	candidates := make([]string, 0, 4)
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

	if len(candidates) == 0 {
		for _, identifier := range oidc.Identifiers {
			if provider, _, found := strings.Cut(identifier, ":"); found {
				candidates = append(candidates, provider)
			}
		}
	}

	return uniqueNonEmpty(candidates)
}
