package retry

import (
	"context"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// DBTX is the interface that sqlc expects for database operations.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// RetryableDBTX wraps a DBTX with retry logic.
type RetryableDBTX struct {
	db     DBTX
	config Config
}

// Wrap wraps a DBTX with retry logic using the provided options.
func Wrap(db DBTX, opts ...Option) DBTX {
	// Don't wrap if it's already a transaction - retries are unsafe in transactions
	if _, ok := db.(pgx.Tx); ok {
		return db
	}

	config := DefaultConfig()
	config.Apply(opts...)

	return &RetryableDBTX{
		db:     db,
		config: config,
	}
}

// Exec executes a query with retry logic.
func (r *RetryableDBTX) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	var result pgconn.CommandTag
	var lastErr error

	for attempt := 1; attempt <= r.config.MaxAttempts; attempt++ {
		result, lastErr = r.db.Exec(ctx, sql, args...)
		if lastErr == nil {
			return result, nil
		}
		if !r.shouldRetry(ctx, lastErr, attempt) {
			return result, lastErr
		}
		r.logRetry(ctx, "Exec", attempt, lastErr)
		r.recordRetrySpan(ctx, attempt, lastErr)
		if err := r.backoff(ctx, attempt); err != nil {
			return result, lastErr
		}
	}

	return result, lastErr
}

// Query executes a query that returns rows with retry logic.
// The caller is responsible for closing the returned rows.
func (r *RetryableDBTX) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	var rows pgx.Rows
	var lastErr error

	for attempt := 1; attempt <= r.config.MaxAttempts; attempt++ {
		var err error

		rows, err = r.db.Query(ctx, sql, args...)
		if err == nil {
			return rows, nil
		}
		lastErr = err
		if !r.shouldRetry(ctx, lastErr, attempt) {
			return rows, lastErr
		}
		r.logRetry(ctx, "Query", attempt, lastErr)
		r.recordRetrySpan(ctx, attempt, lastErr)
		if err := r.backoff(ctx, attempt); err != nil {
			return rows, lastErr
		}
	}

	return rows, lastErr
}

// QueryRow executes a query that returns a single row with retry logic.
// Since pgx.Row doesn't expose errors until Scan(), we wrap it in a retryableRow.
func (r *RetryableDBTX) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return &retryableRow{
		ctx:    ctx,
		sql:    sql,
		args:   args,
		db:     r.db,
		config: r.config,
	}
}

// logRetry logs a retry attempt.
func (r *RetryableDBTX) logRetry(ctx context.Context, operation string, attempt int, err error) {
	logger.L().Warn(ctx, "retrying database operation",
		zap.String("operation", operation),
		zap.Int("attempt", attempt),
		zap.Int("max_attempts", r.config.MaxAttempts),
		zap.Error(err),
	)
}

// recordRetrySpan records retry information in the current span.
func (r *RetryableDBTX) recordRetrySpan(ctx context.Context, attempt int, err error) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}

	span.AddEvent("db.retry", trace.WithAttributes(
		attribute.Int("attempt", attempt),
		attribute.String("error", err.Error()),
	))
}

// shouldRetry determines if we should retry based on error type and attempt count.
func (r *RetryableDBTX) shouldRetry(ctx context.Context, err error, attempt int) bool {
	if ctx.Err() != nil {
		return false
	}
	if attempt >= r.config.MaxAttempts {
		return false
	}

	return IsRetriable(err)
}

// backoff waits before the next retry attempt with exponential backoff and jitter.
func (r *RetryableDBTX) backoff(ctx context.Context, attempt int) error {
	duration := r.calculateBackoff(attempt)

	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// calculateBackoff computes the backoff duration for a given attempt.
func (r *RetryableDBTX) calculateBackoff(attempt int) time.Duration {
	backoff := float64(r.config.InitialBackoff)
	for i := 1; i < attempt; i++ {
		backoff *= r.config.BackoffMultiplier
	}

	if backoff > float64(r.config.MaxBackoff) {
		backoff = float64(r.config.MaxBackoff)
	}

	// Add jitter: +/- 25%
	jitter := backoff * 0.25 * (rand.Float64()*2 - 1)
	backoff += jitter

	return time.Duration(backoff)
}

// retryableRow wraps a pgx.Row to provide retry logic.
// Since pgx.Row.Scan() is where errors are surfaced, we need to retry the entire query.
type retryableRow struct {
	ctx    context.Context //nolint:containedctx // Context must be stored for deferred Scan() retry
	sql    string
	args   []any
	db     DBTX
	config Config
}

// Scan implements pgx.Row.Scan with retry logic.
func (r *retryableRow) Scan(dest ...any) error {
	var lastErr error

	for attempt := 1; attempt <= r.config.MaxAttempts; attempt++ {
		row := r.db.QueryRow(r.ctx, r.sql, r.args...)
		lastErr = row.Scan(dest...)
		if lastErr == nil {
			return nil
		}
		if !r.shouldRetry(lastErr, attempt) {
			return lastErr
		}
		r.logRetry(attempt, lastErr)
		r.recordRetrySpan(attempt, lastErr)
		if err := r.backoff(attempt); err != nil {
			return lastErr
		}
	}

	return lastErr
}

// shouldRetry determines if we should retry based on error type and attempt count.
func (r *retryableRow) shouldRetry(err error, attempt int) bool {
	if r.ctx.Err() != nil {
		return false
	}
	if attempt >= r.config.MaxAttempts {
		return false
	}

	return IsRetriable(err)
}

// backoff waits before the next retry attempt with exponential backoff and jitter.
func (r *retryableRow) backoff(attempt int) error {
	duration := r.calculateBackoff(attempt)

	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-r.ctx.Done():
		return r.ctx.Err()
	case <-timer.C:
		return nil
	}
}

// calculateBackoff computes the backoff duration for a given attempt.
func (r *retryableRow) calculateBackoff(attempt int) time.Duration {
	backoff := float64(r.config.InitialBackoff)
	for i := 1; i < attempt; i++ {
		backoff *= r.config.BackoffMultiplier
	}

	if backoff > float64(r.config.MaxBackoff) {
		backoff = float64(r.config.MaxBackoff)
	}

	// Add jitter: +/- 25%
	jitter := backoff * 0.25 * (rand.Float64()*2 - 1)
	backoff += jitter

	return time.Duration(backoff)
}

// logRetry logs a retry attempt.
func (r *retryableRow) logRetry(attempt int, err error) {
	logger.L().Warn(r.ctx, "retrying database QueryRow",
		zap.Int("attempt", attempt),
		zap.Int("max_attempts", r.config.MaxAttempts),
		zap.Error(err),
	)
}

// recordRetrySpan records retry information in the current span.
func (r *retryableRow) recordRetrySpan(attempt int, err error) {
	span := trace.SpanFromContext(r.ctx)
	if !span.IsRecording() {
		return
	}

	span.AddEvent("db.retry", trace.WithAttributes(
		attribute.Int("attempt", attempt),
		attribute.String("error", err.Error()),
	))
}
