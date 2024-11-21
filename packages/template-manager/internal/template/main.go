package template

import (
	"context"
	"fmt"

	artifactregistry "cloud.google.com/go/artifactregistry/apiv1"
	"cloud.google.com/go/artifactregistry/apiv1/artifactregistrypb"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func GetDockerImageURL(templateID string) string {
	// DockerImagesURL is the URL to the docker images in the artifact registry
	return fmt.Sprintf("projects/%s/locations/%s/repositories/%s/packages/", consts.GCPProject, consts.GCPRegion, consts.DockerRegistry) + templateID
}

func Delete(
	ctx context.Context,
	tracer trace.Tracer,
	artifactRegistry *artifactregistry.Client,
	templateStorage *TemplateStorage,
	templateID string,
) error {
	childCtx, childSpan := tracer.Start(ctx, "delete-template")
	defer childSpan.End()

	err := templateStorage.Remove(ctx, templateID)
	if err != nil {
		return fmt.Errorf("error when deleting template objects: %w", err)
	}

	op, artifactRegistryDeleteErr := artifactRegistry.DeletePackage(ctx, &artifactregistrypb.DeletePackageRequest{Name: GetDockerImageURL(templateID)})
	if artifactRegistryDeleteErr != nil {
		errMsg := fmt.Errorf("error when deleting template image from registry: %w", artifactRegistryDeleteErr)
		telemetry.ReportCriticalError(childCtx, errMsg)
	} else {
		telemetry.ReportEvent(childCtx, "started deleting template image from registry")

		waitErr := op.Wait(childCtx)
		if waitErr != nil {
			errMsg := fmt.Errorf("error when waiting for template image deleting from registry: %w", waitErr)
			telemetry.ReportCriticalError(childCtx, errMsg)
		} else {
			telemetry.ReportEvent(childCtx, "deleted template image from registry")
		}
	}

	return nil
}
