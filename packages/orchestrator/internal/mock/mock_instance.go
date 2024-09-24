package mock

import (
	"context"
	"fmt"
	"time"

	nbd "github.com/e2b-dev/infra/packages/block-storage/pkg/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/consul"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/dns"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	snapshotStorage "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"

	"go.opentelemetry.io/otel"
)

func MockInstance(
	ctx context.Context,
	envID,
	buildID,
	instanceID string,
	dns *dns.DNS,
	templateCache *snapshotStorage.TemplateDataCache,
	keepAlive time.Duration,
) {
	tracer := otel.Tracer(fmt.Sprintf("instance-%s", instanceID))
	childCtx, _ := tracer.Start(ctx, "mock-instance")

	nbdDevicePool, err := nbd.NewNbdDevicePool()
	if err != nil {
		panic(err)
	}

	consulClient, err := consul.New(childCtx)

	networkPool := make(chan sandbox.IPSlot, 1)

	select {
	case <-ctx.Done():
		return
	default:
		ips, err := sandbox.NewSlot(ctx, tracer, consulClient)
		if err != nil {
			fmt.Printf("failed to create network: %v\n", err)

			return
		}

		err = ips.CreateNetwork(ctx, tracer)
		if err != nil {
			ips.Release(ctx, tracer, consulClient)

			fmt.Printf("failed to create network: %v\n", err)

			return
		}

		networkPool <- *ips
	}

	start := time.Now()

	instance, err := sandbox.NewSandbox(
		childCtx,
		tracer,
		consulClient,
		dns,
		networkPool,
		templateCache,
		nbdDevicePool,
		&orchestrator.SandboxConfig{
			TemplateID:         envID,
			FirecrackerVersion: "v1.7.0-dev_8bb88311",
			KernelVersion:      "vmlinux-5.10.186",
			TeamID:             "test-team",
			BuildID:            buildID,
			HugePages:          true,
			MaxInstanceLength:  1,
			SandboxID:          instanceID,
		},
		"trace-test-1",
		time.Now(),
		time.Now(),
	)
	if err != nil {
		errMsg := fmt.Errorf("failed to create sandbox: %v", err)
		telemetry.ReportError(ctx, errMsg)
		return
	}

	duration := time.Since(start)

	fmt.Printf("[Sandbox is running] - started in %dms (without network)\n", duration.Milliseconds())

	time.Sleep(keepAlive)

	defer instance.CleanupAfterFCStop(childCtx, tracer, consulClient, dns, instanceID)

	instance.Stop(childCtx, tracer)
}
