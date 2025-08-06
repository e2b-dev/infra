package layer

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	sbxtemplate "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/config"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// ResumeSandbox creates sandboxes for resuming existing templates
type ResumeSandbox struct {
	config sandbox.Config
}

func NewResumeSandbox(config sandbox.Config) SandboxCreator {
	return &ResumeSandbox{config: config}
}

func (f *ResumeSandbox) Sandbox(
	ctx context.Context,
	layerExecutor *LayerExecutor,
	template sbxtemplate.Template,
	_ storage.TemplateFiles,
) (*sandbox.Sandbox, error) {
	sbx, err := sandbox.ResumeSandbox(
		ctx,
		layerExecutor.tracer,
		layerExecutor.networkPool,
		template,
		f.config,
		sandbox.RuntimeMetadata{
			SandboxID:   config.InstanceBuildPrefix + id.Generate(),
			ExecutionID: uuid.NewString(),
		},
		uuid.New().String(),
		time.Now(),
		time.Now().Add(layerTimeout),
		layerExecutor.devicePool,
		false,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("resume sandbox: %w", err)
	}
	return sbx, nil
}
