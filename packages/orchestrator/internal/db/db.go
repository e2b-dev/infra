package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/pkg/database"
	// "github.com/e2b-dev/infra/packages/orchestrator/internal/pkg/models"
	// "github.com/e2b-dev/infra/packages/orchestrator/internal/pkg/models/sandbox"
)

type DB struct {
	client *sql.DB
	ops    *database.Queries
}

func New(ctx context.Context, client *sql.DB) (*DB, error) {
	db := &DB{client: client, ops: database.New(client)}

	if err := db.ops.SetOrchestratorStatusRunning(ctx); err != nil {
		return nil, err
	}

	return db, nil
}

func (db *DB) Close(ctx context.Context) error {
	return errors.Join(db.ops.SetOrchestratorStatusTerminated(ctx), db.client.Close())
}

func (db *DB) CreateSandbox(ctx context.Context, params database.CreateSandboxParams) error {
	return db.WithTx(ctx, func(ctx context.Context, op *database.Queries) error {
		if _, err := op.IncGlobalVersion(ctx); err != nil {
			return err
		}

		if err := op.CreateSandbox(ctx, params); err != nil {
			return err
		}
		return nil
	})
}

func (db *DB) UpdateSandboxDeadline(ctx context.Context, id string, deadline time.Time) error {
	return db.WithTx(ctx, func(ctx context.Context, op *database.Queries) error {
		if _, err := op.IncGlobalVersion(ctx); err != nil {
			return err
		}

		if err := op.UpdateSandboxDeadline(ctx, database.UpdateSandboxDeadlineParams{ID: id, Deadline: deadline}); err != nil {
			return err
		}

		return nil
	})
}

func (db *DB) SetSandboxTerminated(ctx context.Context, id string, duration time.Duration) error {
	return db.WithTx(ctx, func(ctx context.Context, op *database.Queries) error {
		if _, err := op.IncGlobalVersion(ctx); err != nil {
			return err
		}

		if err := op.ShutdownSandbox(ctx, database.ShutdownSandboxParams{
			ID:         id,
			DurationMs: duration.Milliseconds(),
			Status:     database.SandboxStatusTerminated,
		}); err != nil {
			return err
		}

		return nil
	})
}

func (db *DB) SetSandboxPaused(ctx context.Context, id string, duration time.Duration) error {
	return db.WithTx(ctx, func(ctx context.Context, op *database.Queries) error {
		if _, err := op.IncGlobalVersion(ctx); err != nil {
			return err
		}

		if err := op.ShutdownSandbox(ctx, database.ShutdownSandboxParams{
			ID:         id,
			DurationMs: duration.Milliseconds(),
			Status:     database.SandboxStatusPaused,
		}); err != nil {
			return err
		}

		return nil
	})
}

func (db *DB) WithTx(ctx context.Context, op func(context.Context, *database.Queries) error) (err error) {
	tx, err := db.client.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()
	defer func() {
		if p := recover(); p != nil {
			tx.Rollback()
			panic(p)
		}
	}()
	defer func() {
		if err == nil {
			err = tx.Commit()
		}
	}()

	return op(ctx, db.ops.WithTx(tx))
}
