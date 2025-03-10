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

	"github.com/e2b-dev/infra/packages/orchestrator/internal/dns"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
)

func main() {
	templateId := flag.String("template", "", "template id")
	buildId := flag.String("build", "", "build id")
	sandboxId := flag.String("sandbox", "", "sandbox id")
	keepAlive := flag.Int("alive", 0, "keep alive")
	count := flag.Int("count", 1, "number of serially spawned sandboxes")

	devicePool, err := nbd.NewDevicePool()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create device pool: %v\n", err)

		return
	}

	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt)

	go func() {
		<-done

		cancel()
	}()

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

	for i := 0; i < *count; i++ {
		fmt.Println("--------------------------------")
		fmt.Printf("Starting sandbox %d\n", i)

		v := i

		err = mockSandbox(
			ctx,
			*templateId,
			*buildId,
			*sandboxId+"-"+strconv.Itoa(v),
			dnsServer,
			time.Duration(*keepAlive)*time.Second,
			networkPool,
			templateCache,
			devicePool,
		)
		if err != nil {
			break
		}
	}
}

func mockSandbox(
	ctx context.Context,
	templateId,
	buildId,
	sandboxId string,
	dns *dns.DNS,
	keepAlive time.Duration,
	networkPool *network.Pool,
	templateCache *template.Cache,
	devicePool *nbd.DevicePool,
) error {
	tracer := otel.Tracer(fmt.Sprintf("sandbox-%s", sandboxId))
	childCtx, _ := tracer.Start(ctx, "mock-sandbox")

	start := time.Now()

	loggerCfg := sbxlogger.SandboxLoggerConfig{
		ServiceName:      "mock-sandbox",
		IsInternal:       true,
		CollectorAddress: "http://localhost:8080",
	}
	sbxlogger.SetSandboxLoggerInternal(sbxlogger.NewLogger(ctx, loggerCfg))
	sbxlogger.SetSandboxLoggerExternal(sbxlogger.NewLogger(ctx, loggerCfg))

	sbx, cleanup, err := sandbox.NewSandbox(
		childCtx,
		tracer,
		dns,
		networkPool,
		templateCache,
		&orchestrator.SandboxConfig{
			TemplateId: templateId,
			// FirecrackerVersion: "v1.10.1_1fcdaec",
			// KernelVersion:      "vmlinux-6.1.102",
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
		true,
		templateId,
		"testclient",
		devicePool,
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

	err = sbx.Stop()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to stop sandbox: %v\n", err)

		return err
	}

	return nil
}
