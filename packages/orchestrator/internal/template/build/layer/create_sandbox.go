package layer

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/fc"
	sbxtemplate "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/config"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/constants"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
)

// CreateSandbox creates sandboxes for new templates
type CreateSandbox struct {
	config     sandbox.Config
	fcVersions fc.FirecrackerVersions
}

var _ SandboxCreator = (*CreateSandbox)(nil)

func NewCreateSandbox(config sandbox.Config, fcVersions fc.FirecrackerVersions) *CreateSandbox {
	return &CreateSandbox{config: config, fcVersions: fcVersions}
}

func (f *CreateSandbox) Sandbox(
	ctx context.Context,
	layerExecutor *LayerExecutor,
	sourceTemplate sbxtemplate.Template,
) (*sandbox.Sandbox, error) {
	// Create new memfile with the size of the sandbox RAM, this updates the underlying memfile.
	// This is ok as the sandbox is started from the beginning.
	memfile, err := block.NewEmpty(
		f.config.RamMB<<constants.ToMBShift,
		config.MemfilePageSize(f.config.HugePages),
		uuid.MustParse(sourceTemplate.Files().BuildID),
	)
	if err != nil {
		return nil, fmt.Errorf("create memfile: %w", err)
	}

	template := sbxtemplate.NewCloneTemplate(sourceTemplate, sbxtemplate.WithMemfile(memfile))

	// In case of a new sandbox, base template ID is now used as the potentially exported template base ID.
	sbx, err := sandbox.CreateSandbox(
		ctx,
		layerExecutor.tracer,
		layerExecutor.networkPool,
		layerExecutor.devicePool,
		f.config,
		sandbox.RuntimeMetadata{
			TemplateID:  layerExecutor.Config.TemplateID,
			SandboxID:   config.InstanceBuildPrefix + id.Generate(),
			ExecutionID: uuid.NewString(),
		},
		f.fcVersions,
		template,
		layerTimeout,
		"",
		fc.ProcessOptions{
			InitScriptPath:      constants.SystemdInitPath,
			KernelLogs:          env.IsDevelopment(),
			SystemdToKernelLogs: false,
		},
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("create sandbox: %w", err)
	}

	err = sbx.WaitForEnvd(
		ctx,
		layerExecutor.tracer,
		waitEnvdTimeout,
	)
	if err != nil {
		return nil, fmt.Errorf("wait for envd: %w", err)
	}

	return sbx, nil
}
