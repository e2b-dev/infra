package artifacts_registry

import (
	"context"
	"fmt"

	"github.com/google/go-containerregistry/pkg/name"
	containerregistry "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/daemon"
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

func (g *LocalArtifactsRegistry) GetImage(ctx context.Context, tag string, _ containerregistry.Platform) (containerregistry.Image, error) {
	ref, err := name.ParseReference(tag)
	if err != nil {
		return nil, fmt.Errorf("invalid image reference: %w", err)
	}

	img, err := daemon.Image(ref, daemon.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to get image from local registry: %w", err)
	}

	return img, nil
}
