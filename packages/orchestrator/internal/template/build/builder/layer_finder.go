package builder

import (
	"context"
	"fmt"

	containerregistry "github.com/google/go-containerregistry/pkg/v1"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/templateconfig"
	artifactsregistry "github.com/e2b-dev/infra/packages/shared/pkg/artifacts-registry"
)

// findLastCachedLayer finds the last cached layer in the artifact registry using binary search.
func findLastCachedLayer(
	ctx context.Context,
	tracer trace.Tracer,
	artifactRegistry artifactsregistry.ArtifactsRegistry,
	template *templateconfig.TemplateConfig,
) (string, containerregistry.Image, error) {
	ctx, span := tracer.Start(ctx, "find-last-cached-layer")
	defer span.End()

	if len(template.Steps) == 0 {
		return "", nil, fmt.Errorf("template %s has no steps defined", template.TemplateId)
	}

	platform := containerregistry.Platform{
		OS:           "linux",
		Architecture: "amd64",
	}

	// Binary search to find the last cached layer
	left, right := 0, len(template.Steps)-1
	lastCachedIndex := -1
	var lastCachedImage containerregistry.Image

	for left <= right {
		mid := (left + right) / 2
		step := template.Steps[mid]

		// Check if this layer exists in the artifact registry
		img, err := artifactRegistry.GetLayer(ctx, template.TemplateId, step.Hash, platform)
		if err != nil {
			// Layer doesn't exist, search in the left half
			right = mid - 1
		} else {
			// Layer exists, this could be our answer, search in the right half for a later one
			lastCachedIndex = mid
			lastCachedImage = img
			left = mid + 1
		}
	}

	if lastCachedIndex == -1 {
		return "", nil, fmt.Errorf("no cached layers found for template %s", template.TemplateId)
	}

	return template.Steps[lastCachedIndex].Hash, lastCachedImage, nil
}
