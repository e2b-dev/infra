package clusters

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grafana/loki/v3/pkg/logproto"
	"github.com/launchdarkly/go-server-sdk/v7/testhelpers/ldtestdata"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/clickhouse/pkg/sandboxlogs"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
)

var testLogReadMetricReader = metric.NewManualReader()

func TestMain(m *testing.M) {
	otel.SetMeterProvider(metric.NewMeterProvider(metric.WithReader(testLogReadMetricReader)))
	m.Run()
}

type stubClickhouseLogsReader struct {
	sandbox func(context.Context, uuid.UUID, string, time.Time, time.Time, int, sandboxlogs.SortOrder, *logs.LogLevel, *string) ([]logs.LogEntry, error)
	build   func(context.Context, string, string, time.Time, time.Time, int, int32, *logs.LogLevel, sandboxlogs.SortOrder) ([]logs.LogEntry, error)
}

func (s *stubClickhouseLogsReader) QuerySandboxLogs(ctx context.Context, teamID uuid.UUID, sandboxID string, start, end time.Time, limit int, order sandboxlogs.SortOrder, level *logs.LogLevel, search *string) ([]logs.LogEntry, error) {
	return s.sandbox(ctx, teamID, sandboxID, start, end, limit, order, level, search)
}

func (s *stubClickhouseLogsReader) QueryBuildLogs(ctx context.Context, templateID, buildID string, start, end time.Time, limit int, offset int32, level *logs.LogLevel, order sandboxlogs.SortOrder) ([]logs.LogEntry, error) {
	return s.build(ctx, templateID, buildID, start, end, limit, offset, level, order)
}

type stubLokiLogsReader struct {
	sandbox func(context.Context, string, string, time.Time, time.Time, int, logproto.Direction, *logs.LogLevel, *string) ([]logs.LogEntry, error)
	build   func(context.Context, string, string, time.Time, time.Time, int, int32, *logs.LogLevel, logproto.Direction) ([]logs.LogEntry, error)
}

func (s *stubLokiLogsReader) QuerySandboxLogs(ctx context.Context, teamID string, sandboxID string, start, end time.Time, limit int, direction logproto.Direction, level *logs.LogLevel, search *string) ([]logs.LogEntry, error) {
	return s.sandbox(ctx, teamID, sandboxID, start, end, limit, direction, level, search)
}

func (s *stubLokiLogsReader) QueryBuildLogs(ctx context.Context, templateID, buildID string, start, end time.Time, limit int, offset int32, level *logs.LogLevel, direction logproto.Direction) ([]logs.LogEntry, error) {
	return s.build(ctx, templateID, buildID, start, end, limit, offset, level, direction)
}

func newLogsReadFeatureFlags(t *testing.T, enabled bool) *featureflags.Client {
	t.Helper()

	source := ldtestdata.DataSource()
	source.Update(source.Flag(featureflags.LogsReadConfigFlag.Key()).VariationForAll(enabled))
	client, err := featureflags.NewClientWithDatasource(source)
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, client.Close(context.WithoutCancel(t.Context())))
	})

	return client
}

func assertPersistentLogsUnavailable(t *testing.T, apiErr *api.APIError) {
	t.Helper()
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusServiceUnavailable, apiErr.Code)
	assert.Equal(t, "Persistent logs are temporarily unavailable", apiErr.ClientMsg)
	assert.ErrorIs(t, apiErr.Err, errPersistentLogsUnavailable)
}

func TestLocalLogsUseClickhouseWhenFlagEnabledWithoutLoki(t *testing.T) {
	t.Parallel()

	sandboxEntry := logs.LogEntry{Raw: "sandbox from ClickHouse", Message: "sandbox from ClickHouse"}
	buildEntry := logs.LogEntry{Raw: "build from ClickHouse", Message: "build from ClickHouse"}
	provider := &LocalClusterResourceProvider{
		sandboxLogsReader: &stubClickhouseLogsReader{
			sandbox: func(_ context.Context, _ uuid.UUID, _ string, _ time.Time, _ time.Time, _ int, _ sandboxlogs.SortOrder, _ *logs.LogLevel, _ *string) ([]logs.LogEntry, error) {
				return []logs.LogEntry{sandboxEntry}, nil
			},
			build: func(_ context.Context, _, _ string, _, _ time.Time, _ int, _ int32, _ *logs.LogLevel, _ sandboxlogs.SortOrder) ([]logs.LogEntry, error) {
				return []logs.LogEntry{buildEntry}, nil
			},
		},
		featureFlags: newLogsReadFeatureFlags(t, true),
	}

	sandboxResult, sandboxErr := provider.GetSandboxLogs(t.Context(), uuid.NewString(), "sandbox-id", nil, nil, nil, nil, nil, nil)
	require.Nil(t, sandboxErr)
	require.Len(t, sandboxResult.LogEntries, 1)
	assert.Equal(t, sandboxEntry.Message, sandboxResult.LogEntries[0].Message)

	source := api.LogsSourcePersistent
	buildResult, buildErr := provider.GetBuildLogs(t.Context(), nil, "template-id", "build-id", 0, 100, nil, nil, api.LogsDirectionForward, &source)
	require.Nil(t, buildErr)
	assert.Equal(t, []logs.LogEntry{buildEntry}, buildResult)
}

func TestLocalLogsWithoutLokiReturnUnavailableWhenFlagDisabled(t *testing.T) {
	t.Parallel()

	provider := &LocalClusterResourceProvider{
		sandboxLogsReader: &stubClickhouseLogsReader{
			sandbox: func(_ context.Context, _ uuid.UUID, _ string, _ time.Time, _ time.Time, _ int, _ sandboxlogs.SortOrder, _ *logs.LogLevel, _ *string) ([]logs.LogEntry, error) {
				t.Fatal("ClickHouse must not be used while logs-read-config is disabled")

				return nil, nil
			},
			build: func(_ context.Context, _, _ string, _, _ time.Time, _ int, _ int32, _ *logs.LogLevel, _ sandboxlogs.SortOrder) ([]logs.LogEntry, error) {
				t.Fatal("ClickHouse must not be used while logs-read-config is disabled")

				return nil, nil
			},
		},
		featureFlags: newLogsReadFeatureFlags(t, false),
	}

	_, sandboxErr := provider.GetSandboxLogs(t.Context(), uuid.NewString(), "sandbox-id", nil, nil, nil, nil, nil, nil)
	assertPersistentLogsUnavailable(t, sandboxErr)

	source := api.LogsSourcePersistent
	_, buildErr := provider.GetBuildLogs(t.Context(), nil, "template-id", "build-id", 0, 100, nil, nil, api.LogsDirectionForward, &source)
	assertPersistentLogsUnavailable(t, buildErr)
}

func TestLocalLogsWithoutAnyBackendReturnUnavailableWhenClickhouseFlagEnabled(t *testing.T) {
	t.Parallel()

	provider := &LocalClusterResourceProvider{featureFlags: newLogsReadFeatureFlags(t, true)}

	_, sandboxErr := provider.GetSandboxLogs(t.Context(), uuid.NewString(), "sandbox-id", nil, nil, nil, nil, nil, nil)
	assertPersistentLogsUnavailable(t, sandboxErr)

	source := api.LogsSourcePersistent
	_, buildErr := provider.GetBuildLogs(t.Context(), nil, "template-id", "build-id", 0, 100, nil, nil, api.LogsDirectionForward, &source)
	assertPersistentLogsUnavailable(t, buildErr)
}

func TestLocalLogsPreserveLokiFallback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                  string
		clickhouseFlagEnabled bool
		withClickhouseReader  bool
	}{
		{name: "flag disabled", clickhouseFlagEnabled: false, withClickhouseReader: true},
		{name: "flag enabled without ClickHouse reader", clickhouseFlagEnabled: true, withClickhouseReader: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sandboxEntry := logs.LogEntry{Raw: "sandbox from Loki", Message: "sandbox from Loki"}
			buildEntry := logs.LogEntry{Raw: "build from Loki", Message: "build from Loki"}
			provider := &LocalClusterResourceProvider{
				queryLogsProvider: &stubLokiLogsReader{
					sandbox: func(_ context.Context, _, _ string, _, _ time.Time, _ int, _ logproto.Direction, _ *logs.LogLevel, _ *string) ([]logs.LogEntry, error) {
						return []logs.LogEntry{sandboxEntry}, nil
					},
					build: func(_ context.Context, _, _ string, _, _ time.Time, _ int, _ int32, _ *logs.LogLevel, _ logproto.Direction) ([]logs.LogEntry, error) {
						return []logs.LogEntry{buildEntry}, nil
					},
				},
				featureFlags: newLogsReadFeatureFlags(t, tt.clickhouseFlagEnabled),
			}
			if tt.withClickhouseReader {
				provider.sandboxLogsReader = &stubClickhouseLogsReader{
					sandbox: func(_ context.Context, _ uuid.UUID, _ string, _ time.Time, _ time.Time, _ int, _ sandboxlogs.SortOrder, _ *logs.LogLevel, _ *string) ([]logs.LogEntry, error) {
						t.Fatal("ClickHouse must not bypass logs-read-config")

						return nil, nil
					},
					build: func(_ context.Context, _, _ string, _, _ time.Time, _ int, _ int32, _ *logs.LogLevel, _ sandboxlogs.SortOrder) ([]logs.LogEntry, error) {
						t.Fatal("ClickHouse must not bypass logs-read-config")

						return nil, nil
					},
				}
			}

			sandboxResult, sandboxErr := provider.GetSandboxLogs(t.Context(), uuid.NewString(), "sandbox-id", nil, nil, nil, nil, nil, nil)
			require.Nil(t, sandboxErr)
			require.Len(t, sandboxResult.LogEntries, 1)
			assert.Equal(t, sandboxEntry.Message, sandboxResult.LogEntries[0].Message)

			source := api.LogsSourcePersistent
			buildResult, buildErr := provider.GetBuildLogs(t.Context(), nil, "template-id", "build-id", 0, 100, nil, nil, api.LogsDirectionForward, &source)
			require.Nil(t, buildErr)
			assert.Equal(t, []logs.LogEntry{buildEntry}, buildResult)
		})
	}
}

func TestClickhouseLogReadErrorsAreCountedByKind(t *testing.T) {
	t.Parallel()

	clickhouseErr := errors.New("clickhouse unavailable")
	provider := &LocalClusterResourceProvider{
		sandboxLogsReader: &stubClickhouseLogsReader{
			sandbox: func(_ context.Context, _ uuid.UUID, _ string, _ time.Time, _ time.Time, _ int, _ sandboxlogs.SortOrder, _ *logs.LogLevel, _ *string) ([]logs.LogEntry, error) {
				return nil, clickhouseErr
			},
			build: func(_ context.Context, _, _ string, _, _ time.Time, _ int, _ int32, _ *logs.LogLevel, _ sandboxlogs.SortOrder) ([]logs.LogEntry, error) {
				return nil, clickhouseErr
			},
		},
		featureFlags: newLogsReadFeatureFlags(t, true),
	}

	_, sandboxErr := provider.GetSandboxLogs(t.Context(), uuid.NewString(), "sandbox-id", nil, nil, nil, nil, nil, nil)
	require.NotNil(t, sandboxErr)
	require.ErrorIs(t, sandboxErr.Err, clickhouseErr)

	_, buildErr := provider.logsFromClickhouse(t.Context(), "template-id", "build-id", time.Now().Add(-time.Minute), time.Now(), 100, 0, nil, sandboxlogs.SortOrderForward)()
	require.NotNil(t, buildErr)
	require.ErrorIs(t, buildErr.Err, clickhouseErr)

	var metrics metricdata.ResourceMetrics
	require.NoError(t, testLogReadMetricReader.Collect(t.Context(), &metrics))

	want := []attribute.Set{
		attribute.NewSet(attribute.String("kind", "sandbox")),
		attribute.NewSet(attribute.String("kind", "build")),
	}
	found := make([]bool, len(want))
	for _, scope := range metrics.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != "log_read_clickhouse_error_count" {
				continue
			}

			sum, ok := m.Data.(metricdata.Sum[int64])
			require.True(t, ok)
			for _, point := range sum.DataPoints {
				for i, attributes := range want {
					if point.Value > 0 && point.Attributes.Equals(&attributes) {
						found[i] = true
					}
				}
			}
		}
	}
	for i, attributes := range want {
		assert.True(t, found[i], "no error count with attributes %v", attributes.Encoded(attribute.DefaultEncoder()))
	}
}

// TestBuildLogsFromClickhouseFailsFast asserts that a ClickHouse read error
// during GetBuildLogs is propagated directly (no automatic Loki fallback).
// The migration relies on the logs-read-config flag plus alerting on
// log_read_clickhouse_error_count to drive rollback decisions, rather than
// per-request fallback masking a degraded ClickHouse.
func TestBuildLogsFromClickhouseFailsFast(t *testing.T) {
	t.Parallel()

	clickhouseErr := errors.New("clickhouse unavailable")
	start, end := time.Now().Add(-time.Minute), time.Now()
	level := logs.LevelInfo
	want := []logs.LogEntry{{Message: "from ClickHouse"}}

	tests := []struct {
		name             string
		clickhouseResult []logs.LogEntry
		clickhouseErr    error
		want             []logs.LogEntry
		wantErr          bool
	}{
		{name: "success returns ClickHouse result", clickhouseResult: want, want: want},
		{name: "ClickHouse error propagates without fallback", clickhouseErr: clickhouseErr, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			provider := &LocalClusterResourceProvider{
				sandboxLogsReader: &stubClickhouseLogsReader{build: func(_ context.Context, templateID, buildID string, gotStart, gotEnd time.Time, limit int, offset int32, gotLevel *logs.LogLevel, order sandboxlogs.SortOrder) ([]logs.LogEntry, error) {
					assert.Equal(t, "template-id", templateID)
					assert.Equal(t, "build-id", buildID)
					assert.Equal(t, start, gotStart)
					assert.Equal(t, end, gotEnd)
					assert.Equal(t, 42, limit)
					assert.Equal(t, int32(7), offset)
					assert.Same(t, &level, gotLevel)
					assert.Equal(t, sandboxlogs.SortOrderBackward, order)

					return tt.clickhouseResult, tt.clickhouseErr
				}},
			}

			got, apiErr := provider.logsFromClickhouse(t.Context(), "template-id", "build-id", start, end, 42, 7, &level, sandboxlogs.SortOrderBackward)()
			if tt.wantErr {
				require.NotNil(t, apiErr)
				require.ErrorIs(t, apiErr.Err, clickhouseErr)

				return
			}
			require.Nil(t, apiErr)
			assert.Equal(t, tt.want, got)
		})
	}
}
