package supabaseauthusersync

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type Runner struct {
	cfg       Config
	store     *Store
	processor *Processor
	lockOwner string
	l         logger.Logger
}

func NewRunner(cfg Config, store *Store, lockOwner string, l logger.Logger) *Runner {
	return &Runner{
		cfg:       cfg,
		store:     store,
		processor: NewProcessor(store, cfg.MaxAttempts, l),
		lockOwner: lockOwner,
		l:         l,
	}
}

func (r *Runner) Run(ctx context.Context) error {
	r.l.Info(ctx, "starting supabase auth user sync worker",
		zap.String("lock_owner", r.lockOwner),
		zap.Duration("poll_interval", r.cfg.PollInterval),
		zap.Int32("batch_size", r.cfg.BatchSize),
	)

	ticker := time.NewTicker(r.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			r.l.Info(ctx, "stopping supabase auth user sync worker")

			return ctx.Err()
		case <-ticker.C:
			r.poll(ctx)
		}
	}
}

func (r *Runner) poll(ctx context.Context) {
	items, err := r.store.ClaimBatch(ctx, r.lockOwner, r.cfg.LockTimeout, r.cfg.BatchSize)
	if err != nil {
		r.l.Error(ctx, "failed to claim queue batch", zap.Error(err))

		return
	}

	if len(items) == 0 {
		return
	}

	r.l.Debug(ctx, "claimed queue batch", zap.Int("count", len(items)))

	for _, item := range items {
		r.processor.Process(ctx, item)
	}
}
