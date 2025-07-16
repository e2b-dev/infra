package logs

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/edge"
	edgeapi "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
)

type ClusterPlacementProvider struct {
	HTTP *edge.ClusterHTTP
}

func (c *ClusterPlacementProvider) GetLogs(ctx context.Context, templateID string, buildID string, offset *int32, level *api.LogLevel) ([]api.BuildLogEntry, error) {
	res, err := c.HTTP.Client.V1TemplateBuildLogsWithResponse(
		ctx, buildID, &edgeapi.V1TemplateBuildLogsParams{TemplateID: templateID, OrchestratorID: c.HTTP.NodeID, Offset: offset, Level: apiLevelToEdgeLevel(level)},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get build logs in template manager: %w", err)
	}

	if res.StatusCode() != 200 {
		zap.L().Error("failed to get build logs in template manager", zap.String("body", string(res.Body)))
		return nil, errors.New("failed to get build logs in template manager")
	}

	if res.JSON200 == nil {
		zap.L().Error("unexpected response from template manager", zap.String("body", string(res.Body)))
		return nil, errors.New("unexpected response from template manager")
	}

	logs := make([]api.BuildLogEntry, 0)
	for _, entry := range res.JSON200.LogEntries {
		logs = append(logs, api.BuildLogEntry{
			Timestamp: entry.Timestamp,
			Message:   entry.Message,
			Level:     edgeLevelToAPILevel(entry.Level),
		})
	}

	return logs, nil
}

var edgeToAPILevelMap = map[edgeapi.LogLevel]api.LogLevel{
	edgeapi.LogLevelDebug: api.LogLevelDebug,
	edgeapi.LogLevelInfo:  api.LogLevelInfo,
	edgeapi.LogLevelWarn:  api.LogLevelWarn,
	edgeapi.LogLevelError: api.LogLevelError,
}

func edgeLevelToAPILevel(level edgeapi.LogLevel) api.LogLevel {
	if apiLevel, ok := edgeToAPILevelMap[level]; ok {
		return apiLevel
	}
	return api.LogLevelInfo
}

func apiLevelToEdgeLevel(level *api.LogLevel) *edgeapi.LogLevel {
	if level == nil {
		return nil
	}

	for edge, apiVal := range edgeToAPILevelMap {
		if apiVal == *level {
			return &edge
		}
	}

	defaultLevel := edgeapi.LogLevelInfo
	return &defaultLevel
}
