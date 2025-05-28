package artefacts_registry

import (
	"context"
	"fmt"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/daemon"
)

type LocalArtefactsRegistry struct {
}

func NewLocalArtefactsRegistry() (*LocalArtefactsRegistry, error) {
	return &LocalArtefactsRegistry{}, nil
}

func (g *LocalArtefactsRegistry) Delete(ctx context.Context, templateId string, buildId string) error {
	// for now, just assume local image can be deleted manually
	return nil
}

func (g *LocalArtefactsRegistry) GetUrl(ctx context.Context, templateId string, buildId string) (string, error) {
	return fmt.Sprintf("%s:%s", templateId, buildId), nil
}

func (g *LocalArtefactsRegistry) GetImage(ctx context.Context, templateId string, buildId string, platform v1.Platform) (v1.Image, error) {
	imageUrl, err := g.GetUrl(ctx, templateId, buildId)
	if err != nil {
		return nil, fmt.Errorf("failed to get image URL: %w", err)
	}

	ref, err := name.ParseReference(imageUrl)
	if err != nil {
		return nil, fmt.Errorf("invalid image reference: %w", err)
	}

	return daemon.Image(ref, daemon.WithContext(ctx))
}
