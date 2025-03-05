package db

import (
	"context"
	"errors"
	"time"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/pkg/models"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/pkg/models/sandbox"
)

type DB struct {
	client *models.Client
}

func (db *DB) Close() error { return db.client.Close() }

func (db *DB) CreateSandbox(ctx context.Context, op func(*models.SandboxCreate)) error {
	return db.WithTx(ctx, func(ctx context.Context) error {
		now := time.Now()
		tx := models.FromContext(ctx)

		global, err := tx.Status.UpdateOneID(1).AddVersion(1).SetUpdatedAt(now).Save(ctx)
		if err != nil {
			return err
		}

		obj := tx.Sandbox.Create()
		op(obj)
		obj.SetVersion(1).SetGlobalVersion(global.Version).SetUpdatedAt(now)
		if _, err := obj.Save(ctx); err != nil {
			return err
		}

		return nil
	})
}

func (db *DB) UpdateSandbox(ctx context.Context, id string, op func(*models.SandboxUpdateOne)) error {
	return db.WithTx(ctx, func(ctx context.Context) error {
		now := time.Now()
		tx := models.FromContext(ctx)

		global, err := tx.Status.UpdateOneID(1).AddVersion(1).SetUpdatedAt(now).Save(ctx)
		if err != nil {
			return err
		}

		obj := tx.Sandbox.UpdateOneID(id)
		if err != nil {
			return err
		}

		prev, err := tx.Sandbox.Get(ctx, id)
		if err != nil {
			return err
		}

		op(obj)
		obj = obj.SetUpdatedAt(now).SetGlobalVersion(global.Version).AddVersion(1)
		switch prev.Status {
		case sandbox.StatusPaused, sandbox.StatusPending, sandbox.StatusTerminated:
			obj = obj.AddDurationMs(now.Sub(prev.UpdatedAt).Milliseconds())
		}

		if _, err := obj.Save(ctx); err != nil {
			return err
		}

		return nil
	})
}

func (db *DB) WithTx(ctx context.Context, op func(context.Context) error) (err error) {
	var tx *models.Tx
	tx, err = db.client.Tx(ctx)
	if err != nil {
		return err
	}
	ctx = models.NewTxContext(ctx, tx)
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

	return op(ctx)
}
