package template_manager

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/edge"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	tempaltemanagergrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	api "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
)

type BuildPlacement interface {
	StartBuild(ctx context.Context, req *tempaltemanagergrpc.TemplateCreateRequest) error
	DeleteBuild(ctx context.Context, buildId string, templateId string) error

	GetStatus(ctx context.Context, buildId string, templateId string) (*tempaltemanagergrpc.TemplateBuildStatusResponse, error)
}

type LocalBuildPlacement struct {
	client *GRPCClient
}

func NewLocalBuildPlacement(client *GRPCClient) *LocalBuildPlacement {
	return &LocalBuildPlacement{
		client: client,
	}
}

func (l *LocalBuildPlacement) GetStatus(ctx context.Context, buildId string, templateId string) (*tempaltemanagergrpc.TemplateBuildStatusResponse, error) {
	status, err := l.client.TemplateClient.TemplateBuildStatus(ctx, &tempaltemanagergrpc.TemplateStatusRequest{TemplateID: templateId, BuildID: buildId})
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

func (l *LocalBuildPlacement) StartBuild(ctx context.Context, req *tempaltemanagergrpc.TemplateCreateRequest) error {
	_, err := l.client.TemplateClient.TemplateCreate(ctx, req)
	err = utils.UnwrapGRPCError(err)
	if err != nil {
		return fmt.Errorf("failed to create template '%s': %w", req.Template.TemplateID, err)
	}

	return nil
}

func (l *LocalBuildPlacement) DeleteBuild(ctx context.Context, buildId string, templateId string) error {
	_, err := l.client.TemplateClient.TemplateBuildDelete(
		ctx, &tempaltemanagergrpc.TemplateBuildDeleteRequest{
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

// ---
// clustered
// ---

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
