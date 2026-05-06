package proxy

import (
	"context"
	"testing"
	"time"

	"github.com/launchdarkly/go-server-sdk/v7/testhelpers/ldtestdata"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	proxygrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/proxy"
	reverseproxy "github.com/e2b-dev/infra/packages/shared/pkg/proxy"
	e2bcatalog "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-catalog"
)

type stubResumer struct {
	nodeIP string
	err    error
}

func (s stubResumer) Init(_ context.Context) {}

func (s stubResumer) Resume(_ context.Context, _ string, _ uint64, _ string, _ string) (string, error) {
	return s.nodeIP, s.err
}

func (s stubResumer) KeepAlive(_ context.Context, _ string, _ string, _ uint64, _ string, _ string) error {
	return s.err
}

type recordingResumer struct {
	sandboxID          string
	teamID             string
	sandboxPort        uint64
	trafficAccessToken string
	envdAccessToken    string
}

func (r *recordingResumer) Init(_ context.Context) {}

func (r *recordingResumer) Resume(_ context.Context, sandboxID string, sandboxPort uint64, trafficAccessToken string, envdAccessToken string) (string, error) {
	r.sandboxID = sandboxID
	r.sandboxPort = sandboxPort
	r.trafficAccessToken = trafficAccessToken
	r.envdAccessToken = envdAccessToken

	return "10.0.0.1", nil
}

func (r *recordingResumer) KeepAlive(_ context.Context, sandboxID string, teamID string, sandboxPort uint64, trafficAccessToken string, envdAccessToken string) error {
	r.sandboxID = sandboxID
	r.teamID = teamID
	r.sandboxPort = sandboxPort
	r.trafficAccessToken = trafficAccessToken
	r.envdAccessToken = envdAccessToken

	return nil
}

type resumeCall struct {
	method             string
	sandboxID          string
	teamID             string
	sandboxPort        uint64
	trafficAccessToken string
	envdAccessToken    string
}

type asyncRecordingResumer struct {
	calls chan resumeCall
	block <-chan struct{}
}

func (r *asyncRecordingResumer) Init(_ context.Context) {}

func (r *asyncRecordingResumer) Resume(ctx context.Context, sandboxID string, sandboxPort uint64, trafficAccessToken string, envdAccessToken string) (string, error) {
	call := resumeCall{
		method:             "resume",
		sandboxID:          sandboxID,
		sandboxPort:        sandboxPort,
		trafficAccessToken: trafficAccessToken,
		envdAccessToken:    envdAccessToken,
	}

	select {
	case r.calls <- call:
	case <-ctx.Done():
		return "", ctx.Err()
	}

	if r.block != nil {
		select {
		case <-r.block:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	return "10.0.0.1", nil
}

func (r *asyncRecordingResumer) KeepAlive(ctx context.Context, sandboxID string, teamID string, sandboxPort uint64, trafficAccessToken string, envdAccessToken string) error {
	call := resumeCall{
		method:             "keepalive",
		sandboxID:          sandboxID,
		teamID:             teamID,
		sandboxPort:        sandboxPort,
		trafficAccessToken: trafficAccessToken,
		envdAccessToken:    envdAccessToken,
	}

	select {
	case r.calls <- call:
	case <-ctx.Done():
		return ctx.Err()
	}

	if r.block != nil {
		select {
		case <-r.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return nil
}

func requireResumerCall(t *testing.T, calls <-chan resumeCall) resumeCall {
	t.Helper()

	select {
	case call := <-calls:
		return call
	case <-time.After(time.Second):
		t.Fatal("expected resume call")
		return resumeCall{}
	}
}

func requireNoResumerCall(t *testing.T, calls <-chan resumeCall) {
	t.Helper()

	select {
	case call := <-calls:
		t.Fatalf("unexpected resumer call: %+v", call)
	case <-time.After(50 * time.Millisecond):
	}
}

func testKeepalive() *e2bcatalog.Keepalive {
	return &e2bcatalog.Keepalive{
		Traffic: &e2bcatalog.TrafficKeepalive{
			Enabled: true,
		},
	}
}

func newFF(t *testing.T, autoResumeEnabled bool) *featureflags.Client {
	t.Helper()

	source := ldtestdata.DataSource()
	source.Update(source.Flag(featureflags.SandboxAutoResumeFlag.Key()).VariationForAll(autoResumeEnabled))

	ff, err := featureflags.NewClientWithDatasource(source)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ff.Close(context.Background()) })

	return ff
}

func newFFWithOrchAcceptsCombinedHost(t *testing.T, enabled bool) *featureflags.Client {
	t.Helper()

	source := ldtestdata.DataSource()
	source.Update(source.Flag(featureflags.OrchAcceptsCombinedHostFlag.Key()).VariationForAll(enabled))

	ff, err := featureflags.NewClientWithDatasource(source)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ff.Close(context.Background()) })

	return ff
}

func ptr[T any](v T) *T {
	return &v
}

func TestCatalogResolution_CatalogHit(t *testing.T) {
	t.Parallel()

	c := e2bcatalog.NewMemorySandboxesCatalog()
	ff := newFF(t, true)

	err := c.StoreSandbox(t.Context(), "sbx", &e2bcatalog.SandboxInfo{
		OrchestratorIP: "10.0.0.1",
		ExecutionID:    "exec",
		StartedAt:      time.Now(),
	}, time.Minute)
	require.NoError(t, err)

	nodeIP, err := catalogResolution(t.Context(), "sbx", 8000, "", "", c, nil, ff, nil)
	require.NoError(t, err)
	require.Equal(t, "10.0.0.1", nodeIP)
}

func TestClientProxyMaskRequestHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		flagEnabled bool
		host        string
		want        *string
	}{
		{
			name:        "flag disabled masks sandbox shared host",
			flagEnabled: false,
			host:        "sandbox.e2b.app",
			want:        ptr("49983-sbx.e2b.app"),
		},
		{
			name:        "flag enabled preserves sandbox shared host",
			flagEnabled: true,
			host:        "sandbox.e2b.app",
			want:        nil,
		},
		{
			name:        "flag enabled preserves sandbox shared host with port",
			flagEnabled: true,
			host:        "sandbox.e2b.app:443",
			want:        nil,
		},
		{
			name:        "leaves localhost unchanged",
			flagEnabled: false,
			host:        "localhost:3000",
			want:        nil,
		},
		{
			name:        "leaves loopback unchanged",
			flagEnabled: false,
			host:        "127.0.0.1:3000",
			want:        nil,
		},
		{
			name:        "leaves regular sandbox host unchanged",
			flagEnabled: true,
			host:        "49983-sbx.e2b.app",
			want:        nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ff := newFFWithOrchAcceptsCombinedHost(t, tt.flagEnabled)

			require.Equal(t, tt.want, clientProxyMaskRequestHost(t.Context(), ff, tt.host, "sbx", 49983))
		})
	}
}

func TestCatalogResolution_CatalogHit_EmptyIPReturnsRouteUnavailable(t *testing.T) {
	t.Parallel()

	c := e2bcatalog.NewMemorySandboxesCatalog()
	ff := newFF(t, true)

	err := c.StoreSandbox(t.Context(), "sbx", &e2bcatalog.SandboxInfo{
		OrchestratorIP: "",
		ExecutionID:    "exec",
		StartedAt:      time.Now(),
	}, time.Minute)
	require.NoError(t, err)

	nodeIP, err := catalogResolution(t.Context(), "sbx", 8000, "", "", c, nil, ff, nil)
	require.ErrorIs(t, err, ErrNodeRouteUnavailable)
	require.Empty(t, nodeIP)
}

func TestCatalogResolution_CatalogMiss(t *testing.T) {
	t.Parallel()

	c := e2bcatalog.NewMemorySandboxesCatalog()
	ff := newFF(t, true)

	_, err := catalogResolution(t.Context(), "sbx", 8000, "", "", c, nil, ff, nil)
	require.ErrorIs(t, err, ErrNodeNotFound)
}

func TestCatalogResolution_CatalogMiss_ResumeEmptyIPReturnsRouteUnavailable(t *testing.T) {
	t.Parallel()

	c := e2bcatalog.NewMemorySandboxesCatalog()
	ff := newFF(t, true)

	nodeIP, err := catalogResolution(t.Context(), "sbx", 8000, "", "", c, stubResumer{nodeIP: ""}, ff, nil)
	require.ErrorIs(t, err, ErrNodeRouteUnavailable)
	require.Empty(t, nodeIP)
}

func TestCatalogResolution_CatalogHit_TrafficKeepaliveRefreshes(t *testing.T) {
	t.Parallel()

	c := e2bcatalog.NewMemorySandboxesCatalog()
	ff := newFF(t, true)
	now := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	resumer := &asyncRecordingResumer{calls: make(chan resumeCall, 1)}
	trafficKeepalive := newTrafficKeepaliveManager(resumer)

	err := c.StoreSandbox(t.Context(), "sbx", &e2bcatalog.SandboxInfo{
		OrchestratorIP: "10.0.0.1",
		TeamID:         "8f56d6bc-9b6d-4cbb-8e31-86b62359f716",
		ExecutionID:    "exec",
		StartedAt:      now.Add(-time.Minute),
		Keepalive:      testKeepalive(),
	}, time.Minute)
	require.NoError(t, err)

	nodeIP, err := catalogResolution(t.Context(), "sbx", 49983, "traffic-token", "envd-token", c, nil, ff, trafficKeepalive)
	require.NoError(t, err)
	require.Equal(t, "10.0.0.1", nodeIP)

	call := requireResumerCall(t, resumer.calls)
	require.Equal(t, "keepalive", call.method)
	require.Equal(t, "sbx", call.sandboxID)
	require.Equal(t, "8f56d6bc-9b6d-4cbb-8e31-86b62359f716", call.teamID)
	require.Equal(t, uint64(49983), call.sandboxPort)
	require.Equal(t, "traffic-token", call.trafficAccessToken)
	require.Equal(t, "envd-token", call.envdAccessToken)
}

func TestCatalogResolution_CatalogHit_TrafficKeepaliveRefreshesWhenAutoResumeFlagDisabled(t *testing.T) {
	t.Parallel()

	c := e2bcatalog.NewMemorySandboxesCatalog()
	ff := newFF(t, false)
	now := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	resumer := &asyncRecordingResumer{calls: make(chan resumeCall, 1)}
	trafficKeepalive := newTrafficKeepaliveManager(resumer)

	err := c.StoreSandbox(t.Context(), "sbx", &e2bcatalog.SandboxInfo{
		OrchestratorIP: "10.0.0.1",
		TeamID:         "8f56d6bc-9b6d-4cbb-8e31-86b62359f716",
		ExecutionID:    "exec",
		StartedAt:      now.Add(-time.Minute),
		Keepalive:      testKeepalive(),
	}, time.Minute)
	require.NoError(t, err)

	nodeIP, err := catalogResolution(t.Context(), "sbx", 49983, "traffic-token", "envd-token", c, nil, ff, trafficKeepalive)
	require.NoError(t, err)
	require.Equal(t, "10.0.0.1", nodeIP)

	call := requireResumerCall(t, resumer.calls)
	require.Equal(t, "keepalive", call.method)
	require.Equal(t, "8f56d6bc-9b6d-4cbb-8e31-86b62359f716", call.teamID)
	require.Equal(t, uint64(49983), call.sandboxPort)
	require.Equal(t, "envd-token", call.envdAccessToken)
}

func TestTrafficKeepaliveManager_RefreshesWhenNotNearExpiry(t *testing.T) {
	t.Parallel()

	c := e2bcatalog.NewMemorySandboxesCatalog()
	resumer := &asyncRecordingResumer{calls: make(chan resumeCall, 1)}
	trafficKeepalive := newTrafficKeepaliveManager(resumer)

	trafficKeepalive.MaybeRefresh(t.Context(), "sbx", 49983, "traffic-token", "envd-token", c, &e2bcatalog.SandboxInfo{
		TeamID:    "8f56d6bc-9b6d-4cbb-8e31-86b62359f716",
		Keepalive: testKeepalive(),
	})

	requireResumerCall(t, resumer.calls)
}

func TestTrafficKeepaliveManager_SkipsWhenTeamIDMissing(t *testing.T) {
	t.Parallel()

	c := e2bcatalog.NewMemorySandboxesCatalog()
	resumer := &asyncRecordingResumer{calls: make(chan resumeCall, 1)}
	trafficKeepalive := newTrafficKeepaliveManager(resumer)

	trafficKeepalive.MaybeRefresh(t.Context(), "sbx", 49983, "traffic-token", "envd-token", c, &e2bcatalog.SandboxInfo{
		Keepalive: testKeepalive(),
	})

	requireNoResumerCall(t, resumer.calls)
}

func TestTrafficKeepaliveManager_SkipsWhenCatalogPolicyDisabled(t *testing.T) {
	t.Parallel()

	c := e2bcatalog.NewMemorySandboxesCatalog()
	resumer := &asyncRecordingResumer{calls: make(chan resumeCall, 1)}
	trafficKeepalive := newTrafficKeepaliveManager(resumer)

	trafficKeepalive.MaybeRefresh(t.Context(), "sbx", 49983, "traffic-token", "envd-token", c, &e2bcatalog.SandboxInfo{
		TeamID: "8f56d6bc-9b6d-4cbb-8e31-86b62359f716",
		Keepalive: &e2bcatalog.Keepalive{
			Traffic: &e2bcatalog.TrafficKeepalive{Enabled: false},
		},
	})

	requireNoResumerCall(t, resumer.calls)
}

func TestTrafficKeepaliveManager_SuppressesConcurrentRefreshes(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	resumer := &asyncRecordingResumer{
		calls: make(chan resumeCall, 2),
		block: release,
	}
	trafficKeepalive := newTrafficKeepaliveManager(resumer)
	c := e2bcatalog.NewMemorySandboxesCatalog()
	info := &e2bcatalog.SandboxInfo{
		TeamID:    "8f56d6bc-9b6d-4cbb-8e31-86b62359f716",
		Keepalive: testKeepalive(),
	}

	trafficKeepalive.MaybeRefresh(t.Context(), "sbx", 49983, "traffic-token", "envd-token", c, info)
	call := requireResumerCall(t, resumer.calls)
	require.Equal(t, "keepalive", call.method)

	trafficKeepalive.MaybeRefresh(t.Context(), "sbx", 49983, "traffic-token", "envd-token", c, info)
	requireNoResumerCall(t, resumer.calls)

	close(release)
}

func TestTrafficKeepaliveManager_SkipsWhenCatalogTimerHeld(t *testing.T) {
	t.Parallel()

	resumer := &asyncRecordingResumer{calls: make(chan resumeCall, 1)}
	trafficKeepalive := newTrafficKeepaliveManager(resumer)
	c := e2bcatalog.NewMemorySandboxesCatalog()
	info := &e2bcatalog.SandboxInfo{
		TeamID:    "8f56d6bc-9b6d-4cbb-8e31-86b62359f716",
		Keepalive: testKeepalive(),
	}

	trafficKeepalive.MaybeRefresh(t.Context(), "sbx", 49983, "traffic-token", "envd-token", c, info)
	requireResumerCall(t, resumer.calls)

	trafficKeepalive.MaybeRefresh(t.Context(), "sbx", 49983, "traffic-token", "envd-token", c, info)
	requireNoResumerCall(t, resumer.calls)
}

func TestHandlePausedSandbox_NoResumer_MissingTrafficAccessToken(t *testing.T) {
	t.Parallel()

	ff := newFF(t, true)

	_, res, err := handlePausedSandbox(t.Context(), "sbx", 8000, "", "", nil, ff)
	require.NoError(t, err)
	require.Equal(t, autoResumeNotAllowed, res)
}

func TestHandlePausedSandbox_NoResumer_InvalidTrafficAccessToken(t *testing.T) {
	t.Parallel()

	ff := newFF(t, true)

	_, res, err := handlePausedSandbox(t.Context(), "sbx", 8000, "wrong-token", "", nil, ff)
	require.NoError(t, err)
	require.Equal(t, autoResumeNotAllowed, res)
}

func TestHandlePausedSandbox_FlagDisabled(t *testing.T) {
	t.Parallel()

	ff := newFF(t, false)

	_, res, err := handlePausedSandbox(t.Context(), "sbx", 8000, "token", "", stubResumer{nodeIP: "10.0.0.1"}, ff)
	require.NoError(t, err)
	require.Equal(t, autoResumeNotAllowed, res)
}

func TestHandlePausedSandbox_NotFound(t *testing.T) {
	t.Parallel()

	ff := newFF(t, true)

	_, res, err := handlePausedSandbox(t.Context(), "sbx", 8000, "token", "", stubResumer{err: status.Error(codes.NotFound, "not allowed")}, ff)
	require.NoError(t, err)
	require.Equal(t, autoResumeNotAllowed, res)
}

func TestHandlePausedSandbox_PermissionDenied(t *testing.T) {
	t.Parallel()

	ff := newFF(t, true)

	_, res, err := handlePausedSandbox(t.Context(), "sbx", 8000, "token", "", stubResumer{err: status.Error(codes.PermissionDenied, "permission denied")}, ff)
	require.Error(t, err)
	var deniedErr *reverseproxy.SandboxResumePermissionDeniedError
	require.ErrorAs(t, err, &deniedErr)
	require.Equal(t, autoResumePermissionDenied, res)
}

func TestHandlePausedSandbox_ResourceExhausted(t *testing.T) {
	t.Parallel()

	ff := newFF(t, true)

	_, res, err := handlePausedSandbox(t.Context(), "sbx", 8000, "token", "", stubResumer{err: status.Error(codes.ResourceExhausted, "rate limit hit")}, ff)
	require.Error(t, err)
	var exhaustedErr *reverseproxy.SandboxResourceExhaustedError
	require.ErrorAs(t, err, &exhaustedErr)
	require.Equal(t, "sbx", exhaustedErr.SandboxId)
	require.Equal(t, "rate limit hit", exhaustedErr.Message)
	require.Equal(t, autoResumeResourceExhausted, res)
}

func TestHandlePausedSandbox_FailedPrecondition(t *testing.T) {
	t.Parallel()

	ff := newFF(t, true)

	_, res, err := handlePausedSandbox(t.Context(), "sbx", 8000, "token", "", stubResumer{err: status.Error(codes.FailedPrecondition, proxygrpc.SandboxStillTransitioningMessage)}, ff)
	require.Error(t, err)
	var transitioningErr *reverseproxy.SandboxStillTransitioningError
	require.ErrorAs(t, err, &transitioningErr)
	require.Equal(t, "sbx", transitioningErr.SandboxId)
	require.Equal(t, autoResumeErrored, res)
}

func TestHandlePausedSandbox_FailedPrecondition_OtherMessage(t *testing.T) {
	t.Parallel()

	ff := newFF(t, true)

	_, res, err := handlePausedSandbox(t.Context(), "sbx", 8000, "token", "", stubResumer{err: status.Error(codes.FailedPrecondition, "sandbox resume precondition failed")}, ff)
	require.Error(t, err)
	var transitioningErr *reverseproxy.SandboxStillTransitioningError
	require.NotErrorAs(t, err, &transitioningErr)
	require.Equal(t, autoResumeErrored, res)
}

func TestHandlePausedSandbox_SnapshotNotFound(t *testing.T) {
	t.Parallel()

	ff := newFF(t, true)

	_, res, err := handlePausedSandbox(t.Context(), "sbx", 8000, "token", "", stubResumer{err: status.Error(codes.NotFound, "snapshot not found")}, ff)
	require.NoError(t, err)
	require.Equal(t, autoResumeNotAllowed, res)
}

func TestHandlePausedSandbox_Error(t *testing.T) {
	t.Parallel()

	ff := newFF(t, true)

	_, res, err := handlePausedSandbox(t.Context(), "sbx", 8000, "token", "", stubResumer{err: status.Error(codes.Unavailable, "boom")}, ff)
	require.Error(t, err)
	require.Equal(t, autoResumeErrored, res)
}

func TestHandlePausedSandbox_Succeeded(t *testing.T) {
	t.Parallel()

	ff := newFF(t, true)

	nodeIP, res, err := handlePausedSandbox(t.Context(), "sbx", 8000, "token", "", stubResumer{nodeIP: "10.0.0.1"}, ff)
	require.NoError(t, err)
	require.Equal(t, autoResumeSucceeded, res)
	require.Equal(t, "10.0.0.1", nodeIP)
}

func TestHandlePausedSandbox_Succeeded_EmptyIP(t *testing.T) {
	t.Parallel()

	ff := newFF(t, true)

	nodeIP, res, err := handlePausedSandbox(t.Context(), "sbx", 8000, "token", "", stubResumer{nodeIP: ""}, ff)
	require.ErrorIs(t, err, ErrNodeRouteUnavailable)
	require.Equal(t, autoResumeErrored, res)
	require.Empty(t, nodeIP)
}

func TestHandlePausedSandbox_PassesPortAndTokenToResumer(t *testing.T) {
	t.Parallel()

	ff := newFF(t, true)
	resumer := &recordingResumer{}

	nodeIP, res, err := handlePausedSandbox(t.Context(), "sbx", 49983, "token", "envd-token", resumer, ff)
	require.NoError(t, err)
	require.Equal(t, autoResumeSucceeded, res)
	require.Equal(t, "10.0.0.1", nodeIP)
	require.Equal(t, "sbx", resumer.sandboxID)
	require.EqualValues(t, 49983, resumer.sandboxPort)
	require.Equal(t, "token", resumer.trafficAccessToken)
	require.Equal(t, "envd-token", resumer.envdAccessToken)
}
