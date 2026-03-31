package supabaseauthusersync

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type processorStore interface {
	Ack(ctx context.Context, id int64) error
	Retry(ctx context.Context, id int64, backoff time.Duration, lastError string) error
	DeadLetter(ctx context.Context, id int64, lastError string) error
	GetAuthUser(ctx context.Context, userID uuid.UUID) (*AuthUser, error)
	UpsertPublicUser(ctx context.Context, id uuid.UUID, email string) error
	DeletePublicUser(ctx context.Context, id uuid.UUID) error
}

type Processor struct {
	store       processorStore
	maxAttempts int32
	l           logger.Logger
}

func NewProcessor(store processorStore, maxAttempts int32, l logger.Logger) *Processor {
	return &Processor{
		store:       store,
		maxAttempts: maxAttempts,
		l:           l,
	}
}

func (p *Processor) process(ctx context.Context, item QueueItem) processResult {
	startedAt := time.Now()
	action, err := p.processOnce(ctx, item)
	result := processResult{
		Action:   action,
		Duration: time.Since(startedAt),
	}

	if err == nil {
		if ackErr := p.store.Ack(ctx, item.ID); ackErr != nil {
			result.Outcome = processOutcomeAckFailed

			p.l.Error(ctx, "processed supabase auth sync queue item but failed to ack",
				append(
					processResultFields(item, result, time.Now()),
					zap.NamedError("ack_error", ackErr),
				)...,
			)

			return result
		}

		result.Outcome = processOutcomeAcked
		p.l.Info(ctx, "processed supabase auth sync queue item", processResultFields(item, result, time.Now())...)

		return result
	}

	if item.AttemptCount >= p.maxAttempts {
		if dlErr := p.store.DeadLetter(ctx, item.ID, err.Error()); dlErr != nil {
			result.Outcome = processOutcomeDeadLetterFailed

			p.l.Error(ctx, "failed to dead-letter supabase auth sync queue item",
				append(
					processResultFields(item, result, time.Now()),
					zap.Int32("queue_item.max_attempts", p.maxAttempts),
					zap.NamedError("processing_error", err),
					zap.NamedError("dead_letter_error", dlErr),
				)...,
			)

			return result
		}

		result.Outcome = processOutcomeDeadLettered
		p.l.Error(ctx, "dead-lettered supabase auth sync queue item after max attempts",
			append(
				processResultFields(item, result, time.Now()),
				zap.Int32("queue_item.max_attempts", p.maxAttempts),
				zap.NamedError("processing_error", err),
			)...,
		)

		return result
	}

	backoff := retryBackoff(item.AttemptCount)
	result.Outcome = processOutcomeRetried
	result.Backoff = backoff

	if retryErr := p.store.Retry(ctx, item.ID, backoff, err.Error()); retryErr != nil {
		result.Outcome = processOutcomeRetryFailed

		p.l.Error(ctx, "failed to schedule supabase auth sync queue item retry",
			append(
				processResultFields(item, result, time.Now()),
				zap.NamedError("processing_error", err),
				zap.NamedError("retry_error", retryErr),
			)...,
		)

		return result
	}

	p.l.Warn(ctx, "retrying supabase auth sync queue item after processing error",
		append(
			processResultFields(item, result, time.Now()),
			zap.NamedError("processing_error", err),
		)...,
	)

	return result
}

func (p *Processor) processOnce(ctx context.Context, item QueueItem) (action reconcileAction, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			p.l.Error(ctx, "panic while processing supabase auth sync queue item",
				append(
					queueItemFields(item, time.Now()),
					zap.String("worker.panic", fmt.Sprint(recovered)),
					zap.String("worker.stack", string(debug.Stack())),
				)...,
			)

			err = fmt.Errorf("panic while processing queue item: %v", recovered)
		}
	}()

	return p.reconcile(ctx, item)
}

func (p *Processor) reconcile(ctx context.Context, item QueueItem) (reconcileAction, error) {
	authUser, err := p.store.GetAuthUser(ctx, item.UserID)

	if errors.Is(err, pgx.ErrNoRows) {
		if delErr := p.store.DeletePublicUser(ctx, item.UserID); delErr != nil {
			return "", fmt.Errorf("delete public.users %s: %w", item.UserID, delErr)
		}

		return reconcileActionDeletePublicUser, nil
	}

	if err != nil {
		return "", fmt.Errorf("get auth.users %s: %w", item.UserID, err)
	}

	if err = p.store.UpsertPublicUser(ctx, authUser.ID, authUser.Email); err != nil {
		return "", fmt.Errorf("upsert public.users %s: %w", authUser.ID, err)
	}

	return reconcileActionUpsertPublicUser, nil
}

func retryBackoff(attempt int32) time.Duration {
	switch {
	case attempt <= 1:
		return 5 * time.Second
	case attempt <= 3:
		return 30 * time.Second
	case attempt <= 6:
		return 2 * time.Minute
	case attempt <= 10:
		return 5 * time.Minute
	default:
		return 15 * time.Minute
	}
}
