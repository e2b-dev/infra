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

func TestTemplateBuildStatus_BackwardDirectionReturnsTimestampSortedDescendingLogs(t *testing.T) {
	now := time.Now().UTC()
	serverStore, buildID := newTestServerStore(t, []testLogLine{
		{ts: timeToEpoch(now.Add(-1 * time.Second)), message: "third", level: "info"},
		{ts: timeToEpoch(now.Add(-3 * time.Second)), message: "first", level: "info"},
		{ts: timeToEpoch(now.Add(-2 * time.Second)), message: "second", level: "info"},
	})

	limit := uint32(2)
	direction := template_manager.LogsDirection_Backward

	response, err := serverStore.TemplateBuildStatus(context.Background(), &template_manager.TemplateStatusRequest{
		BuildID:    buildID,
		TemplateID: "template-id",
		Start:      timestamppb.New(now.Add(-1 * time.Hour)),
		End:        timestamppb.New(now.Add(1 * time.Hour)),
		Limit:      &limit,
		Direction:  &direction,
	})
	require.NoError(t, err)
	require.Len(t, response.LogEntries, 2)

	assert.Equal(t, []string{"third", "second"}, []string{response.LogEntries[0].GetMessage(), response.LogEntries[1].GetMessage()})
}

func TestTemplateBuildStatus_ForwardDirectionKeepsAscendingOrderWithOffset(t *testing.T) {
	now := time.Now().UTC()
	serverStore, buildID := newTestServerStore(t, []testLogLine{
		{ts: timeToEpoch(now.Add(-3 * time.Second)), message: "first", level: "info"},
		{ts: timeToEpoch(now.Add(-2 * time.Second)), message: "second", level: "info"},
		{ts: timeToEpoch(now.Add(-1 * time.Second)), message: "third", level: "info"},
	})

	offset := int32(1)
	limit := uint32(2)
	direction := template_manager.LogsDirection_Forward

	response, err := serverStore.TemplateBuildStatus(context.Background(), &template_manager.TemplateStatusRequest{
		BuildID:    buildID,
		TemplateID: "template-id",
		Start:      timestamppb.New(now.Add(-1 * time.Hour)),
		End:        timestamppb.New(now.Add(1 * time.Hour)),
		Offset:     &offset,
		Limit:      &limit,
		Direction:  &direction,
	})
	require.NoError(t, err)
	require.Len(t, response.LogEntries, 2)

	assert.Equal(t, []string{"second", "third"}, []string{response.LogEntries[0].GetMessage(), response.LogEntries[1].GetMessage()})
}

func TestTemplateBuildStatus_ForwardDirectionPreservesOrderForSameTimestamp(t *testing.T) {
	now := time.Now().UTC()
	sameTimestamp := timeToEpoch(now.Add(-1 * time.Second))
	serverStore, buildID := newTestServerStore(t, []testLogLine{
		{ts: timeToEpoch(now.Add(-2 * time.Second)), message: "older", level: "info"},
		{ts: sameTimestamp, message: "same-1", level: "info"},
		{ts: sameTimestamp, message: "same-2", level: "info"},
	})

	direction := template_manager.LogsDirection_Forward

	response, err := serverStore.TemplateBuildStatus(context.Background(), &template_manager.TemplateStatusRequest{
		BuildID:    buildID,
		TemplateID: "template-id",
		Start:      timestamppb.New(now.Add(-1 * time.Hour)),
		End:        timestamppb.New(now.Add(1 * time.Hour)),
		Direction:  &direction,
	})
	require.NoError(t, err)
	require.Len(t, response.LogEntries, 3)

	assert.Equal(t, []string{"older", "same-1", "same-2"}, []string{response.LogEntries[0].GetMessage(), response.LogEntries[1].GetMessage(), response.LogEntries[2].GetMessage()})
}

func TestTemplateBuildStatus_OrderingParityWithPersistentRules(t *testing.T) {
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
			response, err := serverStore.TemplateBuildStatus(context.Background(), &template_manager.TemplateStatusRequest{
				BuildID:    buildID,
				TemplateID: "template-id",
				Start:      timestamppb.New(now.Add(-1 * time.Hour)),
				End:        timestamppb.New(now.Add(1 * time.Hour)),
				Direction:  &tc.direction,
			})
			require.NoError(t, err)
			require.Len(t, response.LogEntries, len(lines))

			expected, err := persistentMapperOrder(lines, tc.direction)
			require.NoError(t, err)
			actual := make([]string, len(response.LogEntries))
			for i, entry := range response.LogEntries {
				actual[i] = entry.GetMessage()
			}

			assert.Equal(t, expected, actual)
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

func timeToEpoch(t time.Time) float64 {
	return float64(t.UnixNano()) / float64(time.Second)
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

func epochToTime(epoch float64) time.Time {
	sec := int64(epoch)
	nsec := int64((epoch - float64(sec)) * 1e9)

	return time.Unix(sec, nsec).UTC()
}
