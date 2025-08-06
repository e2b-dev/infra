package build

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	globalconfig "github.com/e2b-dev/infra/packages/orchestrator/internal/config"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	sbxtemplate "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

type StepsBuilder struct {
	BuildContext

	logger *zap.Logger
	tracer trace.Tracer

	templateStorage storage.StorageProvider
	buildStorage    storage.StorageProvider
	proxy           *proxy.SandboxProxy

	layerExecutor   *LayerExecutor
	commandExecutor *CommandExecutor
}

func NewStepsBuilder(
	buildContext BuildContext,
	logger *zap.Logger,
	tracer trace.Tracer,
	templateStorage storage.StorageProvider,
	buildStorage storage.StorageProvider,
	proxy *proxy.SandboxProxy,
	layerExecutor *LayerExecutor,
) *StepsBuilder {
	commandExecutor := &CommandExecutor{
		BuildContext: buildContext,

		tracer: tracer,
		proxy:  proxy,
	}

	return &StepsBuilder{
		BuildContext: buildContext,

		logger:          logger,
		tracer:          tracer,
		templateStorage: templateStorage,
		buildStorage:    buildStorage,
		proxy:           proxy,

		layerExecutor:   layerExecutor,
		commandExecutor: commandExecutor,
	}
}

func (sb *StepsBuilder) Build(
	ctx context.Context,
	lastStepResult LayerResult,
) (LayerResult, error) {
	sourceLayer := lastStepResult

	baseTemplateID := lastStepResult.Metadata.Template.TemplateID

	for i, step := range sb.Config.Steps {
		currentLayer, err := sb.shouldBuildStep(
			ctx,
			sourceLayer,
			step,
		)
		if err != nil {
			return LayerResult{}, fmt.Errorf("error checking if step %d should be built: %w", i+1, err)
		}

		// If the last layer is cached, update the base metadata to the step metadata
		// This is needed to properly run the sandbox for the next step
		if sourceLayer.Cached {
			baseTemplateID = currentLayer.Metadata.Template.TemplateID
		}

		prefix := fmt.Sprintf("builder %d/%d", i+1, len(sb.Config.Steps))
		cmd := fmt.Sprintf("%s %s", strings.ToUpper(step.Type), strings.Join(step.Args, " "))
		sb.UserLogger.Info(layerInfo(currentLayer.Cached, prefix, cmd, currentLayer.Hash))

		if currentLayer.Cached {
			sourceLayer = currentLayer
		}

		res, err := sb.buildStep(
			ctx,
			step,
			prefix,
			baseTemplateID,
			sourceLayer,
			currentLayer,
		)
		if err != nil {
			return LayerResult{}, fmt.Errorf("error building step %d: %w", i+1, err)
		}

		sourceLayer = res
	}

	return sourceLayer, nil
}

func (sb *StepsBuilder) shouldBuildStep(
	ctx context.Context,
	sourceLayer LayerResult,
	step *templatemanager.TemplateStep,
) (LayerResult, error) {
	hash := hashStep(sourceLayer.Hash, step)

	force := step.Force != nil && *step.Force
	if !force {
		m, err := layerMetaFromHash(ctx, sb.buildStorage, sb.CacheScope, hash)
		if err != nil {
			sb.logger.Info("layer not found in cache, building new base layer", zap.Error(err), zap.String("hash", hash), zap.String("step", step.Type))
		} else {
			// Check if the layer is cached
			found, err := isCached(ctx, sb.templateStorage, m)
			if err != nil {
				return LayerResult{}, fmt.Errorf("error checking if layer is cached: %w", err)
			}

			if found {
				return LayerResult{
					Metadata: m,
					Cached:   true,
					Hash:     hash,
				}, nil
			}
		}
	}

	meta := LayerMetadata{
		Template: storage.TemplateFiles{
			TemplateID:         id.Generate(),
			BuildID:            uuid.NewString(),
			KernelVersion:      sourceLayer.Metadata.Template.KernelVersion,
			FirecrackerVersion: sourceLayer.Metadata.Template.FirecrackerVersion,
		},
		CmdMeta: sourceLayer.Metadata.CmdMeta,
	}

	if sourceLayer.Cached {
		meta.Template = storage.TemplateFiles{
			TemplateID:         id.Generate(),
			BuildID:            uuid.NewString(),
			KernelVersion:      sb.Template.KernelVersion,
			FirecrackerVersion: sb.Template.FirecrackerVersion,
		}
	}

	return LayerResult{
		Metadata: meta,
		Cached:   false,
		Hash:     hash,
	}, nil
}

func (sb *StepsBuilder) buildStep(
	ctx context.Context,
	step *templatemanager.TemplateStep,
	prefix string,
	baseTemplateID string,
	sourceLayer LayerResult,
	currentLayer LayerResult,
) (LayerResult, error) {
	meta, err := sb.layerExecutor.BuildLayer(
		ctx,
		currentLayer.Hash,
		sourceLayer.Metadata.Template,
		currentLayer.Metadata.Template,
		sourceLayer.Cached,
		func(
			context context.Context,
			b *LayerExecutor,
			t sbxtemplate.Template,
			exportTemplate storage.TemplateFiles,
		) (*sandbox.Sandbox, error) {
			sbxConfig := sandbox.Config{
				BaseTemplateID: baseTemplateID,

				Vcpu:      sb.Config.VCpuCount,
				RamMB:     sb.Config.MemoryMB,
				HugePages: sb.Config.HugePages,

				AllowInternetAccess: &globalconfig.AllowSandboxInternet,

				Envd: sandbox.EnvdMetadata{
					Version: sb.EnvdVersion,
				},
			}

			// First not cached layer is create (to change CPU, etc), subsequent are resumes.
			if sourceLayer.Cached {
				return CreateSandboxFromTemplate(ctx, b, t, sbxConfig, exportTemplate)
			} else {
				return ResumeSandbox(ctx, b, t, sbxConfig)
			}
		},
		func(ctx context.Context, sbx *sandbox.Sandbox) (sandboxtools.CommandMetadata, error) {
			sb.UserLogger.Debug(fmt.Sprintf("Running action in: %s/%s", sourceLayer.Metadata.Template.TemplateID, sourceLayer.Metadata.Template.BuildID))

			meta, err := sb.commandExecutor.Execute(
				ctx,
				sbx,
				prefix,
				step,
				sourceLayer.Metadata.CmdMeta,
			)
			if err != nil {
				return sandboxtools.CommandMetadata{}, fmt.Errorf("error processing layer: %w", err)
			}

			err = syncChangesToDisk(
				ctx,
				sb.tracer,
				sb.proxy,
				sbx.Runtime.SandboxID,
			)
			if err != nil {
				return sandboxtools.CommandMetadata{}, fmt.Errorf("error running sync command: %w", err)
			}

			return meta, nil
		},
	)
	if err != nil {
		return LayerResult{}, fmt.Errorf("error running build layer: %w", err)
	}

	return LayerResult{
		Metadata: meta,
		Cached:   false,
		Hash:     currentLayer.Hash,
	}, nil
}
