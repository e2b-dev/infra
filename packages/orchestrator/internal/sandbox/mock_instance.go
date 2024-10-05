package sandbox

import (
	"context"
	"fmt"
	"time"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/consul"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/dns"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"

	"go.opentelemetry.io/otel"
)

func MockInstance(envID, instanceID string, dns *dns.DNS, keepAlive time.Duration, stressTest bool) {
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), telemetry.DebugID, instanceID), time.Second*4)
	defer cancel()

	tracer := otel.Tracer(fmt.Sprintf("instance-%s", instanceID))
	childCtx, _ := tracer.Start(ctx, "mock-instance")

	consulClient, err := consul.New(childCtx)

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
		&orchestrator.SandboxConfig{
			TemplateID:         envID,
			FirecrackerVersion: "v1.7.0-dev_8bb88311",
			KernelVersion:      "vmlinux-5.10.186",
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
		procStats, err := instance.Stats()
		if err != nil {
			panic(err)
		}

		for _, procStat := range procStats {
			fmt.Printf("[%s] %s\n", time.Now().Format("15:04:05"), procStat.String())
		}
	}
}
