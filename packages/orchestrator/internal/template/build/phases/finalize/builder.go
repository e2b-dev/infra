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
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/templates"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var finalizeTimeout = configurationTimeout + readyCommandTimeout + 5*time.Minute

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/phases/finalize")

type PostProcessingBuilder struct {
	buildcontext.BuildContext

	sandboxFactory  *sandbox.Factory
	templateStorage storage.StorageProvider
	proxy           *proxy.SandboxProxy

	layerExecutor *layer.LayerExecutor
	featureFlags  *featureflags.Client

	logger logger.Logger
}

func New(
	buildContext buildcontext.BuildContext,
	sandboxFactory *sandbox.Factory,
	templateStorage storage.StorageProvider,
	proxy *proxy.SandboxProxy,
	layerExecutor *layer.LayerExecutor,
	featureFlags *featureflags.Client,
	logger logger.Logger,
) *PostProcessingBuilder {
	return &PostProcessingBuilder{
		BuildContext: buildContext,

		sandboxFactory:  sandboxFactory,
		templateStorage: templateStorage,
		proxy:           proxy,

		layerExecutor: layerExecutor,
		featureFlags:  featureFlags,

		logger: logger,
	}
}

func (ppb *PostProcessingBuilder) Prefix() string {
	return "finalize"
}

func (ppb *PostProcessingBuilder) String(context.Context) (string, error) {
	return "Finalizing template build", nil
}

func (ppb *PostProcessingBuilder) Metadata() phases.PhaseMeta {
	return phases.PhaseMeta{
		Phase:    metrics.PhaseFinalize,
		StepType: "finalize",
	}
}

func (ppb *PostProcessingBuilder) Hash(_ context.Context, sourceLayer phases.LayerResult) (string, error) {
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
	result.Template = metadata.TemplateMetadata{
		BuildID:            ppb.Template.BuildID,
		KernelVersion:      ppb.Config.KernelVersion,
		FirecrackerVersion: ppb.Config.FirecrackerVersion,
	}

	return phases.LayerResult{
		Metadata: result,
		Cached:   false,
		Hash:     hash,
	}, nil
}

// Build runs post-processing actions in the sandbox
func (ppb *PostProcessingBuilder) Build(
	ctx context.Context,
	userLogger logger.Logger,
	_ string,
	sourceLayer phases.LayerResult,
	currentLayer phases.LayerResult,
) (phases.LayerResult, error) {
	ctx, span := tracer.Start(ctx, "build final", trace.WithAttributes(
		attribute.String("hash", currentLayer.Hash),
	))
	defer span.End()

	defaultUser := utils.ToPtr(currentLayer.Metadata.Context.User)
	defaultWorkdir := currentLayer.Metadata.Context.WorkDir

	ok, err := utils.IsGTEVersion(ppb.Version, templates.TemplateV2ReleaseVersion)
	if err != nil {
		return phases.LayerResult{}, fmt.Errorf("error checking build version: %w", err)
	}
	if !ok {
		// For older builds, always use "user" as the default user
		// and do not set a default workdir (defaults to the user homedir).
		defaultUser = utils.ToPtr("user")
		defaultWorkdir = nil
	}

	// Configure sandbox for final layer
	sbxConfig := sandbox.Config{
		Vcpu:      ppb.Config.VCpuCount,
		RamMB:     ppb.Config.MemoryMB,
		HugePages: ppb.Config.HugePages,

		Envd: sandbox.EnvdMetadata{
			Version:        ppb.EnvdVersion,
			DefaultUser:    defaultUser,
			DefaultWorkdir: defaultWorkdir,
		},

		FirecrackerConfig: fc.Config{
			KernelVersion:      ppb.Config.KernelVersion,
			FirecrackerVersion: ppb.Config.FirecrackerVersion,
		},
	}

	// Select the IO Engine to use for the rootfs drive
	ioEngine := ppb.featureFlags.StringFlag(
		ctx,
		featureflags.BuildIoEngine,
	)

	span.SetAttributes(attribute.String("io_engine", ioEngine))
	ppb.logger.Debug(ctx, "using io engine", zap.String("io_engine", ioEngine))

	// Always restart the sandbox for the final layer to properly wire the rootfs path for the final template
	sandboxCreator := layer.NewCreateSandbox(
		sbxConfig,
		ppb.sandboxFactory,
		finalizeTimeout,
		layer.WithIoEngine(ioEngine),
	)

	actionExecutor := layer.NewFunctionAction(ppb.postProcessingFn(userLogger))

	templateProvider := layer.NewCacheSourceTemplateProvider(sourceLayer.Metadata.Template.BuildID)

	finalLayer, err := ppb.layerExecutor.BuildLayer(
		ctx,
		userLogger,
		layer.LayerBuildCommand{
			SourceTemplate: templateProvider,
			CurrentLayer:   currentLayer.Metadata,
			Hash:           currentLayer.Hash,
			UpdateEnvd:     sourceLayer.Cached,
			SandboxCreator: sandboxCreator,
			ActionExecutor: actionExecutor,
		},
	)
	if err != nil {
		return phases.LayerResult{}, fmt.Errorf("error running start and ready commands in sandbox: %w", err)
	}

	return phases.LayerResult{
		Metadata: finalLayer,
		Cached:   false,
		Hash:     currentLayer.Hash,
	}, nil
}

func (ppb *PostProcessingBuilder) postProcessingFn(userLogger logger.Logger) layer.FunctionActionFn {
	return func(ctx context.Context, sbx *sandbox.Sandbox, meta metadata.Template) (cm metadata.Template, e error) {
		ctx, span := tracer.Start(ctx, "run postprocessing")
		defer span.End()

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
		configCtx, configCancel := context.WithTimeout(ctx, configurationTimeout)
		defer configCancel()
		err := runConfiguration(
			configCtx,
			userLogger,
			ppb.BuildContext,
			ppb.proxy,
			sbx.Runtime.SandboxID,
		)
		if err != nil {
			return metadata.Template{}, phases.NewPhaseBuildError(ppb.Metadata(), fmt.Errorf("configuration script failed: %w", err))
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
			userLogger.Info(ctx, fmt.Sprintf("Running start command: %s", meta.Start.StartCmd))
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
			return metadata.Template{}, phases.NewPhaseBuildError(ppb.Metadata(), fmt.Errorf("ready command failed: %w", err))
		}

		// Wait for the start command to start executing.
		select {
		case <-ctx.Done():
			return metadata.Template{}, phases.NewPhaseBuildError(ppb.Metadata(), fmt.Errorf("waiting for start command failed: %w", commandsCtx.Err()))
		case <-startCmdConfirm:
		}
		// Cancel the start command context (it's running in the background anyway).
		// If it has already finished, check the error.
		commandsCancel()
		err = startCmdRun.Wait()
		if err != nil {
			return metadata.Template{}, phases.NewPhaseBuildError(ppb.Metadata(), fmt.Errorf("start command failed: %w", err))
		}

		return meta, nil
	}
}
