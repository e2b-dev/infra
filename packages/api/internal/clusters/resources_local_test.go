package clusters

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/clickhouse/pkg/sandboxlogs"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
)

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
