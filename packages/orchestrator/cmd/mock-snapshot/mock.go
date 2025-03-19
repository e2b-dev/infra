package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"time"

	"go.opentelemetry.io/otel"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/dns"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/shared/pkg/chdb"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

func main() {
	templateId := flag.String("template", "", "template id")
	buildId := flag.String("build", "", "build id")
	sandboxId := flag.String("sandbox", "", "sandbox id")
	keepAlive := flag.Int("alive", 0, "keep alive")
	count := flag.Int("count", 1, "number of serially spawned sandboxes")

	flag.Parse()

	devicePool, err := nbd.NewDevicePool()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create device pool: %v\n", err)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt)

	go func() {
		<-done

		cancel()
	}()

	proxyServer := proxy.New(3333)
	dnsServer := dns.New()
	go func() {
		log.Printf("Starting DNS server")

		err := dnsServer.Start("127.0.0.4", 53)
		if err != nil {
			log.Fatalf("Failed running DNS server: %s\n", err.Error())
		}
	}()

	templateCache, err := template.NewCache(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create template cache: %v\n", err)

		return
	}

	networkPool, err := network.NewPool(ctx, *count, 0, "mock-node")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create network pool: %v\n", err)

		return
	}
	defer networkPool.Close()

	eg, ctx := errgroup.WithContext(ctx)

	for i := 0; i < *count; i++ {
		fmt.Println("--------------------------------")
		fmt.Printf("Starting sandbox %d\n", i)

		v := i

		err = mockSnapshot(
			ctx,
			*templateId,
			*buildId,
			*sandboxId+"-"+strconv.Itoa(v),
			dnsServer,
			proxyServer,
			time.Duration(*keepAlive)*time.Second,
			networkPool,
			templateCache,
			devicePool,
		)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to start sandbox: %v\n", err)
			return
		}
	}

	err = eg.Wait()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to start sandboxes: %v\n", err)
	}
}

func mockSnapshot(
	ctx context.Context,
	templateId,
	buildId,
	sandboxId string,
	dns *dns.DNS,
	proxy *proxy.SandboxProxy,
	keepAlive time.Duration,
	networkPool *network.Pool,
	templateCache *template.Cache,
	devicePool *nbd.DevicePool,
) error {
	tracer := otel.Tracer(fmt.Sprintf("sandbox-%s", sandboxId))
	childCtx, _ := tracer.Start(ctx, "mock-sandbox")

	loggerCfg := sbxlogger.SandboxLoggerConfig{
		ServiceName:      "mock-snapshot",
		IsInternal:       true,
		CollectorAddress: "http://localhost:8080",
	}
	sbxlogger.SetSandboxLoggerInternal(sbxlogger.NewLogger(ctx, loggerCfg))
	sbxlogger.SetSandboxLoggerExternal(sbxlogger.NewLogger(ctx, loggerCfg))

	mockStore := chdb.NewMockStore()

	start := time.Now()

	sbx, cleanup, err := sandbox.NewSandbox(
		childCtx,
		tracer,
		dns,
		proxy,
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
		false,
		templateId,
		"testclient",
		devicePool,
		mockStore,
		"true",
		"true",
	)
	defer func() {
		cleanupErr := cleanup.Run()
		if cleanupErr != nil {
			fmt.Fprintf(os.Stderr, "failed to cleanup sandbox: %v\n", cleanupErr)
		}
	}()

	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create sandbox: %v\n", err)

		return err
	}

	duration := time.Since(start)

	fmt.Printf("[Sandbox is running] - started in %dms \n", duration.Milliseconds())

	time.Sleep(keepAlive)

	fmt.Println("Snapshotting sandbox")

	snapshotTime := time.Now()

	snapshotTemplateFiles, err := storage.NewTemplateFiles(
		"snapshot-template",
		"f0370054-b669-eee4-b33b-573d5287c6ef",
		sbx.Config.KernelVersion,
		sbx.Config.FirecrackerVersion,
		sbx.Config.HugePages,
	).NewTemplateCacheFiles()
	if err != nil {
		return fmt.Errorf("failed to create snapshot template files: %w", err)
	}

	err = os.MkdirAll(snapshotTemplateFiles.CacheDir(), 0o755)
	if err != nil {
		return fmt.Errorf("failed to create snapshot template files directory: %w", err)
	}

	defer func() {
		err := os.RemoveAll(snapshotTemplateFiles.CacheDir())
		if err != nil {
			fmt.Fprintf(os.Stderr, "error removing sandbox cache dir '%s': %v\n", snapshotTemplateFiles.CacheDir(), err)
		}
	}()

	fmt.Println("Snapshotting sandbox")

	snapshot, err := sbx.Snapshot(ctx, otel.Tracer("orchestrator-mock"), snapshotTemplateFiles, func() {})
	if err != nil {
		return fmt.Errorf("failed to snapshot sandbox: %w", err)
	}

	fmt.Println("Create snapshot time: ", time.Since(snapshotTime).Milliseconds())

	err = templateCache.AddSnapshot(
		snapshotTemplateFiles.TemplateId,
		snapshotTemplateFiles.BuildId,
		snapshotTemplateFiles.KernelVersion,
		snapshotTemplateFiles.FirecrackerVersion,
		snapshotTemplateFiles.Hugepages(),
		snapshot.MemfileDiffHeader,
		snapshot.RootfsDiffHeader,
		snapshot.Snapfile,
		snapshot.MemfileDiff,
		snapshot.RootfsDiff,
	)
	if err != nil {
		return fmt.Errorf("failed to add snapshot to template cache: %w", err)
	}

	fmt.Println("Add snapshot to template cache time: ", time.Since(snapshotTime).Milliseconds())

	start = time.Now()

	sbx, cleanup2, err := sandbox.NewSandbox(
		childCtx,
		tracer,
		dns,
		proxy,
		networkPool,
		templateCache,
		&orchestrator.SandboxConfig{
			TemplateId:         snapshotTemplateFiles.TemplateId,
			FirecrackerVersion: snapshotTemplateFiles.FirecrackerVersion,
			KernelVersion:      snapshotTemplateFiles.KernelVersion,
			TeamId:             "test-team",
			BuildId:            snapshotTemplateFiles.BuildId,
			HugePages:          snapshotTemplateFiles.Hugepages(),
			MaxSandboxLength:   1,
			SandboxId:          sandboxId,
			EnvdVersion:        "0.1.1",
			RamMb:              512,
			Vcpu:               2,
		},
		"trace-test-1",
		time.Now(),
		time.Now(),
		false,
		templateId,
		"testclient",
		devicePool,
		mockStore,
		"true",
		"true",
	)
	defer func() {
		cleanupErr := cleanup2.Run()
		if cleanupErr != nil {
			fmt.Fprintf(os.Stderr, "failed to cleanup sandbox: %v\n", cleanupErr)
		}
	}()

	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create sandbox: %v\n", err)

		return err
	}

	duration = time.Since(start)

	fmt.Printf("[Resumed sandbox is running] - started in %dms \n", duration.Milliseconds())

	time.Sleep(keepAlive)

	// b := storage.NewTemplateBuild(
	// 	snapshot.MemfileDiffHeader,
	// 	snapshot.RootfsDiffHeader,
	// 	snapshotTemplateFiles.TemplateFiles,
	// )

	// err = <-b.Upload(
	// 	ctx,
	// 	snapshotTemplateFiles.CacheSnapfilePath(),
	// 	snapshotTemplateFiles.CacheMemfilePath(),
	// 	snapshotTemplateFiles.CacheRootfsPath(),
	// )
	// if err != nil {
	// 	return fmt.Errorf("failed to upload snapshot template files: %w", err)
	// }

	fmt.Println("Upload snapshot time: ", time.Since(snapshotTime).Milliseconds())

	duration = time.Since(snapshotTime)

	return nil
}
