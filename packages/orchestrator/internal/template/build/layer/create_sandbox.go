package layer

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/fc"
	sbxtemplate "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/config"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/constants"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// CreateSandbox creates sandboxes for new templates
type CreateSandbox struct {
	config     sandbox.Config
	timeout    time.Duration
	fcVersions fc.FirecrackerVersions

	rootfsCachePath string
}

const (
	minEnvdVersionForKVMClock = "0.2.11" // Minimum version of envd that supports KVM clock
)

var _ SandboxCreator = (*CreateSandbox)(nil)

func NewCreateSandbox(config sandbox.Config, timeout time.Duration, fcVersions fc.FirecrackerVersions) *CreateSandbox {
	return &CreateSandbox{config: config, timeout: timeout, fcVersions: fcVersions, rootfsCachePath: ""}
}

func NewCreateSandboxFromCache(config sandbox.Config, timeout time.Duration, fcVersions fc.FirecrackerVersions, rootfsCachePath string) *CreateSandbox {
	return &CreateSandbox{config: config, timeout: timeout, fcVersions: fcVersions, rootfsCachePath: rootfsCachePath}
}

func (cs *CreateSandbox) Sandbox(
	ctx context.Context,
	layerExecutor *LayerExecutor,
	sourceTemplate sbxtemplate.Template,
) (s *sandbox.Sandbox, err error) {
	// Create new memfile with the size of the sandbox RAM, this updates the underlying memfile.
	// This is ok as the sandbox is started from the beginning.
	memfile, err := block.NewEmpty(
		cs.config.RamMB<<constants.ToMBShift,
		config.MemfilePageSize(cs.config.HugePages),
		uuid.MustParse(sourceTemplate.Files().BuildID),
	)
	if err != nil {
		return nil, fmt.Errorf("create memfile: %w", err)
	}

	template := sbxtemplate.NewMaskTemplate(sourceTemplate, sbxtemplate.WithMemfile(memfile))

	kvmClock, err := utils.IsGTEVersion(cs.config.Envd.Version, minEnvdVersionForKVMClock)
	if err != nil {
		return nil, fmt.Errorf("error comparing envd version: %w", err)
	}

	// In case of a new sandbox, base template ID is now used as the potentially exported template base ID.
	sbx, err := sandbox.CreateSandbox(
		ctx,
		layerExecutor.networkPool,
		layerExecutor.devicePool,
		cs.config,
		sandbox.RuntimeMetadata{
			TemplateID:  layerExecutor.Config.TemplateID,
			SandboxID:   config.InstanceBuildPrefix + id.Generate(),
			ExecutionID: uuid.NewString(),
		},
		cs.fcVersions,
		template,
		cs.timeout,
		cs.rootfsCachePath,
		fc.ProcessOptions{
			InitScriptPath:      constants.SystemdInitPath,
			KernelLogs:          env.IsDevelopment(),
			SystemdToKernelLogs: false,
			KvmClock:            kvmClock,
		},
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("create sandbox: %w", err)
	}
	defer func() {
		if err != nil {
			// Close the sandbox in case of error to avoid leaking resources
			_ = sbx.Close(ctx)
		}
	}()

	err = sbx.WaitForEnvd(
		ctx,
		waitEnvdTimeout,
	)
	if err != nil {
		return nil, fmt.Errorf("wait for envd: %w", err)
	}

	return sbx, nil
}
