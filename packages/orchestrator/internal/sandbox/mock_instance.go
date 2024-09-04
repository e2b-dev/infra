package sandbox

import (
	"context"
	"fmt"
	"time"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/constants"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/consul"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/dns"
	snapshotStorage "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"

	"cloud.google.com/go/storage"
	"go.opentelemetry.io/otel"
)

func MockInstance(envID, instanceID string, dns *dns.DNS, keepAlive time.Duration) {
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), telemetry.DebugID, instanceID), time.Second*4)
	defer cancel()

	tracer := otel.Tracer(fmt.Sprintf("instance-%s", instanceID))
	childCtx, _ := tracer.Start(ctx, "mock-instance")

	consulClient, err := consul.New(childCtx)

	client, err := storage.NewClient(ctx, storage.WithJSONReads())
	if err != nil {
		errMsg := fmt.Errorf("failed to create GCS client: %v", err)
		panic(errMsg)
	}

	snapshotCache := snapshotStorage.NewSnapshotDataCache(ctx, client, constants.BucketName)

	networkPool := make(chan IPSlot, 1)

	select {
	case <-ctx.Done():
		return
	default:
		ips, err := NewSlot(ctx, tracer, consulClient)
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

	instance, err := NewSandbox(
		childCtx,
		tracer,
		consulClient,
		dns,
		networkPool,
		snapshotCache,
		&orchestrator.SandboxConfig{
			TemplateID:         envID,
			FirecrackerVersion: "v1.9.0_fake-2476009",
			KernelVersion:      "vmlinux-6.1.99",
			TeamID:             "test-team",
			BuildID:            "id",
			HugePages:          true,
			MaxInstanceLength:  1,
			SandboxID:          instanceID,
		},
		"trace-test-1",
		time.Now(),
		time.Now(),
	)
	if err != nil {
		panic(err)
	}

	duration := time.Since(start)

	fmt.Printf("[Sandbox is running] - started in %dms (without network)\n", duration.Milliseconds())

	time.Sleep(keepAlive)

	defer instance.CleanupAfterFCStop(childCtx, tracer, consulClient, dns, instanceID)

	instance.Stop(childCtx, tracer)
}
