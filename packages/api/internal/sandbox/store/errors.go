package store

import "errors"

var (
	ErrSandboxNotFound         = errors.New("sandbox not found")
	ErrMaxSandboxUptimeReached = errors.New("maximum sandbox uptime reached")
)
