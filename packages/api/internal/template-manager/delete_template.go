package template_manager

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/api/internal/utils"
	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/google/uuid"
)

func (tm *TemplateManager) DeleteInstance(ctx context.Context, buildId uuid.UUID) error {
	_, err := tm.grpc.Client.TemplateDelete(ctx, &template_manager.TemplateDeleteRequest{
		TemplateID: buildId.String(),
	})

	err = utils.UnwrapGRPCError(err)
	if err != nil {
		return fmt.Errorf("failed to delete template '%s': %w", buildId.String(), err)
	}

	return nil
}

func (tm *TemplateManager) DeleteInstances(ctx context.Context, buildIds []uuid.UUID) error {
	for _, buildId := range buildIds {
		err := tm.DeleteInstance(ctx, buildId)
		if err != nil {
			return fmt.Errorf("failed to delete template '%s': %w", buildId, err)
		}
	}

	return nil
}
