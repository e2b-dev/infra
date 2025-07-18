package template_manager

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/api/internal/utils"
	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

func (tm *TemplateManager) InitLayerFileUpload(ctx context.Context, templateId string, hash string) (*template_manager.InitLayerFileUploadResponse, error) {
	resp, err := tm.grpc.Template.InitLayerFileUpload(
		ctx, &template_manager.InitLayerFileUploadRequest{
			TemplateID: templateId,
			Hash:       hash,
		},
	)

	err = utils.UnwrapGRPCError(err)
	if err != nil {
		return nil, fmt.Errorf("failed to request layer files upload for template '%s' with hash '%s': %w", templateId, hash, err)
	}

	return resp, nil
}
