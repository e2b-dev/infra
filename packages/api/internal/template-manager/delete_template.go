package template_manager

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/api/internal/utils"
	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/google/uuid"
)

func (tm *TemplateManager) DeleteBuild(ctx context.Context, templateId string, buildId uuid.UUID) error {
	_, err := tm.grpc.Client.TemplateBuildDelete(ctx, &template_manager.TemplateBuildDeleteRequest{
		BuildID:    buildId.String(),
		TemplateID: templateId,
	})

	err = utils.UnwrapGRPCError(err)
	if err != nil {
		return fmt.Errorf("failed to delete env build '%s': %w", buildId.String(), err)
	}

	return nil
}

func (tm *TemplateManager) DeleteBuilds(ctx context.Context, templateId string, buildIds []uuid.UUID) error {
	for _, buildId := range buildIds {
		err := tm.DeleteBuild(ctx, templateId, buildId)
		if err != nil {
			return fmt.Errorf("failed to delete env build '%s': %w", buildId, err)
		}
	}

	return nil
}
