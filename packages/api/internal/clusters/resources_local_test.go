package clusters

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/cfg"
	"github.com/e2b-dev/infra/packages/clickhouse/pkg/sandboxlogs"
	templatemanagergrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs/loki"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

type stubClickhouseLogsReader struct {
	sandbox func(context.Context, uuid.UUID, string, time.Time, time.Time, int, sandboxlogs.SortOrder, *logs.LogLevel, *string) ([]logs.LogEntry, error)
	build   func(context.Context, string, string, time.Time, time.Time, int, int32, *logs.LogLevel, sandboxlogs.SortOrder) ([]logs.LogEntry, error)
}

func newLokiSpy(t *testing.T, calls *atomic.Int32) *loki.LokiQueryProvider {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	provider, err := loki.NewLokiQueryProvider(server.URL, "", "")
	require.NoError(t, err)

	return provider
}

func TestLogSelectorsUseOnlySelectedBackend(t *testing.T) {
	t.Parallel()
	clickhouseEntry := []logs.LogEntry{{Message: "clickhouse"}}
	clickhouseErr := errors.New("clickhouse unavailable")

	tests := []struct {
		name             string
		enabled          bool
		reader           ClickhouseLogsReader
		clickhouseResult []logs.LogEntry
		clickhouseErr    error
		wantClickhouse   bool
		wantErr          bool
	}{
		{name: "true uses ClickHouse", enabled: true, reader: &stubClickhouseLogsReader{sandbox: func(context.Context, uuid.UUID, string, time.Time, time.Time, int, sandboxlogs.SortOrder, *logs.LogLevel, *string) ([]logs.LogEntry, error) {
			return clickhouseEntry, nil
		}, build: func(context.Context, string, string, time.Time, time.Time, int, int32, *logs.LogLevel, sandboxlogs.SortOrder) ([]logs.LogEntry, error) {
			return clickhouseEntry, nil
		}}, wantClickhouse: true},
		{name: "false uses Loki", enabled: false, reader: &stubClickhouseLogsReader{sandbox: func(context.Context, uuid.UUID, string, time.Time, time.Time, int, sandboxlogs.SortOrder, *logs.LogLevel, *string) ([]logs.LogEntry, error) {
			t.Fatal("ClickHouse sandbox reader called")

			return nil, nil
		}, build: func(context.Context, string, string, time.Time, time.Time, int, int32, *logs.LogLevel, sandboxlogs.SortOrder) ([]logs.LogEntry, error) {
			t.Fatal("ClickHouse build reader called")

			return nil, nil
		}}, wantClickhouse: false},
		{name: "missing reader fails", enabled: true, wantClickhouse: true, wantErr: true},
		{name: "ClickHouse error fails", enabled: true, reader: &stubClickhouseLogsReader{sandbox: func(context.Context, uuid.UUID, string, time.Time, time.Time, int, sandboxlogs.SortOrder, *logs.LogLevel, *string) ([]logs.LogEntry, error) {
			return nil, clickhouseErr
		}, build: func(context.Context, string, string, time.Time, time.Time, int, int32, *logs.LogLevel, sandboxlogs.SortOrder) ([]logs.LogEntry, error) {
			return nil, clickhouseErr
		}}, wantClickhouse: true, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var lokiCalls atomic.Int32
			provider := &LocalClusterResourceProvider{config: cfg.Config{ClickhouseLogsReadEnabled: tt.enabled}, queryLogsProvider: newLokiSpy(t, &lokiCalls), sandboxLogsReader: tt.reader, instances: smap.New[*Instance]()}
			teamID := uuid.NewString()
			_, sandboxErr := provider.GetSandboxLogs(t.Context(), teamID, "sandbox", nil, nil, nil, nil, nil, nil)
			_, buildErr := provider.GetBuildLogs(t.Context(), nil, "template", "build", 0, 10, nil, nil, api.LogsDirectionForward, nil)
			if tt.wantErr {
				require.NotNil(t, sandboxErr)
				require.NotNil(t, buildErr)
			} else {
				require.Nil(t, sandboxErr)
				require.Nil(t, buildErr)
			}
			if tt.wantClickhouse {
				assert.Equal(t, int32(0), lokiCalls.Load())
			}
			if !tt.wantClickhouse {
				assert.Positive(t, lokiCalls.Load())
			}
		})
	}
}

func TestGetBuildLogsDoesNotRequireClickHouseForTemporarySource(t *testing.T) {
	t.Parallel()

	nodeID := "node-id"
	instances := smap.New[*Instance]()
	instances.Insert(nodeID, &Instance{
		NodeID: nodeID,
		client: &GRPCClient{
			Template: &mockTemplateServiceClient{response: &templatemanagergrpc.TemplateBuildStatusResponse{
				LogEntries: []*templatemanagergrpc.TemplateBuildLogEntry{{Message: "temporary"}},
			}},
		},
	})

	provider := &LocalClusterResourceProvider{
		config:            cfg.Config{ClickhouseLogsReadEnabled: true},
		instances:         instances,
		sandboxLogsReader: nil,
	}
	temporary := api.LogsSourceTemporary
	entries, apiErr := provider.GetBuildLogs(t.Context(), &nodeID, "template", "build", 0, 10, nil, nil, api.LogsDirectionForward, &temporary)
	require.Nil(t, apiErr)
	require.Len(t, entries, 1)
	assert.Equal(t, "temporary", entries[0].Message)

	persistent := api.LogsSourcePersistent
	_, apiErr = provider.GetBuildLogs(t.Context(), nil, "template", "build", 0, 10, nil, nil, api.LogsDirectionForward, &persistent)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusInternalServerError, apiErr.Code)
}

func (s *stubClickhouseLogsReader) QuerySandboxLogs(ctx context.Context, teamID uuid.UUID, sandboxID string, start, end time.Time, limit int, order sandboxlogs.SortOrder, level *logs.LogLevel, search *string) ([]logs.LogEntry, error) {
	return s.sandbox(ctx, teamID, sandboxID, start, end, limit, order, level, search)
}

func (s *stubClickhouseLogsReader) QueryBuildLogs(ctx context.Context, templateID, buildID string, start, end time.Time, limit int, offset int32, level *logs.LogLevel, order sandboxlogs.SortOrder) ([]logs.LogEntry, error) {
	return s.build(ctx, templateID, buildID, start, end, limit, offset, level, order)
}

// TestBuildLogsFromClickhouseFailsFast asserts that a ClickHouse read error
// during GetBuildLogs is propagated directly (no automatic Loki fallback).
// ClickHouse errors are intentionally surfaced rather than masked by a Loki fallback.
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
