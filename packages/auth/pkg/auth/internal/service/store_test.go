package service

import (
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/auth/pkg/types"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
)

// Every lookup authentication caches resolves the team and its limits in one
// statement: the team's pushed row, else the deployment's defaults. The tier
// and active add-ons do not reach them.
func TestAuthStoreResolvesTeamAndLimitsInOneStatement(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	ctx := t.Context()
	recordedDB, recorder := db.RecordedAuthDB(t)
	store := newAuthStore(recordedDB)
	// The key's last use is recorded in the background; it is not part of
	// the lookup.
	lookups := func() []string {
		return slices.DeleteFunc(recorder.Take(), func(name string) bool { return name == "UpdateLastTimeUsed" })
	}

	require.NoError(t, db.AuthDB.TestsRawSQL(ctx, `
		UPDATE public.default_limits SET
			max_length_hours = 101, concurrent_sandboxes = 102, concurrent_template_builds = 103,
			max_vcpu = 104, max_ram_mb = 105, disk_mb = 106, events_ttl_days = 107,
			default_free_disk_size_mb = 108, max_disk_size_mb = 109,
			api_team_rps_list = 110, api_team_rps_delete = 111, max_secrets = 112`))
	defaults := &types.TeamLimits{
		MaxLengthHours: 101, SandboxConcurrency: 102, BuildConcurrency: 103,
		MaxVcpu: 104, MaxRamMb: 105, DiskMb: 106, EventsTTLDays: 107,
		DefaultFreeDiskSizeMb: 108, MaxFreeDiskSizeMb: 109,
		APITeamRPSList: 110, APITeamRPSDelete: 111, MaxSecrets: 112,
	}

	unpushed := testutils.CreateTestTeam(t, db)
	require.NoError(t, db.AuthDB.TestsRawSQL(ctx, `
		INSERT INTO public.addons (team_id, name, extra_concurrent_sandboxes, extra_max_secrets, added_by)
		VALUES ($1, 'ignored', 500, 5, '00000000-0000-0000-0000-000000000000')`, unpushed))
	pushed := testutils.CreateTestTeam(t, db)
	require.NoError(t, db.AuthDB.TestsRawSQL(ctx, `
		INSERT INTO public.project_limits (
			team_id, max_length_hours, concurrent_sandboxes, concurrent_template_builds,
			max_vcpu, max_ram_mb, disk_mb, events_ttl_days, default_free_disk_size_mb, max_disk_size_mb,
			api_team_rps_list, api_team_rps_delete, max_secrets
		) VALUES ($1, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12)`, pushed))
	pushedLimits := &types.TeamLimits{
		MaxLengthHours: 1, SandboxConcurrency: 2, BuildConcurrency: 3,
		MaxVcpu: 4, MaxRamMb: 5, DiskMb: 6, EventsTTLDays: 7,
		DefaultFreeDiskSizeMb: 8, MaxFreeDiskSizeMb: 9,
		APITeamRPSList: 10, APITeamRPSDelete: 11, MaxSecrets: 12,
	}

	const keyHash = "store-test-key-hash"
	_, err := db.AuthDB.CreateTeamAPIKey(ctx, authqueries.CreateTeamAPIKeyParams{
		TeamID: pushed, ApiKeyHash: keyHash, ApiKeyPrefix: "e2b_", ApiKeyLength: 40,
		ApiKeyMaskPrefix: "ab", ApiKeyMaskSuffix: "yz", Name: "store test",
	})
	require.NoError(t, err)
	userID := uuid.New()
	require.NoError(t, db.AuthDB.UpsertPublicUser(ctx, userID))
	require.NoError(t, db.AuthDB.CreateTeamMembership(ctx, authqueries.CreateTeamMembershipParams{
		UserID: userID, TeamID: unpushed,
	}))

	byKey, err := store.GetTeamByHashedAPIKey(ctx, keyHash)
	require.NoError(t, err)
	assert.Equal(t, pushed, byKey.ID)
	assert.Equal(t, pushedLimits, byKey.Limits)
	assert.Equal(t, []string{"GetTeamByAPIKey"}, lookups())

	byID, err := store.GetTeamByID(ctx, unpushed)
	require.NoError(t, err)
	assert.Equal(t, unpushed, byID.ID)
	assert.Equal(t, defaults, byID.Limits)
	assert.Equal(t, []string{"GetTeamByTeamID"}, lookups())

	byMember, err := store.GetTeamByIDAndUserID(ctx, userID, unpushed.String())
	require.NoError(t, err)
	assert.Equal(t, unpushed, byMember.ID)
	assert.Equal(t, defaults, byMember.Limits)
	assert.Equal(t, []string{"GetTeamByTeamAndUser"}, lookups())

	_, err = store.GetTeamByID(ctx, uuid.New())
	assert.True(t, dberrors.IsNotFoundError(err), "a missing team is not found: %v", err)
	_, err = store.GetTeamByHashedAPIKey(ctx, "unknown-key-hash")
	assert.True(t, dberrors.IsNotFoundError(err), "an unknown key is not found: %v", err)
	_, err = store.GetTeamByIDAndUserID(ctx, userID, pushed.String())
	assert.True(t, dberrors.IsNotFoundError(err), "a team the user is not in is not found: %v", err)
}
