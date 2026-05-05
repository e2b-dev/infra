package sandbox_catalog

import (
	"context"
	"errors"
	"time"

	"go.opentelemetry.io/otel"
)

type SandboxInfo struct {
	TeamID         string `json:"team_id"`
	OrchestratorID string `json:"orchestrator_id"`
	OrchestratorIP string `json:"orchestrator_ip"` // used only for cases where orchestrator is not registered in edge pool

	ExecutionID      string     `json:"execution_id"`
	StartedAt        time.Time  `json:"sandbox_started_at"`          // when sandbox was started
	EndTime          time.Time  `json:"sandbox_end_time"`            // when sandbox will expire
	MaxLengthInHours int64      `json:"sandbox_max_length_in_hours"` // how long can sandbox can possibly run (in hours)
	Keepalive        *Keepalive `json:"keepalive,omitempty"`         // policies for refreshing the sandbox timeout
}

type Keepalive struct {
	Traffic *TrafficKeepalive `json:"traffic,omitempty"`
}

const TrafficKeepaliveInterval = time.Minute

type TrafficKeepalive struct {
	Enabled bool `json:"enabled"`
}

type SandboxesCatalog interface {
	GetSandbox(ctx context.Context, sandboxID string) (*SandboxInfo, error)
	StoreSandbox(ctx context.Context, sandboxID string, sandboxInfo *SandboxInfo, expiration time.Duration) error
	AcquireTrafficKeepalive(ctx context.Context, sandboxID string) (bool, error)
	DeleteSandbox(ctx context.Context, sandboxID string, executionID string) error
	Close(ctx context.Context) error
}

type CatalogProvider string

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/shared/pkg/sandbox-catalog")

var ErrSandboxNotFound = errors.New("sandbox not found")
