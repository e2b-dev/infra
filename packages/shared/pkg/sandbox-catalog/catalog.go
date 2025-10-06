package sandbox_catalog

import (
	"context"
	"errors"
	"time"
)

type SandboxInfo struct {
	OrchestratorID string `json:"orchestrator_id"`
	ExecutionID    string `json:"execution_id"`

	SandboxStartedAt        time.Time `json:"sandbox_started_at"`          // when sandbox was started
	SandboxMaxLengthInHours int64     `json:"sandbox_max_length_in_hours"` // how long can sandbox can possibly run (in hours)
}

type SandboxesCatalog interface {
	GetSandbox(ctx context.Context, sandboxID string) (*SandboxInfo, error)
	StoreSandbox(ctx context.Context, sandboxID string, sandboxInfo *SandboxInfo, expiration time.Duration) error
	DeleteSandbox(ctx context.Context, sandboxID string, executionID string) error
}

type CatalogProvider string

var ErrSandboxNotFound = errors.New("sandbox not found")
