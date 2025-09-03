package layer

import (
	"context"
	"time"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	sbxtemplate "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
)

const (
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
	Execute(ctx context.Context, sbx *sandbox.Sandbox, meta metadata.Template) (metadata.Template, error)
}

// SourceTemplateProvider provides the source template for the layer build
type SourceTemplateProvider interface {
	Get(ctx context.Context, templateCache *sbxtemplate.Cache) (sbxtemplate.Template, error)
}

// LayerBuildCommand encapsulates all parameters needed for building a layer
type LayerBuildCommand struct {
	SourceTemplate SourceTemplateProvider
	CurrentLayer   metadata.Template
	Hash           string
	UpdateEnvd     bool
	SandboxCreator SandboxCreator
	ActionExecutor ActionExecutor
}
