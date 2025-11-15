package dockerhub

import (
	"context"
	"fmt"

	"github.com/google/go-containerregistry/pkg/name"
	containerregistry "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

type NoopRemoteRepository struct{}

func NewNoopRemoteRepository() *NoopRemoteRepository {
	return &NoopRemoteRepository{}
}

func (n *NoopRemoteRepository) GetImage(_ context.Context, tag string, platform containerregistry.Platform) (containerregistry.Image, error) {
	ref, err := name.ParseReference(tag)
	if err != nil {
		return nil, fmt.Errorf("invalid image reference: %w", err)
	}

	img, err := remote.Image(ref, remote.WithPlatform(platform))
	if err != nil {
		return nil, fmt.Errorf("error pulling image: %w", err)
	}

	return img, nil
}

func (n *NoopRemoteRepository) Close() error {
	return nil
}
