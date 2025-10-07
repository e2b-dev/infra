package limits

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/limits/queries"
	"github.com/google/uuid"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

const retries = 50
const maxDelay = 5 * time.Millisecond

type SQLiteLimiter struct {
	queries *queries.Queries
}

func New(db queries.DBTX) *SQLiteLimiter {
	return &SQLiteLimiter{queries: queries.New(db)}
}

var ErrTimeout = errors.New("timeout")

func (l *SQLiteLimiter) TryAcquire(ctx context.Context, key string, limit int64) error {
	setID := uuid.NewString()

	for range retries {
		_, err := l.queries.Acquire(ctx, queries.AcquireParams{
			Key:   key,
			Setid: setID,
		})

		var sqliteError *sqlite.Error
		if errors.As(err, &sqliteError) && sqliteError.Code() == sqlite3.SQLITE_BUSY {
			// SQLITE_BUSY
			time.Sleep(time.Duration(rand.Intn(10)) * time.Millisecond)
			continue
		}

		if errors.Is(err, sql.ErrNoRows) {
			continue
		}

		if err != nil {
			return fmt.Errorf("failed to acquire item: %w", err)
		}

		return nil
	}

	return ErrTimeout
}

func (l *SQLiteLimiter) Release(ctx context.Context, key string) error {
	if err := l.queries.Release(ctx, key); err != nil {
		return fmt.Errorf("failed to release item")
	}
	return nil
}
