package loki

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/grafana/loki/v3/pkg/loghttp"
	"github.com/grafana/loki/v3/pkg/logproto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
)

func TestResponseMapper_DirectionOrdering(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	res := &loghttp.QueryResponse{
		Data: loghttp.QueryResponseData{
			ResultType: loghttp.ResultTypeStream,
			Result: loghttp.Streams{
				{
					Entries: []loghttp.Entry{
						{Timestamp: now.Add(-1 * time.Second), Line: toLogLine(t, "third")},
						{Timestamp: now.Add(-4 * time.Second), Line: toLogLine(t, "first")},
						{Timestamp: now.Add(-2 * time.Second), Line: toLogLine(t, "second")},
					},
				},
			},
		},
	}

	testCases := []struct {
		name      string
		direction logproto.Direction
		expected  []string
	}{
		{
			name:      "forward",
			direction: logproto.FORWARD,
			expected:  []string{"first", "second", "third"},
		},
		{
			name:      "backward",
			direction: logproto.BACKWARD,
			expected:  []string{"third", "second", "first"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			entries, err := ResponseMapper(t.Context(), res, 0, tc.direction)
			require.NoError(t, err)
			require.Len(t, entries, 3)

			actualMessages := make([]string, len(entries))
			for i, entry := range entries {
				actualMessages[i] = entry.Message
			}

			assert.Equal(t, tc.expected, actualMessages)
		})
	}
}

func TestResponseMapper_UsesEmbeddedDataLogFields(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	embeddedData := `{"severity":"ERROR","logger":"app","message":"database unavailable","request_id":"req_123"}`
	res := &loghttp.QueryResponse{
		Data: loghttp.QueryResponseData{
			ResultType: loghttp.ResultTypeStream,
			Result: loghttp.Streams{
				{
					Entries: []loghttp.Entry{
						{
							Timestamp: now,
							Line:      toRawLogLine(t, map[string]any{"message": "Streaming process event", "level": "info", "logger": "process", "data": embeddedData}),
						},
					},
				},
			},
		},
	}

	entries, err := ResponseMapper(t.Context(), res, 0, logproto.FORWARD)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	entry := entries[0]
	assert.Equal(t, "database unavailable", entry.Message)
	assert.Equal(t, logs.LevelError, entry.Level)
	assert.Equal(t, "app", entry.Fields["logger"])
	assert.Equal(t, "user", entry.Fields["origin"])
	assert.Equal(t, "process", entry.Fields["captured_by_logger"])
	assert.Equal(t, "Streaming process event", entry.Fields["captured_by_message"])
	assert.Equal(t, embeddedData, entry.Fields["data"])
}

func TestResponseMapper_KeepsWrapperLoggerSeparateWhenDataHasNoLogger(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	embeddedData := `{"severity":"WARN","message":"cache warmup skipped","request_id":"req_456"}`
	res := &loghttp.QueryResponse{
		Data: loghttp.QueryResponseData{
			ResultType: loghttp.ResultTypeStream,
			Result: loghttp.Streams{
				{
					Entries: []loghttp.Entry{
						{
							Timestamp: now,
							Line:      toRawLogLine(t, map[string]any{"message": "Streaming process event", "level": "info", "logger": "process", "event_type": "stdout", "data": embeddedData}),
						},
					},
				},
			},
		},
	}

	entries, err := ResponseMapper(t.Context(), res, 0, logproto.FORWARD)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	entry := entries[0]
	assert.Equal(t, "cache warmup skipped", entry.Message)
	assert.Equal(t, logs.LevelWarn, entry.Level)
	assert.NotContains(t, entry.Fields, "logger")
	assert.Equal(t, "user", entry.Fields["origin"])
	assert.Equal(t, "process", entry.Fields["captured_by_logger"])
	assert.Equal(t, "Streaming process event", entry.Fields["captured_by_message"])
	assert.Equal(t, "stdout", entry.Fields["captured_by_event_type"])
	assert.Equal(t, embeddedData, entry.Fields["data"])
}

func toLogLine(t *testing.T, message string) string {
	t.Helper()

	return toRawLogLine(t, map[string]any{
		"message": message,
		"level":   "info",
	})
}

func toRawLogLine(t *testing.T, fields map[string]any) string {
	t.Helper()

	raw, err := json.Marshal(fields)
	require.NoError(t, err)

	return string(raw)
}
