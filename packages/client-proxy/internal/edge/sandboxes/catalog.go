package sandboxes

import (
	"errors"
	"time"
)

type SandboxInfo struct {
	OrchestratorId   string `json:"orchestrator_id"`
	TemplateId       string `json:"template_id"`

	// how long can sandbox can possibly run (in seconds)
	MaxSandboxLength int64  `json:"max_sandbox_length"`
}

type SandboxesCatalog interface {
	GetSandbox(sandboxId string) (*SandboxInfo, error)
	StoreSandbox(sandboxId string, sandboxInfo *SandboxInfo, expiration time.Duration) error
	DeleteSandbox(sandboxId string) error
}

type CatalogProvider string

var (
	ErrSandboxNotFound = errors.New("sandbox not found")
)
