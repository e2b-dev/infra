package sandboxes

import (
	"errors"
	"time"
)

type SandboxInfo struct {
	OrchestratorId string `json:"orchestrator_id"`
	TemplateId     string `json:"template_id"`
}

type SandboxesCatalog interface {
	GetSandbox(sandboxId string) (*SandboxInfo, error)
	StoreSandbox(sandboxId string, sandboxInfo *SandboxInfo) error
	DeleteSandbox(sandboxId string) error
}

type CatalogProvider string

const (
	catalogCacheExpiration = time.Hour * 24
)

var (
	ErrSandboxNotFound = errors.New("sandbox not found")
)
