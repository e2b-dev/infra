package logs

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"go.uber.org/zap"

	template_manager "github.com/e2b-dev/infra/packages/api/internal/template-manager"
	api "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
)

type ClusterPlacementProvider struct {
	TemplateManager *template_manager.TemplateManager
}

func (c *ClusterPlacementProvider) GetLogs(ctx context.Context, templateID string, buildUUID uuid.UUID, clusterID *uuid.UUID, clusterNodeID *string, offset *int32) ([]string, error) {
	_, http, err := c.TemplateManager.GetBuilderClient(clusterID, clusterNodeID, false)
	if err != nil {
		return nil, fmt.Errorf("failed to get builder edgeHttpClient: %w", err)
	}

	res, err := http.Client.V1TemplateBuildLogsWithResponse(
		ctx, buildUUID.String(), &api.V1TemplateBuildLogsParams{TemplateID: templateID, OrchestratorID: http.NodeID, Offset: offset},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get build logs in template manager: %w", err)
	}

	if res.StatusCode() != 200 {
		zap.L().Error("failed to get build logs in template manager", zap.String("body", string(res.Body)))
		return nil, errors.New("failed to get build logs in template manager")
	}

	return res.JSON200.Logs, nil
}
