package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/buildlogger"
	templatecache "github.com/e2b-dev/infra/packages/orchestrator/internal/template/cache"
	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

type testLogLine struct {
	ts      float64
	message string
	level   string
}

func TestTemplateBuildStatus_DirectionOrdering(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	sameTimestamp := now.Add(-2 * time.Second)
	lines := []testLogLine{
		{ts: timeToEpoch(now.Add(-1 * time.Second)), message: "last", level: "info"},
		{ts: timeToEpoch(now.Add(-3 * time.Second)), message: "first", level: "info"},
		{ts: timeToEpoch(sameTimestamp), message: "same-a", level: "info"},
		{ts: timeToEpoch(sameTimestamp), message: "same-b", level: "info"},
	}

	serverStore, buildID := newTestServerStore(t, lines)

	testCases := []struct {
		name      string
		direction template_manager.LogsDirection
		expected  []string
	}{
		{
			name:      "forward_sorts_by_timestamp_and_keeps_equal_timestamps_stable",
			direction: template_manager.LogsDirection_Forward,
			expected:  []string{"first", "same-a", "same-b", "last"},
		},
		{
			name:      "backward_sorts_descending_and_keeps_equal_timestamps_stable",
			direction: template_manager.LogsDirection_Backward,
			expected:  []string{"last", "same-a", "same-b", "first"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			response, err := serverStore.TemplateBuildStatus(t.Context(), &template_manager.TemplateStatusRequest{
				BuildID:    buildID,
				TemplateID: "template-id",
				Start:      timestamppb.New(now.Add(-1 * time.Hour)),
				End:        timestamppb.New(now.Add(1 * time.Hour)),
				Direction:  &tc.direction,
			})
			require.NoError(t, err)

			actualEntries := response.GetLogEntries()
			require.Len(t, actualEntries, len(lines))

			actualMessages := make([]string, len(actualEntries))
			for i, entry := range actualEntries {
				actualMessages[i] = entry.GetMessage()
			}

			assert.Equal(t, tc.expected, actualMessages)
		})
	}
}

func newTestServerStore(t *testing.T, logLines []testLogLine) (*ServerStore, string) {
	t.Helper()

	buildLogs := buildlogger.NewLogEntryLogger()
	writeTestBuildLogs(t, buildLogs, logLines)

	buildCache := templatecache.NewBuildCache(t.Context(), noop.NewMeterProvider())
	buildID := uuid.NewString()

	_, err := buildCache.Create("team-id", buildID, buildLogs)
	require.NoError(t, err)

	return &ServerStore{
		buildCache: buildCache,
	}, buildID
}

func writeTestBuildLogs(t *testing.T, buildLogs *buildlogger.LogEntryLogger, lines []testLogLine) {
	t.Helper()

	var input strings.Builder
	for _, line := range lines {
		payload, err := json.Marshal(map[string]any{
			"ts":    line.ts,
			"msg":   line.message,
			"level": line.level,
		})
		require.NoError(t, err)

		input.Write(payload)
		input.WriteByte('\n')
	}

	_, err := buildLogs.Write([]byte(input.String()))
	require.NoError(t, err)
}

func timeToEpoch(t time.Time) float64 {
	return float64(t.UnixNano()) / float64(time.Second)
}
