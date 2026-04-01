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
	AckBatch(ctx context.Context, ids []int64) error
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

type ackCandidate struct {
	item   QueueItem
	result processResult
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

	for {
		r.drain(ctx)
		if ctx.Err() != nil {
			r.l.Info(ctx, "stopping supabase auth user sync worker", zap.Error(ctx.Err()))

			return ctx.Err()
		}

		timer := time.NewTimer(r.cfg.PollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}

			r.l.Info(ctx, "stopping supabase auth user sync worker", zap.Error(ctx.Err()))

			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (r *Runner) drain(ctx context.Context) {
	for {
		processed := r.pollOnce(ctx)
		if processed == 0 {
			return
		}
	}
}

func (r *Runner) pollOnce(ctx context.Context) int {
	claimedAt := time.Now()
	items, err := r.store.ClaimBatch(ctx, r.lockOwner, r.cfg.LockTimeout, r.cfg.BatchSize)
	if err != nil {
		r.l.Error(ctx, "failed to claim supabase auth sync queue batch",
			zap.String("worker.lock_owner", r.lockOwner),
			zap.Duration("worker.lock_timeout", r.cfg.LockTimeout),
			zap.Int32("worker.batch_size", r.cfg.BatchSize),
			zap.Error(err),
		)

		return 0
	}

	if len(items) == 0 {
		return 0
	}

	summary := newBatchSummary(items, claimedAt)
	ackCandidates := make([]ackCandidate, 0, len(items))

	for _, item := range items {
		result := r.processor.process(ctx, item)
		if result.Outcome == processOutcomeReadyToAck {
			ackCandidates = append(ackCandidates, ackCandidate{
				item:   item,
				result: result,
			})

			continue
		}

		summary.Add(result)
	}

	if len(ackCandidates) > 0 {
		r.finalizeAcks(ctx, ackCandidates, &summary)
	}

	r.l.Log(ctx, summary.Level(), "processed supabase auth sync queue batch", summary.Fields(time.Since(claimedAt))...)

	return len(items)
}

func (r *Runner) finalizeAcks(ctx context.Context, candidates []ackCandidate, summary *batchSummary) {
	ids := make([]int64, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.item.ID)
	}

	if err := r.store.AckBatch(ctx, ids); err != nil {
		for _, candidate := range candidates {
			candidate.result.Outcome = processOutcomeAckFailed
			summary.Add(candidate.result)

			r.l.Error(ctx, "processed supabase auth sync queue item but failed to ack",
				append(
					processResultFields(candidate.item, candidate.result, time.Now()),
					zap.NamedError("ack_error", err),
				)...,
			)
		}

		return
	}

	for _, candidate := range candidates {
		candidate.result.Outcome = processOutcomeAcked
		summary.Add(candidate.result)
		r.l.Info(ctx, "processed supabase auth sync queue item", processResultFields(candidate.item, candidate.result, time.Now())...)
	}
}
