package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/dns"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"go.opentelemetry.io/otel"
)

const (
	// envAlias is the alias of the base template to use for the sandbox
	envAlias = "base"
)

type env struct {
	dnsServer     *dns.DNS
	networkPool   *network.Pool
	templateCache *template.Cache
}

type vars struct {
	templateId string
	buildId    string

	fcVersion     string
	kernelVersion string
	envdVersion   string
}

func prepareEnv(ctx context.Context, _ testing.TB) (*env, error) {
	dnsServer := dns.New()

	templateCache, err := template.NewCache(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create template cache: %w", err)
	}

	networkPool, err := network.NewPool(ctx, 1, 1)
	if err != nil {
		return nil, fmt.Errorf("failed to create network pool: %w", err)
	}

	return &env{
		dnsServer:     dnsServer,
		networkPool:   networkPool,
		templateCache: templateCache,
	}, nil
}

type SandboxTestSuite struct {
	suite.Suite
	env  *env
	vars vars

	parent context.Context
	ctx    context.Context
	cancel context.CancelFunc
}

func NewSandboxTestSuite(
	ctx context.Context,
	env *env,
) *SandboxTestSuite {
	db, err := db.NewClient()
	if err != nil {
		panic(fmt.Sprintf("failed to create db client: %v", err))
	}
	defer db.Close()

	template, build, err := db.GetEnv(ctx, envAlias)
	if err != nil {
		panic(fmt.Sprintf("failed to get %s env: %v", envAlias, err))
	}

	return &SandboxTestSuite{
		env:    env,
		parent: ctx,

		vars: vars{
			templateId: template.TemplateID,
			buildId:    template.BuildID,

			fcVersion:     build.FirecrackerVersion,
			kernelVersion: build.KernelVersion,
			envdVersion:   *build.EnvdVersion,
		},
	}
}

func TestSandboxTestSuite(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lEnv, err := prepareEnv(ctx, t)
	if err != nil {
		t.Fatalf("failed to prepare environment: %v", err)
	}

	suite.Run(t, NewSandboxTestSuite(ctx, lEnv))
}

func (suite *SandboxTestSuite) SetupTest() {
	ctx, cancel := context.WithCancel(suite.parent)

	suite.ctx = ctx
	suite.cancel = cancel
}

func (suite *SandboxTestSuite) TearDownTest() {
	suite.ctx = nil
	suite.cancel()
}

func (suite *SandboxTestSuite) TestNewSandbox() {
	sbx, cleanup, err := suite.createSandbox(512, 2)
	if err != nil {
		suite.T().Fatalf("failed to create sandbox: %v", err)
	}
	defer cleanup.Run()

	suite.Assert().NotNil(sbx)
}

func (suite *SandboxTestSuite) TestSnapshot() {
	sbx, cleanup, err := suite.createSandbox(512, 2)
	if err != nil {
		suite.T().Fatalf("failed to create sandbox: %v", err)
	}
	defer cleanup.Run()

	snapshot, err := suite.snapshotSandbox(sbx)
	if err != nil {
		suite.T().Fatalf("failed to snapshot sandbox: %v", err)
	}

	suite.Assert().NotNil(snapshot)
}

func genericBenchmarkSandbox(b *testing.B, ramMb, vCpu int64) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lEnv, err := prepareEnv(ctx, b)
	if err != nil {
		b.Fatalf("failed to prepare environment: %v", err)
	}
	suite := NewSandboxTestSuite(ctx, lEnv)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()

		func() {
			suite.SetupTest()
			defer suite.TearDownTest()

			sbx, cleanup, err := suite.createSandbox(ramMb, vCpu)
			if err != nil {
				b.Fatalf("failed to create sandbox: %v", err)
			}
			defer cleanup.Run()

			b.StartTimer()
			suite.snapshotSandbox(sbx)
			b.StopTimer()
		}()
	}
}

func BenchmarkCreate(b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lEnv, err := prepareEnv(ctx, b)
	if err != nil {
		b.Fatalf("failed to prepare environment: %v", err)
	}

	suite := NewSandboxTestSuite(ctx, lEnv)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()

		func() {
			suite.SetupTest()
			defer suite.TearDownTest()

			b.StartTimer()
			_, cleanup, err := suite.createSandbox(512, 2)
			b.StopTimer()
			if err != nil {
				b.Fatalf("failed to create sandbox: %v", err)
			}
			defer cleanup.Run()
		}()
	}
}

func BenchmarkSnapshot512(b *testing.B) {
	genericBenchmarkSandbox(b, 512, 1)
}

func BenchmarkSnapshot1024(b *testing.B) {
	genericBenchmarkSandbox(b, 1024, 1)
}

func BenchmarkSnapshot2048(b *testing.B) {
	genericBenchmarkSandbox(b, 2*1024, 1)
}

func BenchmarkSnapshot4096(b *testing.B) {
	genericBenchmarkSandbox(b, 4*1024, 1)
}

func BenchmarkSnapshot8192(b *testing.B) {
	genericBenchmarkSandbox(b, 8*1024, 1)
}

func (suite *SandboxTestSuite) snapshotSandbox(sbx *Sandbox) (*Snapshot, error) {
	snapshotTemplateFiles, err := storage.NewTemplateFiles(
		"snapshot-template",
		"f0370054-b669-eee4-b33b-573d5287c6ef",
		sbx.Config.KernelVersion,
		sbx.Config.FirecrackerVersion,
		sbx.Config.HugePages,
	).NewTemplateCacheFiles()
	if err != nil {
		return nil, fmt.Errorf("failed to create snapshot template files: %s", err)
	}

	err = os.MkdirAll(snapshotTemplateFiles.CacheDir(), 0o755)
	if err != nil {
		return nil, fmt.Errorf("failed to create snapshot template files directory: %s", err)
	}
	defer os.RemoveAll(snapshotTemplateFiles.CacheDir())

	snapshot, err := sbx.Snapshot(suite.ctx, otel.Tracer("orchestrator-mock"), snapshotTemplateFiles, func() {})
	if err != nil {
		return nil, fmt.Errorf("failed to snapshot sandbox: %s", err)
	}

	return snapshot, nil
}

func (suite *SandboxTestSuite) createSandbox(ramMb, vCpu int64) (*Sandbox, *Cleanup, error) {
	sandboxId := "test-sandbox-1"
	teamId := "test-team"

	tracer := otel.Tracer(fmt.Sprintf("sandbox-%s", sandboxId))
	childCtx, _ := tracer.Start(suite.ctx, "mock-sandbox")

	logger := logs.NewSandboxLogger(sandboxId, suite.vars.templateId, teamId, vCpu, ramMb, false)

	sbx, cleanup, err := NewSandbox(
		childCtx,
		tracer,
		suite.env.dnsServer,
		suite.env.networkPool,
		suite.env.templateCache,
		&orchestrator.SandboxConfig{
			TemplateId:         suite.vars.templateId,
			FirecrackerVersion: suite.vars.fcVersion,
			KernelVersion:      suite.vars.kernelVersion,
			TeamId:             teamId,
			BuildId:            suite.vars.buildId,
			HugePages:          true,
			MaxSandboxLength:   1,
			SandboxId:          sandboxId,
			EnvdVersion:        suite.vars.envdVersion,
			RamMb:              ramMb,
			Vcpu:               vCpu,
		},
		"trace-test-1",
		time.Now(),
		time.Now(),
		logger,
		false,
		suite.vars.templateId,
	)
	if err != nil {
		errCleanup := cleanup.Run()
		return nil, nil, fmt.Errorf("failed to create sandbox: %v", errors.Join(err, errCleanup))
	}

	return sbx, cleanup, nil
}
