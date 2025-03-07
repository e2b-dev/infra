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

	"github.com/e2b-dev/infra/packages/orchestrator/internal/dns"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/build"
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

func (m *NoOpCleanup) Run() error {
	return errors.New("platform does not support sandbox")
}

type Sandbox struct {
	Config    *orchestrator.SandboxConfig
	process   NoOpProcess
	uffdExit  chan error
	cleanup   NoOpCleanup
	healthy   atomic.Bool
	Slot      network.Slot
	EndAt     time.Time
	StartedAt time.Time
}

func (s *Sandbox) LoggerMetadata() sbxlogger.SandboxMetadata {
	panic("platform does not support sandbox")
}

// Run cleanup functions for the already initialized resources if there is any error or after you are done with the started sandbox.
func NewSandbox(
	ctx context.Context,
	tracer trace.Tracer,
	dns *dns.DNS,
	networkPool *network.Pool,
	templateCache *template.Cache,
	config *orchestrator.SandboxConfig,
	traceID string,
	startedAt time.Time,
	endAt time.Time,
	isSnapshot bool,
	baseTemplateID string,
	clientID string,
) (*Sandbox, *Cleanup, error) {
	return nil, nil, errors.New("platform does not support sandbox")
}

func (s *Sandbox) Wait() error {
	return errors.New("platform does not support sandbox")
}

func (s *Sandbox) Stop() error {
	err := s.cleanup.Run()
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
	Snapfile          *template.LocalFile
}
