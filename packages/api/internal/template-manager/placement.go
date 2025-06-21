package template_manager

import (
	"context"
	"fmt"
	"time"

	"github.com/pkg/errors"
	"go.uber.org/zap"
	"google.golang.org/grpc/metadata"

	"github.com/e2b-dev/infra/packages/api/internal/orchestrator"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	tempaltemanagergrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

type BuildPlacement struct {
	client       *orchestrator.GRPCClient
	metadata     metadata.MD
	logsProvider PlacementLogsProvider
}

const (
	templateBuildLogsLimit       = 1_000
	templateBuildOldestLogsLimit = 24 * time.Hour // 1 day
)

func NewBuildPlacement(client *orchestrator.GRPCClient, metadata metadata.MD, logsProvider PlacementLogsProvider) *BuildPlacement {
	return &BuildPlacement{
		client:       client,
		metadata:     metadata,
		logsProvider: logsProvider,
	}
}

func (l *BuildPlacement) GetStatus(ctx context.Context, buildId string, templateId string) (*tempaltemanagergrpc.TemplateBuildStatusResponse, error) {
	reqCtx := metadata.NewOutgoingContext(ctx, l.metadata)

	status, err := l.client.Templates.TemplateBuildStatus(reqCtx, &tempaltemanagergrpc.TemplateStatusRequest{TemplateID: templateId, BuildID: buildId})
	if err != nil && errors.Is(err, context.DeadlineExceeded) {
		return nil, errors.Wrap(err, "context deadline exceeded")
	} else if err != nil { // retry only on context deadline exceeded
		zap.L().Error("terminal error when polling build status", zap.Error(err))
		return nil, newTerminalError(err)
	}

	if status == nil {
		return nil, errors.New("nil status") // this should never happen
	}

	return status, nil
}

func (l *BuildPlacement) StartBuild(ctx context.Context, req *tempaltemanagergrpc.TemplateCreateRequest) error {
	reqCtx := metadata.NewOutgoingContext(ctx, l.metadata)

	_, err := l.client.Templates.TemplateCreate(reqCtx, req)
	err = utils.UnwrapGRPCError(err)
	if err != nil {
		return fmt.Errorf("failed to create template '%s': %w", req.Template.TemplateID, err)
	}

	return nil
}

func (l *BuildPlacement) DeleteBuild(ctx context.Context, buildId string, templateId string) error {
	reqCtx := metadata.NewOutgoingContext(ctx, l.metadata)

	_, err := l.client.Templates.TemplateBuildDelete(
		reqCtx, &tempaltemanagergrpc.TemplateBuildDeleteRequest{
			BuildID:    buildId,
			TemplateID: templateId,
		},
	)

	err = utils.UnwrapGRPCError(err)
	if err != nil {
		return fmt.Errorf("failed to delete env build '%s': %w", buildId, err)
	}

	return nil
}

func (l *BuildPlacement) GetLogs(ctx context.Context, buildId string, templateId string, offset *int32) (*[]string, error) {
	return l.logsProvider.GetLogs(ctx, buildId, templateId, offset)
}
