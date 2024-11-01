package sandbox

import (
	"context"
	"fmt"
	"os"
	"time"

	"go.opentelemetry.io/otel"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/consul"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/dns"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func MockInstance(envID, instanceID string, dns *dns.DNS, keepAlive time.Duration) {
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), telemetry.DebugID, instanceID), time.Second*4)
	defer cancel()

	tracer := otel.Tracer(fmt.Sprintf("instance-%s", instanceID))
	childCtx, _ := tracer.Start(ctx, "mock-instance")

	consulClient, err := consul.New(childCtx)
	if err != nil {
		panic(err)
	}

	//logger := logs.NewSandboxLogger(instanceID, envID, "test-team", 2, 512, false)

	networkPool := NewNetworkSlotPool(1, 0)

	go func() {
		poolErr := networkPool.Start(ctx, consulClient)
		if poolErr != nil {
			fmt.Fprintf(os.Stderr, "network pool error: %v\n", poolErr)
		}

		closeErr := networkPool.Close(consulClient)
		if closeErr != nil {
			fmt.Fprintf(os.Stderr, "network pool close error: %v\n", closeErr)
		}
	}()

	start := time.Now()

	//instance, err := NewSandbox(
	//	childCtx,
	//	tracer,
	//	consulClient,
	//	dns,
	//	networkPool,
	//	&orchestrator.SandboxConfig{
	//		TemplateID:         envID,
	//		FirecrackerVersion: "v1.9.1_3370eaf8",
	//		KernelVersion:      "vmlinux-6.1.102",
	//		TeamID:             "test-team",
	//		BuildID:            "id",
	//		HugePages:          true,
	//		MaxInstanceLength:  1,
	//		SandboxID:          instanceID,
	//	},
	//	"trace-test-1",
	//	time.Now(),
	//	time.Now(),
	//	logger,
	//)
	//if err != nil {
	//	panic(err)
	//}

	duration := time.Since(start)
	fmt.Printf("[Sandbox is running] - started in %dms (without network)\n", duration.Milliseconds())

	time.Sleep(keepAlive)

	//defer instance.CleanupAfterFCStop(childCtx, tracer, consulClient, dns, instanceID)
	//
	//instance.Stop(childCtx, tracer)
}
