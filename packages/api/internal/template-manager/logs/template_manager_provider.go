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
	reqCtx := metadata.NewOutgoingContext(ctx, t.GRPC.Metadata)
	res, err := t.GRPC.Client.Template.TemplateBuildStatus(
		reqCtx, &templatemanagergrpc.TemplateStatusRequest{
			TemplateID: templateID,
			BuildID:    buildID,
			Offset:     offset,
		},
	)

	if err != nil {
		telemetry.ReportError(ctx, "error when returning logs for template build", err)
		zap.L().Error("error when returning logs for template build", zap.Error(err), logger.WithBuildID(buildID))
		return nil, err
	}

	logs := res.GetLogs()
	// Add an extra newline
	for i := range res.GetLogs() {
		logs[i] += "\n"
	}

	return logs, nil
}
