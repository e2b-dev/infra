package builder

import (
	"context"
	"fmt"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
)

// Template builder sandbox configuration
var builderConfig = orchestrator.SandboxConfig{
	TemplateId:         "p9kw2u9cc1zj1cov2zru",
	BuildId:            "6bea9b8c-7344-4e8d-bfdc-16b10876606c",
	KernelVersion:      "vmlinux-6.1.102",
	FirecrackerVersion: "v1.10.1_1fcdaec",
	HugePages:          true,
	EnvdVersion:        "0.2.0",
	Vcpu:               8,
	RamMb:              8 * 1024, // 8 GB of RAM

	BaseTemplateId: "p9kw2u9cc1zj1cov2zru",
}

const (
	instanceBuilderPrefix = "bd"
	engineTimeout         = 60 * time.Minute
)

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
) *DaggerEngine {
	config := proto.Clone(&builderConfig).(*orchestrator.SandboxConfig)
	config.SandboxId = instanceBuilderPrefix + id.Generate()
	config.ExecutionId = uuid.New().String()

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
		nil,
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
