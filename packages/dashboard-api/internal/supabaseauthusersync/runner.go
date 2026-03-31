package supabaseauthusersync

import (
	"context"
	"time"

	"go.uber.org/zap"

	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	authdb "github.com/e2b-dev/infra/packages/db/pkg/auth"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type runnerStore interface {
	ClaimBatch(ctx context.Context, lockOwner string, lockTimeout time.Duration, batchSize int32) ([]QueueItem, error)
}

type workerStore interface {
	runnerStore
	processorStore
}

type Runner struct {
	cfg       Config
	store     runnerStore
	processor *Processor
	lockOwner string
	l         logger.Logger
}

func NewRunner(cfg Config, authDB *authdb.Client, mainDB *sqlcdb.Client, lockOwner string, l logger.Logger) *Runner {
	workerLogger := l.With(logger.WithServiceInstanceID(lockOwner))
	store := NewStore(authDB, mainDB)

	return &Runner{
		cfg:       cfg,
		store:     store,
		processor: NewProcessor(store, cfg.MaxAttempts, workerLogger),
		lockOwner: lockOwner,
		l:         workerLogger,
	}
}

func (r *Runner) Run(ctx context.Context) error {
	r.l.Info(ctx, "starting supabase auth user sync worker",
		zap.String("worker.lock_owner", r.lockOwner),
		zap.Duration("worker.poll_interval", r.cfg.PollInterval),
		zap.Int32("worker.batch_size", r.cfg.BatchSize),
		zap.Duration("worker.lock_timeout", r.cfg.LockTimeout),
		zap.Int32("worker.max_attempts", r.cfg.MaxAttempts),
	)

	ticker := time.NewTicker(r.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			r.l.Info(ctx, "stopping supabase auth user sync worker", zap.Error(ctx.Err()))

			return ctx.Err()
		case <-ticker.C:
			r.poll(ctx)
		}
	}
}

func (r *Runner) poll(ctx context.Context) {
	claimedAt := time.Now()
	items, err := r.store.ClaimBatch(ctx, r.lockOwner, r.cfg.LockTimeout, r.cfg.BatchSize)
	if err != nil {
		r.l.Error(ctx, "failed to claim supabase auth sync queue batch",
			zap.String("worker.lock_owner", r.lockOwner),
			zap.Duration("worker.lock_timeout", r.cfg.LockTimeout),
			zap.Int32("worker.batch_size", r.cfg.BatchSize),
			zap.Error(err),
		)

		return
	}

	if len(items) == 0 {
		return
	}

	summary := newBatchSummary(items, claimedAt)

	for _, item := range items {
		summary.Add(r.processor.process(ctx, item))
	}

	r.l.Log(ctx, summary.Level(), "processed supabase auth sync queue batch", summary.Fields(time.Since(claimedAt))...)
}
