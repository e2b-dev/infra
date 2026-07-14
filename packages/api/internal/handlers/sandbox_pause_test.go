package handlers

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	snapshotcache "github.com/e2b-dev/infra/packages/api/internal/cache/snapshots"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
)

// newTestSnapshotCache builds a SnapshotCache backed by a real Postgres and Redis
// test container, mirroring the wiring in NewSnapshotCache used by the API store.
func newTestSnapshotCache(t *testing.T, db *testutils.Database) *snapshotcache.SnapshotCache {
	t.Helper()

	redisClient := redis_utils.SetupInstance(t)
	sc := snapshotcache.NewSnapshotCache(db.SqlcClient, redisClient)
	t.Cleanup(func() {
		_ = sc.Close(t.Context())
	})

	return sc
}

// TestPauseHandleNotRunningSandbox_WrongTeamReturnsNotFound verifies the ENG-3544
// behavior in the pause path: pausing a not-running sandbox whose snapshot belongs to
// another team must return 404 (indistinguishable from a sandbox that does not exist),
// rather than leaking that a paused snapshot exists.
func TestPauseHandleNotRunningSandbox_WrongTeamReturnsNotFound(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	ctx := t.Context()

	sc := newTestSnapshotCache(t, db)

	ownerTeamID := testutils.CreateTestTeam(t, db)
	baseTemplateID := testutils.CreateTestTemplate(t, db, ownerTeamID)

	sandboxID := "sandbox-" + uuid.New().String()
	snapshotTemplateID := "snapshot-template-" + uuid.New().String()
	testutils.UpsertTestSnapshot(t, ctx, db, snapshotTemplateID, sandboxID, ownerTeamID, baseTemplateID)

	otherTeamID := testutils.CreateTestTeam(t, db)

	apiErr := pauseHandleNotRunningSandbox(ctx, sc, sandboxID, otherTeamID)
	assert.Equal(t, http.StatusNotFound, apiErr.Code,
		"another team's paused sandbox must be reported as not found")
}

// TestPauseHandleNotRunningSandbox_OwnerReturnsAlreadyPaused verifies that when the
// owning team pauses a sandbox whose snapshot already exists, the handler reports the
// already-paused conflict rather than a not-found.
func TestPauseHandleNotRunningSandbox_OwnerReturnsAlreadyPaused(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	ctx := t.Context()

	sc := newTestSnapshotCache(t, db)

	teamID := testutils.CreateTestTeam(t, db)
	baseTemplateID := testutils.CreateTestTemplate(t, db, teamID)

	sandboxID := "sandbox-" + uuid.New().String()
	snapshotTemplateID := "snapshot-template-" + uuid.New().String()
	testutils.UpsertTestSnapshot(t, ctx, db, snapshotTemplateID, sandboxID, teamID, baseTemplateID)

	apiErr := pauseHandleNotRunningSandbox(ctx, sc, sandboxID, teamID)
	assert.Equal(t, http.StatusConflict, apiErr.Code,
		"the owning team's already-paused sandbox should return a conflict")
}
