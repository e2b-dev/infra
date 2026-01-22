package lock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	// defaultLockTTL is the default time-to-live for locks (10 seconds)
	defaultLockTTL = 10 * time.Second
	// lockFileMode is the file mode for lock files
	lockFileMode = 0o644
)

var ErrLockAlreadyHeld = errors.New("lock is already held by another process")

// getLockFilePath generates a lock file path from a key
func getLockFilePath(path string) string {
	return path + ".lock"
}

// TryAcquireLock attempts to acquire a lock for the given key
// Returns (file handle, result, error)
// If result is Acquired, the caller MUST call ReleaseLock with the returned file handle
func TryAcquireLock(ctx context.Context, path string) (*os.File, error) {
	lockPath := getLockFilePath(path)

	// Check if lock file exists and is stale
	if info, err := os.Stat(lockPath); err == nil {
		age := time.Since(info.ModTime())
		if age <= defaultLockTTL {
			return nil, ErrLockAlreadyHeld
		}

		// Lock is stale, try to remove it
		logger.L().Debug(ctx, "Found stale lock file, attempting cleanup",
			zap.String("path", lockPath),
			zap.Duration("age", age))

		// There's a possibility for a race condition,
		// but it would mean write didn't finish in 10 seconds
		// The worst that can happen is more than one node will acquire the lock
		if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to remove lock file %s: %w", lockPath, err)
		}
	}

	// Try to create the lock file (exclusive - fails if it already exists)
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL, lockFileMode)
	if err != nil {
		if os.IsExist(err) {
			return nil, ErrLockAlreadyHeld
		}

		return nil, fmt.Errorf("failed to open lock file: %w", err)
	}

	return file, nil
}

// ReleaseLock releases a previously acquired lock
func ReleaseLock(ctx context.Context, file *os.File) error {
	if file == nil {
		return nil
	}

	lockPath := file.Name()

	// Close the file (which also releases the lock)
	if err := file.Close(); err != nil {
		logger.L().Warn(ctx, "Failed to close lock file",
			zap.String("path", lockPath),
			zap.Error(err))
	}

	// Remove the lock file
	if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
		logger.L().Warn(ctx, "Failed to remove lock file",
			zap.String("path", lockPath),
			zap.Error(err))

		return fmt.Errorf("failed to remove lock file: %w", err)
	}

	return nil
}
