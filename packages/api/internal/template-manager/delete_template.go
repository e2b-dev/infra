package template_manager

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/utils"
	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

func (tm *TemplateManager) DeleteBuild(ctx context.Context, buildId uuid.UUID) error {
	_, err := tm.grpc.Client.TemplateBuildDelete(
		ctx, &template_manager.TemplateBuildDeleteRequest{
			BuildID: buildId.String(),
		},
	)

	err = utils.UnwrapGRPCError(err)
	if err != nil {
		return fmt.Errorf("failed to delete env build '%s': %w", buildId.String(), err)
	}

	return nil
}

func (tm *TemplateManager) DeleteBuilds(ctx context.Context, buildIds []uuid.UUID) error {
	for _, buildId := range buildIds {
		err := tm.DeleteBuild(ctx, buildId)
		if err != nil {
			return fmt.Errorf("failed to delete env build '%s': %w", buildId, err)
		}
	}

	return nil
}
