package artifacts_registry

import (
	"context"
	"fmt"

	"github.com/google/go-containerregistry/pkg/name"
	containerregistry "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/daemon"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

type LocalArtifactsRegistry struct{}

func NewLocalArtifactsRegistry() (*LocalArtifactsRegistry, error) {
	return &LocalArtifactsRegistry{}, nil
}

func (g *LocalArtifactsRegistry) Delete(ctx context.Context, templateId string, buildId string) error {
	// for now, just assume local image can be deleted manually
	return nil
}

func (g *LocalArtifactsRegistry) GetTag(ctx context.Context, templateId string, buildId string) (string, error) {
	return fmt.Sprintf("%s:%s", templateId, buildId), nil
}

func (g *LocalArtifactsRegistry) GetImage(ctx context.Context, templateId string, buildId string, platform containerregistry.Platform) (containerregistry.Image, error) {
	imageUrl, err := g.GetTag(ctx, templateId, buildId)
	if err != nil {
		return nil, fmt.Errorf("failed to get image URL: %w", err)
	}

	ref, err := name.ParseReference(imageUrl)
	if err != nil {
		return nil, fmt.Errorf("invalid image reference: %w", err)
	}

	img, err := daemon.Image(ref, daemon.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to get image from local registry: %w", err)
	}

	img = mutate.MediaType(img, types.OCIManifestSchema1)
	img = mutate.ConfigMediaType(img, types.OCIConfigJSON)
	return img, nil
}
