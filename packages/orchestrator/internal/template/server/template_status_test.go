package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grafana/loki/v3/pkg/loghttp"
	"github.com/grafana/loki/v3/pkg/logproto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/buildlogger"
	templatecache "github.com/e2b-dev/infra/packages/orchestrator/internal/template/cache"
	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	sharedloki "github.com/e2b-dev/infra/packages/shared/pkg/logs/loki"
)

type testLogLine struct {
	ts      float64
	message string
	level   string
}

func TestTemplateBuildStatus_OrderingParityWithPersistentMapper(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	lines := []testLogLine{
		{ts: timeToEpoch(now.Add(-1 * time.Second)), message: "third", level: "info"},
		{ts: timeToEpoch(now.Add(-4 * time.Second)), message: "first", level: "info"},
		{ts: timeToEpoch(now.Add(-2 * time.Second)), message: "second", level: "info"},
	}

	serverStore, buildID := newTestServerStore(t, lines)

	testCases := []struct {
		name      string
		direction template_manager.LogsDirection
	}{
		{name: "forward", direction: template_manager.LogsDirection_Forward},
		{name: "backward", direction: template_manager.LogsDirection_Backward},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			response, err := serverStore.TemplateBuildStatus(context.Background(), &template_manager.TemplateStatusRequest{
				BuildID:    buildID,
				TemplateID: "template-id",
				Start:      timestamppb.New(now.Add(-1 * time.Hour)),
				End:        timestamppb.New(now.Add(1 * time.Hour)),
				Direction:  &tc.direction,
			})
			require.NoError(t, err)

			actualEntries := response.GetLogEntries()
			require.Len(t, actualEntries, len(lines))

			expectedMessages, err := persistentMapperOrder(lines, tc.direction)
			require.NoError(t, err)

			actualMessages := make([]string, len(actualEntries))
			for i, entry := range actualEntries {
				actualMessages[i] = entry.GetMessage()
			}

			assert.Equal(t, expectedMessages, actualMessages)
		})
	}
}

func newTestServerStore(t *testing.T, logLines []testLogLine) (*ServerStore, string) {
	t.Helper()

	buildLogs := buildlogger.NewLogEntryLogger()
	writeTestBuildLogs(t, buildLogs, logLines)

	buildCache := templatecache.NewBuildCache(context.Background(), noop.NewMeterProvider())
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

func persistentMapperOrder(lines []testLogLine, direction template_manager.LogsDirection) ([]string, error) {
	entries := make([]loghttp.Entry, 0, len(lines))
	for _, line := range lines {
		raw, err := json.Marshal(map[string]any{
			"message": line.message,
			"level":   line.level,
		})
		if err != nil {
			return nil, err
		}

		entries = append(entries, loghttp.Entry{
			Timestamp: epochToTime(line.ts),
			Line:      string(raw),
		})
	}

	res := &loghttp.QueryResponse{
		Data: loghttp.QueryResponseData{
			ResultType: loghttp.ResultTypeStream,
			Result: loghttp.Streams{
				{
					Entries: entries,
				},
			},
		},
	}

	lokiDirection := logproto.FORWARD
	if direction == template_manager.LogsDirection_Backward {
		lokiDirection = logproto.BACKWARD
	}

	mapped, err := sharedloki.ResponseMapper(context.Background(), res, 0, nil, lokiDirection)
	if err != nil {
		return nil, err
	}

	messages := make([]string, len(mapped))
	for i, entry := range mapped {
		messages[i] = entry.Message
	}

	return messages, nil
}

func timeToEpoch(t time.Time) float64 {
	return float64(t.UnixNano()) / float64(time.Second)
}

func epochToTime(epoch float64) time.Time {
	sec := int64(epoch)
	nsec := int64((epoch - float64(sec)) * 1e9)

	return time.Unix(sec, nsec).UTC()
}
