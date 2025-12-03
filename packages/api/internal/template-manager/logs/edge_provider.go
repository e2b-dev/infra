package logs

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/edge"
	edgeapi "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type EdgeProvider struct {
	HTTP *edge.ClusterHTTP
}

func logToEdgeLevel(level *logs.LogLevel) *edgeapi.LogLevel {
	if level == nil {
		return nil
	}

	value := edgeapi.LogLevel(logs.LevelToString(*level))

	return &value
}

func (c *EdgeProvider) GetLogs(ctx context.Context, templateID string, buildID string, offset int32, level *logs.LogLevel) ([]logs.LogEntry, error) {
	res, err := c.HTTP.Client.V1TemplateBuildLogsWithResponse(
		ctx, buildID, &edgeapi.V1TemplateBuildLogsParams{
			TemplateID: templateID,
			Offset:     &offset,
			Level:      logToEdgeLevel(level),
			// TODO: remove this once the API spec is not required to have orchestratorID (https://linear.app/e2b/issue/ENG-3352)
			OrchestratorID: utils.ToPtr("unused"),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get build logs in template manager: %w", err)
	}

	if res.StatusCode() != 200 || res.JSON200 == nil {
		logger.L().Error(ctx, "failed to get build logs in template manager", zap.String("body", string(res.Body)))

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
