package layer

import (
	"context"
	"errors"
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
	"github.com/e2b-dev/infra/packages/shared/pkg/fc/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// CreateSandbox creates sandboxes for new templates
type CreateSandbox struct {
	config         sandbox.Config
	timeout        time.Duration
	fcVersions     fc.FirecrackerVersions
	sandboxFactory *sandbox.Factory

	rootfsCachePath string
	ioEngine        *string
}

const (
	minEnvdVersionForKVMClock = "0.2.11"                 // Minimum version of envd that supports KVM clock
	defaultIoEngine           = models.DriveIoEngineSync // Use the Sync io engine by default to avoid issues with Async.
)

var _ SandboxCreator = (*CreateSandbox)(nil)

type createSandboxOptions struct {
	rootfsCachePath string
	ioEngine        *string
}

type CreateSandboxOption func(*createSandboxOptions)

func WithIoEngine(ioEngine string) CreateSandboxOption {
	return func(opts *createSandboxOptions) {
		opts.ioEngine = &ioEngine
	}
}

func WithRootfsCachePath(rootfsCachePath string) CreateSandboxOption {
	return func(opts *createSandboxOptions) {
		opts.rootfsCachePath = rootfsCachePath
	}
}

func NewCreateSandbox(config sandbox.Config, sandboxFactory *sandbox.Factory, timeout time.Duration, fcVersions fc.FirecrackerVersions, options ...CreateSandboxOption) *CreateSandbox {
	opts := &createSandboxOptions{
		rootfsCachePath: "",
		ioEngine:        utils.ToPtr(defaultIoEngine),
	}
	for _, option := range options {
		option(opts)
	}

	return &CreateSandbox{
		config:          config,
		timeout:         timeout,
		fcVersions:      fcVersions,
		rootfsCachePath: opts.rootfsCachePath,
		sandboxFactory:  sandboxFactory,
		ioEngine:        opts.ioEngine,
	}
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
	sbx, err := cs.sandboxFactory.CreateSandbox(
		ctx,
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
			IoEngine:            cs.ioEngine,
		},
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("create sandbox: %w", err)
	}
	defer func() {
		if err != nil {
			// Close the sandbox in case of error to avoid leaking resources
			err = errors.Join(err, sbx.Close(ctx))
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
