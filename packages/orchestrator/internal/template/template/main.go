package template

import (
	"context"
	"errors"
	"fmt"

	"go.opentelemetry.io/otel/trace"

	artefactsregistry "github.com/e2b-dev/infra/packages/shared/pkg/artefacts-registry"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func Delete(ctx context.Context, tracer trace.Tracer, artifactRegistry artefactsregistry.ArtefactsRegistry, templateStorage *Storage, templateId string, buildId string) error {
	childCtx, childSpan := tracer.Start(ctx, "delete-template")
	defer childSpan.End()

	err := templateStorage.Remove(ctx, buildId)
	if err != nil {
		return fmt.Errorf("error when deleting template objects: %w", err)
	}

	err = artifactRegistry.Delete(childCtx, templateId, buildId)
	if err != nil {
		// snapshot build are not stored in docker repository
		if errors.Is(err, artefactsregistry.ErrImageNotExists) {
			return nil
		}

		telemetry.ReportEvent(childCtx, err.Error())
		return err
	}

	return nil
}
