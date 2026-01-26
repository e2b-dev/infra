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

type operation string

const (
	operationExec  operation = "Exec"
	operationQuery operation = "Query"
	operationScan  operation = "Scan"
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
	// It's just safety check, we shouldn't be calling it from transaction anyway
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

		if !shouldRetry(ctx, attempt, r.config.MaxAttempts, lastErr) {
			return result, lastErr
		}

		logRetry(ctx, operationExec, attempt, r.config.MaxAttempts, lastErr)
		recordRetrySpan(ctx, attempt, lastErr)

		if err := backoffFunc(ctx, attempt, float64(r.config.InitialBackoff), r.config.BackoffMultiplier, float64(r.config.MaxBackoff)); err != nil {
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
		if !shouldRetry(ctx, attempt, r.config.MaxAttempts, lastErr) {
			return rows, lastErr
		}
		logRetry(ctx, operationQuery, attempt, r.config.MaxAttempts, lastErr)
		recordRetrySpan(ctx, attempt, lastErr)
		if err := backoffFunc(ctx, attempt, float64(r.config.InitialBackoff), r.config.BackoffMultiplier, float64(r.config.MaxBackoff)); err != nil {
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

		if !shouldRetry(r.ctx, attempt, r.config.MaxAttempts, lastErr) {
			return lastErr
		}

		logRetry(r.ctx, operationScan, attempt, r.config.MaxAttempts, lastErr)
		recordRetrySpan(r.ctx, attempt, lastErr)

		if err := backoffFunc(r.ctx, attempt, float64(r.config.InitialBackoff), r.config.BackoffMultiplier, float64(r.config.MaxBackoff)); err != nil {
			return lastErr
		}
	}

	return lastErr
}

// shouldRetry determines if we should retry based on error type and attempt count.
func shouldRetry(ctx context.Context, attempt, maxAttempts int, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	if attempt >= maxAttempts {
		return false
	}

	return IsRetriable(err)
}

// backoffFunc waits before the next retry attempt with exponential backoff and jitter.
func backoffFunc(ctx context.Context, attempt int, initialBackoff, backoffMultiplier, maxBackoff float64) error {
	duration := calculateBackoff(initialBackoff, backoffMultiplier, maxBackoff, attempt)

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
func calculateBackoff(initialBackoff, backoffMultiplier, maxBackoff float64, attempt int) time.Duration {
	backoff := initialBackoff
	for i := 1; i < attempt; i++ {
		backoff *= backoffMultiplier
	}

	if backoff > maxBackoff {
		backoff = maxBackoff
	}

	// Add jitter: +/- 25%
	jitter := backoff * 0.25 * (rand.Float64()*2 - 1)
	backoff += jitter

	return time.Duration(backoff)
}

// logRetry logs a retry attempt.
func logRetry(ctx context.Context, operation operation, attempt, maxAttempts int, err error) {
	logger.L().Warn(ctx, "retrying database QueryRow",
		zap.String("operation", string(operation)),
		zap.Int("attempt", attempt),
		zap.Int("max_attempts", maxAttempts),
		zap.Error(err),
	)
}

// recordRetrySpan records retry information in the current span.
func recordRetrySpan(ctx context.Context, attempt int, err error) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}

	span.AddEvent("db.retry", trace.WithAttributes(
		attribute.Int("attempt", attempt),
		attribute.String("error", err.Error()),
	))
}
