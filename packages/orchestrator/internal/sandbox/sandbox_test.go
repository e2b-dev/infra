package sandbox

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/assert"
	"log"
	"os"
	"testing"
	"time"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/dns"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"go.opentelemetry.io/otel"
)

const (
	templateId = "5wzg6c91u51yaebviysf"
	buildId    = "f0370054-b669-eeee-b33b-573d5287c6ef"

	fcVersion     = "v1.7.0-dev_8bb88311"
	kernelVersion = "vmlinux-5.10.186"
	envdVersion   = "0.1.1"
)

type Env struct {
	ctx           context.Context
	dnsServer     *dns.DNS
	networkPool   *network.Pool
	templateCache *template.Cache
}

func prepareEnv(ctx context.Context) (*Env, error) {
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
		return nil, fmt.Errorf("failed to create template cache: %w", err)
	}

	networkPool, err := network.NewPool(ctx, 1, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to create network pool: %w", err)
	}

	return &Env{
		ctx:           ctx,
		dnsServer:     dnsServer,
		networkPool:   networkPool,
		templateCache: templateCache,
	}, nil
}

func TestSnapshot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	env, err := prepareEnv(ctx)
	if err != nil {
		t.Fatalf("failed to prepare environment: %v", err)
	}

	sandboxId := "test-sandbox-1"
	teamId := "test-team"
	keepAlive := time.Duration(10) * time.Second

	tracer := otel.Tracer(fmt.Sprintf("sandbox-%s", sandboxId))
	childCtx, _ := tracer.Start(env.ctx, "mock-sandbox")

	logger := logs.NewSandboxLogger(sandboxId, templateId, teamId, 2, 512, false)

	start := time.Now()

	sbx, cleanup, err := NewSandbox(
		childCtx,
		tracer,
		env.dnsServer,
		env.networkPool,
		env.templateCache,
		&orchestrator.SandboxConfig{
			TemplateId:         templateId,
			FirecrackerVersion: fcVersion,
			KernelVersion:      kernelVersion,
			TeamId:             teamId,
			BuildId:            buildId,
			HugePages:          true,
			MaxSandboxLength:   1,
			SandboxId:          sandboxId,
			EnvdVersion:        envdVersion,
			RamMb:              512,
			Vcpu:               2,
		},
		"trace-test-1",
		time.Now(),
		time.Now(),
		logger,
		false,
		templateId,
	)
	defer func() {
		cleanupErr := cleanup.Run()
		if cleanupErr != nil {
			t.Errorf("failed to cleanup sandbox: %v\n", cleanupErr)
		}
	}()

	if err != nil {
		t.Fatalf("failed to create sandbox: %v", err)
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
		t.Fatalf("failed to create snapshot template files: %w", err)
	}

	err = os.MkdirAll(snapshotTemplateFiles.CacheDir(), 0o755)
	if err != nil {
		t.Fatalf("failed to create snapshot template files directory: %w", err)
	}

	defer func() {
		err := os.RemoveAll(snapshotTemplateFiles.CacheDir())
		if err != nil {
			t.Errorf("error removing sandbox cache dir '%s': %v\n", snapshotTemplateFiles.CacheDir(), err)
		}
	}()

	fmt.Println("Snapshotting sandbox")

	_, err = sbx.Snapshot(ctx, otel.Tracer("orchestrator-mock"), snapshotTemplateFiles, func() {})
	if err != nil {
		t.Fatalf("failed to snapshot sandbox: %w", err)
	}

	fmt.Println("Create snapshot time: ", time.Since(snapshotTime).Milliseconds())

	assert.True(t, true)
}

/*
func TestSnapshot(t *testing.T) {
	var out io.Writer
	stopSandbox := func() error {
		return nil
	}
	buildId := uuid.MustParse("f0370054-b669-eee4-b33b-573d5287c6ef")
	cachePath := createTempDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	tracer := otel.Tracer("orchestrator-mock")
	t.Cleanup(cancel)

	sbx := &Sandbox{
		template:       template,
		rootfs:         template,
		cleanup:        NewCleanup(),
		process:        template,
		uffd:           template,
		files:          template, //MemfilePageSize
		healthcheckCtx: utils.NewLockableCancelableContext(ctx),
	}

	snapshotTemplateFiles, err := storage.NewTemplateFiles(
		"snapshot-template",
		buildId.String(),
		sbx.Config.KernelVersion,
		sbx.Config.FirecrackerVersion,
		sbx.Config.HugePages,
	).NewTemplateCacheFiles()
	if err != nil {
		t.Fatalf("Failed to create snapshot template files: %v", err)
	}

	_, err = sbx.Snapshot(ctx, tracer, snapshotTemplateFiles, func() {})
	if err != nil {
		t.Fatalf("Failed to snapshot sandbox: %v", err)
	}

	assert.True(t, true)
}
*/
