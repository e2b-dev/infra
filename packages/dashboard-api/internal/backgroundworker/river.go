package backgroundworker

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"go.uber.org/zap"

	authdb "github.com/e2b-dev/infra/packages/db/pkg/auth"
	supabasedb "github.com/e2b-dev/infra/packages/db/pkg/supabase"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func RunRiverMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	driver := riverpgxv5.New(pool)

	migrator, err := rivermigrate.New(driver, &rivermigrate.Config{
		Schema: authCustomSchema,
	})
	if err != nil {
		return err
	}

	_, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)

	return err
}

func NewRiverClient(pool *pgxpool.Pool, workers *river.Workers) (*river.Client[pgx.Tx], error) {
	return river.NewClient(riverpgxv5.New(pool), &river.Config{
		Schema: authCustomSchema,
		Queues: map[string]river.QueueConfig{
			authUserProjectionQueue: {MaxWorkers: authUserProjectionMaxWorkers},
		},
		Workers: workers,
	})
}

func StartAuthUserSyncWorker(setupCtx, runCtx context.Context, supabaseDB *supabasedb.Client, authDB *authdb.Client, l logger.Logger) (*river.Client[pgx.Tx], error) {
	if err := RunRiverMigrations(setupCtx, supabaseDB.WritePool()); err != nil {
		return nil, fmt.Errorf("run River migrations on supabase DB: %w", err)
	}

	workerLogger := l.With(zap.String("worker", authUserProjectionKind))

	workers := river.NewWorkers()
	river.AddWorker(workers, NewAuthUserSyncWorker(setupCtx, supabaseDB, authDB, workerLogger))

	riverClient, err := NewRiverClient(supabaseDB.WritePool(), workers)
	if err != nil {
		return nil, fmt.Errorf("create River client: %w", err)
	}

	if err := riverClient.Start(runCtx); err != nil {
		return nil, fmt.Errorf("start River client: %w", err)
	}

	l.Info(setupCtx, "background worker started",
		zap.String("queue", authUserProjectionQueue),
		zap.String("schema", authCustomSchema),
	)

	return riverClient, nil
}
