package dockerhub

import (
	"context"
	"fmt"

	artifactregistry "cloud.google.com/go/artifactregistry/apiv1"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	containerregistry "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/e2b-dev/infra/packages/shared/pkg/gcpauth"
)

type GCPRemoteRepository struct {
	repositoryURL string
	registry      *artifactregistry.Client
	authToken     authn.Authenticator
}

func NewGCPRemoteRepository(ctx context.Context, repositoryURL string) (*GCPRemoteRepository, error) {
	registry, err := artifactregistry.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("error creating artifact registry client: %w", err)
	}

	authToken, err := gcpauth.NewRegistryAuthenticator(ctx)
	if err != nil {
		_ = registry.Close()

		return nil, fmt.Errorf("error getting auth token: %w", err)
	}

	return &GCPRemoteRepository{repositoryURL: repositoryURL, registry: registry, authToken: authToken}, nil
}

func (g *GCPRemoteRepository) GetImage(ctx context.Context, tag string, platform containerregistry.Platform) (containerregistry.Image, error) {
	tagWithoutRegistry, err := removeRegistryFromTag(tag)
	if err != nil {
		return nil, fmt.Errorf("error removing registry from tag: %w", err)
	}

	ref, err := name.ParseReference(g.repositoryURL + "/" + tagWithoutRegistry)
	if err != nil {
		return nil, fmt.Errorf("invalid image reference: %w", err)
	}

	img, err := remote.Image(ref, remote.WithAuth(g.authToken), remote.WithPlatform(platform), remote.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("error pulling image: %w", err)
	}

	return img, nil
}

func (g *GCPRemoteRepository) Close() error {
	return g.registry.Close()
}
