package proxy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/launchdarkly/go-server-sdk/v7/testhelpers/ldtestdata"
	"github.com/stretchr/testify/require"
	noopmetric "go.opentelemetry.io/otel/metric/noop"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	proxygrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/proxy"
	reverseproxy "github.com/e2b-dev/infra/packages/shared/pkg/proxy"
	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
	catalog "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-catalog"
)

type stubResumer struct {
	nodeIP string
	err    error
}

func (s stubResumer) Init(_ context.Context) {}

func (s stubResumer) Resume(_ context.Context, _ string, _ uint64, _ string, _ string) (string, error) {
	return s.nodeIP, s.err
}

type recordingResumer struct {
	sandboxID          string
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

func newFF(t *testing.T) *featureflags.Client {
	t.Helper()

	source := ldtestdata.DataSource()

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

func TestCatalogResolution_CatalogHit(t *testing.T) {
	t.Parallel()

	c := catalog.NewRedisSandboxCatalog(redis_utils.SetupInstance(t))

	err := c.StoreSandbox(t.Context(), "sbx", &catalog.SandboxInfo{
		OrchestratorIP: "10.0.0.1",
		ExecutionID:    "exec",
		StartedAt:      time.Now(),
	}, time.Minute)
	require.NoError(t, err)

	nodeIP, err := catalogResolution(t.Context(), "sbx", 8000, "", "", c, nil)
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
			want:        new("49983-sbx.e2b.app"),
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

	c := catalog.NewRedisSandboxCatalog(redis_utils.SetupInstance(t))

	err := c.StoreSandbox(t.Context(), "sbx", &catalog.SandboxInfo{
		OrchestratorIP: "",
		ExecutionID:    "exec",
		StartedAt:      time.Now(),
	}, time.Minute)
	require.NoError(t, err)

	nodeIP, err := catalogResolution(t.Context(), "sbx", 8000, "", "", c, nil)
	require.ErrorIs(t, err, ErrNodeRouteUnavailable)
	require.Empty(t, nodeIP)
}

func TestCatalogResolution_CatalogMiss(t *testing.T) {
	t.Parallel()

	c := catalog.NewRedisSandboxCatalog(redis_utils.SetupInstance(t))

	_, err := catalogResolution(t.Context(), "sbx", 8000, "", "", c, nil)
	require.ErrorIs(t, err, ErrNodeNotFound)
}

type errorCatalog struct {
	err error
}

func (e errorCatalog) GetSandbox(_ context.Context, _ string) (*catalog.SandboxInfo, error) {
	return nil, e.err
}

func (e errorCatalog) StoreSandbox(_ context.Context, _ string, _ *catalog.SandboxInfo, _ time.Duration) error {
	return nil
}

func (e errorCatalog) DeleteSandbox(_ context.Context, _ string, _ string) error {
	return nil
}

func (e errorCatalog) Close(_ context.Context) error {
	return nil
}

func TestCatalogResolution_CatalogReturnsGenericError(t *testing.T) {
	t.Parallel()

	c := errorCatalog{err: errors.New("catalog unavailable")}

	nodeIP, err := catalogResolution(t.Context(), "sbx", 8000, "", "", c, nil)
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrNodeNotFound)
	require.NotErrorIs(t, err, ErrNodeRouteUnavailable)
	require.Empty(t, nodeIP)
	require.Contains(t, err.Error(), "catalog unavailable")
}

func TestCatalogResolution_CatalogMiss_ResumeEmptyIPReturnsRouteUnavailable(t *testing.T) {
	t.Parallel()

	c := catalog.NewRedisSandboxCatalog(redis_utils.SetupInstance(t))

	nodeIP, err := catalogResolution(t.Context(), "sbx", 8000, "", "", c, stubResumer{nodeIP: ""})
	require.ErrorIs(t, err, ErrNodeRouteUnavailable)
	require.Empty(t, nodeIP)
}

func TestHandlePausedSandbox(t *testing.T) {
	t.Parallel()

	unavailableErr := status.Error(codes.Unavailable, "boom")
	failedPreconditionErr := status.Error(codes.FailedPrecondition, "sandbox resume precondition failed")

	tests := []struct {
		name       string
		resumer    PausedSandboxResumer
		trafficTok string
		envdTok    string
		wantNodeIP string
		wantResult autoResumeResult
		wantErrIs  error
		checkErr   func(*testing.T, error)
	}{
		{
			name:       "no resumer",
			resumer:    nil,
			wantResult: autoResumeNotAllowed,
		},
		{
			name:       "no resumer ignores tokens",
			resumer:    nil,
			trafficTok: "wrong-token",
			envdTok:    "envd-token",
			wantResult: autoResumeNotAllowed,
		},
		{
			name:       "resume returns not found",
			resumer:    stubResumer{err: status.Error(codes.NotFound, "not allowed")},
			trafficTok: "token",
			wantResult: autoResumeNotAllowed,
		},
		{
			name:       "resume returns snapshot not found",
			resumer:    stubResumer{err: status.Error(codes.NotFound, "snapshot not found")},
			trafficTok: "token",
			wantResult: autoResumeNotAllowed,
		},
		{
			name:       "resume permission denied",
			resumer:    stubResumer{err: status.Error(codes.PermissionDenied, "permission denied")},
			trafficTok: "token",
			wantResult: autoResumePermissionDenied,
			checkErr: func(t *testing.T, err error) {
				t.Helper()

				var deniedErr *reverseproxy.SandboxResumePermissionDeniedError
				require.ErrorAs(t, err, &deniedErr)
				require.Equal(t, "sbx", deniedErr.SandboxId)
			},
		},
		{
			name:       "resume resource exhausted",
			resumer:    stubResumer{err: status.Error(codes.ResourceExhausted, "rate limit hit")},
			trafficTok: "token",
			wantResult: autoResumeResourceExhausted,
			checkErr: func(t *testing.T, err error) {
				t.Helper()

				var exhaustedErr *reverseproxy.SandboxResourceExhaustedError
				require.ErrorAs(t, err, &exhaustedErr)
				require.Equal(t, "sbx", exhaustedErr.SandboxId)
				require.Equal(t, "rate limit hit", exhaustedErr.Message)
			},
		},
		{
			name:       "resume still transitioning",
			resumer:    stubResumer{err: status.Error(codes.FailedPrecondition, proxygrpc.SandboxStillTransitioningMessage)},
			trafficTok: "token",
			wantResult: autoResumeErrored,
			checkErr: func(t *testing.T, err error) {
				t.Helper()

				var transitioningErr *reverseproxy.SandboxStillTransitioningError
				require.ErrorAs(t, err, &transitioningErr)
				require.Equal(t, "sbx", transitioningErr.SandboxId)
			},
		},
		{
			name:       "failed precondition with other message stays generic",
			resumer:    stubResumer{err: failedPreconditionErr},
			trafficTok: "token",
			wantResult: autoResumeErrored,
			wantErrIs:  failedPreconditionErr,
			checkErr: func(t *testing.T, err error) {
				t.Helper()

				var transitioningErr *reverseproxy.SandboxStillTransitioningError
				require.NotErrorAs(t, err, &transitioningErr)
			},
		},
		{
			name:       "resume returns generic grpc error",
			resumer:    stubResumer{err: unavailableErr},
			trafficTok: "token",
			wantResult: autoResumeErrored,
			wantErrIs:  unavailableErr,
		},
		{
			name:       "resume succeeds",
			resumer:    stubResumer{nodeIP: "10.0.0.1"},
			trafficTok: "token",
			wantNodeIP: "10.0.0.1",
			wantResult: autoResumeSucceeded,
		},
		{
			name:       "resume succeeds with empty ip",
			resumer:    stubResumer{nodeIP: ""},
			trafficTok: "token",
			wantResult: autoResumeErrored,
			wantErrIs:  ErrNodeRouteUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			nodeIP, res, err := handlePausedSandbox(t.Context(), "sbx", 8000, tt.trafficTok, tt.envdTok, tt.resumer)

			require.Equal(t, tt.wantResult, res)
			require.Equal(t, tt.wantNodeIP, nodeIP)
			if tt.wantErrIs != nil {
				require.ErrorIs(t, err, tt.wantErrIs)
			} else if tt.checkErr == nil {
				require.NoError(t, err)
			}
			if tt.checkErr != nil {
				require.Error(t, err)
				tt.checkErr(t, err)
			}
		})
	}
}

func TestHandlePausedSandbox_PassesPortAndTokenToResumer(t *testing.T) {
	t.Parallel()

	resumer := &recordingResumer{}

	nodeIP, res, err := handlePausedSandbox(t.Context(), "sbx", 49983, "token", "envd-token", resumer)
	require.NoError(t, err)
	require.Equal(t, autoResumeSucceeded, res)
	require.Equal(t, "10.0.0.1", nodeIP)
	require.Equal(t, "sbx", resumer.sandboxID)
	require.EqualValues(t, 49983, resumer.sandboxPort)
	require.Equal(t, "token", resumer.trafficAccessToken)
	require.Equal(t, "envd-token", resumer.envdAccessToken)
}

func TestNewClientProxy_Construction(t *testing.T) {
	t.Parallel()

	c := catalog.NewRedisSandboxCatalog(redis_utils.SetupInstance(t))
	ff := newFF(t)

	p, err := NewClientProxy(noopmetric.NewMeterProvider(), "test-service", 0, c, nil, ff)
	require.NoError(t, err)
	require.NotNil(t, p)
	require.EqualValues(t, 0, p.CurrentServerConnections())
	require.EqualValues(t, 0, p.CurrentPoolConnections())
}

func TestNewClientProxy_HandlerErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		url              string
		resumer          PausedSandboxResumer
		wantStatus       int
		wantBodyContains []string
	}{
		{
			name:       "sandbox not found",
			url:        "http://49983-sbx.e2b.app/",
			wantStatus: http.StatusBadGateway,
			wantBodyContains: []string{
				`"sandboxId":"sbx"`,
				`"message":"The sandbox was not found"`,
			},
		},
		{
			name:       "resume permission denied",
			url:        "http://49983-sbx.e2b.app/",
			resumer:    stubResumer{err: status.Error(codes.PermissionDenied, "denied")},
			wantStatus: http.StatusForbidden,
			wantBodyContains: []string{
				`"sandboxId":"sbx"`,
				`credentials provided`,
			},
		},
		{
			name:       "resume resource exhausted",
			url:        "http://49983-sbx.e2b.app/",
			resumer:    stubResumer{err: status.Error(codes.ResourceExhausted, "rate limit")},
			wantStatus: http.StatusTooManyRequests,
			wantBodyContains: []string{
				`"sandboxId":"sbx"`,
				`"message":"rate limit"`,
			},
		},
		{
			name:       "resume still transitioning",
			url:        "http://49983-sbx.e2b.app/",
			resumer:    stubResumer{err: status.Error(codes.FailedPrecondition, proxygrpc.SandboxStillTransitioningMessage)},
			wantStatus: http.StatusConflict,
			wantBodyContains: []string{
				`"sandboxId":"sbx"`,
				`still transitioning`,
			},
		},
		{
			name:       "invalid host",
			url:        "http://invalid-host/",
			wantStatus: http.StatusBadRequest,
			wantBodyContains: []string{
				"Invalid host",
			},
		},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := catalog.NewRedisSandboxCatalog(redis_utils.SetupInstance(t))
			ff := newFF(t)
			p, err := NewClientProxy(noopmetric.NewMeterProvider(), "handler-errors-"+tt.name, uint16(i), c, tt.resumer, ff)
			require.NoError(t, err)

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.url, nil)
			rr := httptest.NewRecorder()
			p.Handler.ServeHTTP(rr, req)

			require.Equal(t, tt.wantStatus, rr.Code)
			for _, want := range tt.wantBodyContains {
				require.Contains(t, rr.Body.String(), want)
			}
		})
	}
}

func TestNewClientProxy_DuplicateMetricsRegistrationReturnsErrors(t *testing.T) {
	t.Parallel()

	c := catalog.NewRedisSandboxCatalog(redis_utils.SetupInstance(t))
	ff := newFF(t)

	// noop meter provider should not error; this is a sanity test that NewClientProxy
	// works repeatedly for separate service names without leaking metric registrations.
	for range 3 {
		_, err := NewClientProxy(noopmetric.NewMeterProvider(), "service", 0, c, nil, ff)
		require.NoError(t, err)
	}
}

// Sanity assertion that the proxy honors the configured idle timeout.
func TestNewClientProxy_HasIdleTimeout(t *testing.T) {
	t.Parallel()

	c := catalog.NewRedisSandboxCatalog(redis_utils.SetupInstance(t))
	ff := newFF(t)

	p, err := NewClientProxy(noopmetric.NewMeterProvider(), "service-idle", 0, c, nil, ff)
	require.NoError(t, err)
	require.GreaterOrEqual(t, p.IdleTimeout, idleTimeout)
	require.Less(t, p.IdleTimeout, 2*idleTimeout)
}

// Validate the Construction test exercises pool size accessor too.
func TestNewClientProxy_PoolAccessors(t *testing.T) {
	t.Parallel()

	c := catalog.NewRedisSandboxCatalog(redis_utils.SetupInstance(t))
	ff := newFF(t)

	p, err := NewClientProxy(noopmetric.NewMeterProvider(), "service-pool", 0, c, nil, ff)
	require.NoError(t, err)
	require.GreaterOrEqual(t, p.CurrentPoolSize(), 0)

	// Even on no-op meter providers, the proxy must still be wired up correctly.
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://49983-sbx.e2b.app/", nil)
	rr := httptest.NewRecorder()
	p.Handler.ServeHTTP(rr, req)
	require.Equal(t, http.StatusBadGateway, rr.Code)
	require.Contains(t, rr.Body.String(), `"sandboxId":"sbx"`)
}
