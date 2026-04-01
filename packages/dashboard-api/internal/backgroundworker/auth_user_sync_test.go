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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	testEventuallyTimeout = 10 * time.Second
	testEventuallyTick    = 50 * time.Millisecond
	testStopTimeout       = 5 * time.Second
)

type riverProcess struct {
	cancel   context.CancelFunc
	done     chan struct{}
	stopOnce sync.Once
}

func TestAuthUserSync_EndToEnd(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)

	authMigrationsDir := "packages/db/pkg/auth/migrations"

	db.ApplyMigrationsUpTo(t, 20260401000001, authMigrationsDir)

	authPool := db.AuthDb.WritePool()
	require.NoError(t, RunRiverMigrations(t.Context(), authPool))

	db.ApplyMigrations(t, authMigrationsDir)

	runUpsertProjection(t, db)
	runDeleteProjection(t, db)
	runBurstBacklog(t, db)
}

func runUpsertProjection(t *testing.T, db *testutils.Database) {
	t.Helper()

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

	proc.Stop(t)
}

func runDeleteProjection(t *testing.T, db *testutils.Database) {
	t.Helper()

	ctx := t.Context()
	userID := uuid.New()
	email := fmt.Sprintf("river-del-%s@example.com", userID.String()[:8])

	proc := startRiverWorker(t, db)
	t.Cleanup(func() { proc.Stop(t) })

	insertAuthUser(t, ctx, db, userID, email)
	waitForPublicUser(t, ctx, db, userID, email)

	deleteAuthUser(t, ctx, db, userID)
	waitForPublicUserGone(t, ctx, db, userID)

	proc.Stop(t)
}

func runBurstBacklog(t *testing.T, db *testutils.Database) {
	t.Helper()

	ctx := t.Context()
	const userCount = 40

	type testUser struct {
		id        uuid.UUID
		email     string
		shouldDel bool
	}

	users := make([]testUser, 0, userCount)
	for i := range userCount {
		u := testUser{
			id:        uuid.New(),
			email:     fmt.Sprintf("river-burst-%02d@example.com", i),
			shouldDel: i%3 == 0,
		}
		users = append(users, u)
		insertAuthUser(t, ctx, db, u.id, u.email)
	}

	proc := startRiverWorker(t, db)
	t.Cleanup(func() { proc.Stop(t) })

	for _, u := range users {
		waitForPublicUser(t, ctx, db, u.id, u.email)
	}

	for _, u := range users {
		if u.shouldDel {
			deleteAuthUser(t, ctx, db, u.id)
		}
	}

	for _, u := range users {
		if u.shouldDel {
			waitForPublicUserGone(t, ctx, db, u.id)
		} else {
			waitForPublicUser(t, ctx, db, u.id, u.email)
		}
	}

	proc.Stop(t)
}

func startRiverWorker(t *testing.T, db *testutils.Database) *riverProcess {
	t.Helper()

	authPool := db.AuthDb.WritePool()
	l := logger.NewNopLogger()

	workers := river.NewWorkers()
	river.AddWorker(workers, NewAuthUserSyncWorker(db.SqlcClient, l))

	client, err := NewRiverClient(authPool, workers)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
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

	err := db.AuthDb.TestsRawSQL(ctx,
		"INSERT INTO auth.users (id, email) VALUES ($1, $2)", userID, email)
	require.NoError(t, err)
}

func updateAuthUserEmail(t *testing.T, ctx context.Context, db *testutils.Database, userID uuid.UUID, email string) {
	t.Helper()

	err := db.AuthDb.TestsRawSQL(ctx,
		"UPDATE auth.users SET email = $1 WHERE id = $2", email, userID)
	require.NoError(t, err)
}

func deleteAuthUser(t *testing.T, ctx context.Context, db *testutils.Database, userID uuid.UUID) {
	t.Helper()

	err := db.AuthDb.TestsRawSQL(ctx,
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
		var count int

		err := db.AuthDb.TestsRawSQLQuery(ctx,
			"SELECT count(*) FROM public.users WHERE id = $1",
			func(rows pgx.Rows) error {
				if !rows.Next() {
					return nil
				}

				return rows.Scan(&count)
			}, userID)

		if !assert.NoError(c, err) {
			return
		}

		assert.Equal(c, 0, count)
	}, testEventuallyTimeout, testEventuallyTick)
}
