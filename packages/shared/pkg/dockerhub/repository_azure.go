package dockerhub

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	containerregistry "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/e2b-dev/infra/packages/shared/pkg/acr"
)

type AzureRemoteRepository struct {
	repositoryURL string
	authToken     authn.Authenticator
}

func NewAzureRemoteRepository(_ context.Context, repositoryURL string) (*AzureRemoteRepository, error) {
	credential, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure default credential: %w", err)
	}

	// The registry login server (e.g. myregistry.azurecr.io) is the host part
	// of the remote repository URL.
	loginServer, _, _ := strings.Cut(repositoryURL, "/")

	return &AzureRemoteRepository{
		repositoryURL: repositoryURL,
		authToken:     acr.NewAuthenticator(loginServer, credential),
	}, nil
}

func (g *AzureRemoteRepository) GetImage(_ context.Context, tag string, platform containerregistry.Platform) (containerregistry.Image, error) {
	tagWithoutRegistry, err := removeRegistryFromTag(tag)
	if err != nil {
		return nil, fmt.Errorf("error removing registry from tag: %w", err)
	}

	ref, err := name.ParseReference(g.repositoryURL + "/" + tagWithoutRegistry)
	if err != nil {
		return nil, fmt.Errorf("invalid image reference: %w", err)
	}

	img, err := remote.Image(ref, remote.WithAuth(g.authToken), remote.WithPlatform(platform))
	if err != nil {
		return nil, fmt.Errorf("error pulling image: %w", err)
	}

	return img, nil
}

func (g *AzureRemoteRepository) Close() error {
	return nil
}
