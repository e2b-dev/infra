package logs

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/edge"
	edgeapi "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
)

type ClusterPlacementProvider struct {
	HTTP *edge.ClusterHTTP
}

func logToEdgeLevel(level *logs.LogLevel) *edgeapi.LogLevel {
	if level == nil {
		return nil
	}

	value := edgeapi.LogLevel(logs.LevelToString(*level))
	return &value
}

func (c *ClusterPlacementProvider) GetLogs(ctx context.Context, templateID string, buildID string, offset int32, level *logs.LogLevel) ([]logs.LogEntry, error) {
	res, err := c.HTTP.Client.V1TemplateBuildLogsWithResponse(
		ctx, buildID, &edgeapi.V1TemplateBuildLogsParams{TemplateID: templateID, OrchestratorID: c.HTTP.NodeID, Offset: &offset, Level: logToEdgeLevel(level)},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get build logs in template manager: %w", err)
	}

	if res.StatusCode() != 200 || res.JSON200 == nil {
		zap.L().Error("failed to get build logs in template manager", zap.String("body", string(res.Body)))
		return nil, errors.New("failed to get build logs in template manager")
	}

	l := make([]logs.LogEntry, 0)
	for _, entry := range res.JSON200.LogEntries {
		l = append(l, logs.LogEntry{
			Timestamp: entry.Timestamp,
			Message:   entry.Message,
			Level:     logs.StringToLevel(string(entry.Level)),
			Fields:    entry.Fields,
		})
	}

	return l, nil
}
