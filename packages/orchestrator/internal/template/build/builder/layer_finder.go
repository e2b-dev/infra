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
	platform containerregistry.Platform,
) (string, containerregistry.Image, error) {
	ctx, span := tracer.Start(ctx, "find-last-cached-layer")
	defer span.End()

	if len(template.Steps) == 0 {
		return "", nil, fmt.Errorf("template %s has no steps defined", template.TemplateID)
	}

	// Binary search to find the last cached layer
	left, right := 0, len(template.Steps)-1
	lastCachedIndex := -1
	var lastCachedImage containerregistry.Image

	for left <= right {
		mid := (left + right) / 2
		step := template.Steps[mid]

		// Check if this layer exists in the artifact registry
		isForced := step.Force != nil && *step.Force
		img, err := artifactRegistry.GetLayer(ctx, template.TemplateID, step.Hash, platform)
		if err != nil || isForced {
			// Layer doesn't exist or is forced to rebuild, search in the left half
			right = mid - 1
		} else {
			// Layer exists, this could be our answer, search in the right half for a later one
			lastCachedIndex = mid
			lastCachedImage = img
			left = mid + 1
		}
	}

	if lastCachedIndex == -1 {
		return "", nil, fmt.Errorf("no cached layers found for template %s", template.TemplateID)
	}

	return template.Steps[lastCachedIndex].Hash, lastCachedImage, nil
}
