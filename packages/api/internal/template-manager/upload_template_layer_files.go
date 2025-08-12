package template_manager

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"google.golang.org/grpc/metadata"

	"github.com/e2b-dev/infra/packages/api/internal/utils"
	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	ut "github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func (tm *TemplateManager) InitLayerFileUpload(ctx context.Context, clusterID *uuid.UUID, nodeID string, teamID uuid.UUID, templateID string, hash string) (*template_manager.InitLayerFileUploadResponse, error) {
	client, err := tm.GetBuildClient(clusterID, &nodeID, true)
	if err != nil {
		return nil, fmt.Errorf("failed to get build client for template '%s': %w", templateID, err)
	}

	reqCtx := metadata.NewOutgoingContext(ctx, client.GRPC.Metadata)
	resp, err := client.GRPC.Client.Template.InitLayerFileUpload(
		reqCtx, &template_manager.InitLayerFileUploadRequest{
			CacheScope: ut.ToPtr(teamID.String()),
			TemplateID: templateID,
			Hash:       hash,
		},
	)

	err = utils.UnwrapGRPCError(err)
	if err != nil {
		return nil, fmt.Errorf("failed to request layer files upload for template '%s' with hash '%s': %w", templateID, hash, err)
	}

	return resp, nil
}
