package loki

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/grafana/loki/v3/pkg/loghttp"
	"github.com/grafana/loki/v3/pkg/logproto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

			entries, err := ResponseMapper(t.Context(), res, 0, nil, tc.direction)
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

func toLogLine(t *testing.T, message string) string {
	t.Helper()

	raw, err := json.Marshal(map[string]string{
		"message": message,
		"level":   "info",
	})
	require.NoError(t, err)

	return string(raw)
}
