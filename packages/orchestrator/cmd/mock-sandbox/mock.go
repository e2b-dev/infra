package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	nbd "github.com/e2b-dev/infra/packages/block-storage/pkg/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/consul"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/dns"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	sandboxStorage "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/storage"
	snapshotStorage "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	templateStorage "github.com/e2b-dev/infra/packages/shared/pkg/storage"
	consulapi "github.com/hashicorp/consul/api"

	"cloud.google.com/go/storage"
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

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Start of mock build for testing
	dns := dns.New()
	go dns.Start("127.0.0.4:53")

	client, err := storage.NewClient(ctx, storage.WithJSONReads())
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create GCS client: %v\n", err)

		return
	}

	consulClient, err := consul.New(context.Background())

	networkPool := network.NewSlotPool(*count, consulClient)

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

	nbdDevicePool, err := nbd.NewDevicePool()
	if err != nil {
		fmt.Printf("failed to create NBD device pool: %v\n", err)

		return
	}

	templateCache := sandboxStorage.NewTemplateCache(ctx, client, templateStorage.BucketName, nbdDevicePool)

	for i := 0; i < *count; i++ {
		fmt.Printf("Starting sandbox %d\n", i)
		mockSandbox(
			ctx,
			*templateId,
			*buildId,
			*sandboxId+"-"+strconv.Itoa(i),
			dns,
			templateCache,
			time.Duration(*keepAlive)*time.Second,
			nbdDevicePool,
			networkPool,
			consulClient,
		)
	}
}

func mockSandbox(
	ctx context.Context,
	templateId,
	buildId,
	sandboxId string,
	dns *dns.DNS,
	templateCache *snapshotStorage.TemplateCache,
	keepAlive time.Duration,
	nbdDevicePool *nbd.DevicePool,
	networkPool *network.SlotPool,
	consulClient *consulapi.Client,
) {
	tracer := otel.Tracer(fmt.Sprintf("sandbox-%s", sandboxId))
	childCtx, _ := tracer.Start(ctx, "mock-sandbox")

	start := time.Now()

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
		},
		"trace-test-1",
		time.Now(),
		time.Now(),
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

		fmt.Fprintf(os.Stderr, "failed to cleanup sandbox: %v\n", cleanupErr)
	}()

	sbx.Stop()
}
