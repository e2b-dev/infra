package logs

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc/metadata"

	"github.com/e2b-dev/infra/packages/api/internal/edge"
	templatemanagergrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type TemplateManagerProvider struct {
	GRPC *edge.ClusterGRPC
}

func (t *TemplateManagerProvider) GetLogs(ctx context.Context, templateID string, buildID string, offset *int32) ([]string, error) {
	logs := make([]string, 0)

	reqCtx := metadata.NewOutgoingContext(ctx, t.GRPC.Metadata)
	res, err := t.GRPC.Client.Template.TemplateBuildStatus(
		reqCtx, &templatemanagergrpc.TemplateStatusRequest{
			TemplateID: templateID,
			BuildID:    buildID,
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
		zap.L().Error("error when returning logs for template build", zap.Error(err), logger.WithBuildID(buildID))
	}

	return logs, err
}
