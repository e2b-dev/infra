package backgroundworker

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
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

func StartAuthUserSyncWorker(setupCtx, runCtx context.Context, authPool *pgxpool.Pool, mainDB *sqlcdb.Client, meterProvider metric.MeterProvider, l logger.Logger) (*river.Client[pgx.Tx], error) {
	if err := RunRiverMigrations(setupCtx, authPool); err != nil {
		return nil, fmt.Errorf("run River migrations on auth DB: %w", err)
	}

	workerLogger := l.With(zap.String("worker", authUserProjectionKind))
	workerMeter := meterProvider.Meter(workerMeterName)

	workers := river.NewWorkers()
	river.AddWorker(workers, NewAuthUserSyncWorker(setupCtx, mainDB, workerMeter, workerLogger))

	riverClient, err := NewRiverClient(authPool, workers)
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
