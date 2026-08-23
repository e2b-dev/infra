package dockerhub

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	containerregistry "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/e2b-dev/infra/packages/shared/pkg/acr"
	"github.com/e2b-dev/infra/packages/shared/pkg/azure"
)

type AzureRemoteRepository struct {
	repositoryURL string
	authToken     *acr.Authenticator
}

func NewAzureRemoteRepository(_ context.Context, repositoryURL string) (*AzureRemoteRepository, error) {
	// Explicit scheme check: the name package parses "https://host/repo" into
	// registry "https:" without error, even under strict validation.
	if strings.Contains(repositoryURL, "://") {
		return nil, fmt.Errorf("invalid Azure remote repository URL %q: want registry.host/repository without a scheme", repositoryURL)
	}

	repo, err := name.NewRepository(repositoryURL, name.StrictValidation)
	if err != nil {
		return nil, fmt.Errorf("invalid Azure remote repository URL %q (want registry.host/repository): %w", repositoryURL, err)
	}

	credential, err := azure.DefaultCredential()
	if err != nil {
		return nil, err
	}

	authenticator, err := acr.NewAuthenticator(repo.RegistryStr(), credential, nil)
	if err != nil {
		return nil, err
	}

	return &AzureRemoteRepository{
		repositoryURL: repositoryURL,
		authToken:     authenticator,
	}, nil
}

func (g *AzureRemoteRepository) GetImage(ctx context.Context, tag string, platform containerregistry.Platform) (containerregistry.Image, error) {
	tagWithoutRegistry, err := removeRegistryFromTag(tag)
	if err != nil {
		return nil, fmt.Errorf("error removing registry from tag: %w", err)
	}

	ref, err := name.ParseReference(g.repositoryURL + "/" + tagWithoutRegistry)
	if err != nil {
		return nil, fmt.Errorf("invalid image reference: %w", err)
	}

	img, err := remote.Image(ref, remote.WithContext(ctx), remote.WithAuth(g.authToken), remote.WithPlatform(platform))
	if acr.IsUnauthorized(err) {
		// The cached ACR token can go dead mid-TTL (role rotated); retry once with a fresh one.
		g.authToken.Invalidate()
		img, err = remote.Image(ref, remote.WithContext(ctx), remote.WithAuth(g.authToken), remote.WithPlatform(platform))
	}
	if err != nil {
		return nil, fmt.Errorf("error pulling image: %w", err)
	}

	return img, nil
}

func (g *AzureRemoteRepository) Close() error {
	return nil
}
