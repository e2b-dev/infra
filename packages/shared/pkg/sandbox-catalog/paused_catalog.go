package sandbox_catalog

import (
	"context"
	"errors"
	"time"
)

type PausedSandboxInfo struct {
	AutoResumePolicy string    `json:"auto_resume_policy"`
	PausedAt         time.Time `json:"paused_at"`
}

type PausedSandboxesCatalog interface {
	GetPaused(ctx context.Context, sandboxID string) (*PausedSandboxInfo, error)
	StorePaused(ctx context.Context, sandboxID string, info *PausedSandboxInfo, expiration time.Duration) error
	DeletePaused(ctx context.Context, sandboxID string) error
	Close(ctx context.Context) error
}

var ErrPausedSandboxNotFound = errors.New("paused sandbox not found")
