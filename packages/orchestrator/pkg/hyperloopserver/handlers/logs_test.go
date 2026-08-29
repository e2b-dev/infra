//go:build linux

package handlers

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/network"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const staleLogWarningMessage = "dropping envd log with a stale pre-resume timestamp"

func TestLogForwardWriteCountName(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "orchestrator.hyperloop.log_forward.write_count", logForwardWriteCountName)
}

func TestHasStaleLogTimestamp(t *testing.T) {
	t.Parallel()

	lifecycleStart, err := time.Parse(envdTimestampLayout, "2026-07-16T10:00:00Z")
	require.NoError(t, err)

	t.Run("timestamp from the snapshot-restored clock is stale", func(t *testing.T) {
		t.Parallel()

		payload := map[string]any{"timestamp": "2026-06-09T08:00:00.123456789Z"}

		assert.True(t, hasStaleLogTimestamp(payload, lifecycleStart))
	})

	t.Run("timestamp after lifecycle start is not stale", func(t *testing.T) {
		t.Parallel()

		freshRaw := lifecycleStart.Add(time.Second).Format(envdTimestampLayout)
		payload := map[string]any{"timestamp": freshRaw}

		assert.False(t, hasStaleLogTimestamp(payload, lifecycleStart))
	})

	t.Run("timestamp at lifecycle start is not stale", func(t *testing.T) {
		t.Parallel()

		payload := map[string]any{"timestamp": lifecycleStart.Format(envdTimestampLayout)}

		assert.False(t, hasStaleLogTimestamp(payload, lifecycleStart))
	})

	t.Run("timestamp at the cutoff is not stale", func(t *testing.T) {
		t.Parallel()

		cutoffRaw := lifecycleStart.Add(-clockSkewTolerance).Format(envdTimestampLayout)
		payload := map[string]any{"timestamp": cutoffRaw}

		assert.False(t, hasStaleLogTimestamp(payload, lifecycleStart))
	})

	t.Run("timestamp within clock-skew tolerance is not stale", func(t *testing.T) {
		t.Parallel()

		withinToleranceRaw := lifecycleStart.Add(-clockSkewTolerance / 2).Format(envdTimestampLayout)
		payload := map[string]any{"timestamp": withinToleranceRaw}

		assert.False(t, hasStaleLogTimestamp(payload, lifecycleStart))
	})

	t.Run("timestamp past the cutoff is stale", func(t *testing.T) {
		t.Parallel()

		staleRaw := lifecycleStart.Add(-clockSkewTolerance - time.Second).Format(envdTimestampLayout)
		payload := map[string]any{"timestamp": staleRaw}

		assert.True(t, hasStaleLogTimestamp(payload, lifecycleStart))
	})

	t.Run("zero lifecycle start disables stale detection", func(t *testing.T) {
		t.Parallel()

		payload := map[string]any{"timestamp": "2026-06-09T08:00:00Z"}

		assert.False(t, hasStaleLogTimestamp(payload, time.Time{}))
	})

	t.Run("missing timestamp is not stale", func(t *testing.T) {
		t.Parallel()

		payload := map[string]any{"message": "hello"}

		assert.False(t, hasStaleLogTimestamp(payload, lifecycleStart))
	})

	t.Run("non-string timestamp is not stale", func(t *testing.T) {
		t.Parallel()

		payload := map[string]any{"timestamp": 12345}

		assert.False(t, hasStaleLogTimestamp(payload, lifecycleStart))
	})

	t.Run("invalid timestamp is not stale", func(t *testing.T) {
		t.Parallel()

		payload := map[string]any{"timestamp": "not-a-time"}

		assert.False(t, hasStaleLogTimestamp(payload, lifecycleStart))
	})
}

func TestAPIStoreLogsSamplesStaleTimestampWarning(t *testing.T) { //nolint:paralleltest // swaps package-global metric instrument
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { require.NoError(t, meterProvider.Shutdown(t.Context())) })
	testCounter, err := meterProvider.Meter(
		"github.com/e2b-dev/infra/packages/orchestrator/pkg/hyperloopserver/handlers",
	).Int64Counter(logForwardWriteCountName)
	require.NoError(t, err)
	previousCounter := logForwardWriteCount
	logForwardWriteCount = testCounter
	t.Cleanup(func() { logForwardWriteCount = previousCounter })

	var collectorRequests atomic.Int64
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		collectorRequests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(collector.Close)

	core, observedLogs := observer.New(zap.WarnLevel)
	testLogger := logger.NewTracedLoggerFromCore(core)
	lifecycleStart := time.Now().UTC()
	hostIP := net.IPv4(127, 0, 0, 2)
	sbx := &sandbox.Sandbox{
		LifecycleStartedAt: lifecycleStart,
		Metadata: &sandbox.Metadata{
			Runtime: sandbox.RuntimeMetadata{
				SandboxID:  "sandbox-1",
				TemplateID: "template-1",
				TeamID:     "team-1",
			},
		},
		Resources: &sandbox.Resources{
			Slot: &network.Slot{HostIP: hostIP},
		},
	}
	sandboxes := sandbox.NewSandboxesMap()
	sandboxes.AssignNetwork(t.Context(), sbx)

	store := NewHyperloopStore(testLogger, sandboxes, collector.URL, nil)
	router := gin.New()
	router.POST("/logs", store.Logs)

	postLog := func(timestamp time.Time) *httptest.ResponseRecorder {
		t.Helper()

		request := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			"/logs",
			strings.NewReader(fmt.Sprintf(
				`{"instanceID":"sandbox-1","timestamp":%q,"message":"hello"}`,
				timestamp.Format(envdTimestampLayout),
			)),
		)
		request.Header.Set("Content-Type", "application/json")
		request.RemoteAddr = net.JoinHostPort(hostIP.String(), "4321")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)

		return response
	}

	staleTimestamp := lifecycleStart.Add(-clockSkewTolerance - time.Second)
	for range 50 {
		response := postLog(staleTimestamp)
		assert.Equal(t, http.StatusBadRequest, response.Code)
	}

	warnings := observedLogs.FilterMessage(staleLogWarningMessage).All()
	require.Len(t, warnings, 1)
	assert.Equal(t, sbx.Runtime.SandboxID, warnings[0].ContextMap()["sandbox.id"])
	assert.NotContains(t, warnings[0].ContextMap(), "suppressed_since_last")
	assert.Zero(t, collectorRequests.Load())

	var metrics metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &metrics))
	require.Len(t, metrics.ScopeMetrics, 1)
	require.Len(t, metrics.ScopeMetrics[0].Metrics, 1)
	metric := metrics.ScopeMetrics[0].Metrics[0]
	assert.Equal(t, logForwardWriteCountName, metric.Name)
	sum, ok := metric.Data.(metricdata.Sum[int64])
	require.True(t, ok)
	require.Len(t, sum.DataPoints, 1)
	assert.Equal(t, int64(50), sum.DataPoints[0].Value)
	assert.Equal(t, attribute.NewSet(
		attribute.String("route", "ingest"),
		attribute.String("result", "dropped"),
		attribute.String("reason", "stale_timestamp"),
	), sum.DataPoints[0].Attributes)

	response := postLog(lifecycleStart.Add(time.Second))
	assert.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, int64(1), collectorRequests.Load())
}

func TestAPIStoreForwardLogsStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{name: "success", status: http.StatusAccepted},
		{name: "client error", status: http.StatusUnprocessableEntity, wantErr: true},
		{name: "server error", status: http.StatusBadGateway, wantErr: true},
		{name: "rate limited", status: http.StatusTooManyRequests, wantErr: true},
		{name: "internal server error", status: http.StatusInternalServerError, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte("collector diagnostic"))
			}))
			t.Cleanup(server.Close)

			store := &APIStore{collectorClient: *server.Client()}
			err := store.forwardLogs(t.Context(), server.URL, []byte(`{"secret":"not-in-error"}`), 0)
			if tt.wantErr && err == nil {
				t.Fatal("expected an error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if err != nil && strings.Contains(err.Error(), "not-in-error") {
				t.Fatalf("error contains request payload: %v", err)
			}
		})
	}
}

// TestAPIStoreForwardLogsRespectsTimeout verifies a slow/hung Vector doesn't
// block forwardLogs forever when a non-zero timeout is passed: the request is
// aborted client-side and an error is returned promptly, regardless of
// whether the server ever responds.
func TestAPIStoreForwardLogsRespectsTimeout(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() {
		close(release)
		server.Close()
	})

	store := &APIStore{collectorClient: *server.Client()}

	start := time.Now()
	err := store.forwardLogs(t.Context(), server.URL, []byte(`{"msg":"slow"}`), 50*time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error from a slow/hung response once the timeout passes")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("forwardLogs took %v to return; expected it to abort promptly once the 50ms timeout passed", elapsed)
	}
}

// TestAPIStoreForwardLogsZeroTimeoutRespectsClientTimeout is the legacy-mode
// equivalent: with timeout == 0 (route.Timeout unset, matching pre-flag
// behavior), forwardLogs must still be bounded by collectorClient's own
// Timeout rather than hanging forever.
func TestAPIStoreForwardLogsZeroTimeoutRespectsClientTimeout(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() {
		close(release)
		server.Close()
	})

	client := server.Client()
	client.Timeout = 50 * time.Millisecond
	store := &APIStore{collectorClient: *client}

	start := time.Now()
	err := store.forwardLogs(t.Context(), server.URL, []byte(`{"msg":"slow"}`), 0)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error from a slow/hung response once the client timeout passes")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("forwardLogs took %v to return; expected it to abort promptly once the 50ms client timeout passed", elapsed)
	}
}
