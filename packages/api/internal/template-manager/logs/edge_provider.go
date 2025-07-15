package logs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/edge"
	api "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
)

type ClusterPlacementProvider struct {
	HTTP *edge.ClusterHTTP
}

func (c *ClusterPlacementProvider) GetLogs(ctx context.Context, templateID string, buildID string, offset *int32) ([]LogEntry, error) {
	res, err := c.HTTP.Client.V1TemplateBuildLogsWithResponse(
		ctx, buildID, &api.V1TemplateBuildLogsParams{TemplateID: templateID, OrchestratorID: c.HTTP.NodeID, Offset: offset},
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

	logs := make([]LogEntry, 0)
	for _, entry := range res.JSON200.Logs {
		logs = append(logs, LogEntry{
			Timestamp: time.Now(),
			Message:   entry,
			Level:     "debug",
		})
	}

	return logs, nil
}
