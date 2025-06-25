package builder

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
)

const (
	instanceBuilderPrefix = "bd"
	engineTimeout         = 60 * time.Minute
)

type Engine interface {
	Start(ctx context.Context, tracer trace.Tracer) (string, error)
	Stop(ctx context.Context, tracer trace.Tracer) error
}

type NoopEngine struct{}

func (n *NoopEngine) Start(_ context.Context, _ trace.Tracer) (string, error) {
	return "", nil
}

func (n *NoopEngine) Stop(_ context.Context, _ trace.Tracer) error {
	return nil
}

type DaggerEngine struct {
	config *orchestrator.SandboxConfig

	networkPool   *network.Pool
	templateCache *template.Cache
	devicePool    *nbd.DevicePool

	cleanup *sandbox.Cleanup
}

func NewDaggerEngine(
	networkPool *network.Pool,
	templateCache *template.Cache,
	devicePool *nbd.DevicePool,
	engineConfig *templatemanager.EngineConfig,
) Engine {
	if engineConfig == nil {
		return &NoopEngine{}
	}

	config := &orchestrator.SandboxConfig{
		TemplateId:         engineConfig.TemplateId,
		BuildId:            engineConfig.BuildId,
		KernelVersion:      engineConfig.KernelVersion,
		FirecrackerVersion: engineConfig.FirecrackerVersion,
		HugePages:          engineConfig.HugePages,
		EnvdVersion:        engineConfig.EnvdVersion,
		Vcpu:               engineConfig.Vcpu,
		RamMb:              engineConfig.RamMb,

		BaseTemplateId: engineConfig.BaseTemplateId,

		SandboxId:   instanceBuilderPrefix + id.Generate(),
		ExecutionId: uuid.New().String(),
	}

	return &DaggerEngine{
		config:        config,
		networkPool:   networkPool,
		templateCache: templateCache,
		devicePool:    devicePool,
	}
}

func (d *DaggerEngine) Start(ctx context.Context, tracer trace.Tracer) (string, error) {
	ctx, childSpan := tracer.Start(ctx, "dagger-engine-start")
	defer childSpan.End()

	sbx, cleanup, err := sandbox.ResumeSandbox(
		ctx,
		tracer,
		d.networkPool,
		d.templateCache,
		d.config,
		uuid.New().String(),
		time.Now(),
		time.Now().Add(engineTimeout),
		d.config.BaseTemplateId,
		d.devicePool,
		true,
		false,
	)
	d.cleanup = cleanup
	if err != nil {
		errStop := d.Stop(ctx, tracer)
		if errStop != nil {
			return "", fmt.Errorf("error stopping build engine after failure: %w", errStop)
		} else {
			return "", fmt.Errorf("error creating build engine: %w", err)
		}
	}

	return fmt.Sprintf("tcp://%s:1234", sbx.Slot.HostIPString()), nil
}

func (d *DaggerEngine) Stop(ctx context.Context, tracer trace.Tracer) error {
	ctx, childSpan := tracer.Start(ctx, "dagger-engine-stop")
	defer childSpan.End()

	if d.cleanup == nil {
		return fmt.Errorf("cleanup not initialized, cannot stop build engine")
	}

	cleanupErr := d.cleanup.Run(ctx)
	d.cleanup = nil
	if cleanupErr != nil {
		return fmt.Errorf("error cleaning up build engine: %w", cleanupErr)
	}

	return nil
}
