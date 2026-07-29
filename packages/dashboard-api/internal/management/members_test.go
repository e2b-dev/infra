package management

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
)

// The push is the only way membership reaches this side, so a repeated one must
// land on the same state rather than a duplicate row or a rejection.
func TestSetAddsAndIsIdempotent(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	teamID := testutils.CreateTestTeam(t, db)
	service, _ := newService(db)
	userID := uuid.New()

	require.NoError(t, service.SetProjectMembers(t.Context(), MemberChange{ProjectID: teamID, Present: []uuid.UUID{userID}}))
	require.NoError(t, service.SetProjectMembers(t.Context(), MemberChange{ProjectID: teamID, Present: []uuid.UUID{userID}}))

	require.Equal(t, []uuid.UUID{userID}, teamMembers(t, db, teamID))
}

// The caller knows only opaque ids, and users_teams still points at
// public.users, so without the anchor the first push for an unseen user fails.
func TestSetAnchorsUnknownUsers(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	teamID := testutils.CreateTestTeam(t, db)
	service, _ := newService(db)
	userID := uuid.New()
	addedBy := uuid.New()

	require.NoError(t, service.SetProjectMembers(t.Context(), MemberChange{ProjectID: teamID, Present: []uuid.UUID{userID}, AddedBy: &addedBy}))

	require.True(t, publicUserExists(t, db, userID))
	// added_by carries its own key, so an unknown actor fails the insert on a
	// column nobody was looking at.
	require.True(t, publicUserExists(t, db, addedBy))
}

// The assertion the type exists for: InvalidateTeamCache finds keys by reading
// users_teams, so a removed member is one it cannot see. Writing and
// invalidating separately would leave this user authenticating until expiry.
func TestSetEvictsRemovedMembersTheSweepCannotSee(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	teamID := testutils.CreateTestTeam(t, db)
	service, cache := newService(db)
	userID := uuid.New()

	require.NoError(t, service.SetProjectMembers(t.Context(), MemberChange{ProjectID: teamID, Present: []uuid.UUID{userID}}))
	cache.reset()

	require.NoError(t, service.SetProjectMembers(t.Context(), MemberChange{ProjectID: teamID, Absent: []uuid.UUID{userID}}))

	require.Empty(t, teamMembers(t, db, teamID))
	require.Equal(t, []memberKey{{userID: userID, teamID: teamID.String()}}, cache.members)
}

// Membership does not change the team, so entries keyed by API key hold nothing
// this write invalidated. Sweeping them would evict every key on the project.
func TestSetLeavesTeamWideCacheEntriesAlone(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	teamID := testutils.CreateTestTeam(t, db)
	service, cache := newService(db)

	require.NoError(t, service.SetProjectMembers(t.Context(), MemberChange{ProjectID: teamID, Present: []uuid.UUID{uuid.New()}}))

	require.Empty(t, cache.teams)
}

// Removing a membership that is not there is the desired state, not a failure —
// and the eviction still has to run.
//
// A crash before the eviction leaves a stale entry only a retry can clear, and
// that retry sees exactly this: nothing left to delete. Keying the eviction off
// what the statement touched would make the recovery path a no-op.
func TestSetRemovingAnAbsentMemberSucceedsAndStillEvicts(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	teamID := testutils.CreateTestTeam(t, db)
	service, cache := newService(db)
	userID := uuid.New()

	require.NoError(t, service.SetProjectMembers(t.Context(), MemberChange{ProjectID: teamID, Absent: []uuid.UUID{userID}}))

	require.Equal(t, []memberKey{{userID: userID, teamID: teamID.String()}}, cache.members)
}

// One call carries both directions so the caller never has to order them.
func TestSetAppliesBothDirectionsAtOnce(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	teamID := testutils.CreateTestTeam(t, db)
	service, _ := newService(db)
	staying, leaving, joining := uuid.New(), uuid.New(), uuid.New()

	require.NoError(t, service.SetProjectMembers(t.Context(), MemberChange{ProjectID: teamID, Present: []uuid.UUID{staying, leaving}}))
	require.NoError(t, service.SetProjectMembers(t.Context(), MemberChange{ProjectID: teamID, Present: []uuid.UUID{joining}, Absent: []uuid.UUID{leaving}}))

	require.ElementsMatch(t, []uuid.UUID{staying, joining}, teamMembers(t, db, teamID))
}

// A request states presence only for the users it lists, so a batch naming two
// members must not disturb a third.
func TestSetIgnoresUnlistedMembers(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	teamID := testutils.CreateTestTeam(t, db)
	service, _ := newService(db)
	listed, unlisted := uuid.New(), uuid.New()

	require.NoError(t, service.SetProjectMembers(t.Context(), MemberChange{ProjectID: teamID, Present: []uuid.UUID{listed, unlisted}}))
	require.NoError(t, service.SetProjectMembers(t.Context(), MemberChange{ProjectID: teamID, Absent: []uuid.UUID{listed}}))

	require.Equal(t, []uuid.UUID{unlisted}, teamMembers(t, db, teamID))
}

// A push naming an unknown project is divergence, and the caller decides what to
// do about it. Nothing may be written on the way to saying so.
func TestSetReportsAnUnknownProject(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	service, cache := newService(db)
	userID := uuid.New()

	err := service.SetProjectMembers(t.Context(), MemberChange{ProjectID: uuid.New(), Present: []uuid.UUID{userID}})

	require.ErrorIs(t, err, ErrProjectNotFound)
	require.False(t, publicUserExists(t, db, userID))
	require.Empty(t, cache.members)
}

// Backfilled legacy teams carry default memberships, so a push can remove one.
// Allowed: the caller owns membership, and signup recreates a missing default.
func TestSetRemovesADefaultMembership(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	teamID := testutils.CreateTestTeam(t, db)
	service, _ := newService(db)
	userID := uuid.New()

	require.NoError(t, service.SetProjectMembers(t.Context(), MemberChange{ProjectID: teamID, Present: []uuid.UUID{userID}}))
	require.NoError(t, db.AuthDB.TestsRawSQL(t.Context(),
		"UPDATE public.users_teams SET is_default = true WHERE team_id = $1 AND user_id = $2", teamID, userID))

	require.NoError(t, service.SetProjectMembers(t.Context(), MemberChange{ProjectID: teamID, Absent: []uuid.UUID{userID}}))
	require.Empty(t, teamMembers(t, db, teamID))
}

// The evicted set has to equal the deleted set: exactly the projects the purge
// removed the user from, and no others. Deriving it from the delete rather than
// a preceding read is what keeps that true when a membership is committed while
// the purge is running.
func TestPurgeUserClearsMembershipsAndTokensAcrossProjects(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	first := testutils.CreateTestTeam(t, db)
	second := testutils.CreateTestTeam(t, db)
	service, cache := newService(db)
	userID, other := uuid.New(), uuid.New()

	require.NoError(t, service.SetProjectMembers(t.Context(), MemberChange{ProjectID: first, Present: []uuid.UUID{userID, other}}))
	require.NoError(t, service.SetProjectMembers(t.Context(), MemberChange{ProjectID: second, Present: []uuid.UUID{userID}}))
	createAccessToken(t, db, userID)
	createAccessToken(t, db, other)
	cache.reset()

	require.NoError(t, service.PurgeUser(t.Context(), userID))

	require.Equal(t, []uuid.UUID{other}, teamMembers(t, db, first))
	require.Empty(t, teamMembers(t, db, second))
	require.Zero(t, accessTokenCount(t, db, userID))
	require.Equal(t, 1, accessTokenCount(t, db, other))
	require.ElementsMatch(t, []memberKey{
		{userID: userID, teamID: first.String()},
		{userID: userID, teamID: second.String()},
	}, cache.members)
}

// The row anchors keys that outlive the user's access: addons.added_by refuses
// the delete, and two created_by columns would null out provenance.
func TestPurgeUserLeavesTheUserRowStanding(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	teamID := testutils.CreateTestTeam(t, db)
	service, _ := newService(db)
	userID := uuid.New()

	require.NoError(t, service.SetProjectMembers(t.Context(), MemberChange{ProjectID: teamID, Present: []uuid.UUID{userID}}))
	require.NoError(t, service.PurgeUser(t.Context(), userID))

	require.True(t, publicUserExists(t, db, userID))
}

// The caller fans a purge out to every control plane, so most hold nothing.
func TestPurgeUserWithNothingToPurgeSucceeds(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	service, _ := newService(db)

	require.NoError(t, service.PurgeUser(t.Context(), uuid.New()))
	require.NoError(t, service.PurgeUser(t.Context(), uuid.New()))
}
