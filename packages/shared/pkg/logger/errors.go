package logger

import (
	"errors"
	"syscall"
)

// IsSyncError checks if the error from Sync is not EINVAL.
// Sync returns EINVAL when path is /dev/stdout (for example)
func IsSyncError(err error) bool {
	return err != nil && !errors.Is(err, syscall.EINVAL)
}
