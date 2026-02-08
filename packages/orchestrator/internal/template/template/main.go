package template

import (
	"context"
	"errors"
	"fmt"

	"go.opentelemetry.io/otel"

	artifactsregistry "github.com/e2b-dev/infra/packages/shared/pkg/artifacts-registry"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/internal/template/template")

func Delete(ctx context.Context, artifactRegistry artifactsregistry.ArtifactsRegistry, templateStorage storage.StorageProvider, templateId string, buildId string) error {
	childCtx, childSpan := tracer.Start(ctx, "delete-template")
	defer childSpan.End()

	err := templateStorage.DeleteWithPrefix(ctx, buildId)
	if err != nil {
		return fmt.Errorf("error when deleting template objects: %w", err)
	}

	err = artifactRegistry.Delete(childCtx, templateId, buildId)
	if err != nil {
		// snapshot build are not stored in docker repository
		if errors.Is(err, artifactsregistry.ErrImageNotExists) {
			return nil
		}

		telemetry.ReportEvent(childCtx, err.Error())

		return err
	}

	return nil
}
