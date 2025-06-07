package template_manager

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/utils"
	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

func (tm *TemplateManager) DeleteBuild(ctx context.Context, buildId uuid.UUID, templateId string) error {
	_, err := tm.grpc.TemplateClient.TemplateBuildDelete(
		ctx, &template_manager.TemplateBuildDeleteRequest{
			BuildID:    buildId.String(),
			TemplateID: templateId,
		},
	)

	err = utils.UnwrapGRPCError(err)
	if err != nil {
		return fmt.Errorf("failed to delete env build '%s': %w", buildId.String(), err)
	}

	return nil
}

type DeleteBuild struct {
	BuildID    uuid.UUID
	TemplateId string
}

func (tm *TemplateManager) DeleteBuilds(ctx context.Context, builds []DeleteBuild) error {
	for _, build := range builds {
		err := tm.DeleteBuild(ctx, build.BuildID, build.TemplateId)
		if err != nil {
			return fmt.Errorf("failed to delete env build '%s': %w", build.BuildID, err)
		}
	}

	return nil
}
