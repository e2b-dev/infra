package backgroundworker

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	testEventuallyTimeout = 10 * time.Second
	testEventuallyTick    = 50 * time.Millisecond
	testStopTimeout       = 5 * time.Second
	supabaseMigrationsDir = "packages/db/pkg/supabase/migrations"
)

type riverProcess struct {
	cancel   context.CancelFunc
	done     chan struct{}
	stopOnce sync.Once
}

func TestAuthUserSync_EndToEnd(t *testing.T) {
	t.Parallel()

	db := setupDatabase(t)

	ctx := t.Context()
	userID := uuid.New()
	email := fmt.Sprintf("river-sync-%s@example.com", userID.String()[:8])

	proc := startRiverWorker(t, db)
	t.Cleanup(func() { proc.Stop(t) })

	insertAuthUser(t, ctx, db, userID, email)
	waitForPublicUser(t, ctx, db, userID, email)

	updatedEmail := fmt.Sprintf("river-sync-%s-updated@example.com", userID.String()[:8])
	updateAuthUserEmail(t, ctx, db, userID, updatedEmail)
	waitForPublicUser(t, ctx, db, userID, updatedEmail)

	deleteAuthUser(t, ctx, db, userID)
	waitForPublicUserGone(t, ctx, db, userID)
}

func TestAuthUserSyncWorker_UpsertDeletesStaleProjectedUser(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db := setupDatabase(t)

	userID := uuid.New()
	staleEmail := fmt.Sprintf("stale-%s@example.com", userID.String()[:8])

	err := db.AuthDb.Write.UpsertPublicUser(ctx, authqueries.UpsertPublicUserParams{
		ID:    userID,
		Email: staleEmail,
	})
	require.NoError(t, err)
	require.Equal(t, 1, publicUserCount(t, ctx, db, userID))

	worker := NewAuthUserSyncWorker(ctx, db.SupabaseDB, db.AuthDb, logger.NewNopLogger())

	err = worker.Work(ctx, &river.Job[AuthUserSyncArgs]{
		JobRow: &rivertype.JobRow{ID: 1, Attempt: 1},
		Args: AuthUserSyncArgs{
			UserID:    userID.String(),
			Operation: "upsert",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 0, publicUserCount(t, ctx, db, userID))
}

func TestAuthUserSyncTrigger_SameEmailUpdateDoesNotEnqueueJob(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db := setupDatabase(t)

	userID := uuid.New()
	email := fmt.Sprintf("trigger-%s@example.com", userID.String()[:8])

	insertAuthUser(t, ctx, db, userID, email)
	require.Equal(t, 1, riverJobCountForUser(t, ctx, db, userID))

	updateAuthUserEmail(t, ctx, db, userID, email)
	assert.Equal(t, 1, riverJobCountForUser(t, ctx, db, userID))
}

var (
	migrateLock sync.Mutex
	hasMigrated bool
)

func setupDatabase(t *testing.T) *testutils.Database {
	t.Helper()

	db := testutils.SetupNamedDatabase(t, "supabase")

	migrateLock.Lock()
	defer migrateLock.Unlock()

	if hasMigrated {
		return db
	}

	db.ApplyMigrations(t, "packages/db/pkg/supabase/migrations")

	// The auth user sync bootstraps `auth_custom` in three steps:
	// 1. create the shared schema needed by River migrations
	// 2. River library migrations create River tables inside that schema
	// 3. the remaining auth migrations add triggers that enqueue into River
	require.NoError(t, db.SupabaseDB.TestsRawSQL(t.Context(), `
CREATE SCHEMA IF NOT EXISTS auth_custom;
`))

	err := RunRiverMigrations(t.Context(), db.SupabaseDB.WritePool())
	require.NoError(t, err)

	db.ApplyMigrations(t, supabaseMigrationsDir)

	hasMigrated = true

	return db
}

func startRiverWorker(t *testing.T, db *testutils.Database) *riverProcess {
	t.Helper()
	ctx := t.Context()

	l := logger.NewNopLogger()

	workers := river.NewWorkers()
	river.AddWorker(workers, NewAuthUserSyncWorker(ctx, db.SupabaseDB, db.AuthDb, l))

	client, err := NewRiverClient(db.SupabaseDB.WritePool(), workers)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(ctx)
	require.NoError(t, client.Start(ctx))

	done := make(chan struct{})

	go func() {
		<-ctx.Done()
		stopCtx, stopCancel := context.WithTimeout(context.WithoutCancel(ctx), testStopTimeout)
		defer stopCancel()

		_ = client.Stop(stopCtx)
		close(done)
	}()

	return &riverProcess{cancel: cancel, done: done}
}

func (p *riverProcess) Stop(t *testing.T) {
	t.Helper()

	p.stopOnce.Do(func() {
		p.cancel()

		select {
		case <-p.done:
		case <-time.After(testStopTimeout):
			t.Fatal("river client did not stop in time")
		}
	})
}

func insertAuthUser(t *testing.T, ctx context.Context, db *testutils.Database, userID uuid.UUID, email string) {
	t.Helper()

	err := db.SupabaseDB.TestsRawSQL(ctx,
		"INSERT INTO auth.users (id, email) VALUES ($1, $2)", userID, email)
	require.NoError(t, err)
}

func updateAuthUserEmail(t *testing.T, ctx context.Context, db *testutils.Database, userID uuid.UUID, email string) {
	t.Helper()

	err := db.SupabaseDB.TestsRawSQL(ctx,
		"UPDATE auth.users SET email = $1 WHERE id = $2", email, userID)
	require.NoError(t, err)
}

func deleteAuthUser(t *testing.T, ctx context.Context, db *testutils.Database, userID uuid.UUID) {
	t.Helper()

	err := db.SupabaseDB.TestsRawSQL(ctx,
		"DELETE FROM auth.users WHERE id = $1", userID)
	require.NoError(t, err)
}

func waitForPublicUser(t *testing.T, ctx context.Context, db *testutils.Database, userID uuid.UUID, expectedEmail string) {
	t.Helper()

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var email string

		err := db.AuthDb.TestsRawSQLQuery(ctx,
			"SELECT email FROM public.users WHERE id = $1",
			func(rows pgx.Rows) error {
				if !rows.Next() {
					return fmt.Errorf("user %s not found in public.users", userID)
				}

				return rows.Scan(&email)
			}, userID)

		if !assert.NoError(c, err) {
			return
		}

		assert.Equal(c, expectedEmail, email)
	}, testEventuallyTimeout, testEventuallyTick)
}

func waitForPublicUserGone(t *testing.T, ctx context.Context, db *testutils.Database, userID uuid.UUID) {
	t.Helper()

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		count, err := publicUserCountE(ctx, db, userID)

		if !assert.NoError(c, err) {
			return
		}

		assert.Equal(c, 0, count)
	}, testEventuallyTimeout, testEventuallyTick)
}

func publicUserCount(t *testing.T, ctx context.Context, db *testutils.Database, userID uuid.UUID) int {
	t.Helper()

	count, err := publicUserCountE(ctx, db, userID)
	require.NoError(t, err)

	return count
}

func publicUserCountE(ctx context.Context, db *testutils.Database, userID uuid.UUID) (int, error) {
	var count int

	err := db.AuthDb.TestsRawSQLQuery(ctx,
		"SELECT count(*) FROM public.users WHERE id = $1",
		func(rows pgx.Rows) error {
			if !rows.Next() {
				return nil
			}

			return rows.Scan(&count)
		}, userID)

	return count, err
}

func riverJobCountForUser(t *testing.T, ctx context.Context, db *testutils.Database, userID uuid.UUID) int {
	t.Helper()

	var count int

	err := db.SupabaseDB.TestsRawSQLQuery(ctx,
		"SELECT count(*) FROM auth_custom.river_job WHERE args->>'user_id' = $1",
		func(rows pgx.Rows) error {
			if !rows.Next() {
				return nil
			}

			return rows.Scan(&count)
		}, userID.String())
	require.NoError(t, err)

	return count
}
