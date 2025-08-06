package finalize

import (
	"context"
	"errors"
	"fmt"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/sync/errgroup"

	globalconfig "github.com/e2b-dev/infra/packages/orchestrator/internal/config"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	sbxtemplate "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/buildcontext"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/layer"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/phases"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/storage/cache"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

type PostProcessingBuilder struct {
	buildcontext.BuildContext

	logger *zap.Logger
	tracer trace.Tracer

	templateStorage storage.StorageProvider
	proxy           *proxy.SandboxProxy

	layerExecutor *layer.LayerExecutor
}

func New(
	buildContext buildcontext.BuildContext,
	logger *zap.Logger,
	tracer trace.Tracer,
	templateStorage storage.StorageProvider,
	proxy *proxy.SandboxProxy,
	layerExecutor *layer.LayerExecutor,
) *PostProcessingBuilder {
	return &PostProcessingBuilder{
		BuildContext: buildContext,

		logger: logger,
		tracer: tracer,

		templateStorage: templateStorage,
		proxy:           proxy,

		layerExecutor: layerExecutor,
	}
}

// runPostProcessing runs post-processing actions in the sandbox
func (ppb *PostProcessingBuilder) Build(
	ctx context.Context,
	lastStepResult phases.LayerResult,
) (phases.LayerResult, error) {
	var startMetadata *metadata.StartMetadata
	if ppb.Config.StartCmd != "" || ppb.Config.ReadyCmd != "" {
		startMetadata = &metadata.StartMetadata{
			StartCmd: ppb.Config.StartCmd,
			ReadyCmd: ppb.Config.ReadyCmd,
			Metadata: lastStepResult.Metadata.CmdMeta,
		}
	}

	// If the template is built from another template, and the start metadata are not set,
	// use the start metadata from the template it is built from.
	if startMetadata == nil && ppb.Config.FromTemplate != nil {
		tm, err := metadata.ReadTemplateMetadata(ctx, ppb.templateStorage, ppb.Config.FromTemplate.BuildID)
		if err != nil {
			return phases.LayerResult{}, fmt.Errorf("error reading from template metadata: %w", err)
		}
		startMetadata = tm.Start
	}

	hash := cache.HashKeys(lastStepResult.Hash, "config-run-cmd")
	finalLayer, err := ppb.layerExecutor.BuildLayer(
		ctx,
		hash,
		lastStepResult.Metadata.Template,
		ppb.Template,
		lastStepResult.Cached,
		func(
			context context.Context,
			b *layer.LayerExecutor,
			t sbxtemplate.Template,
			exportTemplate storage.TemplateFiles,
		) (*sandbox.Sandbox, error) {
			// Always restart the sandbox for the final layer to properly wire the rootfs path for the final template.
			return b.CreateSandboxFromTemplate(ctx, t, sandbox.Config{
				Vcpu:      ppb.Config.VCpuCount,
				RamMB:     ppb.Config.MemoryMB,
				HugePages: ppb.Config.HugePages,

				AllowInternetAccess: &globalconfig.AllowSandboxInternet,

				Envd: sandbox.EnvdMetadata{
					Version: ppb.EnvdVersion,
				},
			}, exportTemplate)
		},
		ppb.postProcessingFn(lastStepResult.Metadata.CmdMeta, startMetadata),
	)
	if err != nil {
		return phases.LayerResult{}, fmt.Errorf("error running start and ready commands in sandbox: %w", err)
	}

	return phases.LayerResult{
		Metadata:      finalLayer,
		Cached:        false,
		Hash:          hash,
		StartMetadata: startMetadata,
	}, nil
}

func (ppb *PostProcessingBuilder) postProcessingFn(
	cmdMeta sandboxtools.CommandMetadata,
	start *metadata.StartMetadata,
) func(ctx context.Context, sbx *sandbox.Sandbox) (sandboxtools.CommandMetadata, error) {
	return func(ctx context.Context, sbx *sandbox.Sandbox) (cm sandboxtools.CommandMetadata, e error) {
		defer func() {
			if e != nil {
				return
			}

			// Ensure all changes are synchronized to disk so the sandbox can be restarted
			err := sandboxtools.SyncChangesToDisk(
				ctx,
				ppb.tracer,
				ppb.proxy,
				sbx.Runtime.SandboxID,
			)
			if err != nil {
				e = fmt.Errorf("error running sync command: %w", err)
				return
			}
		}()

		// Run configuration script
		err := runConfiguration(
			ctx,
			ppb.tracer,
			ppb.proxy,
			ppb.UserLogger,
			ppb.Template,
			sbx.Runtime.SandboxID,
		)
		if err != nil {
			return sandboxtools.CommandMetadata{}, fmt.Errorf("error running configuration script: %w", err)
		}

		if start == nil {
			return cmdMeta, nil
		}

		// Start command
		commandsCtx, commandsCancel := context.WithCancel(ctx)
		defer commandsCancel()

		var startCmdRun errgroup.Group
		startCmdConfirm := make(chan struct{})
		if start.StartCmd != "" {
			ppb.UserLogger.Info("Running start command")
			startCmdRun.Go(func() error {
				err := sandboxtools.RunCommandWithConfirmation(
					commandsCtx,
					ppb.tracer,
					ppb.proxy,
					ppb.UserLogger,
					zapcore.InfoLevel,
					"start",
					sbx.Runtime.SandboxID,
					start.StartCmd,
					start.Metadata,
					startCmdConfirm,
				)
				// If the ctx is canceled, the ready command succeeded and no start command await is necessary.
				if err != nil && !errors.Is(err, context.Canceled) {
					// Cancel the ready command context, so the ready command does not wait anymore if an error occurs.
					commandsCancel()
					return fmt.Errorf("error running start command: %w", err)
				}

				return nil
			})
		} else {
			// If no start command is defined, we still need to confirm that the start command has started.
			close(startCmdConfirm)
		}

		// Ready command
		readyCmd := start.ReadyCmd
		if readyCmd == "" {
			if start.StartCmd == "" {
				readyCmd = "sleep 0"
			} else {
				readyCmd = GetDefaultReadyCommand(ppb.Template)
			}
		}
		err = ppb.runReadyCommand(
			commandsCtx,
			sbx.Runtime.SandboxID,
			readyCmd,
			start.Metadata,
		)
		if err != nil {
			return sandboxtools.CommandMetadata{}, fmt.Errorf("error running ready command: %w", err)
		}

		// Wait for the start command to start executing.
		select {
		case <-ctx.Done():
			return sandboxtools.CommandMetadata{}, fmt.Errorf("error waiting for start command: %w", commandsCtx.Err())
		case <-startCmdConfirm:
		}
		// Cancel the start command context (it's running in the background anyway).
		// If it has already finished, check the error.
		commandsCancel()
		err = startCmdRun.Wait()
		if err != nil {
			return sandboxtools.CommandMetadata{}, fmt.Errorf("error running start command: %w", err)
		}

		return cmdMeta, nil
	}
}
