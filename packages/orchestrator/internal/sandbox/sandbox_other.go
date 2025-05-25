//go:build !linux
// +build !linux

package sandbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/build"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/rootfs"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd"
	"github.com/e2b-dev/infra/packages/shared/pkg/chdb"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

var httpClient = http.Client{
	Timeout: 10 * time.Second,
}

type NoOpProcess struct {
	Exit chan error
}

type NoOpCleanup struct {
}

func (m *NoOpCleanup) Run(ctx context.Context) error {
	return errors.New("platform does not support sandbox")
}

type Resources struct {
	Slot     network.Slot
	rootfs   *rootfs.CowDevice
	memory   uffd.MemoryBackend
	uffdExit chan error
}

type Metadata struct {
	Config    *orchestrator.SandboxConfig
	StartedAt time.Time
	EndAt     time.Time
	StartID   string
}

type Sandbox struct {

	// YOU ARE IN SANDBOX_OTHER.GO
	// YOU PROBABLY WANT TO BE IN SANDBOX_LINUX.GO

	*Resources
	*Metadata

	files   *storage.SandboxFiles
	cleanup *Cleanup

	process *fc.Process

	template template.Template

	ClickhouseStore chdb.Store

	Checks *Checks
}

func (s *Sandbox) LoggerMetadata() sbxlogger.SandboxMetadata {
	panic("platform does not support sandbox")
}

// Run cleanup functions for the already initialized resources if there is any error or after you are done with the started sandbox.
func NewSandbox(

	// YOU ARE IN SANDBOX_OTHER.GO
	// YOU PROBABLY WANT TO BE IN SANDBOX_LINUX.GO

	ctx context.Context,
	tracer trace.Tracer,
	networkPool *network.Pool,
	templateCache *template.Cache,
	config *orchestrator.SandboxConfig,
	traceID string,
	startedAt time.Time,
	endAt time.Time,
	baseTemplateID string,
	clientID string,
	devicePool *nbd.DevicePool,
	clickhouseStore chdb.Store,
	useLokiMetrics string,
	useClickhouseMetrics string,
) (*Sandbox, *Cleanup, error) {
	return nil, nil, errors.New("platform does not support sandbox")
}

func (s *Sandbox) Wait(ctx context.Context) error {
	return errors.New("platform does not support sandbox")
}

func (s *Sandbox) Stop(ctx context.Context) error {
	err := s.cleanup.Run(ctx)
	if err != nil {
		return fmt.Errorf("failed to stop sandbox: %w", err)
	}

	return nil
}

func (s *Sandbox) Snapshot(
	ctx context.Context,
	tracer trace.Tracer,
	snapshotTemplateFiles *storage.TemplateCacheFiles,
	releaseLock func(),
) (*Snapshot, error) {
	return nil, errors.New("platform does not support snapshot")
}

func (s *Sandbox) Close(ctx context.Context, tracer trace.Tracer) error {
	return errors.New("platform does not support sandbox")
}

func (s *Sandbox) Pause(
	ctx context.Context,
	tracer trace.Tracer,
	snapshotTemplateFiles *storage.TemplateCacheFiles,
) (*Snapshot, error) {
	return nil, errors.New("platform does not support sandbox")
}

func (s *Sandbox) waitForStart(
	ctx context.Context,
	tracer trace.Tracer,
) error {
	return errors.New("platform does not support sandbox")
}

func CreateSandbox(
	ctx context.Context,

	tracer trace.Tracer,
	networkPool *network.Pool,
	devicePool *nbd.DevicePool,
	config *orchestrator.SandboxConfig,
	template template.Template,
	sandboxTimeout time.Duration,
	rootfsCOWCachePath string,
) (*Sandbox, *Cleanup, error) {
	return nil, nil, errors.New("platform does not support sandbox")
}

func ResumeSandbox(
	ctx context.Context,
	tracer trace.Tracer,
	networkPool *network.Pool,
	templateCache *template.Cache,
	config *orchestrator.SandboxConfig,
	traceID string,
	startedAt time.Time,
	endAt time.Time,
	baseTemplateID string,
	devicePool *nbd.DevicePool,
	clickhouseStore chdb.Store,
	useLokiMetrics string,
	useClickhouseMetrics string,
) (*Sandbox, *Cleanup, error) {
	return nil, nil, errors.New("platform does not support sandbox")
}

type Snapshot struct {
	MemfileDiff       build.Diff
	MemfileDiffHeader *header.Header
	RootfsDiff        build.Diff
	RootfsDiffHeader  *header.Header
	Snapfile          *template.LocalFileLink
}
