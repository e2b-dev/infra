package template_manager

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/utils"
	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	ut "github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func (tm *TemplateManager) InitLayerFileUpload(ctx context.Context, teamID uuid.UUID, templateID string, hash string) (*template_manager.InitLayerFileUploadResponse, error) {
	resp, err := tm.grpc.Template.InitLayerFileUpload(
		ctx, &template_manager.InitLayerFileUploadRequest{
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
