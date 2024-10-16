package sandbox

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/consul"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/dns"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func MockInstance(envID, instanceID string, dns *dns.DNS, keepAlive time.Duration, stressTest bool) {
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), telemetry.DebugID, instanceID), time.Second*4)
	defer cancel()

	tracer := otel.Tracer(fmt.Sprintf("instance-%s", instanceID))
	childCtx, _ := tracer.Start(ctx, "mock-instance")

	consulClient, err := consul.New(childCtx)

	networkPool := make(chan IPSlot, 1)

	exporter := logs.NewSandboxLogExporter(logs.OrchestratorServiceName)

	logger := exporter.CreateSandboxLogger(instanceID, envID, "test-team", 2, 512)

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
		&orchestrator.SandboxConfig{
			TemplateID:         envID,
			FirecrackerVersion: "v1.9.1_3370eaf8",
			KernelVersion:      "vmlinux-6.1.102",
			TeamID:             "test-team",
			BuildID:            "id",
			HugePages:          true,
			MaxInstanceLength:  1,
			SandboxID:          instanceID,
		},
		"trace-test-1",
		time.Now(),
		time.Now(),
		logger,
	)
	if err != nil {
		panic(err)
	}

	duration := time.Since(start)
	fmt.Printf("[Sandbox is running] - started in %dms (without network)\n", duration.Milliseconds())

	if stressTest {
		fmt.Println("[Sandbox is about to be stressed]")
		runStressTest(instance, keepAlive, start)
	} else {
		time.Sleep(keepAlive)
	}

	defer instance.CleanupAfterFCStop(childCtx, tracer, consulClient, dns, instanceID)
	defer KillAllFirecrackerProcesses()

	instance.Stop(childCtx, tracer)
}

func runStressTest(instance *Sandbox, keepAlive time.Duration, start time.Time) {
	go func() {
		output, err := Stress(instance.fc.id)
		if err != nil {
			fmt.Printf("[Stress Test][ERROR] error:%s\noutput:%s\n", err, output)
			panic(err)
		}
	}()

	for time.Since(start) < keepAlive {
		time.Sleep(2 * time.Second)
		procStats, err := instance.stats.GetStats()
		if err != nil {
			panic(err)
		}

		fmt.Printf("[%s] CPU:%.2f Memery:%.2f \n", time.Now().Format("15:04:05"), procStats.CPUCount, procStats.MemoryMB)
	}
}
