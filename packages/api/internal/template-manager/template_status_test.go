package template_manager

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/db/pkg/types"
	templatemanagergrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// grpcDeadlineExceeded is the error the API sees when the status RPC to the
// template builder times out, e.g. "rpc error: code = DeadlineExceeded desc =
// context deadline exceeded".
func grpcDeadlineExceeded() error {
	return grpcstatus.Error(codes.DeadlineExceeded, "context deadline exceeded")
}

type getStatusResult struct {
	resp *templatemanagergrpc.TemplateBuildStatusResponse
	err  error
}

// scriptedClient replays a canned sequence of GetStatus outcomes (the last one
// repeats forever) and records what the poller persisted for the build.
type scriptedClient struct {
	mu sync.Mutex

	script []getStatusResult
	calls  int

	failedReasons []string
	finished      bool
}

func (s *scriptedClient) GetStatus(context.Context, uuid.UUID, string, uuid.UUID, string) (*templatemanagergrpc.TemplateBuildStatusResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := s.script[min(s.calls, len(s.script)-1)]
	s.calls++

	return result.resp, result.err
}

func (s *scriptedClient) SetStatus(_ context.Context, _ uuid.UUID, statusGroup types.BuildStatusGroup, reason *templatemanagergrpc.TemplateBuildStatusReason) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if statusGroup == types.BuildStatusGroupFailed {
		s.failedReasons = append(s.failedReasons, reason.GetMessage())
	}

	return nil
}

func (s *scriptedClient) SetFinished(context.Context, uuid.UUID, int64, string, string, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.finished = true

	return nil
}

func (s *scriptedClient) snapshot() ([]string, bool, int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]string(nil), s.failedReasons...), s.finished, s.calls
}

func completedStatus() *templatemanagergrpc.TemplateBuildStatusResponse {
	return &templatemanagergrpc.TemplateBuildStatusResponse{
		Status: templatemanagergrpc.TemplateBuildState_Completed,
		Metadata: &templatemanagergrpc.TemplateBuildMetadata{
			RootfsSizeKey:  100,
			EnvdVersionKey: "1.0.0",
		},
	}
}

// TestPollBuildStatus_transientRPCErrorKeepsBuildAlive covers EN-828: a status
// RPC that times out is a backend hiccup, not a build failure, and must not
// terminate an otherwise healthy build.
func TestPollBuildStatus_transientRPCErrorKeepsBuildAlive(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		script := make([]getStatusResult, 0, 61)
		for range 60 {
			script = append(script, getStatusResult{err: grpcDeadlineExceeded()})
		}
		script = append(script, getStatusResult{resp: completedStatus()})

		client := &scriptedClient{script: script}
		c := &PollBuildStatus{
			client:  client,
			logger:  logger.NewNopLogger(),
			buildID: uuid.New(),
		}

		ctx, cancel := context.WithTimeout(t.Context(), buildTimeout)
		defer cancel()

		c.poll(ctx)

		failedReasons, finished, calls := client.snapshot()
		if len(failedReasons) > 0 {
			t.Fatalf("build was failed after %d status calls, reasons: %q", calls, failedReasons)
		}
		if !finished {
			t.Fatalf("build was not finished after %d status calls", calls)
		}
	})
}

// TestPollBuildStatus_transientRPCErrorEventuallyFailsBuild makes sure the
// tolerance above stays bounded: a builder that never answers again still fails
// the build, it just takes the grace period instead of a single hiccup.
func TestPollBuildStatus_transientRPCErrorEventuallyFailsBuild(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		client := &scriptedClient{script: []getStatusResult{{err: grpcDeadlineExceeded()}}}
		c := &PollBuildStatus{
			client:  client,
			logger:  logger.NewNopLogger(),
			buildID: uuid.New(),
		}

		ctx, cancel := context.WithTimeout(t.Context(), buildTimeout)
		defer cancel()

		start := time.Now()
		c.poll(ctx)
		elapsed := time.Since(start)

		failedReasons, _, _ := client.snapshot()
		if len(failedReasons) != 1 {
			t.Fatalf("expected exactly one failure, got %q", failedReasons)
		}
		if !strings.Contains(failedReasons[0], "DeadlineExceeded") {
			t.Errorf("failure reason should keep the underlying error, got %q", failedReasons[0])
		}
		if elapsed < transientErrorGracePeriod {
			t.Errorf("build failed after %s, expected the poller to retry for at least %s", elapsed, transientErrorGracePeriod)
		}
		if elapsed >= buildTimeout {
			t.Errorf("build failed only once the build timed out, after %s", elapsed)
		}
	})
}

// TestPollBuildStatus_terminalRPCErrorFailsBuild keeps the existing behaviour
// for errors that will not fix themselves.
func TestPollBuildStatus_terminalRPCErrorFailsBuild(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		client := &scriptedClient{script: []getStatusResult{
			{err: errors.New("error while getting build info, maybe already expired")},
		}}
		c := &PollBuildStatus{
			client:  client,
			logger:  logger.NewNopLogger(),
			buildID: uuid.New(),
		}

		ctx, cancel := context.WithTimeout(t.Context(), buildTimeout)
		defer cancel()

		start := time.Now()
		c.poll(ctx)
		elapsed := time.Since(start)

		failedReasons, _, _ := client.snapshot()
		if len(failedReasons) != 1 {
			t.Fatalf("expected exactly one failure, got %q", failedReasons)
		}
		if !strings.Contains(failedReasons[0], "polling received unrecoverable error") {
			t.Errorf("unexpected failure reason %q", failedReasons[0])
		}
		if elapsed >= transientErrorGracePeriod {
			t.Errorf("terminal error took %s to fail the build", elapsed)
		}
	})
}

func TestIsTransientStatusError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "grpc deadline exceeded", err: grpcDeadlineExceeded(), want: true},
		{name: "wrapped grpc deadline exceeded", err: fmt.Errorf("polling: %w", grpcDeadlineExceeded()), want: true},
		{name: "grpc unavailable", err: grpcstatus.Error(codes.Unavailable, "connection refused"), want: true},
		{name: "grpc resource exhausted", err: grpcstatus.Error(codes.ResourceExhausted, "too many builds"), want: true},
		{name: "bare context deadline exceeded", err: context.DeadlineExceeded, want: true},
		{name: "wrapped context deadline exceeded", err: fmt.Errorf("polling: %w", context.DeadlineExceeded), want: true},
		{name: "grpc not found", err: grpcstatus.Error(codes.NotFound, "no such build"), want: false},
		{name: "grpc internal", err: grpcstatus.Error(codes.Internal, "boom"), want: false},
		{name: "plain error", err: errors.New("build info expired"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := isTransientStatusError(tt.err); got != tt.want {
				t.Errorf("isTransientStatusError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
