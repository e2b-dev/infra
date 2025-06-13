package template_manager

import (
	"context"
	"errors"
	"fmt"
	"github.com/e2b-dev/infra/packages/api/internal/edge"
	tempaltemanagergrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	api "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	"go.uber.org/zap"
)

type ClusteredBuildPlacement struct {
	cluster        *edge.Cluster
	orchestratorId string
}

func NewClusteredBuildPlacement(cluster *edge.Cluster, orchestratorId string) *ClusteredBuildPlacement {
	return &ClusteredBuildPlacement{
		cluster:        cluster,
		orchestratorId: orchestratorId,
	}
}

func (c *ClusteredBuildPlacement) GetStatus(ctx context.Context, buildId string, templateId string) (*tempaltemanagergrpc.TemplateBuildStatusResponse, error) {
	res, err := c.cluster.Client.V1TemplateBuildStatusWithResponse(ctx, buildId, &api.V1TemplateBuildStatusParams{OrchestratorId: c.orchestratorId, TemplateId: templateId})
	if err != nil {
		return nil, fmt.Errorf("failed to get build status from template manager: %w", err)
	}

	if res.JSON200 == nil {
		zap.L().Error("failed to get build status from template manager", zap.String("body", string(res.Body)))
		return nil, fmt.Errorf("failed to get build status from template manager")
	}

	var status *tempaltemanagergrpc.TemplateBuildStatusResponse

	switch res.JSON200.Status {
	case api.TemplateBuildStatusResponseStatusBuilding:
		status = &tempaltemanagergrpc.TemplateBuildStatusResponse{
			Status:   tempaltemanagergrpc.TemplateBuildState_Building,
			Metadata: nil,
		}
	case api.TemplateBuildStatusResponseStatusError:
		status = &tempaltemanagergrpc.TemplateBuildStatusResponse{
			Status:   tempaltemanagergrpc.TemplateBuildState_Failed,
			Metadata: nil,
		}
	case api.TemplateBuildStatusResponseStatusReady:
		var metadata *tempaltemanagergrpc.TemplateBuildMetadata

		if res.JSON200.Metadata != nil {
			metadata = &tempaltemanagergrpc.TemplateBuildMetadata{
				RootfsSizeKey:  res.JSON200.Metadata.RootfsSizeKey,
				EnvdVersionKey: res.JSON200.Metadata.EnvdVersionKey,
			}
		}

		status = &tempaltemanagergrpc.TemplateBuildStatusResponse{
			Status:   tempaltemanagergrpc.TemplateBuildState_Completed,
			Metadata: metadata,
		}
	default:
		return nil, fmt.Errorf("unknown build status: %s", res.JSON200.Status)
	}

	return status, nil
}

func (c *ClusteredBuildPlacement) StartBuild(ctx context.Context, req *tempaltemanagergrpc.TemplateCreateRequest) error {
	res, err := c.cluster.Client.V1TemplateBuildCreateWithResponse(
		ctx,
		api.V1TemplateBuildCreateJSONRequestBody{
			OrchestratorId: c.orchestratorId,
			BuildId:        req.Template.BuildID,
			TemplateId:     req.Template.TemplateID,

			FirecrackerVersion: req.Template.FirecrackerVersion,
			KernelVersion:      req.Template.KernelVersion,
			HugePages:          req.Template.HugePages,

			ReadyCommand: req.Template.ReadyCommand,
			StartCommand: req.Template.StartCommand,

			DiskSizeMB: int64(req.Template.DiskSizeMB),
			RamMB:      int64(req.Template.MemoryMB),
			VCPU:       int64(req.Template.VCpuCount),
		},
	)

	if err != nil {
		return fmt.Errorf("failed to start build in template manager: %w", err)
	}

	if res.StatusCode() != 200 {
		zap.L().Error("failed to start build in template manager", zap.String("body", string(res.Body)))
		return errors.New("failed to start build in template manager")
	}

	return nil
}

func (c *ClusteredBuildPlacement) DeleteBuild(ctx context.Context, buildId string, templateId string) error {
	res, err := c.cluster.Client.V1TemplateBuildDeleteWithResponse(
		ctx, buildId, &api.V1TemplateBuildDeleteParams{
			TemplateId: templateId, OrchestratorId: c.orchestratorId,
		},
	)

	if err != nil {
		return fmt.Errorf("failed to delete build in template manager: %w", err)
	}

	if res.StatusCode() != 200 {
		zap.L().Error("failed to delete build in template manager", zap.String("body", string(res.Body)))
		return errors.New("failed to delete build in template manager")
	}

	return nil
}

func (c *ClusteredBuildPlacement) GetLogs(ctx context.Context, buildId string, templateId string, offset *int32) (*[]string, error) {
	res, err := c.cluster.Client.V1TemplateBuildLogsWithResponse(
		ctx, buildId, &api.V1TemplateBuildLogsParams{
			TemplateId: templateId, OrchestratorId: c.orchestratorId, Offset: offset,
		},
	)

	if err != nil {
		return nil, fmt.Errorf("failed to get build logs in template manager: %w", err)
	}

	if res.StatusCode() != 200 {
		zap.L().Error("failed to get build logs in template manager", zap.String("body", string(res.Body)))
		return nil, errors.New("failed to get build logs in template manager")
	}

	return &res.JSON200.Logs, nil
}
