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
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/phases"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/storage/cache"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

type StepBuilder struct {
	buildcontext.BuildContext

	stepNumber int
	step       *templatemanager.TemplateStep

	logger *zap.Logger
	tracer trace.Tracer
	proxy  *proxy.SandboxProxy

	layerExecutor   *layer.LayerExecutor
	commandExecutor *commands.CommandExecutor
	index           cache.Index
	metrics         *metrics.BuildMetrics
}

func New(
	buildContext buildcontext.BuildContext,
	logger *zap.Logger,
	tracer trace.Tracer,
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
		tracer: tracer,
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
		Phase:    metrics.PhaseSteps,
		StepType: sb.step.Type,
	}
}

func (sb *StepBuilder) Layer(
	ctx context.Context,
	sourceLayer phases.LayerResult,
	hash string,
) (phases.LayerResult, error) {
	forceBuild := sb.step.Force != nil && *sb.step.Force
	if !forceBuild {
		m, err := sb.index.LayerMetaFromHash(ctx, hash)
		if err != nil {
			sb.logger.Info("layer not found in cache, building new base layer", zap.Error(err), zap.String("hash", hash))
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

func (sb *StepBuilder) Build(
	ctx context.Context,
	sourceLayer phases.LayerResult,
	currentLayer phases.LayerResult,
	baseTemplateID string,
) (phases.LayerResult, error) {
	prefix := sb.Prefix()
	step := sb.step

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
		}, currentLayer.Metadata.Template.TemplateID)
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
			return sandboxtools.CommandMetadata{}, &phases.PhaseBuildError{
				Phase:   string(metrics.PhaseSteps),
				Step:    fmt.Sprintf("%d", sb.stepNumber),
				Message: "error processing step",
				Err:     err,
			}
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
