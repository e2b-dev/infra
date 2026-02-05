package proxy

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	sharedproxy "github.com/e2b-dev/infra/packages/shared/pkg/proxy"
	catalog "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-catalog"
)

type fakeCatalog struct {
	info      *catalog.SandboxInfo
	failCount int
	calls     int
}

func (f *fakeCatalog) GetSandbox(_ context.Context, _ string) (*catalog.SandboxInfo, error) {
	f.calls++
	if f.calls <= f.failCount {
		return nil, catalog.ErrSandboxNotFound
	}

	return f.info, nil
}

func (f *fakeCatalog) StoreSandbox(_ context.Context, _ string, _ *catalog.SandboxInfo, _ time.Duration) error {
	return nil
}

func (f *fakeCatalog) DeleteSandbox(_ context.Context, _ string, _ string) error {
	return nil
}

func (f *fakeCatalog) Close(_ context.Context) error {
	return nil
}

type fakePausedChecker struct {
	resumeErr   error
	resumeCalls int
}

func (f *fakePausedChecker) Resume(_ context.Context, _ string, _ int32) error {
	f.resumeCalls++

	return f.resumeErr
}

func TestCatalogResolutionPaused_NoAutoResume(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c := &fakeCatalog{info: nil, failCount: 1}
	paused := &fakePausedChecker{}

	// With optimistic resume, when autoResumeEnabled=false we don't attempt resume
	// and just return "not found" instead of checking pause status
	_, err := catalogResolution(ctx, "sbx-1", c, paused, false)
	if !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("expected ErrNodeNotFound, got %v", err)
	}
	if paused.resumeCalls != 0 {
		t.Fatalf("expected no resume calls when autoResumeEnabled=false, got %d", paused.resumeCalls)
	}
}

func TestCatalogResolutionPaused_AutoResumeSuccess(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	info := &catalog.SandboxInfo{OrchestratorIP: "10.0.0.1"}
	c := &fakeCatalog{info: info, failCount: 1}
	paused := &fakePausedChecker{}

	ip, err := catalogResolution(ctx, "sbx-2", c, paused, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ip != "10.0.0.1" {
		t.Fatalf("expected ip 10.0.0.1, got %s", ip)
	}
	if paused.resumeCalls != 1 {
		t.Fatalf("expected resume to be called once, got %d", paused.resumeCalls)
	}
}

func TestCatalogResolutionPaused_AutoResumeFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c := &fakeCatalog{info: nil, failCount: 10}
	paused := &fakePausedChecker{
		resumeErr: errors.New("nope"),
	}

	_, err := catalogResolution(ctx, "sbx-3", c, paused, true)
	if err == nil {
		t.Fatalf("expected error")
	}

	var pausedErr *sharedproxy.SandboxPausedError
	if !errors.As(err, &pausedErr) {
		t.Fatalf("expected SandboxPausedError, got %T", err)
	}
	if !pausedErr.CanAutoResume {
		t.Fatalf("expected canAutoResume=true")
	}
}

func TestCatalogResolutionPaused_AutoResumeDenied(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c := &fakeCatalog{info: nil, failCount: 10}
	paused := &fakePausedChecker{
		resumeErr: status.Error(codes.FailedPrecondition, "auto-resume disabled"),
	}

	_, err := catalogResolution(ctx, "sbx-denied", c, paused, true)
	if err == nil {
		t.Fatalf("expected error")
	}

	var pausedErr *sharedproxy.SandboxPausedError
	if !errors.As(err, &pausedErr) {
		t.Fatalf("expected SandboxPausedError, got %T", err)
	}
	if pausedErr.CanAutoResume {
		t.Fatalf("expected canAutoResume=false")
	}
}

func TestCatalogResolution_NoPausedChecker(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	info := &catalog.SandboxInfo{OrchestratorIP: "10.0.0.2"}
	c := &fakeCatalog{info: info, failCount: 0}

	ip, err := catalogResolution(ctx, "sbx-nil", c, nil, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ip != "10.0.0.2" {
		t.Fatalf("expected ip 10.0.0.2, got %s", ip)
	}
}

func TestCatalogResolution_NoPausedChecker_NotFound(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c := &fakeCatalog{info: nil, failCount: 1}

	_, err := catalogResolution(ctx, "sbx-missing", c, nil, true)
	if !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("expected ErrNodeNotFound, got %v", err)
	}
}

func TestWithProxyAuthMetadata(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	header := http.Header{}
	header.Set("Authorization", "Bearer sk_e2b_test")
	header.Set("X-API-Key", "e2b_test")

	ctx = withProxyAuthMetadata(ctx, header)
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		t.Fatalf("expected outgoing metadata")
	}

	if got := md.Get("authorization"); len(got) != 1 || got[0] != "Bearer sk_e2b_test" {
		t.Fatalf("unexpected authorization metadata: %v", got)
	}
	if got := md.Get("x-api-key"); len(got) != 1 || got[0] != "e2b_test" {
		t.Fatalf("unexpected api key metadata: %v", got)
	}
}
