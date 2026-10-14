package proxy

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	reverseproxy "github.com/e2b-dev/infra/packages/shared/pkg/proxy"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// gatedResumer blocks every Resume call until release is closed or the call's
// context ends, and counts the calls it received. With firstErr set the first
// call fails with it at once instead of waiting.
type gatedResumer struct {
	release  chan struct{}
	firstErr error
	nodeIP   string
	err      error
	calls    atomic.Int32
	returned atomic.Int32 // calls that have ended, however they ended
}

func (g *gatedResumer) Resume(ctx context.Context, _ string, _ uint64, _ string, _ string) (string, error) {
	defer g.returned.Add(1)

	if g.calls.Add(1) == 1 && g.firstErr != nil {
		return "", g.firstErr
	}

	select {
	case <-g.release:
		return g.nodeIP, g.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// awaitingContext reports, once, when a caller starts waiting on it, which the
// flight does only after it has attached to the registry and awaitAttempt only
// in its select, after it captured the attempt number and changed under the mutex.
type awaitingContext struct {
	context.Context //nolint:containedctx // the type is a context, wrapped to observe when it is waited on

	once     sync.Once
	awaiting chan struct{}
}

func newAwaitingContext(ctx context.Context) *awaitingContext {
	return &awaitingContext{Context: ctx, awaiting: make(chan struct{})}
}

func (c *awaitingContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.awaiting) })

	return c.Context.Done()
}

type resumeResult struct {
	nodeIP string
	err    error
}

// resumeInBackground calls Resume on its own goroutine and delivers the result
// on the returned channel, so the assertions stay on the test goroutine.
func resumeInBackground(flights *resumeFlights, ctx context.Context) <-chan resumeResult {
	return resumeInBackgroundWith(flights, ctx, 8000, "token", "")
}

func resumeInBackgroundWith(flights *resumeFlights, ctx context.Context, port uint64, trafficAccessToken string, envdAccessToken string) <-chan resumeResult {
	result := make(chan resumeResult, 1)
	go func() {
		nodeIP, err := flights.Resume(ctx, "sbx", port, trafficAccessToken, envdAccessToken)
		result <- resumeResult{nodeIP: nodeIP, err: err}
	}()

	return result
}

// tokenResumer blocks like gatedResumer and then answers by the traffic
// token: the valid one is served, any other is denied.
type tokenResumer struct {
	release    chan struct{}
	validToken string
	nodeIP     string
	calls      atomic.Int32
}

func (g *tokenResumer) Resume(ctx context.Context, _ string, _ uint64, trafficAccessToken string, _ string) (string, error) {
	g.calls.Add(1)

	select {
	case <-g.release:
	case <-ctx.Done():
		return "", ctx.Err()
	}

	if trafficAccessToken != g.validToken {
		return "", status.Error(codes.PermissionDenied, "invalid traffic access token")
	}

	return g.nodeIP, nil
}

type metricReader struct {
	provider *sdkmetric.MeterProvider
	meter    metric.Meter
	reader   *sdkmetric.ManualReader
}

func newMetricReader(t *testing.T) metricReader {
	t.Helper()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = provider.Shutdown(context.WithoutCancel(t.Context())) })

	return metricReader{provider: provider, meter: provider.Meter("github.com/e2b-dev/infra/packages/client-proxy/internal/proxy"), reader: reader}
}

func (m metricReader) collect(t *testing.T) metricdata.ResourceMetrics {
	t.Helper()

	var collected metricdata.ResourceMetrics
	require.NoError(t, m.reader.Collect(t.Context(), &collected))

	return collected
}

func (m metricReader) counter(t *testing.T, name string, attrs ...attribute.KeyValue) int64 {
	t.Helper()

	want := attribute.NewSet(attrs...)
	for _, scope := range m.collect(t).ScopeMetrics {
		for _, metric := range scope.Metrics {
			if metric.Name != name {
				continue
			}

			sum, ok := metric.Data.(metricdata.Sum[int64])
			require.True(t, ok, "%s is not a counter", name)
			for _, point := range sum.DataPoints {
				if point.Attributes.Equals(&want) {
					return point.Value
				}
			}
		}
	}

	return 0
}

func (m metricReader) flightCount(t *testing.T, role string, result string) int64 {
	t.Helper()

	return m.counter(t, string(telemetry.ClientProxyResumeFlightCounterName),
		attribute.String("role", role), attribute.String("result", result))
}

func newTestResumeFlights(t *testing.T, metrics metricReader, resumer SandboxResumer) *resumeFlights {
	t.Helper()

	flights, err := newResumeFlights(metrics.meter, resumer, newFF(t))
	require.NoError(t, err)

	return flights
}

// Concurrent callers for one paused sandbox share a single Resume call
// and every one of them receives its node IP.
func TestResumeFlights_ConcurrentCallersShareOneResume(t *testing.T) {
	t.Parallel()

	const callers = 10

	metrics := newMetricReader(t)
	resumer := &gatedResumer{release: make(chan struct{}), nodeIP: "10.0.0.1"}
	flights := newTestResumeFlights(t, metrics, resumer)

	results := make([]<-chan resumeResult, 0, callers)
	contexts := make([]*awaitingContext, 0, callers)
	for range callers {
		ctx := newAwaitingContext(t.Context())
		contexts = append(contexts, ctx)
		results = append(results, resumeInBackground(flights, ctx))
	}

	for _, ctx := range contexts {
		<-ctx.awaiting
	}
	close(resumer.release)

	for _, result := range results {
		r := <-result
		require.NoError(t, r.err)
		require.Equal(t, "10.0.0.1", r.nodeIP)
	}

	require.EqualValues(t, 1, resumer.calls.Load())
	require.EqualValues(t, 1, metrics.flightCount(t, resumeFlightRoleLeader, resumeFlightResultServed))
	require.EqualValues(t, callers-1, metrics.flightCount(t, resumeFlightRoleJoiner, resumeFlightResultServed))
}

// The leader's request ends mid-flight; the flight completes and serves
// the callers that joined it.
func TestResumeFlights_LeaderCancelDoesNotFailJoiners(t *testing.T) {
	t.Parallel()

	metrics := newMetricReader(t)
	resumer := &gatedResumer{release: make(chan struct{}), nodeIP: "10.0.0.1"}
	flights := newTestResumeFlights(t, metrics, resumer)

	leaderCtx, cancelLeader := context.WithCancel(t.Context())
	leader := newAwaitingContext(leaderCtx)
	leaderResult := resumeInBackground(flights, leader)
	<-leader.awaiting

	joiner := newAwaitingContext(t.Context())
	joinerResult := resumeInBackground(flights, joiner)
	<-joiner.awaiting

	cancelLeader()
	require.ErrorIs(t, (<-leaderResult).err, context.Canceled)
	require.EqualValues(t, 1, metrics.flightCount(t, resumeFlightRoleLeader, resumeFlightResultCanceled))

	close(resumer.release)
	joined := <-joinerResult
	require.NoError(t, joined.err)
	require.Equal(t, "10.0.0.1", joined.nodeIP)
	require.EqualValues(t, 1, resumer.calls.Load())
	require.EqualValues(t, 1, metrics.flightCount(t, resumeFlightRoleJoiner, resumeFlightResultServed))
}

// A resume that succeeds on its first attempt is served straight away
// and never touches the parking lot.
func TestResumeFlights_FirstAttemptServes(t *testing.T) {
	t.Parallel()

	metrics := newMetricReader(t)
	resumer := &recordingResumer{}
	flights := newTestResumeFlights(t, metrics, resumer)
	flights.featureFlags = newFFWithParking(t, true, 15000, 512)

	nodeIP, err := flights.Resume(t.Context(), "sbx", 49983, "token", "envd-token")
	require.NoError(t, err)
	require.Equal(t, "10.0.0.1", nodeIP)
	require.Equal(t, "sbx", resumer.sandboxID)
	require.EqualValues(t, 49983, resumer.sandboxPort)
	require.Equal(t, "token", resumer.trafficAccessToken)
	require.Equal(t, "envd-token", resumer.envdAccessToken)
	require.EqualValues(t, 1, metrics.flightCount(t, resumeFlightRoleLeader, resumeFlightResultServed))
	require.Zero(t, metrics.parkingWaitSamples(t))
	require.Zero(t, metrics.counter(t, string(telemetry.ClientProxyResumeParkingActiveName)))
}

// An attempt that never answers ends at the ceiling with the unavailable
// page, not the not-found page, and the registry forgets the flight before
// its callers learn the result.
func TestResumeFlights_AttemptCeilingEndsAnUnansweredResumeWithTheUnavailablePage(t *testing.T) {
	t.Parallel()

	metrics := newMetricReader(t)
	resumer := &gatedResumer{release: make(chan struct{})}
	flights := newTestResumeFlights(t, metrics, resumer)
	flights.attemptCeiling = 50 * time.Millisecond

	start := time.Now()
	nodeIP, res, err := handlePausedSandbox(t.Context(), "sbx", 8000, "token", "", flights)
	require.Less(t, time.Since(start), 5*time.Second)
	require.Equal(t, autoResumeErrored, res)
	require.Empty(t, nodeIP)

	var capacityErr *reverseproxy.SandboxCapacityError
	require.ErrorAs(t, err, &capacityErr)
	require.Equal(t, "sbx", capacityErr.SandboxId)
	require.Equal(t, errAttemptDeadline.Error(), capacityErr.Message)

	flights.mu.Lock()
	defer flights.mu.Unlock()
	require.Empty(t, flights.flights)
	require.EqualValues(t, 1, metrics.flightCount(t, resumeFlightRoleLeader, resumeFlightResultError))
}

// A leader carrying an invalid token does not decide the answer for a
// concurrent caller carrying the valid one: the joiner is served, the leader
// is denied.
func TestResumeFlights_InvalidLeaderTokenDoesNotDenyValidJoiner(t *testing.T) {
	t.Parallel()

	metrics := newMetricReader(t)
	resumer := &tokenResumer{release: make(chan struct{}), validToken: "valid", nodeIP: "10.0.0.1"}
	flights := newTestResumeFlights(t, metrics, resumer)

	type pausedResult struct {
		nodeIP string
		res    autoResumeResult
		err    error
	}
	resumeThroughHandler := func(ctx context.Context, token string) <-chan pausedResult {
		result := make(chan pausedResult, 1)
		go func() {
			nodeIP, res, err := handlePausedSandbox(ctx, "sbx", 8000, token, "", flights)
			result <- pausedResult{nodeIP: nodeIP, res: res, err: err}
		}()

		return result
	}

	leader := newAwaitingContext(t.Context())
	leaderResult := resumeThroughHandler(leader, "wrong")
	<-leader.awaiting

	joiner := newAwaitingContext(t.Context())
	joinerResult := resumeThroughHandler(joiner, "valid")
	<-joiner.awaiting

	close(resumer.release)

	denied := <-leaderResult
	require.Equal(t, autoResumePermissionDenied, denied.res)
	var deniedErr *reverseproxy.SandboxResumePermissionDeniedError
	require.ErrorAs(t, denied.err, &deniedErr)

	served := <-joinerResult
	require.NoError(t, served.err)
	require.Equal(t, autoResumeSucceeded, served.res)
	require.Equal(t, "10.0.0.1", served.nodeIP)
	require.EqualValues(t, 2, resumer.calls.Load())
}

// Callers that differ in port or in either token run their own resume;
// only identical credentials on the same port share a flight.
func TestResumeFlights_DifferentPortOrCredentialsDoNotShare(t *testing.T) {
	t.Parallel()

	metrics := newMetricReader(t)
	resumer := &gatedResumer{release: make(chan struct{}), nodeIP: "10.0.0.1"}
	flights := newTestResumeFlights(t, metrics, resumer)

	callers := []struct {
		port    uint64
		traffic string
		envd    string
	}{
		{port: 8000, traffic: "token", envd: ""},
		{port: 49983, traffic: "token", envd: ""},
		{port: 8000, traffic: "other", envd: ""},
		{port: 8000, traffic: "token", envd: "envd"},
		{port: 8000, traffic: "token", envd: ""},
	}

	results := make([]<-chan resumeResult, 0, len(callers))
	for _, c := range callers {
		ctx := newAwaitingContext(t.Context())
		results = append(results, resumeInBackgroundWith(flights, ctx, c.port, c.traffic, c.envd))
		<-ctx.awaiting
	}
	close(resumer.release)

	for _, result := range results {
		r := <-result
		require.NoError(t, r.err)
		require.Equal(t, "10.0.0.1", r.nodeIP)
	}

	require.EqualValues(t, 4, resumer.calls.Load())
	require.EqualValues(t, 4, metrics.flightCount(t, resumeFlightRoleLeader, resumeFlightResultServed))
	require.EqualValues(t, 1, metrics.flightCount(t, resumeFlightRoleJoiner, resumeFlightResultServed))
}
