package lock

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"go.uber.org/zap"
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
func TryAcquireLock(path string) (*os.File, error) {
	lockPath := getLockFilePath(path)

	// Check if lock file exists and is stale
	if info, err := os.Stat(lockPath); err == nil {
		age := time.Since(info.ModTime())
		if age > defaultLockTTL {
			// Lock is stale, try to remove it
			zap.L().Debug("Found stale lock file, attempting cleanup",
				zap.String("path", path),
				zap.String("lock_path", lockPath),
				zap.Duration("age", age))

			if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
				zap.L().Warn("Failed to remove stale lock file",
					zap.String("path", path),
					zap.Error(err))
			}
		}
	}

	// Try to open or create the lock file
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, lockFileMode)
	if err != nil {
		return nil, fmt.Errorf("failed to open lock file: %w", err)
	}

	// Try to acquire an exclusive lock (non-blocking)
	err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrLockAlreadyHeld
		}

		return nil, fmt.Errorf("failed to acquire lock: %w", err)
	}

	// Write timestamp to lock file for TTL tracking
	timestamp := []byte(fmt.Sprintf("%d", time.Now().Unix()))
	if err := file.Truncate(0); err != nil {
		_ = file.Close()

		return nil, fmt.Errorf("failed to truncate lock file: %w", err)
	}
	if _, err := file.WriteAt(timestamp, 0); err != nil {
		_ = file.Close()

		return nil, fmt.Errorf("failed to write timestamp: %w", err)
	}

	// Update file modification time
	now := time.Now()
	if err := os.Chtimes(lockPath, now, now); err != nil {
		_ = file.Close()

		return nil, fmt.Errorf("failed to update lock file mtime: %w", err)
	}

	return file, nil
}

// ReleaseLock releases a previously acquired lock
func ReleaseLock(file *os.File) error {
	if file == nil {
		return nil
	}

	lockPath := file.Name()

	// Release the flock
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_UN); err != nil {
		zap.L().Warn("Failed to release flock",
			zap.String("lock_path", lockPath),
			zap.Error(err))
	}

	// Close the file
	if err := file.Close(); err != nil {
		zap.L().Warn("Failed to close lock file",
			zap.String("lock_path", lockPath),
			zap.Error(err))
	}

	// Remove the lock file
	if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
		zap.L().Warn("Failed to remove lock file",
			zap.String("lock_path", lockPath),
			zap.Error(err))

		return fmt.Errorf("failed to remove lock file: %w", err)
	}

	zap.L().Debug("Lock released successfully",
		zap.String("lock_path", lockPath))

	return nil
}
