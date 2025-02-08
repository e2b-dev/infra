package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"

	"github.com/bits-and-blooms/bitset"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/dns"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"

	mocks "github.com/e2b-dev/infra/packages/orchestrator/mocks/internal_/sandbox"
)

const (
	// envAlias is the alias of the base template to use for the sandbox
	envAlias = "base"

	// giB is the number of bytes in a gibibyte
	giB = int64(1 << 30)

	// miB is the number of bytes in a megabyte
	miB = int64(1 << 20)
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

	sandboxId string
	teamId    string
	tracer    trace.Tracer
	logger    *logs.SandboxLogger
}

func NewSandboxTestSuite(
	ctx context.Context,
	env *env,
) *SandboxTestSuite {
	sandboxId := "test-sandbox-1"
	teamId := "test-team"

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
		env:       env,
		parent:    ctx,
		sandboxId: sandboxId,
		teamId:    teamId,
		tracer:    otel.Tracer(fmt.Sprintf("sandbox-%s", sandboxId)),
		logger:    logs.NewSandboxLogger(sandboxId, template.TemplateID, teamId, 2, 512, false),
		vars: vars{
			templateId:    template.TemplateID,
			buildId:       template.BuildID,
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
	suite.ctx, suite.cancel = context.WithCancel(suite.parent)
}

func (suite *SandboxTestSuite) TearDownTest() {
	if suite.cancel != nil {
		suite.cancel()
	}
}

func (suite *SandboxTestSuite) TestNewSandbox() {
	sbx, cleanup, err := suite.createSandbox(512, 2)
	suite.Require().NoError(err)
	defer cleanup.Run()

	suite.Require().NotNil(sbx)
}

func (suite *SandboxTestSuite) TestSandboxSnapshot() {
	sbx, cleanup, err := suite.createSandbox(512, 2)
	suite.Require().NoError(err)
	defer cleanup.Run()

	snapshot, err := suite.snapshotSandbox(sbx)
	suite.Require().NoError(err)

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
		suite.SetupTest()

		sbx, cleanup, err := suite.createSandbox(ramMb, vCpu)
		if err != nil {
			b.Fatalf("failed to create sandbox: %v", err)
		}

		b.StartTimer()
		_, err = suite.snapshotSandbox(sbx)
		b.StopTimer()

		cleanup.Run()
		suite.TearDownTest()

		if err != nil {
			b.Fatalf("failed to snapshot sandbox: %v", err)
		}
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
		suite.SetupTest()

		b.StartTimer()
		_, cleanup, err := suite.createSandbox(512, 2)
		b.StopTimer()

		if err != nil {
			b.Fatalf("failed to create sandbox: %v", err)
		}
		cleanup.Run()
		suite.TearDownTest()
	}
}

func BenchmarkSandboxSnapshot(b *testing.B) {
	tests := []struct {
		name  string
		ramMb int64
		vCpu  int64
	}{
		{"512MB", 512, 1},
		{"1GB", 1024, 1},
		{"2GB", 2 * 1024, 1},
		{"4GB", 4 * 1024, 1},
		{"8GB", 8 * 1024, 1},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			genericBenchmarkSandbox(b, tt.ramMb, tt.vCpu)
		})
	}
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

	snapshot, err := sbx.Snapshot(suite.ctx, suite.tracer, snapshotTemplateFiles, func() {})
	if err != nil {
		return nil, fmt.Errorf("failed to snapshot sandbox: %s", err)
	}

	return snapshot, nil
}

func (suite *SandboxTestSuite) createSandbox(ramMb, vCpu int64) (*Sandbox, *Cleanup, error) {
	childCtx, _ := suite.tracer.Start(suite.ctx, "mock-sandbox")

	sbx, cleanup, err := NewSandbox(
		childCtx,
		suite.tracer,
		suite.env.dnsServer,
		suite.env.networkPool,
		suite.env.templateCache,
		&orchestrator.SandboxConfig{
			TemplateId:         suite.vars.templateId,
			FirecrackerVersion: suite.vars.fcVersion,
			KernelVersion:      suite.vars.kernelVersion,
			TeamId:             suite.teamId,
			BuildId:            suite.vars.buildId,
			HugePages:          true,
			MaxSandboxLength:   1,
			SandboxId:          suite.sandboxId,
			EnvdVersion:        suite.vars.envdVersion,
			RamMb:              ramMb,
			Vcpu:               vCpu,
		},
		"trace-test-1",
		time.Now(),
		time.Now(),
		suite.logger,
		false,
		suite.vars.templateId,
	)
	if err != nil {
		errCleanup := cleanup.Run()
		return nil, nil, fmt.Errorf("failed to create sandbox: %v", errors.Join(err, errCleanup))
	}

	return sbx, cleanup, nil
}

func setupMocksWithMemory(snapshotTemplateFiles *storage.TemplateCacheFiles, sp *mocks.SnapshotProvider, tp *mocks.TemplateProvider, memorySize int64) func() {
	// Dummy mocks
	sp.On("PauseVM", mock.Anything).Return(nil)
	sp.On("DisableUffd").Return(nil)
	sp.On("CreateVMSnapshot", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	sp.On("FlushRootfsNBD", mock.Anything).Return(nil)

	// Memory setup
	memoryPageSize := int64(4096)
	memoryBlocks := uint(memorySize / memoryPageSize)
	memoryData := make([]byte, int64(memoryBlocks)*memoryPageSize)

	sp.On("GetMemfilePageSize").Return(memoryPageSize)

	dirtyPages := bitset.New(memoryBlocks)
	// Sets dirty pages (0-indexed)
	dirtyPages.Set(1)
	dirtyPages.Set(3)
	sp.On("GetDirtyUffd").Return(dirtyPages)

	// Rootfs setup
	rootfsPageSize := int64(4096)
	rootfsBlocks := uint(5 * giB / rootfsPageSize)

	dirtyPagesRootfs := bitset.New(rootfsBlocks)
	// Sets dirty pages (0-indexed)
	dirtyPagesRootfs.Set(1)
	dirtyPagesRootfs.Set(5)
	// Creates a file with the rootfs data
	rootfsData := make([]byte, int64(rootfsBlocks)*rootfsPageSize)
	os.WriteFile(snapshotTemplateFiles.StorageRootfsPath(), rootfsData, 0644)
	sp.On("ExportRootfs", mock.Anything, mock.Anything, mock.Anything).Return(dirtyPagesRootfs, nil)

	// Headers
	memStorageHeader := header.NewHeader(&header.Metadata{
		Version:    1,
		Size:       1024,
		BlockSize:  uint64(memoryPageSize),
		Generation: 1,
		BuildId:    uuid.New(),
	}, nil)
	rootfsStorageHeader := header.NewHeader(&header.Metadata{
		Version:    1,
		Size:       1024,
		BlockSize:  4096,
		Generation: 1,
		BuildId:    uuid.New(),
	}, nil)
	tp.On("MemfileHeader").Return(memStorageHeader, nil)
	tp.On("RootfsHeader").Return(rootfsStorageHeader, nil)

	// Create a temporary file with some content for the memfile snapshot
	os.WriteFile(snapshotTemplateFiles.CacheMemfileFullSnapshotPath(), memoryData, 0644)

	return func() {
		os.Remove(snapshotTemplateFiles.CacheMemfileFullSnapshotPath())
		os.Remove(snapshotTemplateFiles.StorageRootfsPath())
	}
}

func TestSnapshotLite(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := db.NewClient()
	if err != nil {
		t.Fatalf("%s", fmt.Sprintf("failed to create db client: %v", err))
	}
	defer db.Close()
	_, build, err := db.GetEnv(ctx, envAlias)
	if err != nil {
		t.Fatalf("%s", fmt.Sprintf("failed to get %s env: %v", envAlias, err))
	}
	snapshotTemplateFiles, err := storage.NewTemplateFiles(
		"snapshot-template",
		"f0370054-b669-eee4-b33b-573d5287c6ef",
		build.KernelVersion,
		build.FirecrackerVersion,
		true,
	).NewTemplateCacheFiles()
	if err != nil {
		t.Fatalf("failed to create snapshot template files: %s", err)
	}
	os.MkdirAll(snapshotTemplateFiles.CacheDir(), 0o755)
	defer os.RemoveAll(snapshotTemplateFiles.CacheDir())

	tests := []struct {
		name           string
		setupMocks     func(*mocks.SnapshotProvider, *mocks.TemplateProvider)
		closeMocks     func(*mocks.SnapshotProvider, *mocks.TemplateProvider)
		expectedError  string
		expectedResult *Snapshot
	}{
		{
			name: "successful snapshot - 1 GiB memory",
			setupMocks: func(sp *mocks.SnapshotProvider, tp *mocks.TemplateProvider) {
				setupMocksWithMemory(snapshotTemplateFiles, sp, tp, 1*giB)
			},
			closeMocks: func(sp *mocks.SnapshotProvider, tp *mocks.TemplateProvider) {
				os.Remove(snapshotTemplateFiles.CacheMemfileFullSnapshotPath())
				os.Remove(snapshotTemplateFiles.StorageRootfsPath())
			},
			expectedError:  "",
			expectedResult: &Snapshot{},
		},
		{
			name: "successful snapshot - 2 GiB memory",
			setupMocks: func(sp *mocks.SnapshotProvider, tp *mocks.TemplateProvider) {
				setupMocksWithMemory(snapshotTemplateFiles, sp, tp, 2*giB)
			},
			closeMocks: func(sp *mocks.SnapshotProvider, tp *mocks.TemplateProvider) {
				os.Remove(snapshotTemplateFiles.CacheMemfileFullSnapshotPath())
				os.Remove(snapshotTemplateFiles.StorageRootfsPath())
			},
			expectedError:  "",
			expectedResult: &Snapshot{},
		},
		{
			name: "successful snapshot - 4 GiB memory",
			setupMocks: func(sp *mocks.SnapshotProvider, tp *mocks.TemplateProvider) {
				setupMocksWithMemory(snapshotTemplateFiles, sp, tp, 4*giB)
			},
			closeMocks: func(sp *mocks.SnapshotProvider, tp *mocks.TemplateProvider) {
				os.Remove(snapshotTemplateFiles.CacheMemfileFullSnapshotPath())
				os.Remove(snapshotTemplateFiles.StorageRootfsPath())
			},
			expectedError:  "",
			expectedResult: &Snapshot{},
		},
		{
			name: "successful snapshot - 8 GiB memory",
			setupMocks: func(sp *mocks.SnapshotProvider, tp *mocks.TemplateProvider) {
				setupMocksWithMemory(snapshotTemplateFiles, sp, tp, 8*giB)
			},
			closeMocks: func(sp *mocks.SnapshotProvider, tp *mocks.TemplateProvider) {
				os.Remove(snapshotTemplateFiles.CacheMemfileFullSnapshotPath())
				os.Remove(snapshotTemplateFiles.StorageRootfsPath())
			},
			expectedError:  "",
			expectedResult: &Snapshot{},
		},
		{
			name: "pause VM fails",
			setupMocks: func(sp *mocks.SnapshotProvider, tp *mocks.TemplateProvider) {
				sp.On("PauseVM", mock.Anything).Return(errors.New("failed to pause VM"))
			},
			expectedError:  "failed to pause VM",
			expectedResult: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockSP := mocks.NewSnapshotProvider(t)
			mockTP := mocks.NewTemplateProvider(t)

			if tt.setupMocks != nil {
				tt.setupMocks(mockSP, mockTP)
			}

			if tt.closeMocks != nil {
				defer tt.closeMocks(mockSP, mockTP)
			}

			sandbox := createMockSandbox()

			result, err := sandbox.createSnapshot(
				context.Background(),
				otel.Tracer("test-tracer"),
				uuid.New(),
				snapshotTemplateFiles,
				mockSP,
				mockTP,
				func() {}, // release lock
			)

			if tt.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				assert.NoError(t, err)
				// assert.Equal(t, tt.expectedResult, result)
				assert.NotNil(t, result)
			}

			mockSP.AssertExpectations(t)
			mockTP.AssertExpectations(t)
		})
	}
}

func BenchmarkSandboxSnapshotLite(b *testing.B) {
	memorySizes := []int64{512, 1024, 2048, 4096} // MB

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := db.NewClient()
	if err != nil {
		b.Fatalf("%s", fmt.Sprintf("failed to create db client: %v", err))
	}
	defer db.Close()
	_, build, err := db.GetEnv(ctx, envAlias)
	if err != nil {
		b.Fatalf("%s", fmt.Sprintf("failed to get %s env: %v", envAlias, err))
	}
	snapshotTemplateFiles, err := storage.NewTemplateFiles(
		"snapshot-template",
		"f0370054-b669-eee4-b33b-573d5287c6ef",
		build.KernelVersion,
		build.FirecrackerVersion,
		true,
	).NewTemplateCacheFiles()
	if err != nil {
		b.Fatalf("failed to create snapshot template files: %s", err)
	}
	os.MkdirAll(snapshotTemplateFiles.CacheDir(), 0o755)
	defer os.RemoveAll(snapshotTemplateFiles.CacheDir())

	for _, memSize := range memorySizes {
		b.Run(fmt.Sprintf("Memory_%dMB", memSize), func(b *testing.B) {
			mockSP := mocks.NewSnapshotProvider(b)
			mockTP := mocks.NewTemplateProvider(b)

			setupMocksWithMemory(snapshotTemplateFiles, mockSP, mockTP, memSize*miB)
			sandbox := createMockSandbox()

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				result, err := sandbox.createSnapshot(
					context.Background(),
					otel.Tracer("test-tracer"),
					uuid.New(),
					snapshotTemplateFiles,
					mockSP,
					mockTP,
					func() {}, // release lock
				)
				b.StopTimer()
				if err != nil {
					b.Fatal(err)
				}
				if result == nil {
					b.Fatal("expected non-nil result")
				}
			}

			os.Remove(snapshotTemplateFiles.CacheMemfileFullSnapshotPath())
			os.Remove(snapshotTemplateFiles.StorageRootfsPath())
		})
	}
}

func createMockSandbox() *Sandbox {
	return &Sandbox{
		Config: &orchestrator.SandboxConfig{
			RamMb:         512,
			Vcpu:          1,
			SandboxId:     "test-sandbox",
			TeamId:        "test-team",
			TemplateId:    "test-template",
			BuildId:       "test-build",
			HugePages:     true,
			EnvdVersion:   "test-envd",
			KernelVersion: "test-kernel",
		},
		Logger: logs.NewSandboxLogger(
			"test-sandbox",
			"test-template",
			"test-team",
			1,
			512,
			false,
		),
	}
}
