package clusters

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	templatemanagergrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

type mockTemplateServiceClient struct {
	response  *templatemanagergrpc.TemplateBuildStatusResponse
	callCount int
}

func (m *mockTemplateServiceClient) TemplateCreate(_ context.Context, _ *templatemanagergrpc.TemplateCreateRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}

func (m *mockTemplateServiceClient) TemplateBuildStatus(_ context.Context, _ *templatemanagergrpc.TemplateStatusRequest, _ ...grpc.CallOption) (*templatemanagergrpc.TemplateBuildStatusResponse, error) {
	m.callCount++

	return m.response, nil
}

func (m *mockTemplateServiceClient) TemplateBuildDelete(_ context.Context, _ *templatemanagergrpc.TemplateBuildDeleteRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}

func (m *mockTemplateServiceClient) InitLayerFileUpload(_ context.Context, _ *templatemanagergrpc.InitLayerFileUploadRequest, _ ...grpc.CallOption) (*templatemanagergrpc.InitLayerFileUploadResponse, error) {
	return &templatemanagergrpc.InitLayerFileUploadResponse{}, nil
}

func TestGetBuildLogsWithSources_ParityBetweenTemporaryAndPersistent(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	testCases := []struct {
		name      string
		direction api.LogsDirection
		expected  []logs.LogEntry
	}{
		{
			name:      "forward",
			direction: api.LogsDirectionForward,
			expected: []logs.LogEntry{
				{Timestamp: now.Add(-3 * time.Second), Message: "first", Level: logs.LevelInfo},
				{Timestamp: now.Add(-2 * time.Second), Message: "same-a", Level: logs.LevelInfo},
				{Timestamp: now.Add(-2 * time.Second), Message: "same-b", Level: logs.LevelInfo},
				{Timestamp: now.Add(-1 * time.Second), Message: "last", Level: logs.LevelInfo},
			},
		},
		{
			name:      "backward",
			direction: api.LogsDirectionBackward,
			expected: []logs.LogEntry{
				{Timestamp: now.Add(-1 * time.Second), Message: "last", Level: logs.LevelInfo},
				{Timestamp: now.Add(-2 * time.Second), Message: "same-b", Level: logs.LevelInfo},
				{Timestamp: now.Add(-2 * time.Second), Message: "same-a", Level: logs.LevelInfo},
				{Timestamp: now.Add(-3 * time.Second), Message: "first", Level: logs.LevelInfo},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			nodeID := "node-id"
			templateID := "template-id"
			buildID := "build-id"

			templateClient := &mockTemplateServiceClient{
				response: &templatemanagergrpc.TemplateBuildStatusResponse{
					LogEntries: toTemplateLogEntries(tc.expected),
				},
			}

			instances := smap.New[*Instance]()
			instances.Insert(nodeID, &Instance{
				NodeID: nodeID,
				client: &GRPCClient{
					Template: templateClient,
				},
			})

			persistentCalls := 0
			persistentFetcher := func() ([]logs.LogEntry, *api.APIError) {
				persistentCalls++
				entries := make([]logs.LogEntry, len(tc.expected))
				copy(entries, tc.expected)

				return entries, nil
			}

			temporarySource := api.LogsSourceTemporary
			fromTemporary, temporaryErr := getBuildLogsWithSources(
				t.Context(),
				instances,
				&nodeID,
				templateID,
				buildID,
				0,
				100,
				nil,
				nil,
				tc.direction,
				&temporarySource,
				persistentFetcher,
			)
			require.Nil(t, temporaryErr)
			assert.Equal(t, tc.expected, fromTemporary)
			assert.Equal(t, 1, templateClient.callCount)
			assert.Equal(t, 0, persistentCalls)

			templateClient.callCount = 0
			persistentCalls = 0

			persistentSource := api.LogsSourcePersistent
			fromPersistent, persistentErr := getBuildLogsWithSources(
				t.Context(),
				instances,
				&nodeID,
				templateID,
				buildID,
				0,
				100,
				nil,
				nil,
				tc.direction,
				&persistentSource,
				persistentFetcher,
			)
			require.Nil(t, persistentErr)
			assert.Equal(t, tc.expected, fromPersistent)
			assert.Equal(t, 0, templateClient.callCount)
			assert.Equal(t, 1, persistentCalls)

			assert.Equal(t, fromTemporary, fromPersistent)
		})
	}
}

func toTemplateLogEntries(entries []logs.LogEntry) []*templatemanagergrpc.TemplateBuildLogEntry {
	result := make([]*templatemanagergrpc.TemplateBuildLogEntry, len(entries))
	for i, entry := range entries {
		result[i] = &templatemanagergrpc.TemplateBuildLogEntry{
			Timestamp: timestamppb.New(entry.Timestamp),
			Message:   entry.Message,
			Level:     templatemanagergrpc.LogLevel(entry.Level),
			Fields:    entry.Fields,
		}
	}

	return result
}
