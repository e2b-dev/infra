package supabaseauthusersync

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	testRunnerPollInterval = 20 * time.Millisecond
	testRunnerLockTimeout  = 150 * time.Millisecond
	testEventuallyTimeout  = 8 * time.Second
	testEventuallyTick     = 25 * time.Millisecond
	testRunnerStopTimeout  = 2 * time.Second
)

type runnerProcess struct {
	cancel   context.CancelFunc
	done     chan error
	stopOnce sync.Once
}

type userExpectation struct {
	Email  string
	Exists bool
}

type queueSnapshot struct {
	Total        int
	DeadLettered int
}

func TestSupabaseAuthUserSyncRunner_EndToEnd(t *testing.T) {
	db := testutils.SetupDatabase(t)

	t.Run("repairs_insert_update_delete_drift", func(t *testing.T) {
		ctx := t.Context()
		userID := uuid.New()
		initialEmail := fmt.Sprintf("auth-sync-%s-initial@example.com", userID.String()[:8])
		updatedEmail := fmt.Sprintf("auth-sync-%s-updated@example.com", userID.String()[:8])

		insertAuthUser(t, ctx, db, userID, initialEmail)
		deletePublicUser(t, ctx, db, userID)
		assertQueueBacklog(t, ctx, db, 1)

		insertRunner := startRunnerProcess(t, db, newTestRunnerConfig(4), "repair-insert")
		t.Cleanup(func() {
			insertRunner.Stop(t)
		})
		waitForPublicUsers(t, ctx, db, map[uuid.UUID]userExpectation{
			userID: {
				Email:  initialEmail,
				Exists: true,
			},
		})
		waitForQueueDrain(t, ctx, db)
		insertRunner.Stop(t)

		updateAuthUserEmail(t, ctx, db, userID, updatedEmail)
		setPublicUserEmail(t, ctx, db, userID, "stale@example.com")
		assertQueueBacklog(t, ctx, db, 1)

		updateRunner := startRunnerProcess(t, db, newTestRunnerConfig(4), "repair-update")
		t.Cleanup(func() {
			updateRunner.Stop(t)
		})
		waitForPublicUsers(t, ctx, db, map[uuid.UUID]userExpectation{
			userID: {
				Email:  updatedEmail,
				Exists: true,
			},
		})
		waitForQueueDrain(t, ctx, db)
		updateRunner.Stop(t)

		deleteAuthUser(t, ctx, db, userID)
		insertPublicUser(t, ctx, db, userID, "ghost@example.com")
		assertQueueBacklog(t, ctx, db, 1)

		deleteRunner := startRunnerProcess(t, db, newTestRunnerConfig(4), "repair-delete")
		t.Cleanup(func() {
			deleteRunner.Stop(t)
		})
		waitForPublicUsers(t, ctx, db, map[uuid.UUID]userExpectation{
			userID: {
				Exists: false,
			},
		})
		waitForQueueDrain(t, ctx, db)
		deleteRunner.Stop(t)
	})

	t.Run("reclaims_stale_queue_locks", func(t *testing.T) {
		ctx := t.Context()
		userID := uuid.New()
		email := fmt.Sprintf("auth-sync-%s-locked@example.com", userID.String()[:8])

		insertAuthUser(t, ctx, db, userID, email)
		deletePublicUser(t, ctx, db, userID)
		lockQueueItems(t, ctx, db, userID, time.Now().Add(-time.Minute), "stale-worker")
		assertQueueBacklog(t, ctx, db, 1)

		runner := startRunnerProcess(t, db, newTestRunnerConfig(2), "lock-reclaimer")
		t.Cleanup(func() {
			runner.Stop(t)
		})

		waitForPublicUsers(t, ctx, db, map[uuid.UUID]userExpectation{
			userID: {
				Email:  email,
				Exists: true,
			},
		})
		waitForQueueDrain(t, ctx, db)
		runner.Stop(t)
	})

	t.Run("drains_burst_backlog_with_multiple_runners", func(t *testing.T) {
		ctx := t.Context()
		const userCount = 60

		userIDs := make([]uuid.UUID, 0, userCount)

		for i := 0; i < userCount; i++ {
			userID := uuid.New()
			userIDs = append(userIDs, userID)

			initialEmail := fmt.Sprintf("auth-sync-burst-%02d-initial@example.com", i)
			insertAuthUser(t, ctx, db, userID, initialEmail)

			if i%2 == 0 {
				updateAuthUserEmail(t, ctx, db, userID, fmt.Sprintf("auth-sync-burst-%02d-v2@example.com", i))
			}
			if i%5 == 0 {
				updateAuthUserEmail(t, ctx, db, userID, fmt.Sprintf("auth-sync-burst-%02d-v3@example.com", i))
			}

			if i%3 == 0 {
				deleteAuthUser(t, ctx, db, userID)
				enqueueUserSyncItem(t, ctx, db, userID, "delete")
				if i%6 == 0 {
					insertPublicUser(t, ctx, db, userID, fmt.Sprintf("ghost-%02d@example.com", i))
				}

				continue
			}

			if i%8 == 0 {
				deletePublicUser(t, ctx, db, userID)
			} else if i%7 == 0 {
				setPublicUserEmail(t, ctx, db, userID, fmt.Sprintf("stale-%02d@example.com", i))
			}

			if i%4 == 0 {
				enqueueUserSyncItem(t, ctx, db, userID, "upsert")
			}
			if i%9 == 0 {
				enqueueUserSyncItem(t, ctx, db, userID, "upsert")
			}
		}

		authUsers, err := loadAuthUsers(ctx, db)
		require.NoError(t, err)

		want := expectedUsersForIDs(userIDs, authUsers)
		assertQueueBacklog(t, ctx, db, userCount)

		runnerA := startRunnerProcess(t, db, newTestRunnerConfig(5), "burst-a")
		runnerB := startRunnerProcess(t, db, newTestRunnerConfig(5), "burst-b")
		t.Cleanup(func() {
			runnerA.Stop(t)
			runnerB.Stop(t)
		})

		waitForPublicUsers(t, ctx, db, want)
		waitForQueueDrain(t, ctx, db)

		runnerA.Stop(t)
		runnerB.Stop(t)
	})
}

func newTestRunnerConfig(batchSize int32) Config {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.BatchSize = batchSize
	cfg.PollInterval = testRunnerPollInterval
	cfg.LockTimeout = testRunnerLockTimeout
	cfg.MaxAttempts = 5

	return cfg
}

func startRunnerProcess(t *testing.T, db *testutils.Database, cfg Config, lockOwner string) *runnerProcess {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	runner := NewRunner(
		cfg,
		db.AuthDb,
		db.SqlcClient,
		lockOwner,
		logger.NewNopLogger(),
	)

	go func() {
		done <- runner.Run(ctx)
	}()

	return &runnerProcess{
		cancel: cancel,
		done:   done,
	}
}

func (p *runnerProcess) Stop(t *testing.T) {
	t.Helper()

	p.stopOnce.Do(func() {
		p.cancel()

		select {
		case err := <-p.done:
			require.ErrorIs(t, err, context.Canceled)
		case <-time.After(testRunnerStopTimeout):
			t.Fatalf("runner did not stop within %s", testRunnerStopTimeout)
		}
	})
}

func insertAuthUser(t *testing.T, ctx context.Context, db *testutils.Database, userID uuid.UUID, email string) {
	t.Helper()

	err := db.AuthDb.TestsRawSQL(ctx,
		"INSERT INTO auth.users (id, email) VALUES ($1, $2)",
		userID,
		email,
	)
	require.NoError(t, err)
}

func updateAuthUserEmail(t *testing.T, ctx context.Context, db *testutils.Database, userID uuid.UUID, email string) {
	t.Helper()

	err := db.AuthDb.TestsRawSQL(ctx,
		"UPDATE auth.users SET email = $1 WHERE id = $2",
		email,
		userID,
	)
	require.NoError(t, err)
}

func deleteAuthUser(t *testing.T, ctx context.Context, db *testutils.Database, userID uuid.UUID) {
	t.Helper()

	err := db.AuthDb.TestsRawSQL(ctx,
		"DELETE FROM auth.users WHERE id = $1",
		userID,
	)
	require.NoError(t, err)
}

func deletePublicUser(t *testing.T, ctx context.Context, db *testutils.Database, userID uuid.UUID) {
	t.Helper()

	err := db.AuthDb.TestsRawSQL(ctx,
		"DELETE FROM public.users WHERE id = $1",
		userID,
	)
	require.NoError(t, err)
}

func insertPublicUser(t *testing.T, ctx context.Context, db *testutils.Database, userID uuid.UUID, email string) {
	t.Helper()

	err := db.AuthDb.TestsRawSQL(ctx, `
INSERT INTO public.users (id, email)
VALUES ($1, $2)
ON CONFLICT (id) DO UPDATE
SET email = EXCLUDED.email,
    updated_at = now()
`,
		userID,
		email,
	)
	require.NoError(t, err)
}

func setPublicUserEmail(t *testing.T, ctx context.Context, db *testutils.Database, userID uuid.UUID, email string) {
	t.Helper()

	err := db.AuthDb.TestsRawSQL(ctx,
		"UPDATE public.users SET email = $1, updated_at = now() WHERE id = $2",
		email,
		userID,
	)
	require.NoError(t, err)
}

func enqueueUserSyncItem(t *testing.T, ctx context.Context, db *testutils.Database, userID uuid.UUID, operation string) {
	t.Helper()

	err := db.AuthDb.TestsRawSQL(ctx,
		"INSERT INTO public.user_sync_queue (user_id, operation) VALUES ($1, $2)",
		userID,
		operation,
	)
	require.NoError(t, err)
}

func lockQueueItems(t *testing.T, ctx context.Context, db *testutils.Database, userID uuid.UUID, lockedAt time.Time, lockOwner string) {
	t.Helper()

	err := db.AuthDb.TestsRawSQL(ctx, `
UPDATE public.user_sync_queue
SET locked_at = $2,
    lock_owner = $3
WHERE user_id = $1
`,
		userID,
		lockedAt,
		lockOwner,
	)
	require.NoError(t, err)
}

func loadPublicUsers(ctx context.Context, db *testutils.Database) (map[uuid.UUID]string, error) {
	users := make(map[uuid.UUID]string)

	err := db.AuthDb.TestsRawSQLQuery(ctx,
		"SELECT id, email FROM public.users",
		func(rows pgx.Rows) error {
			for rows.Next() {
				var userID uuid.UUID
				var email string
				if err := rows.Scan(&userID, &email); err != nil {
					return err
				}

				users[userID] = email
			}

			return rows.Err()
		},
	)
	if err != nil {
		return nil, err
	}

	return users, nil
}

func loadAuthUsers(ctx context.Context, db *testutils.Database) (map[uuid.UUID]string, error) {
	users := make(map[uuid.UUID]string)

	err := db.AuthDb.TestsRawSQLQuery(ctx,
		"SELECT id, email FROM auth.users",
		func(rows pgx.Rows) error {
			for rows.Next() {
				var userID uuid.UUID
				var email string
				if err := rows.Scan(&userID, &email); err != nil {
					return err
				}

				users[userID] = email
			}

			return rows.Err()
		},
	)
	if err != nil {
		return nil, err
	}

	return users, nil
}

func loadQueueSnapshot(ctx context.Context, db *testutils.Database) (queueSnapshot, error) {
	var snapshot queueSnapshot

	err := db.AuthDb.TestsRawSQLQuery(ctx, `
SELECT
	count(*)::int AS total,
	count(*) FILTER (WHERE dead_lettered_at IS NOT NULL)::int AS dead_lettered
FROM public.user_sync_queue
`,
		func(rows pgx.Rows) error {
			if !rows.Next() {
				return nil
			}

			return rows.Scan(&snapshot.Total, &snapshot.DeadLettered)
		},
	)
	if err != nil {
		return queueSnapshot{}, err
	}

	return snapshot, nil
}

func expectedUsersForIDs(userIDs []uuid.UUID, authUsers map[uuid.UUID]string) map[uuid.UUID]userExpectation {
	want := make(map[uuid.UUID]userExpectation, len(userIDs))

	for _, userID := range userIDs {
		email, ok := authUsers[userID]
		want[userID] = userExpectation{
			Email:  email,
			Exists: ok,
		}
	}

	return want
}

func assertQueueBacklog(t *testing.T, ctx context.Context, db *testutils.Database, minimum int) {
	t.Helper()

	snapshot, err := loadQueueSnapshot(ctx, db)
	require.NoError(t, err)
	require.GreaterOrEqual(t, snapshot.Total, minimum)
}

func waitForQueueDrain(t *testing.T, ctx context.Context, db *testutils.Database) {
	t.Helper()

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		snapshot, err := loadQueueSnapshot(ctx, db)
		if !assert.NoError(c, err) {
			return
		}

		assert.Equal(c, 0, snapshot.Total)
		assert.Equal(c, 0, snapshot.DeadLettered)
	}, testEventuallyTimeout, testEventuallyTick)
}

func waitForPublicUsers(t *testing.T, ctx context.Context, db *testutils.Database, want map[uuid.UUID]userExpectation) {
	t.Helper()

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		got, err := loadPublicUsers(ctx, db)
		if !assert.NoError(c, err) {
			return
		}

		var gotExisting int
		var wantExisting int

		for userID, expectation := range want {
			email, ok := got[userID]
			if ok {
				gotExisting++
			}
			if expectation.Exists {
				wantExisting++
			}

			if !assert.Equalf(c, expectation.Exists, ok, "public.users presence for %s", userID) {
				continue
			}
			if expectation.Exists {
				assert.Equalf(c, expectation.Email, email, "public.users email for %s", userID)
			}
		}

		assert.Equal(c, wantExisting, gotExisting)
	}, testEventuallyTimeout, testEventuallyTick)
}
