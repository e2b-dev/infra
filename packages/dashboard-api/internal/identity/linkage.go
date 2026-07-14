package identity

import (
	"context"

	"github.com/google/uuid"

	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
)

type LinkedIdentity struct {
	Issuer  string
	Subject string
	UserID  uuid.UUID
}

// Linkage resolves the user_id <-> (issuer, subject) mapping. It is the only
// layer that knows about oidc_iss.
type Linkage interface {
	IdentitiesForUsers(ctx context.Context, issuers []string, userIDs []uuid.UUID) ([]LinkedIdentity, error)
	UsersForSubjects(ctx context.Context, issuer string, subjects []string) ([]LinkedIdentity, error)
}

type linkageQueries interface {
	GetUserIdentitiesByUserIDsAndIssuers(ctx context.Context, arg authqueries.GetUserIdentitiesByUserIDsAndIssuersParams) ([]authqueries.GetUserIdentitiesByUserIDsAndIssuersRow, error)
	GetUserIdentitiesBySubjects(ctx context.Context, arg authqueries.GetUserIdentitiesBySubjectsParams) ([]authqueries.GetUserIdentitiesBySubjectsRow, error)
}

type queriesLinkage struct {
	queries linkageQueries
}

func NewQueriesLinkage(queries linkageQueries) Linkage {
	return queriesLinkage{queries: queries}
}

func (l queriesLinkage) IdentitiesForUsers(ctx context.Context, issuers []string, userIDs []uuid.UUID) ([]LinkedIdentity, error) {
	rows, err := l.queries.GetUserIdentitiesByUserIDsAndIssuers(ctx, authqueries.GetUserIdentitiesByUserIDsAndIssuersParams{
		OidcIssuers: issuers,
		UserIds:     userIDs,
	})
	if err != nil {
		return nil, err
	}

	linked := make([]LinkedIdentity, 0, len(rows))
	for _, row := range rows {
		linked = append(linked, LinkedIdentity{Issuer: row.OidcIss, Subject: row.OidcSub, UserID: row.UserID})
	}

	return linked, nil
}

func (l queriesLinkage) UsersForSubjects(ctx context.Context, issuer string, subjects []string) ([]LinkedIdentity, error) {
	rows, err := l.queries.GetUserIdentitiesBySubjects(ctx, authqueries.GetUserIdentitiesBySubjectsParams{
		OidcIss:  issuer,
		OidcSubs: subjects,
	})
	if err != nil {
		return nil, err
	}

	linked := make([]LinkedIdentity, 0, len(rows))
	for _, row := range rows {
		linked = append(linked, LinkedIdentity{Issuer: row.OidcIss, Subject: row.OidcSub, UserID: row.UserID})
	}

	return linked, nil
}
