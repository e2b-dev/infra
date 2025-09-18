package finalize

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/sync/errgroup"

	globalconfig "github.com/e2b-dev/infra/packages/orchestrator/internal/config"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/buildcontext"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/layer"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/phases"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/storage/cache"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

var finalizeTimeout = configurationTimeout + readyCommandTimeout + 5*time.Minute

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/phases/finalize")

type PostProcessingBuilder struct {
	buildcontext.BuildContext

	templateStorage storage.StorageProvider
	proxy           *proxy.SandboxProxy

	layerExecutor *layer.LayerExecutor
}

func New(
	buildContext buildcontext.BuildContext,
	templateStorage storage.StorageProvider,
	proxy *proxy.SandboxProxy,
	layerExecutor *layer.LayerExecutor,
) *PostProcessingBuilder {
	return &PostProcessingBuilder{
		BuildContext: buildContext,

		templateStorage: templateStorage,
		proxy:           proxy,

		layerExecutor: layerExecutor,
	}
}

func (ppb *PostProcessingBuilder) Prefix() string {
	return "finalize"
}

func (ppb *PostProcessingBuilder) String(ctx context.Context) (string, error) {
	return "Finalizing template build", nil
}

func (ppb *PostProcessingBuilder) Metadata() phases.PhaseMeta {
	return phases.PhaseMeta{
		Phase:    metrics.PhaseFinalize,
		StepType: "finalize",
	}
}

func (ppb *PostProcessingBuilder) Hash(sourceLayer phases.LayerResult) (string, error) {
	return cache.HashKeys(sourceLayer.Hash, "config-run-cmd"), nil
}

func (ppb *PostProcessingBuilder) Layer(
	_ context.Context,
	sourceLayer phases.LayerResult,
	hash string,
) (phases.LayerResult, error) {
	result := sourceLayer.Metadata

	// If the start/ready commands are set,
	// use them instead of start metadata from the template it is built from.
	if ppb.Config.StartCmd != "" || ppb.Config.ReadyCmd != "" {
		result.Start = &metadata.Start{
			StartCmd: ppb.Config.StartCmd,
			ReadyCmd: ppb.Config.ReadyCmd,
			Context:  result.Context,
		}
	}

	// The final template is the one from the configuration
	result.Template = ppb.Template

	return phases.LayerResult{
		Metadata: result,
		Cached:   false,
		Hash:     hash,
	}, nil
}

// Build runs post-processing actions in the sandbox
func (ppb *PostProcessingBuilder) Build(
	ctx context.Context,
	userLogger *zap.Logger,
	sourceLayer phases.LayerResult,
	currentLayer phases.LayerResult,
) (phases.LayerResult, error) {
	ctx, span := tracer.Start(ctx, "build final", trace.WithAttributes(
		attribute.String("hash", currentLayer.Hash),
	))
	defer span.End()

	// Configure sandbox for final layer
	sbxConfig := sandbox.Config{
		Vcpu:      ppb.Config.VCpuCount,
		RamMB:     ppb.Config.MemoryMB,
		HugePages: ppb.Config.HugePages,

		AllowInternetAccess: &globalconfig.AllowSandboxInternet,

		Envd: sandbox.EnvdMetadata{
			Version: ppb.EnvdVersion,
		},
	}

	// Always restart the sandbox for the final layer to properly wire the rootfs path for the final template
	sandboxCreator := layer.NewCreateSandbox(
		sbxConfig,
		finalizeTimeout,
		fc.FirecrackerVersions{
			KernelVersion:      currentLayer.Metadata.Template.KernelVersion,
			FirecrackerVersion: currentLayer.Metadata.Template.FirecrackerVersion,
		},
	)

	actionExecutor := layer.NewFunctionAction(ppb.postProcessingFn(userLogger))

	templateProvider := layer.NewCacheSourceTemplateProvider(sourceLayer.Metadata.Template)

	finalLayer, err := ppb.layerExecutor.BuildLayer(ctx, userLogger, layer.LayerBuildCommand{
		SourceTemplate: templateProvider,
		CurrentLayer:   currentLayer.Metadata,
		Hash:           currentLayer.Hash,
		UpdateEnvd:     sourceLayer.Cached,
		SandboxCreator: sandboxCreator,
		ActionExecutor: actionExecutor,
	})
	if err != nil {
		return phases.LayerResult{}, fmt.Errorf("error running start and ready commands in sandbox: %w", err)
	}

	return phases.LayerResult{
		Metadata: finalLayer,
		Cached:   false,
		Hash:     currentLayer.Hash,
	}, nil
}

func (ppb *PostProcessingBuilder) postProcessingFn(userLogger *zap.Logger) layer.FunctionActionFn {
	return func(ctx context.Context, sbx *sandbox.Sandbox, meta metadata.Template) (cm metadata.Template, e error) {
		defer func() {
			if e != nil {
				return
			}

			// Ensure all changes are synchronized to disk so the sandbox can be restarted
			err := sandboxtools.SyncChangesToDisk(
				ctx,
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
			userLogger,
			ppb.BuildContext,
			ppb.proxy,
			sbx.Runtime.SandboxID,
		)
		if err != nil {
			return metadata.Template{}, phases.NewPhaseBuildError(ppb, fmt.Errorf("configuration script failed: %w", err))
		}

		if meta.Start == nil {
			return meta, nil
		}

		// Start command
		commandsCtx, commandsCancel := context.WithCancel(ctx)
		defer commandsCancel()

		var startCmdRun errgroup.Group
		startCmdConfirm := make(chan struct{})
		if meta.Start.StartCmd != "" {
			userLogger.Info("Running start command")
			startCmdRun.Go(func() error {
				err := sandboxtools.RunCommandWithConfirmation(
					commandsCtx,
					ppb.proxy,
					userLogger,
					zapcore.InfoLevel,
					"start",
					sbx.Runtime.SandboxID,
					meta.Start.StartCmd,
					meta.Start.Context,
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
		readyCmd := meta.Start.ReadyCmd
		if readyCmd == "" {
			if meta.Start.StartCmd == "" {
				readyCmd = "sleep 0"
			} else {
				readyCmd = GetDefaultReadyCommand(ppb.Config.TemplateID)
			}
		}
		err = ppb.runReadyCommand(
			commandsCtx,
			userLogger,
			sbx.Runtime.SandboxID,
			readyCmd,
			meta.Start.Context,
		)
		if err != nil {
			return metadata.Template{}, phases.NewPhaseBuildError(ppb, fmt.Errorf("ready command failed: %w", err))
		}

		// Wait for the start command to start executing.
		select {
		case <-ctx.Done():
			return metadata.Template{}, phases.NewPhaseBuildError(ppb, fmt.Errorf("waiting for start command failed: %w", commandsCtx.Err()))
		case <-startCmdConfirm:
		}
		// Cancel the start command context (it's running in the background anyway).
		// If it has already finished, check the error.
		commandsCancel()
		err = startCmdRun.Wait()
		if err != nil {
			return metadata.Template{}, phases.NewPhaseBuildError(ppb, fmt.Errorf("start command failed: %w", err))
		}

		return meta, nil
	}
}
