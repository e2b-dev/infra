package sandboxes

import (
	"errors"
	"time"
)

type SandboxInfo struct {
	OrchestratorId string `json:"orchestrator_id"`
	TemplateId     string `json:"template_id"`

	SandboxStartedAt        time.Time `json:"sandbox_started_at"`          // when sandbox was started
	SandboxMaxLengthInHours int64     `json:"sandbox_max_length_in_hours"` // how long can sandbox can possibly run (in hours)
}

type SandboxesCatalog interface {
	GetSandbox(sandboxId string) (*SandboxInfo, error)
	StoreSandbox(sandboxId string, sandboxInfo *SandboxInfo, expiration time.Duration) error
	DeleteSandbox(sandboxId string) error
}

type CatalogProvider string

const (
	// We want to have some buffer so redis ttl will not expire exactly before api will try to shut down or do some other operation
	// with sandbox running behind edge node. For resume this should not be problem because for both redis and memory backed catalogs
	// we will re-write sandbox info with new one and local machine-level cache is tiny.
	sandboxTtlBuffer = 1 * time.Minute
)

var ErrSandboxNotFound = errors.New("sandbox not found")
