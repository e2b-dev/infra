package proxy

import (
	"context"
	"errors"
	"testing"
	"time"

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
	info        PausedInfo
	resumeErr   error
	resumeCalls int
}

func (f *fakePausedChecker) PausedInfo(_ context.Context, _ string) (PausedInfo, error) {
	return f.info, nil
}

func (f *fakePausedChecker) Resume(_ context.Context, _ string, _ int32) error {
	f.resumeCalls++
	return f.resumeErr
}

func TestCatalogResolutionPaused_NoAutoResume(t *testing.T) {
	ctx := context.Background()
	c := &fakeCatalog{info: nil, failCount: 1}
	paused := &fakePausedChecker{info: PausedInfo{Paused: true, CanAutoResume: true}}

	_, err := catalogResolution(ctx, "sbx-1", c, paused, false)
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

func TestCatalogResolutionPaused_AutoResumeSuccess(t *testing.T) {
	ctx := context.Background()
	info := &catalog.SandboxInfo{OrchestratorIP: "10.0.0.1"}
	c := &fakeCatalog{info: info, failCount: 1}
	paused := &fakePausedChecker{info: PausedInfo{Paused: true, CanAutoResume: true}}

	origInterval := resumeWaitInterval
	origTimeout := resumeWaitTimeout
	resumeWaitInterval = 1 * time.Millisecond
	resumeWaitTimeout = 20 * time.Millisecond
	defer func() {
		resumeWaitInterval = origInterval
		resumeWaitTimeout = origTimeout
	}()

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
	ctx := context.Background()
	c := &fakeCatalog{info: nil, failCount: 10}
	paused := &fakePausedChecker{
		info:      PausedInfo{Paused: true, CanAutoResume: true},
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
