package clusters

import (
	"context"
	"net/http"
	"time"

	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	templatemanagergrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	edgeapi "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type ClusterResource interface {
	GetSandboxMetrics(ctx context.Context, teamID string, sandboxID string, qStart *int64, qEnd *int64) ([]api.SandboxMetric, *api.APIError)
	GetSandboxesMetrics(ctx context.Context, teamID string, sandboxIDs []string) (map[string]api.SandboxMetric, *api.APIError)
	GetSandboxLogs(ctx context.Context, teamID string, sandboxID string, start *int64, limit *int32, direction *api.LogsDirection) (api.SandboxLogs, *api.APIError)
	GetBuildLogs(ctx context.Context, nodeID *string, templateID string, buildID string, offset int32, limit int32, level *logs.LogLevel, cursor *time.Time, direction api.LogsDirection, source *api.LogsSource) ([]logs.LogEntry, *api.APIError)
}

const (
	maxTimeRangeDuration = 7 * 24 * time.Hour
)

func logQueryWindow(cursor *time.Time, direction api.LogsDirection) (time.Time, time.Time) {
	start, end := time.Now().Add(-maxTimeRangeDuration), time.Now()
	if cursor == nil {
		return start, end
	}

	if direction == api.LogsDirectionForward {
		start = *cursor
		end = start.Add(maxTimeRangeDuration)
	} else {
		end = *cursor
		start = end.Add(-maxTimeRangeDuration)
	}

	return start, end
}

func logDirectionToTemplateManagerDirection(direction api.LogsDirection) templatemanagergrpc.LogsDirection {
	switch direction {
	case api.LogsDirectionForward:
		return templatemanagergrpc.LogsDirection_Forward
	case api.LogsDirectionBackward:
		return templatemanagergrpc.LogsDirection_Backward
	default:
		return templatemanagergrpc.LogsDirection_Forward
	}
}

func logToEdgeLevel(level *logs.LogLevel) *edgeapi.LogLevel {
	if level == nil {
		return nil
	}

	value := edgeapi.LogLevel(logs.LevelToString(*level))

	return &value
}

func logCheckSourceType(source *api.LogsSource, sourceType api.LogsSource) bool {
	return source == nil || *source == sourceType
}

type logSourceFunc func() ([]logs.LogEntry, *api.APIError)

func logsFromBuilderInstance(ctx context.Context, instance *Instance, templateID string, buildID string, offset int32, limit int32, level *logs.LogLevel, start time.Time, end time.Time, direction api.LogsDirection) logSourceFunc {
	return func() ([]logs.LogEntry, *api.APIError) {
		var lvlReq *templatemanagergrpc.LogLevel
		if level != nil {
			lvlReq = templatemanagergrpc.LogLevel(*level).Enum()
		}

		res, err := instance.GetClient().Template.TemplateBuildStatus(
			ctx, &templatemanagergrpc.TemplateStatusRequest{
				TemplateID: templateID,
				BuildID:    buildID,
				Offset:     &offset,
				Limit:      utils.ToPtr(uint32(limit)),
				Level:      lvlReq,
				Start:      timestamppb.New(start),
				End:        timestamppb.New(end),
				Direction:  utils.ToPtr(logDirectionToTemplateManagerDirection(direction)),
			},
		)
		if err != nil {
			return nil, &api.APIError{
				Err:       err,
				ClientMsg: "Failed to fetch build logs from builder instance",
				Code:      http.StatusInternalServerError,
			}
		}

		raw := res.GetLogEntries()
		entries := make([]logs.LogEntry, len(raw))
		for i, entry := range raw {
			entries[i] = logs.LogEntry{
				Timestamp: entry.GetTimestamp().AsTime(),
				Message:   entry.GetMessage(),
				Level:     logs.LogLevel(entry.GetLevel()),
				Fields:    entry.GetFields(),
			}
		}

		return entries, nil
	}
}

// getBuildLogsWithSources implements the shared logic for fetching build logs from multiple sources.
// This function extracts the common pattern used by both local and remote cluster resource providers,
// avoiding code duplication between the two implementations.
//
// The function tries log sources in order based on availability and configuration:
// 1. Temporary logs from the builder instance (if nodeID is provided and source allows)
// 2. Persistent logs from backend storage (strategy provided by caller)
//
// It returns the first successful result, logging warnings for any failures encountered.
// This unified approach ensures consistent behavior and makes maintenance easier by centralizing
// the source selection and fallback logic.
func getBuildLogsWithSources(
	ctx context.Context,
	instances *smap.Map[*Instance],
	nodeID *string,
	templateID string,
	buildID string,
	offset int32,
	limit int32,
	level *logs.LogLevel,
	cursor *time.Time,
	direction api.LogsDirection,
	source *api.LogsSource,
	persistentLogFetcher logSourceFunc, // Backend-specific strategy for persistent logs (Loki for local, Edge API for remote)
) ([]logs.LogEntry, *api.APIError) {
	ctx, span := tracer.Start(ctx, "get build-logs")
	defer span.End()

	start, end := logQueryWindow(cursor, direction)

	var sources []logSourceFunc

	// Handle temporary logs from builder instance
	if nodeID != nil && logCheckSourceType(source, api.LogsSourceTemporary) {
		instance, found := instances.Get(*nodeID)
		if found {
			sourceCallback := logsFromBuilderInstance(ctx, instance, templateID, buildID, offset, limit, level, start, end, direction)
			sources = append(sources, sourceCallback)
		} else {
			logger.L().Warn(
				ctx, "Node instance not found for build logs, falling back to other sources",
				logger.WithNodeID(*nodeID),
				logger.WithTemplateID(templateID),
				logger.WithBuildID(buildID),
			)
		}
	}

	// Handle persistent logs (backend-specific implementation provided by caller)
	if logCheckSourceType(source, api.LogsSourcePersistent) {
		sources = append(sources, persistentLogFetcher)
	}

	// Iterate through sources and return the first successful fetch
	for _, sourceFetch := range sources {
		entries, err := sourceFetch()
		if err != nil {
			logger.L().Warn(ctx, "Error fetching build logs", logger.WithTemplateID(templateID), logger.WithBuildID(buildID), zap.Error(err))

			continue
		}

		return entries, nil
	}

	return nil, nil
}
