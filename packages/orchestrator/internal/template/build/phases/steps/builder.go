package steps

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
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/buildcontext"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/commands"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/layer"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/phases"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/storage/cache"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

type StepsBuilder struct {
	buildcontext.BuildContext

	logger *zap.Logger
	tracer trace.Tracer
	proxy  *proxy.SandboxProxy

	layerExecutor   *layer.LayerExecutor
	commandExecutor *commands.CommandExecutor
	index           cache.Index
}

func New(
	buildContext buildcontext.BuildContext,
	logger *zap.Logger,
	tracer trace.Tracer,
	proxy *proxy.SandboxProxy,
	layerExecutor *layer.LayerExecutor,
	commandExecutor *commands.CommandExecutor,
	index cache.Index,
) *StepsBuilder {
	return &StepsBuilder{
		BuildContext: buildContext,

		logger: logger,
		tracer: tracer,
		proxy:  proxy,

		layerExecutor:   layerExecutor,
		commandExecutor: commandExecutor,
		index:           index,
	}
}

func (sb *StepsBuilder) Build(
	ctx context.Context,
	lastStepResult phases.LayerResult,
) (phases.LayerResult, error) {
	sourceLayer := lastStepResult

	baseTemplateID := lastStepResult.Metadata.Template.TemplateID

	for i, step := range sb.Config.Steps {
		currentLayer, err := sb.shouldBuildStep(
			ctx,
			sourceLayer,
			step,
		)
		if err != nil {
			return phases.LayerResult{}, fmt.Errorf("error checking if step %d should be built: %w", i+1, err)
		}

		// If the last layer is cached, update the base metadata to the step metadata
		// This is needed to properly run the sandbox for the next step
		if sourceLayer.Cached {
			baseTemplateID = currentLayer.Metadata.Template.TemplateID
		}

		prefix := fmt.Sprintf("builder %d/%d", i+1, len(sb.Config.Steps))
		cmd := fmt.Sprintf("%s %s", strings.ToUpper(step.Type), strings.Join(step.Args, " "))
		sb.UserLogger.Info(phases.LayerInfo(currentLayer.Cached, prefix, cmd, currentLayer.Hash))

		if currentLayer.Cached {
			sourceLayer = currentLayer
			continue
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
			return phases.LayerResult{}, fmt.Errorf("error building step %d: %w", i+1, err)
		}

		sourceLayer = res
	}

	return sourceLayer, nil
}

func (sb *StepsBuilder) shouldBuildStep(
	ctx context.Context,
	sourceLayer phases.LayerResult,
	step *templatemanager.TemplateStep,
) (phases.LayerResult, error) {
	hash := HashStep(sourceLayer.Hash, step)

	force := step.Force != nil && *step.Force
	if !force {
		m, err := sb.index.LayerMetaFromHash(ctx, hash)
		if err != nil {
			sb.logger.Info("layer not found in cache, building new base layer", zap.Error(err), zap.String("hash", hash), zap.String("step", step.Type))
		} else {
			// Check if the layer is cached
			found, err := sb.index.IsCached(ctx, m)
			if err != nil {
				return phases.LayerResult{}, fmt.Errorf("error checking if layer is cached: %w", err)
			}

			if found {
				return phases.LayerResult{
					Metadata: m,
					Cached:   true,
					Hash:     hash,
				}, nil
			}
		}
	}

	meta := cache.LayerMetadata{
		Template: storage.TemplateFiles{
			TemplateID:         id.Generate(),
			BuildID:            uuid.NewString(),
			KernelVersion:      sourceLayer.Metadata.Template.KernelVersion,
			FirecrackerVersion: sourceLayer.Metadata.Template.FirecrackerVersion,
		},
		CmdMeta: sourceLayer.Metadata.CmdMeta,
	}

	return phases.LayerResult{
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
	sourceLayer phases.LayerResult,
	currentLayer phases.LayerResult,
) (phases.LayerResult, error) {
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

	// First not cached layer is create (to change CPU, Memory, etc), subsequent are layers are resumes.
	var sandboxCreator layer.SandboxCreator
	if sourceLayer.Cached {
		sandboxCreator = layer.NewCreateSandbox(sbxConfig, fc.FirecrackerVersions{
			KernelVersion:      sb.Template.KernelVersion,
			FirecrackerVersion: sb.Template.FirecrackerVersion,
		}, sb.Template.TemplateID)
	} else {
		sandboxCreator = layer.NewResumeSandbox(sbxConfig)
	}

	actionExecutor := layer.NewFunctionAction(func(ctx context.Context, sbx *sandbox.Sandbox, cmdMeta sandboxtools.CommandMetadata) (sandboxtools.CommandMetadata, error) {
		meta, err := sb.commandExecutor.Execute(
			ctx,
			sbx,
			prefix,
			step,
			cmdMeta,
		)
		if err != nil {
			return sandboxtools.CommandMetadata{}, fmt.Errorf("error processing layer: %w", err)
		}

		err = sandboxtools.SyncChangesToDisk(
			ctx,
			sb.tracer,
			sb.proxy,
			sbx.Runtime.SandboxID,
		)
		if err != nil {
			return sandboxtools.CommandMetadata{}, fmt.Errorf("error running sync command: %w", err)
		}

		return meta, nil
	})

	meta, err := sb.layerExecutor.BuildLayer(ctx, layer.LayerBuildCommand{
		Hash:           currentLayer.Hash,
		SourceLayer:    sourceLayer.Metadata,
		ExportTemplate: currentLayer.Metadata.Template,
		UpdateEnvd:     sourceLayer.Cached,
		SandboxCreator: sandboxCreator,
		ActionExecutor: actionExecutor,
	})
	if err != nil {
		return phases.LayerResult{}, fmt.Errorf("error running build layer: %w", err)
	}

	return phases.LayerResult{
		Metadata: meta,
		Cached:   false,
		Hash:     currentLayer.Hash,
	}, nil
}
