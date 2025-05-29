//go:build !linux
// +build !linux

package sandbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/clickhouse/pkg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/build"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
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

type Sandbox struct {

	// YOU ARE IN SANDBOX_OTHER.GO
	// YOU PROBABLY WANT TO BE IN SANDBOX_LINUX.GO

	Config          *orchestrator.SandboxConfig
	process         NoOpProcess
	uffdExit        chan error
	cleanup         NoOpCleanup
	healthy         atomic.Bool
	Slot            network.Slot
	EndAt           time.Time
	StartedAt       time.Time
	ClickhouseStore clickhouse.Clickhouse

	useLokiMetrics       string
	useClickhouseMetrics string

	// Unique ID for the sandbox start.
	StartID string
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
	clickhouseStore clickhouse.Clickhouse,
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

type Snapshot struct {
	MemfileDiff       build.Diff
	MemfileDiffHeader *header.Header
	RootfsDiff        build.Diff
	RootfsDiffHeader  *header.Header
	Snapfile          *template.LocalFileLink
}
