package logs

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/grpc/metadata"

	template_manager "github.com/e2b-dev/infra/packages/api/internal/template-manager"
	templatemanagergrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type TemplateManagerProvider struct {
	TemplateManager *template_manager.TemplateManager
}

func (t *TemplateManagerProvider) GetLogs(ctx context.Context, templateID string, buildUUID uuid.UUID, clusterID *uuid.UUID, clusterNodeID *string, offset *int32) ([]string, error) {
	grpc, _, err := t.TemplateManager.GetBuilderClient(clusterID, clusterNodeID, false)
	if err != nil {
		return nil, fmt.Errorf("failed to get builder edgeHttpClient: %w", err)
	}

	logs := make([]string, 0)

	reqCtx := metadata.NewOutgoingContext(ctx, grpc.Metadata)
	res, err := grpc.Client.Template.TemplateBuildStatus(
		reqCtx, &templatemanagergrpc.TemplateStatusRequest{
			TemplateID: templateID,
			BuildID:    buildUUID.String(),
		},
	)
	if err == nil {
		logsCrawled := int32(0)

		for _, entry := range res.GetLogs() {
			logsCrawled++

			// does not support offset pagination, so we need to skip logs manually
			if offset != nil && logsCrawled <= *offset {
				continue
			}
			logs = append(logs, fmt.Sprintf("%s\n", entry))
		}
	} else {
		telemetry.ReportError(ctx, "error when returning logs for template build", err)
		zap.L().Error("error when returning logs for template build", zap.Error(err), logger.WithBuildID(buildUUID.String()))
	}

	return logs, err
}
