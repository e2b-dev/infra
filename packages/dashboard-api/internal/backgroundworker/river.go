package backgroundworker

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

func RunRiverMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	driver := riverpgxv5.New(pool)

	migrator, err := rivermigrate.New(driver, &rivermigrate.Config{
		Schema: AuthCustomSchema,
	})
	if err != nil {
		return err
	}

	_, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)

	return err
}

func NewRiverClient(pool *pgxpool.Pool, workers *river.Workers) (*river.Client[pgx.Tx], error) {
	return river.NewClient(riverpgxv5.New(pool), &river.Config{
		Schema: AuthCustomSchema,
		Queues: map[string]river.QueueConfig{
			AuthUserProjectionQueue: {MaxWorkers: AuthUserProjectionMaxWorkers},
		},
		Workers: workers,
	})
}
