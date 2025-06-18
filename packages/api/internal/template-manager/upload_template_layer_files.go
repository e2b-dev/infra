package template_manager

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/api/internal/utils"
	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

func (tm *TemplateManager) UploadLayerFiles(ctx context.Context, templateId string, hash string) (*template_manager.TemplateLayerFilesUploadResponse, error) {
	resp, err := tm.grpc.TemplateClient.TemplateLayerFilesUpload(
		ctx, &template_manager.TemplateLayerFilesUploadRequest{
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
