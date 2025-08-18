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

func NewCreateSandbox(config sandbox.Config, fcVersions fc.FirecrackerVersions) SandboxCreator {
	return &CreateSandbox{config: config, fcVersions: fcVersions}
}

func (f *CreateSandbox) Sandbox(
	ctx context.Context,
	layerExecutor *LayerExecutor,
	template sbxtemplate.Template,
) (*sandbox.Sandbox, error) {
	// Create new sandbox path
	var oldMemfile block.ReadonlyDevice
	oldMemfile, err := template.Memfile()
	if err != nil {
		return nil, fmt.Errorf("get memfile: %w", err)
	}

	// Create new memfile with the size of the sandbox RAM, this updates the underlying memfile.
	// This is ok as the sandbox is started from the beginning.
	var memfile block.ReadonlyDevice
	memfile, err = block.NewEmpty(
		f.config.RamMB<<constants.ToMBShift,
		oldMemfile.BlockSize(),
		uuid.MustParse(template.Files().BuildID),
	)
	if err != nil {
		return nil, fmt.Errorf("create memfile: %w", err)
	}

	err = template.ReplaceMemfile(memfile)
	if err != nil {
		return nil, fmt.Errorf("replace memfile: %w", err)
	}

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
