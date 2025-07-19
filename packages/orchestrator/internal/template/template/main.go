package template

import (
	"context"
	"errors"
	"fmt"

	"go.opentelemetry.io/otel/trace"

	artifactsregistry "github.com/e2b-dev/infra/packages/shared/pkg/artifacts-registry"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func Delete(ctx context.Context, tracer trace.Tracer, artifactRegistry artifactsregistry.ArtifactsRegistry, templateStorage storage.StorageProvider, templateId string, buildId string) error {
	childCtx, childSpan := tracer.Start(ctx, "delete-template")
	defer childSpan.End()

	err := templateStorage.DeleteObjectsWithPrefix(ctx, buildId)
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
