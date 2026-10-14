package proxy

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/launchdarkly/go-server-sdk/v7/testhelpers/ldtestdata"
	"github.com/stretchr/testify/require"
	noopmetric "go.opentelemetry.io/otel/metric/noop"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
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

func (s stubResumer) Resume(_ context.Context, _ string, _ uint64, _ string, _ string) (string, error) {
	return s.nodeIP, s.err
}

type recordingResumer struct {
	sandboxID          string
	sandboxPort        uint64
	trafficAccessToken string
	envdAccessToken    string
}

func (r *recordingResumer) Resume(_ context.Context, sandboxID string, sandboxPort uint64, trafficAccessToken string, envdAccessToken string) (string, error) {
	r.sandboxID = sandboxID
	r.sandboxPort = sandboxPort
	r.trafficAccessToken = trafficAccessToken
	r.envdAccessToken = envdAccessToken

	return "10.0.0.1", nil
}

// placementStatusWithCode is the status the API sends for a placement
// failure: the gRPC code its HTTP status maps to, with the semantic error
// code as an ErrorInfo detail (the shape resumeStatusError on the API side
// builds).
func placementStatusWithCode(t *testing.T, grpcCode codes.Code, code string) error {
	t.Helper()

	st, err := status.New(grpcCode, "Failed to place sandbox: "+code).
		WithDetails(&errdetails.ErrorInfo{Reason: code, Domain: proxygrpc.ResumeErrorDomain})
	require.NoError(t, err)

	return st.Err()
}

// placementStatusWithHTTPStatus is placementStatusWithCode with the HTTP
// status the API answered with carried on the detail, as the API sends it
// since it attaches the status.
func placementStatusWithHTTPStatus(t *testing.T, grpcCode codes.Code, code string, httpStatus int) error {
	t.Helper()

	st, err := status.New(grpcCode, "Failed to place sandbox: "+code).
		WithDetails(&errdetails.ErrorInfo{
			Reason:   code,
			Domain:   proxygrpc.ResumeErrorDomain,
			Metadata: map[string]string{proxygrpc.ResumeErrorHTTPStatusKey: strconv.Itoa(httpStatus)},
		})
	require.NoError(t, err)

	return st.Err()
}

func newFF(t *testing.T) *featureflags.Client {
	t.Helper()

	source := ldtestdata.DataSource()

	ff, err := featureflags.NewClientWithDatasource(source)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ff.Close(context.WithoutCancel(t.Context())) })

	return ff
}

func newFFWithOrchAcceptsCombinedHost(t *testing.T, enabled bool) *featureflags.Client {
	t.Helper()

	source := ldtestdata.DataSource()
	source.Update(source.Flag(featureflags.OrchAcceptsCombinedHostFlag.Key()).VariationForAll(enabled))

	ff, err := featureflags.NewClientWithDatasource(source)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ff.Close(context.WithoutCancel(t.Context())) })

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

	internalErr := status.Error(codes.Internal, "boom")
	deadlineErr := status.Error(codes.DeadlineExceeded, "orchestrator did not answer")
	internalPlacementErr := placementStatusWithCode(t, codes.Internal, "internal_server_error")
	createRefusedErr := placementStatusWithCode(t, codes.Internal, proxygrpc.PlacementCreateRefused)
	failedPreconditionErr := status.Error(codes.FailedPrecondition, "sandbox resume precondition failed")

	tests := []struct {
		name       string
		resumer    SandboxResumer
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
			name:       "resume unavailable is a capacity error",
			resumer:    stubResumer{err: status.Error(codes.Unavailable, "no capacity")},
			trafficTok: "token",
			wantResult: autoResumeErrored,
			checkErr: func(t *testing.T, err error) {
				t.Helper()

				var capacityErr *reverseproxy.SandboxCapacityError
				require.ErrorAs(t, err, &capacityErr)
				require.Equal(t, "sbx", capacityErr.SandboxId)
				require.Equal(t, "no capacity", capacityErr.Message)
			},
		},
		{
			name:       "resume returns generic grpc error",
			resumer:    stubResumer{err: internalErr},
			trafficTok: "token",
			wantResult: autoResumeErrored,
			wantErrIs:  internalErr,
		},
		{
			// The API sends a placement timeout as 504, which maps to
			// DeadlineExceeded; the code it carries says a retry helps.
			name:       "placement timeout is a capacity error",
			resumer:    stubResumer{err: placementStatusWithCode(t, codes.DeadlineExceeded, proxygrpc.PlacementTimeout)},
			trafficTok: "token",
			wantResult: autoResumeErrored,
			checkErr: func(t *testing.T, err error) {
				t.Helper()

				var capacityErr *reverseproxy.SandboxCapacityError
				require.ErrorAs(t, err, &capacityErr)
				require.False(t, capacityErr.Permanent)
			},
		},
		{
			// A create that failed on the node is a 500 with a code that
			// says to retry.
			name:       "create failed is a capacity error",
			resumer:    stubResumer{err: placementStatusWithCode(t, codes.Internal, proxygrpc.PlacementCreateFailed)},
			trafficTok: "token",
			wantResult: autoResumeErrored,
			checkErr: func(t *testing.T, err error) {
				t.Helper()

				var capacityErr *reverseproxy.SandboxCapacityError
				require.ErrorAs(t, err, &capacityErr)
				require.False(t, capacityErr.Permanent)
			},
		},
		{
			name:       "deadline exceeded without a code stays generic",
			resumer:    stubResumer{err: deadlineErr},
			trafficTok: "token",
			wantResult: autoResumeErrored,
			wantErrIs:  deadlineErr,
		},
		{
			// A create the node refused for good is a placement refusal no
			// retry helps, whatever gRPC code its HTTP status mapped to.
			name:       "create refused is a permanent placement refusal",
			resumer:    stubResumer{err: createRefusedErr},
			trafficTok: "token",
			wantResult: autoResumeErrored,
			checkErr: func(t *testing.T, err error) {
				t.Helper()

				var capacityErr *reverseproxy.SandboxCapacityError
				require.ErrorAs(t, err, &capacityErr)
				require.True(t, capacityErr.Permanent)
				require.Equal(t, proxygrpc.PlacementCreateRefused, capacityErr.ErrorCode)
				require.Equal(t, http.StatusServiceUnavailable, capacityErr.StatusCode)
			},
		},
		{
			name:       "create refused as a bad request keeps the API's 400",
			resumer:    stubResumer{err: placementStatusWithCode(t, codes.InvalidArgument, proxygrpc.PlacementCreateRefused)},
			trafficTok: "token",
			wantResult: autoResumeErrored,
			checkErr: func(t *testing.T, err error) {
				t.Helper()

				var capacityErr *reverseproxy.SandboxCapacityError
				require.ErrorAs(t, err, &capacityErr)
				require.True(t, capacityErr.Permanent)
				require.Equal(t, http.StatusBadRequest, capacityErr.StatusCode)
			},
		},
		{
			// The API answers a refusal of a stored build as 500; the
			// Internal that maps to must not fold it into the 503 page.
			name:       "create refused as a server error keeps the API's 500",
			resumer:    stubResumer{err: placementStatusWithHTTPStatus(t, codes.Internal, proxygrpc.PlacementCreateRefused, http.StatusInternalServerError)},
			trafficTok: "token",
			wantResult: autoResumeErrored,
			checkErr: func(t *testing.T, err error) {
				t.Helper()

				var capacityErr *reverseproxy.SandboxCapacityError
				require.ErrorAs(t, err, &capacityErr)
				require.True(t, capacityErr.Permanent)
				require.Equal(t, http.StatusInternalServerError, capacityErr.StatusCode)
			},
		},
		{
			name:       "create refused with a carried status that is no error falls back to the page's 503",
			resumer:    stubResumer{err: placementStatusWithHTTPStatus(t, codes.Internal, proxygrpc.PlacementCreateRefused, http.StatusOK)},
			trafficTok: "token",
			wantResult: autoResumeErrored,
			checkErr: func(t *testing.T, err error) {
				t.Helper()

				var capacityErr *reverseproxy.SandboxCapacityError
				require.ErrorAs(t, err, &capacityErr)
				require.True(t, capacityErr.Permanent)
				require.Equal(t, http.StatusServiceUnavailable, capacityErr.StatusCode)
			},
		},
		{
			name:       "internal with the generic placement code stays generic",
			resumer:    stubResumer{err: internalPlacementErr},
			trafficTok: "token",
			wantResult: autoResumeErrored,
			wantErrIs:  internalPlacementErr,
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

	p, _, err := NewClientProxy(noopmetric.NewMeterProvider(), "test-service", 0, 5007, c, nil, ff)
	require.NoError(t, err)
	require.NotNil(t, p)
	require.EqualValues(t, 0, p.CurrentServerConnections())
	require.EqualValues(t, 0, p.CurrentPoolConnections())
}

type fixedCatalog struct {
	errorCatalog

	nodeIP string
}

func (c fixedCatalog) GetSandbox(context.Context, string) (*catalog.SandboxInfo, error) {
	return &catalog.SandboxInfo{OrchestratorIP: c.nodeIP}, nil
}

func TestNewClientProxy_ForwardsToConfiguredOrchestratorPort(t *testing.T) {
	t.Parallel()

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/commands" || r.Header.Get("X-Access-Token") != "sandbox-token" {
			w.WriteHeader(http.StatusBadRequest)

			return
		}
		_, _ = w.Write([]byte("selected-orchestrator"))
	}))
	t.Cleanup(backend.Close)
	address := backend.Listener.Addr().(*net.TCPAddr)
	c := fixedCatalog{nodeIP: address.IP.String()}
	p, _, err := NewClientProxy(noopmetric.NewMeterProvider(), "configured-port", 0, uint16(address.Port), c, nil, newFF(t))
	require.NoError(t, err)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://49983-sbx.e2b.app/commands", nil)
	req.Header.Set("X-Access-Token", "sandbox-token")
	rr := httptest.NewRecorder()
	p.Handler.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "selected-orchestrator", rr.Body.String())
}

func TestNewClientProxy_HandlerErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		url              string
		resumer          SandboxResumer
		wantStatus       int
		wantRetryAfter   string
		wantBodyContains []string
		wantBodyOmits    []string
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
			name:           "resume unavailable keeps the transport error off the page",
			url:            "http://49983-sbx.e2b.app/",
			resumer:        stubResumer{err: status.Error(codes.Unavailable, "transport: Error while dialing: dial tcp 10.1.2.3:5008: connect: connection refused")},
			wantStatus:     http.StatusServiceUnavailable,
			wantRetryAfter: "2",
			wantBodyContains: []string{
				`"sandboxId":"sbx"`,
				`"message":"The sandbox cannot be resumed right now. Try again in a moment."`,
			},
			wantBodyOmits: []string{"dial tcp", "10.1.2.3"},
		},
		{
			name:           "resume unavailable without a message",
			url:            "http://49983-sbx.e2b.app/",
			resumer:        stubResumer{err: status.Error(codes.Unavailable, "")},
			wantStatus:     http.StatusServiceUnavailable,
			wantRetryAfter: "2",
			wantBodyContains: []string{
				`"sandboxId":"sbx"`,
				`"message":"The sandbox cannot be resumed right now. Try again in a moment."`,
			},
		},
		{
			// A 504 from the API maps to DeadlineExceeded; the code it
			// carries puts it on the capacity page, not the not-found page
			// the SDKs read as the sandbox being gone.
			name:           "placement timeout answers the capacity page",
			url:            "http://49983-sbx.e2b.app/",
			resumer:        stubResumer{err: placementStatusWithCode(t, codes.DeadlineExceeded, proxygrpc.PlacementTimeout)},
			wantStatus:     http.StatusServiceUnavailable,
			wantRetryAfter: "2",
			wantBodyContains: []string{
				`"sandboxId":"sbx"`,
				`"message":"The sandbox cannot be resumed right now. Try again in a moment."`,
			},
			wantBodyOmits: []string{"Failed to place sandbox"},
		},
		{
			name:           "create failed answers the capacity page",
			url:            "http://49983-sbx.e2b.app/",
			resumer:        stubResumer{err: placementStatusWithCode(t, codes.Internal, proxygrpc.PlacementCreateFailed)},
			wantStatus:     http.StatusServiceUnavailable,
			wantRetryAfter: "2",
			wantBodyContains: []string{
				`"sandboxId":"sbx"`,
				`"message":"The sandbox cannot be resumed right now. Try again in a moment."`,
			},
			wantBodyOmits: []string{"Failed to place sandbox"},
		},
		{
			name:       "create refused answers the refused page with the code and no Retry-After",
			url:        "http://49983-sbx.e2b.app/",
			resumer:    stubResumer{err: placementStatusWithCode(t, codes.Internal, proxygrpc.PlacementCreateRefused)},
			wantStatus: http.StatusServiceUnavailable,
			wantBodyContains: []string{
				`"sandboxId":"sbx"`,
				`"message":"The sandbox cannot be resumed: no node in this cluster can run it."`,
				`"error_code":"` + proxygrpc.PlacementCreateRefused + `"`,
			},
			wantBodyOmits: []string{"Failed to place sandbox", "was not found"},
		},
		{
			// A refusal the API answered as a bad request keeps its 400: a
			// 503 would invite the SDKs' retries, each starting a resume.
			name:       "create refused as a bad request answers the refused page as 400",
			url:        "http://49983-sbx.e2b.app/",
			resumer:    stubResumer{err: placementStatusWithCode(t, codes.InvalidArgument, proxygrpc.PlacementCreateRefused)},
			wantStatus: http.StatusBadRequest,
			wantBodyContains: []string{
				`"sandboxId":"sbx"`,
				`"error_code":"` + proxygrpc.PlacementCreateRefused + `"`,
				`"code":400`,
			},
		},
		{
			// The API answers a refusal of a stored build as 500, which maps
			// to Internal; the page answers with the API's 500 and no
			// Retry-After, since a 503 would invite the SDKs' retries, each
			// starting a resume for an answer no retry changes.
			name:       "create refused as a server error answers the refused page as 500",
			url:        "http://49983-sbx.e2b.app/",
			resumer:    stubResumer{err: placementStatusWithHTTPStatus(t, codes.Internal, proxygrpc.PlacementCreateRefused, http.StatusInternalServerError)},
			wantStatus: http.StatusInternalServerError,
			wantBodyContains: []string{
				`"sandboxId":"sbx"`,
				`"message":"The sandbox cannot be resumed: no node in this cluster can run it."`,
				`"error_code":"` + proxygrpc.PlacementCreateRefused + `"`,
				`"code":500`,
			},
			wantBodyOmits: []string{"Failed to place sandbox", "was not found"},
		},
		{
			name:       "deadline exceeded without a code stays on the not-found page",
			url:        "http://49983-sbx.e2b.app/",
			resumer:    stubResumer{err: status.Error(codes.DeadlineExceeded, "orchestrator did not answer")},
			wantStatus: http.StatusBadGateway,
			wantBodyContains: []string{
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
			name:           "resume still transitioning on a user port",
			url:            "http://3000-sbx.e2b.app/",
			resumer:        stubResumer{err: status.Error(codes.FailedPrecondition, proxygrpc.SandboxStillTransitioningMessage)},
			wantStatus:     http.StatusConflict,
			wantRetryAfter: "2",
			wantBodyContains: []string{
				`"sandboxId":"sbx"`,
				`still transitioning`,
			},
		},
		{
			// The SDK reads an envd-port 409 as "already exists".
			name:           "resume still transitioning on the envd port",
			url:            "http://49983-sbx.e2b.app/",
			resumer:        stubResumer{err: status.Error(codes.FailedPrecondition, proxygrpc.SandboxStillTransitioningMessage)},
			wantStatus:     http.StatusServiceUnavailable,
			wantRetryAfter: "2",
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
			p, _, err := NewClientProxy(noopmetric.NewMeterProvider(), "handler-errors-"+tt.name, uint16(i), 5007, c, tt.resumer, ff)
			require.NoError(t, err)

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.url, nil)
			rr := httptest.NewRecorder()
			p.Handler.ServeHTTP(rr, req)

			require.Equal(t, tt.wantStatus, rr.Code)
			require.Equal(t, tt.wantRetryAfter, rr.Header().Get("Retry-After"))
			wantExposed := ""
			if tt.wantRetryAfter != "" {
				// A browser hides the delay from a cross-origin page unless
				// the response lists the header.
				wantExposed = "Retry-After"
			}
			require.Equal(t, wantExposed, rr.Header().Get("Access-Control-Expose-Headers"))
			for _, want := range tt.wantBodyContains {
				require.Contains(t, rr.Body.String(), want)
			}
			for _, omitted := range tt.wantBodyOmits {
				require.NotContains(t, rr.Body.String(), omitted)
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
		_, _, err := NewClientProxy(noopmetric.NewMeterProvider(), "service", 0, 5007, c, nil, ff)
		require.NoError(t, err)
	}
}

// Sanity assertion that the proxy honors the configured idle timeout.
func TestNewClientProxy_HasIdleTimeout(t *testing.T) {
	t.Parallel()

	c := catalog.NewRedisSandboxCatalog(redis_utils.SetupInstance(t))
	ff := newFF(t)

	p, _, err := NewClientProxy(noopmetric.NewMeterProvider(), "service-idle", 0, 5007, c, nil, ff)
	require.NoError(t, err)
	require.GreaterOrEqual(t, p.IdleTimeout, idleTimeout)
	require.Less(t, p.IdleTimeout, 2*idleTimeout)
}

// Validate the Construction test exercises pool size accessor too.
func TestNewClientProxy_PoolAccessors(t *testing.T) {
	t.Parallel()

	c := catalog.NewRedisSandboxCatalog(redis_utils.SetupInstance(t))
	ff := newFF(t)

	p, _, err := NewClientProxy(noopmetric.NewMeterProvider(), "service-pool", 0, 5007, c, nil, ff)
	require.NoError(t, err)
	require.GreaterOrEqual(t, p.CurrentPoolSize(), 0)

	// Even on no-op meter providers, the proxy must still be wired up correctly.
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://49983-sbx.e2b.app/", nil)
	rr := httptest.NewRecorder()
	p.Handler.ServeHTTP(rr, req)
	require.Equal(t, http.StatusBadGateway, rr.Code)
	require.Contains(t, rr.Body.String(), `"sandboxId":"sbx"`)
}
