package supabaseauthusersync

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type Processor struct {
	store       *Store
	maxAttempts int32
	l           logger.Logger
}

func NewProcessor(store *Store, maxAttempts int32, l logger.Logger) *Processor {
	return &Processor{
		store:       store,
		maxAttempts: maxAttempts,
		l:           l,
	}
}

func (p *Processor) Process(ctx context.Context, item QueueItem) {
	err := p.reconcile(ctx, item)

	if err == nil {
		if ackErr := p.store.Ack(ctx, item.ID); ackErr != nil {
			p.l.Error(ctx, "failed to ack queue item",
				zap.Int64("queue_item_id", item.ID),
				zap.String("user_id", item.UserID.String()),
				zap.Error(ackErr),
			)
		}

		return
	}

	p.l.Warn(ctx, "failed to process queue item",
		zap.Int64("queue_item_id", item.ID),
		zap.String("user_id", item.UserID.String()),
		zap.Int32("attempt", item.AttemptCount),
		zap.Error(err),
	)

	if item.AttemptCount >= p.maxAttempts {
		if dlErr := p.store.DeadLetter(ctx, item.ID, err.Error()); dlErr != nil {
			p.l.Error(ctx, "failed to dead-letter queue item",
				zap.Int64("queue_item_id", item.ID),
				zap.Error(dlErr),
			)
		}

		return
	}

	backoff := retryBackoff(item.AttemptCount)

	if retryErr := p.store.Retry(ctx, item.ID, backoff, err.Error()); retryErr != nil {
		p.l.Error(ctx, "failed to retry queue item",
			zap.Int64("queue_item_id", item.ID),
			zap.Error(retryErr),
		)
	}
}

func (p *Processor) reconcile(ctx context.Context, item QueueItem) error {
	authUser, err := p.store.GetAuthUser(ctx, item.UserID)

	if errors.Is(err, pgx.ErrNoRows) {
		if delErr := p.store.DeletePublicUser(ctx, item.UserID); delErr != nil {
			return fmt.Errorf("delete public.users %s: %w", item.UserID, delErr)
		}

		return nil
	}

	if err != nil {
		return fmt.Errorf("get auth.users %s: %w", item.UserID, err)
	}

	if err = p.store.UpsertPublicUser(ctx, authUser.ID, authUser.Email); err != nil {
		return fmt.Errorf("upsert public.users %s: %w", authUser.ID, err)
	}

	return nil
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
