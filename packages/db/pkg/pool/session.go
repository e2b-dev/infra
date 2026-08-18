package pool

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	sessionReleaseTimeout = 5 * time.Second
	serializableAttempts  = 10
	serializableRetryStep = 5 * time.Millisecond
)

var (
	errLockReleased = errors.New("advisory lock has been released")
	errLockNotHeld  = errors.New("database advisory lock was not held")
)

// ErrAdvisoryLockBusy reports that a non-blocking lock attempt found a holder.
var ErrAdvisoryLockBusy = errors.New("database advisory lock is held")

// AdvisoryLock is a PostgreSQL session lock held on one pool connection. One
// lock belongs to one goroutine and must be released when its work finishes.
type AdvisoryLock struct {
	key  string
	conn *pgxpool.Conn
}

// AcquireAdvisoryLock waits for a session lock derived from key. Hash
// collisions may serialize unrelated callers, but cannot weaken exclusion.
func (c *Client) AcquireAdvisoryLock(ctx context.Context, key string) (*AdvisoryLock, error) {
	conn, err := c.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire an advisory lock connection: %w", err)
	}

	if _, err := conn.Exec(ctx,
		"SELECT pg_advisory_lock(hashtextextended($1, 0))", key,
	); err != nil {
		// The server may have granted the lock before cancellation reached the
		// client, so an ambiguous session must not return to the pool.
		discard(ctx, conn)

		return nil, fmt.Errorf("acquire an advisory lock: %w", err)
	}

	return &AdvisoryLock{key: key, conn: conn}, nil
}

// TryAcquireAdvisoryLock takes the same session lock without waiting.
func (c *Client) TryAcquireAdvisoryLock(ctx context.Context, key string) (*AdvisoryLock, error) {
	conn, err := c.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire an advisory lock connection: %w", err)
	}

	var locked bool
	if err := conn.QueryRow(ctx,
		"SELECT pg_try_advisory_lock(hashtextextended($1, 0))", key,
	).Scan(&locked); err != nil {
		discard(ctx, conn)

		return nil, fmt.Errorf("try an advisory lock: %w", err)
	}
	if !locked {
		conn.Release()

		return nil, ErrAdvisoryLockBusy
	}

	return &AdvisoryLock{key: key, conn: conn}, nil
}

// InSerializableTx runs fn in a SERIALIZABLE transaction on the session that
// holds the lock. Serialization failures and deadlocks replay the whole
// callback, so fn must not perform work outside PostgreSQL.
func (l *AdvisoryLock) InSerializableTx(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	if l.conn == nil {
		return errLockReleased
	}

	var conflict error
	for attempt := 1; attempt <= serializableAttempts; attempt++ {
		err := runInTx(ctx, l.conn, fn)
		switch {
		case err == nil:
			return nil
		case isSerializationConflict(err):
			conflict = err
		default:
			return err
		}

		if attempt == serializableAttempts {
			break
		}
		if err := waitToReplay(ctx, attempt); err != nil {
			return errors.Join(err, conflict)
		}
	}

	return fmt.Errorf("serializable transaction did not commit in %d attempts: %w",
		serializableAttempts, conflict)
}

func runInTx(ctx context.Context, conn *pgxpool.Conn, fn func(context.Context, pgx.Tx) error) error {
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin a serializable transaction: %w", err)
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sessionReleaseTimeout)
		defer cancel()

		_ = tx.Rollback(releaseCtx)
	}()

	if err := fn(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit a serializable transaction: %w", err)
	}

	return nil
}

func isSerializationConflict(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}

	return pgErr.Code == pgerrcode.SerializationFailure || pgErr.Code == pgerrcode.DeadlockDetected
}

func waitToReplay(ctx context.Context, attempt int) error {
	timer := time.NewTimer(serializableRetryStep * time.Duration(attempt))
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Release unlocks the session and returns its connection to the pool. It is
// idempotent. If the unlock result is unknown, Release destroys the session so
// a connection carrying an unknown lock cannot re-enter the pool.
func (l *AdvisoryLock) Release(ctx context.Context) error {
	conn := l.conn
	if conn == nil {
		return nil
	}
	l.conn = nil

	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sessionReleaseTimeout)
	defer cancel()

	var unlocked bool
	err := conn.QueryRow(releaseCtx,
		"SELECT pg_advisory_unlock(hashtextextended($1, 0))", l.key,
	).Scan(&unlocked)

	switch {
	case err != nil:
		discard(ctx, conn)

		return fmt.Errorf("release an advisory lock: %w", err)
	case !unlocked:
		discard(ctx, conn)

		return errLockNotHeld
	}

	conn.Release()

	return nil
}

func discard(ctx context.Context, conn *pgxpool.Conn) {
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sessionReleaseTimeout)
	defer cancel()

	_ = conn.Hijack().Close(releaseCtx)
}
