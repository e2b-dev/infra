package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"time"

	"cloud.google.com/go/storage"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/consul"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/dns"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	localStorage "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/local_storage"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
	templateStorage "github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"go.opentelemetry.io/otel"
)

func main() {
	templateId := flag.String("template", "", "template id")
	buildId := flag.String("build", "", "build id")
	sandboxId := flag.String("sandbox", "", "sandbox id")
	keepAlive := flag.Int("alive", 0, "keep alive")
	count := flag.Int("count", 1, "number of serially spawned sandboxes")

	flag.Parse()

	timeout := time.Second*time.Duration(*keepAlive) + time.Second*50
	fmt.Printf("timeout: %d\n", timeout)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt)

	go func() {
		<-done

		cancel()
	}()

	// Start of mock build for testing
	dns := dns.New()
	go dns.Start("127.0.0.4:53")

	client, err := storage.NewClient(ctx, storage.WithJSONReads())
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create GCS client: %v\n", err)

		return
	}

	consulClient, err := consul.New(context.Background())

	templateCache := localStorage.NewTemplateCache(ctx, client, templateStorage.BucketName)

	networkPool := network.NewSlotPool(10, 0, consulClient)

	go func() {
		poolErr := networkPool.Populate(ctx)
		if poolErr != nil {
			fmt.Fprintf(os.Stderr, "network pool error: %v\n", poolErr)
		}

		closeErr := networkPool.Close()
		if closeErr != nil {
			fmt.Fprintf(os.Stderr, "network pool close error: %v\n", closeErr)
		}
	}()

	eg, ctx := errgroup.WithContext(ctx)

	for i := 0; i < *count; i++ {
		fmt.Printf("Starting sandbox %d\n", i)

		v := i

		eg.Go(func() error {
			mockSandbox(
				ctx,
				*templateId,
				*buildId,
				*sandboxId+"-"+strconv.Itoa(v),
				dns,
				time.Duration(*keepAlive)*time.Second,
				networkPool,
				templateCache,
			)

			return nil
		})

	}

	err = eg.Wait()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to start sandboxes: %v\n", err)
	}
}

func mockSandbox(
	ctx context.Context,
	templateId,
	buildId,
	sandboxId string,
	dns *dns.DNS,
	keepAlive time.Duration,
	networkPool *network.SlotPool,
	templateCache *localStorage.TemplateCache,
) {
	tracer := otel.Tracer(fmt.Sprintf("sandbox-%s", sandboxId))
	childCtx, _ := tracer.Start(ctx, "mock-sandbox")

	start := time.Now()
	logger := logs.NewSandboxLogger(sandboxId, templateId, "test-team", 2, 512, false)

	sbx, cleanup, err := sandbox.NewSandbox(
		childCtx,
		tracer,
		dns,
		networkPool,
		templateCache,
		&orchestrator.SandboxConfig{
			TemplateId:         templateId,
			FirecrackerVersion: "v1.7.0-dev_8bb88311",
			KernelVersion:      "vmlinux-5.10.186",
			TeamId:             "test-team",
			BuildId:            buildId,
			HugePages:          true,
			MaxSandboxLength:   1,
			SandboxId:          sandboxId,
			EnvdVersion:        "0.1.1",
			RamMb:              512,
			Vcpu:               2,
		},
		"trace-test-1",
		time.Now(),
		time.Now(),
		logger,
	)
	if err != nil {
		cleanupErr := sandbox.HandleCleanup(cleanup)

		fmt.Fprintf(os.Stderr, "failed to create sandbox: %v\n", errors.Join(err, cleanupErr))

		return
	}

	duration := time.Since(start)

	fmt.Printf("[Sandbox is running] - started in %dms (without network)\n", duration.Milliseconds())

	time.Sleep(keepAlive)

	defer func() {
		cleanupErr := sandbox.HandleCleanup(cleanup)
		if cleanupErr != nil {
			fmt.Fprintf(os.Stderr, "failed to cleanup sandbox: %v\n", cleanupErr)
		}
	}()

	sbx.Stop(childCtx, tracer)
}
