package limits

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/limits/queries"
	"github.com/google/uuid"
)

const retries = 50
const maxDelay = 50 * time.Millisecond

type SQLiteLimiter struct {
	queries *queries.Queries
}

func New(db queries.DBTX) *SQLiteLimiter {
	return &SQLiteLimiter{queries: queries.New(db)}
}

var ErrTimeout = errors.New("timeout")

func (l *SQLiteLimiter) TryAcquire(ctx context.Context, key string, limit int64) error {
	setID := uuid.NewString()

	return retryBusy(retries, maxDelay, func() error {
		result, err := l.queries.Acquire(ctx, queries.AcquireParams{
			Key:   key,
			Setid: setID,
		})
		if err != nil {
			return err
		}

		rowsAffected, err := result.RowsAffected()
		if errors.Is(err, sql.ErrNoRows) {
			return ErrRetry
		}

		if rowsAffected == 1 {
			return nil
		}

		return ErrRetry
	})
}

func (l *SQLiteLimiter) Release(ctx context.Context, key string) error {
	return retryBusy(retries, maxDelay, func() error {
		return l.queries.Release(ctx, key)
	})
}
