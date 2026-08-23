package management

import (
	"context"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	sharedauth "github.com/e2b-dev/infra/packages/auth/pkg/auth"
	authtypes "github.com/e2b-dev/infra/packages/auth/pkg/types"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
)

func newService(db *testutils.Database) (*Service, *recordingCache) {
	cache := &recordingCache{}

	return NewService(db.AuthDB, db.SqlcClient, cache), cache
}

func teamMembers(t *testing.T, db *testutils.Database, teamID uuid.UUID) []uuid.UUID {
	t.Helper()

	var members []uuid.UUID
	err := db.AuthDB.TestsRawSQLQuery(t.Context(),
		"SELECT user_id FROM public.users_teams WHERE team_id = $1 ORDER BY user_id",
		func(rows pgx.Rows) error {
			for rows.Next() {
				var userID uuid.UUID
				if err := rows.Scan(&userID); err != nil {
					return err
				}
				members = append(members, userID)
			}

			return nil
		}, teamID)
	require.NoError(t, err)

	return members
}

func publicUserExists(t *testing.T, db *testutils.Database, userID uuid.UUID) bool {
	t.Helper()

	var exists bool
	err := db.AuthDB.TestsRawSQLQuery(t.Context(),
		"SELECT EXISTS (SELECT 1 FROM public.users WHERE id = $1)",
		func(rows pgx.Rows) error {
			rows.Next()

			return rows.Scan(&exists)
		}, userID)
	require.NoError(t, err)

	return exists
}

func identityOwner(t *testing.T, db *testutils.Database, identity ProjectMemberIdentity) uuid.UUID {
	t.Helper()

	var userID uuid.UUID
	err := db.AuthDB.TestsRawSQLQuery(t.Context(),
		"SELECT user_id FROM public.user_identities WHERE oidc_iss = $1 AND oidc_sub = $2",
		func(rows pgx.Rows) error {
			rows.Next()

			return rows.Scan(&userID)
		}, identity.Issuer, identity.Subject)
	require.NoError(t, err)

	return userID
}

func accessTokenCount(t *testing.T, db *testutils.Database, userID uuid.UUID) int {
	t.Helper()

	var count int
	err := db.AuthDB.TestsRawSQLQuery(t.Context(),
		"SELECT count(*) FROM public.access_tokens WHERE user_id = $1",
		func(rows pgx.Rows) error {
			rows.Next()

			return rows.Scan(&count)
		}, userID)
	require.NoError(t, err)

	return count
}

func TestPurgeUserAccessTokens(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	service, _ := newService(db)
	userID := uuid.New()
	require.NoError(t, db.AuthDB.UpsertPublicUser(t.Context(), userID))
	err := db.AuthDB.TestsRawSQL(t.Context(), `
INSERT INTO public.access_tokens (id, user_id, access_token_hash, access_token_prefix, access_token_length, access_token_mask_prefix, access_token_mask_suffix, name)
VALUES ($1, $2, 'access-token-hash', 'e2b_', 32, 'e2b_', 'token', 'default')
`, uuid.New(), userID)
	require.NoError(t, err)
	require.Equal(t, 1, accessTokenCount(t, db, userID))

	require.NoError(t, service.PurgeUserAccessTokens(t.Context(), userID))

	require.Zero(t, accessTokenCount(t, db, userID))
}

type memberKey struct {
	userID uuid.UUID
	teamID string
}

// recordingCache captures the evictions, which are the half of these operations
// with no trace in the database.
type recordingCache struct {
	noopAuthService

	members []memberKey
	teams   []uuid.UUID
}

func (c *recordingCache) reset() {
	c.members = nil
	c.teams = nil
}

func (c *recordingCache) InvalidateTeamMemberCache(_ context.Context, userID uuid.UUID, teamID string) {
	c.members = append(c.members, memberKey{userID: userID, teamID: teamID})
}

func (c *recordingCache) InvalidateTeamCache(_ context.Context, teamID uuid.UUID) error {
	c.teams = append(c.teams, teamID)

	return nil
}

// noopAuthService covers what these tests never reach, so recordingCache only
// implements the two methods it asserts on.
type noopAuthService struct{}

var _ sharedauth.Service = noopAuthService{}

func (noopAuthService) ValidateAPIKey(context.Context, *gin.Context, string) (*authtypes.Team, *sharedauth.APIError) {
	return nil, nil
}

func (noopAuthService) ValidateAuthProviderToken(context.Context, *gin.Context, string) (uuid.UUID, *sharedauth.APIError) {
	return uuid.Nil, nil
}

func (noopAuthService) ValidateAuthProviderTeam(context.Context, *gin.Context, string) (*authtypes.Team, *sharedauth.APIError) {
	return nil, nil
}

func (noopAuthService) GetTeamByID(context.Context, uuid.UUID) (*authtypes.Team, error) {
	return nil, nil
}

func (noopAuthService) InvalidateTeamMemberCache(context.Context, uuid.UUID, string) {}

func (noopAuthService) InvalidateAPIKeyCache(context.Context, string) {}

func (noopAuthService) InvalidateTeamCache(context.Context, uuid.UUID) error {
	return nil
}

func (noopAuthService) Close(context.Context) error {
	return nil
}
