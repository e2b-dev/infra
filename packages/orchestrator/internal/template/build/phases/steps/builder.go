package steps

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	globalconfig "github.com/e2b-dev/infra/packages/orchestrator/internal/config"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/buildcontext"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/commands"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/layer"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/phases"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/storage/cache"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

const layerTimeout = time.Hour

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/phases/steps")

type StepBuilder struct {
	buildcontext.BuildContext

	stepNumber int
	step       *templatemanager.TemplateStep

	logger *zap.Logger
	proxy  *proxy.SandboxProxy

	layerExecutor   *layer.LayerExecutor
	commandExecutor *commands.CommandExecutor
	index           cache.Index
	metrics         *metrics.BuildMetrics
}

func New(
	buildContext buildcontext.BuildContext,
	logger *zap.Logger,
	proxy *proxy.SandboxProxy,
	layerExecutor *layer.LayerExecutor,
	commandExecutor *commands.CommandExecutor,
	index cache.Index,
	metrics *metrics.BuildMetrics,
	step *templatemanager.TemplateStep,
	stepNumber int,
) *StepBuilder {
	return &StepBuilder{
		BuildContext: buildContext,

		stepNumber: stepNumber,
		step:       step,

		logger: logger,
		proxy:  proxy,

		layerExecutor:   layerExecutor,
		commandExecutor: commandExecutor,
		index:           index,
		metrics:         metrics,
	}
}

func (sb *StepBuilder) Prefix() string {
	return fmt.Sprintf("builder %d/%d", sb.stepNumber, len(sb.Config.Steps))
}

func (sb *StepBuilder) String(ctx context.Context) (string, error) {
	return fmt.Sprintf("%s %s", strings.ToUpper(sb.step.Type), strings.Join(sb.step.Args, " ")), nil
}

func (sb *StepBuilder) Metadata() phases.PhaseMeta {
	return phases.PhaseMeta{
		Phase:      metrics.PhaseSteps,
		StepType:   sb.step.Type,
		StepNumber: &sb.stepNumber,
	}
}

func (sb *StepBuilder) Layer(
	ctx context.Context,
	sourceLayer phases.LayerResult,
	hash string,
) (phases.LayerResult, error) {
	ctx, span := tracer.Start(ctx, "compute step", trace.WithAttributes(
		attribute.Int("step", sb.stepNumber),
		attribute.String("type", sb.step.Type),
		attribute.String("hash", hash),
	))
	defer span.End()

	forceBuild := sb.step.Force != nil && *sb.step.Force
	if !forceBuild {
		m, err := sb.index.LayerMetaFromHash(ctx, hash)
		if err != nil {
			sb.logger.Info("layer not found in cache, building new base layer", zap.Error(err), zap.String("hash", hash))
		} else {
			// Check if the layer is cached
			meta, err := sb.index.Cached(ctx, m.Template.BuildID)
			if err != nil {
				zap.L().Info("layer not cached, building new layer", zap.Error(err), zap.String("hash", hash))
			} else {
				return phases.LayerResult{
					Metadata: meta,
					Cached:   true,
					Hash:     hash,
				}, nil
			}
		}
	}

	finalMetadata := sourceLayer.Metadata
	finalMetadata.Template = storage.TemplateFiles{
		BuildID:            uuid.NewString(),
		KernelVersion:      sourceLayer.Metadata.Template.KernelVersion,
		FirecrackerVersion: sourceLayer.Metadata.Template.FirecrackerVersion,
	}

	return phases.LayerResult{
		Metadata: finalMetadata,
		Cached:   false,
		Hash:     hash,
	}, nil
}

func (sb *StepBuilder) Build(
	ctx context.Context,
	userLogger *zap.Logger,
	sourceLayer phases.LayerResult,
	currentLayer phases.LayerResult,
) (phases.LayerResult, error) {
	ctx, span := tracer.Start(ctx, "build step", trace.WithAttributes(
		attribute.Int("step", sb.stepNumber),
		attribute.String("type", sb.step.Type),
		attribute.String("hash", currentLayer.Hash),
	))
	defer span.End()

	prefix := sb.Prefix()
	step := sb.step

	sbxConfig := sandbox.Config{
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
		sandboxCreator = layer.NewCreateSandbox(
			sbxConfig,
			layerTimeout,
			fc.FirecrackerVersions{
				KernelVersion:      sb.Template.KernelVersion,
				FirecrackerVersion: sb.Template.FirecrackerVersion,
			},
		)
	} else {
		sandboxCreator = layer.NewResumeSandbox(sbxConfig, layerTimeout)
	}

	actionExecutor := layer.NewFunctionAction(func(ctx context.Context, sbx *sandbox.Sandbox, meta metadata.Template) (metadata.Template, error) {
		cmdMeta, err := sb.commandExecutor.Execute(
			ctx,
			userLogger,
			sbx,
			prefix,
			step,
			meta.Context,
		)
		if err != nil {
			return metadata.Template{}, phases.NewPhaseBuildError(sb, err)
		}

		err = sandboxtools.SyncChangesToDisk(
			ctx,
			sb.proxy,
			sbx.Runtime.SandboxID,
		)
		if err != nil {
			return metadata.Template{}, fmt.Errorf("error running sync command: %w", err)
		}

		meta.Context = cmdMeta
		return meta, nil
	})

	templateProvider := layer.NewCacheSourceTemplateProvider(sourceLayer.Metadata.Template)

	meta, err := sb.layerExecutor.BuildLayer(ctx, userLogger, layer.LayerBuildCommand{
		SourceTemplate: templateProvider,
		CurrentLayer:   currentLayer.Metadata,
		Hash:           currentLayer.Hash,
		UpdateEnvd:     sourceLayer.Cached,
		SandboxCreator: sandboxCreator,
		ActionExecutor: actionExecutor,
	})
	if err != nil {
		return phases.LayerResult{}, fmt.Errorf("error building step %d: %w", sb.stepNumber, err)
	}

	return phases.LayerResult{
		Metadata: meta,
		Cached:   false,
		Hash:     currentLayer.Hash,
	}, nil
}
