package layer

import (
	"context"
	"time"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	sbxtemplate "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/storage/cache"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

const (
	layerTimeout    = time.Hour
	waitEnvdTimeout = 60 * time.Second
)

// SandboxCreator creates sandboxes for layer building
type SandboxCreator interface {
	Sandbox(
		ctx context.Context,
		layerExecutor *LayerExecutor,
		template sbxtemplate.Template,
	) (*sandbox.Sandbox, error)
}

// ActionExecutor executes actions within a sandbox during layer building
type ActionExecutor interface {
	Execute(ctx context.Context, sbx *sandbox.Sandbox, cmdMeta sandboxtools.CommandMetadata) (sandboxtools.CommandMetadata, error)
}

// LayerBuildCommand encapsulates all parameters needed for building a layer
type LayerBuildCommand struct {
	Hash           string
	SourceLayer    cache.LayerMetadata
	ExportTemplate storage.TemplateFiles
	UpdateEnvd     bool
	SandboxCreator SandboxCreator
	ActionExecutor ActionExecutor
}
