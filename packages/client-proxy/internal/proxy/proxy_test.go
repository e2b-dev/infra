package proxy

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"google.golang.org/grpc/metadata"

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
	info            PausedInfo
	resumeErr       error
	resumeCalls     int
	pausedInfoCalls int
}

func (f *fakePausedChecker) PausedInfo(_ context.Context, _ string) (PausedInfo, error) {
	f.pausedInfoCalls++
	return f.info, nil
}

func (f *fakePausedChecker) Resume(_ context.Context, _ string, _ int32) error {
	f.resumeCalls++
	return f.resumeErr
}

type fakePausedCatalog struct {
	info  *catalog.PausedSandboxInfo
	err   error
	calls int
}

func (f *fakePausedCatalog) GetPaused(_ context.Context, _ string) (*catalog.PausedSandboxInfo, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if f.info == nil {
		return nil, catalog.ErrPausedSandboxNotFound
	}

	return f.info, nil
}

func (f *fakePausedCatalog) StorePaused(_ context.Context, _ string, _ *catalog.PausedSandboxInfo, _ time.Duration) error {
	return nil
}

func (f *fakePausedCatalog) DeletePaused(_ context.Context, _ string) error {
	return nil
}

func (f *fakePausedCatalog) Close(_ context.Context) error {
	return nil
}

func TestCatalogResolutionPaused_NoAutoResume(t *testing.T) {
	ctx := context.Background()
	c := &fakeCatalog{info: nil, failCount: 1}
	paused := &fakePausedChecker{info: PausedInfo{Paused: true, AutoResumePolicy: "any"}}

	_, err := catalogResolution(ctx, "sbx-1", c, nil, paused, false, false)
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
	paused := &fakePausedChecker{info: PausedInfo{Paused: true, AutoResumePolicy: "any"}}

	origInterval := resumeWaitInterval
	origTimeout := resumeWaitTimeout
	resumeWaitInterval = 1 * time.Millisecond
	resumeWaitTimeout = 20 * time.Millisecond
	defer func() {
		resumeWaitInterval = origInterval
		resumeWaitTimeout = origTimeout
	}()

	ip, err := catalogResolution(ctx, "sbx-2", c, nil, paused, true, false)
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
		info:      PausedInfo{Paused: true, AutoResumePolicy: "any"},
		resumeErr: errors.New("nope"),
	}

	_, err := catalogResolution(ctx, "sbx-3", c, nil, paused, true, false)
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

func TestShouldAutoResumePolicy(t *testing.T) {
	if !shouldAutoResume("any", true, false) {
		t.Fatalf("expected any=true")
	}
	if shouldAutoResume("null", true, true) {
		t.Fatalf("expected null=false")
	}
	if shouldAutoResume("authed", true, false) {
		t.Fatalf("expected authed=false without token")
	}
	if !shouldAutoResume("authed", true, true) {
		t.Fatalf("expected authed=true with token")
	}
	if shouldAutoResume("unknown", true, false) {
		t.Fatalf("expected default=false for unknown policy")
	}
}

func TestAutoResumePolicies(t *testing.T) {
	cases := []struct {
		name             string
		policy           string
		requestHasAuth   bool
		expectAutoResume bool
	}{
		{name: "any-authed", policy: "any", requestHasAuth: true, expectAutoResume: true},
		{name: "any-unauthed", policy: "any", requestHasAuth: false, expectAutoResume: true},
		{name: "authed-authed", policy: "authed", requestHasAuth: true, expectAutoResume: true},
		{name: "authed-unauthed", policy: "authed", requestHasAuth: false, expectAutoResume: false},
		{name: "null-authed", policy: "null", requestHasAuth: true, expectAutoResume: false},
		{name: "null-unauthed", policy: "null", requestHasAuth: false, expectAutoResume: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldAutoResume(tc.policy, true, tc.requestHasAuth)
			if got != tc.expectAutoResume {
				t.Fatalf("expected autoResume=%v, got %v", tc.expectAutoResume, got)
			}
		})
	}
}

func TestCatalogResolutionPaused_AutoResumePolicyAny(t *testing.T) {
	ctx := context.Background()
	info := &catalog.SandboxInfo{OrchestratorIP: "10.0.0.2"}
	c := &fakeCatalog{info: info, failCount: 1}
	paused := &fakePausedChecker{info: PausedInfo{Paused: true, AutoResumePolicy: "any"}}

	origInterval := resumeWaitInterval
	origTimeout := resumeWaitTimeout
	resumeWaitInterval = 1 * time.Millisecond
	resumeWaitTimeout = 20 * time.Millisecond
	defer func() {
		resumeWaitInterval = origInterval
		resumeWaitTimeout = origTimeout
	}()

	ip, err := catalogResolution(ctx, "sbx-any", c, nil, paused, true, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ip != "10.0.0.2" {
		t.Fatalf("expected ip 10.0.0.2, got %s", ip)
	}
}

func TestCatalogResolutionPaused_AutoResumePolicyAuthed(t *testing.T) {
	ctx := context.Background()
	info := &catalog.SandboxInfo{OrchestratorIP: "10.0.0.3"}
	c := &fakeCatalog{info: info, failCount: 1}
	paused := &fakePausedChecker{info: PausedInfo{Paused: true, AutoResumePolicy: "authed"}}

	origInterval := resumeWaitInterval
	origTimeout := resumeWaitTimeout
	resumeWaitInterval = 1 * time.Millisecond
	resumeWaitTimeout = 20 * time.Millisecond
	defer func() {
		resumeWaitInterval = origInterval
		resumeWaitTimeout = origTimeout
	}()

	_, err := catalogResolution(ctx, "sbx-authed-no-auth", c, nil, paused, true, false)
	if err == nil {
		t.Fatalf("expected error without auth")
	}

	ip, err := catalogResolution(ctx, "sbx-authed-with-auth", c, nil, paused, true, true)
	if err != nil {
		t.Fatalf("unexpected error with auth: %v", err)
	}
	if ip != "10.0.0.3" {
		t.Fatalf("expected ip 10.0.0.3, got %s", ip)
	}
}

func TestHasProxyAuth(t *testing.T) {
	if hasProxyAuth(http.Header{}) {
		t.Fatalf("expected no auth headers")
	}

	header := http.Header{}
	header.Set("Authorization", "Bearer sk_e2b_test")
	if !hasProxyAuth(header) {
		t.Fatalf("expected auth header to be detected")
	}

	header = http.Header{}
	header.Set("X-API-Key", "e2b_test")
	if !hasProxyAuth(header) {
		t.Fatalf("expected api key header to be detected")
	}
}

func TestWithProxyAuthMetadata(t *testing.T) {
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

func TestCatalogResolutionPaused_AutoResumePolicyNull(t *testing.T) {
	ctx := context.Background()
	c := &fakeCatalog{info: nil, failCount: 1}
	paused := &fakePausedChecker{info: PausedInfo{Paused: true, AutoResumePolicy: "null"}}

	_, err := catalogResolution(ctx, "sbx-null", c, nil, paused, true, true)
	if err == nil {
		t.Fatalf("expected error for null policy")
	}

	var pausedErr *sharedproxy.SandboxPausedError
	if !errors.As(err, &pausedErr) {
		t.Fatalf("expected SandboxPausedError, got %T", err)
	}
	if pausedErr.CanAutoResume {
		t.Fatalf("expected canAutoResume=false")
	}
}

func TestCatalogResolutionPaused_UsesPausedCatalogPolicy(t *testing.T) {
	ctx := context.Background()
	info := &catalog.SandboxInfo{OrchestratorIP: "10.0.0.4"}
	c := &fakeCatalog{info: info, failCount: 1}
	pausedCatalog := &fakePausedCatalog{
		info: &catalog.PausedSandboxInfo{
			AutoResumePolicy: "any",
			PausedAt:         time.Now(),
		},
	}
	paused := &fakePausedChecker{info: PausedInfo{Paused: false}}

	origInterval := resumeWaitInterval
	origTimeout := resumeWaitTimeout
	resumeWaitInterval = 1 * time.Millisecond
	resumeWaitTimeout = 20 * time.Millisecond
	defer func() {
		resumeWaitInterval = origInterval
		resumeWaitTimeout = origTimeout
	}()

	ip, err := catalogResolution(ctx, "sbx-paused-catalog", c, pausedCatalog, paused, true, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ip != "10.0.0.4" {
		t.Fatalf("expected ip 10.0.0.4, got %s", ip)
	}
	if pausedCatalog.calls != 1 {
		t.Fatalf("expected paused catalog to be called once, got %d", pausedCatalog.calls)
	}
	if paused.pausedInfoCalls != 0 {
		t.Fatalf("expected paused checker lookup to be skipped, got %d", paused.pausedInfoCalls)
	}
	if paused.resumeCalls != 1 {
		t.Fatalf("expected resume to be called once, got %d", paused.resumeCalls)
	}
}
