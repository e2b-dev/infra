package artifacts_registry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/containers/azcontainerregistry"
	"github.com/google/go-containerregistry/pkg/name"
	containerregistry "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/e2b-dev/infra/packages/shared/pkg/acr"
)

type AzureArtifactsRegistry struct {
	loginServer    string
	repositoryName string
	client         *azcontainerregistry.Client
	authenticator  *acr.Authenticator
}

var (
	AzureRegistryNameEnvVar = "AZURE_CONTAINER_REGISTRY_NAME"
	AzureRegistryName       = os.Getenv(AzureRegistryNameEnvVar)

	AzureRepositoryNameEnvVar = "AZURE_DOCKER_REPOSITORY_NAME"
	AzureRepositoryName       = os.Getenv(AzureRepositoryNameEnvVar)
)

func NewAzureArtifactsRegistry(_ context.Context) (*AzureArtifactsRegistry, error) {
	if AzureRegistryName == "" {
		return nil, fmt.Errorf("%s environment variable is not set", AzureRegistryNameEnvVar)
	}

	if AzureRepositoryName == "" {
		return nil, fmt.Errorf("%s environment variable is not set", AzureRepositoryNameEnvVar)
	}

	credential, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure default credential: %w", err)
	}

	loginServer := fmt.Sprintf("%s.azurecr.io", AzureRegistryName)

	client, err := azcontainerregistry.NewClient(fmt.Sprintf("https://%s", loginServer), credential, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating azure container registry client: %w", err)
	}

	return &AzureArtifactsRegistry{
		loginServer:    loginServer,
		repositoryName: AzureRepositoryName,
		client:         client,
		authenticator:  acr.NewAuthenticator(loginServer, credential),
	}, nil
}

func (g *AzureArtifactsRegistry) Delete(ctx context.Context, _ string, buildId string) error {
	// Resolve the tag to its manifest digest first — deleting only the tag
	// would leave the manifest (and its layers) behind in the registry.
	props, err := g.client.GetTagProperties(ctx, g.repositoryName, buildId, nil)
	if err != nil {
		if isAzureNotFound(err) {
			return ErrImageNotExists
		}

		return fmt.Errorf("failed to get tag properties from azure acr: %w", err)
	}

	if props.Tag == nil || props.Tag.Digest == nil {
		return errors.New("azure acr tag properties did not contain a digest")
	}

	_, err = g.client.DeleteManifest(ctx, g.repositoryName, *props.Tag.Digest, nil)
	if err != nil {
		if isAzureNotFound(err) {
			return ErrImageNotExists
		}

		return fmt.Errorf("failed to delete image from azure acr: %w", err)
	}

	return nil
}

func (g *AzureArtifactsRegistry) GetTag(_ context.Context, _ string, buildId string) (string, error) {
	// for Azure implementation we are using only build id as image tag
	return fmt.Sprintf("%s/%s:%s", g.loginServer, g.repositoryName, buildId), nil
}

func (g *AzureArtifactsRegistry) GetImage(ctx context.Context, templateId string, buildId string, platform containerregistry.Platform) (containerregistry.Image, error) {
	imageUrl, err := g.GetTag(ctx, templateId, buildId)
	if err != nil {
		return nil, fmt.Errorf("failed to get image URL: %w", err)
	}

	ref, err := name.ParseReference(imageUrl)
	if err != nil {
		return nil, fmt.Errorf("invalid image reference: %w", err)
	}

	img, err := remote.Image(ref, remote.WithAuth(g.authenticator), remote.WithPlatform(platform), remote.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("error pulling image: %w", err)
	}

	return img, nil
}

func isAzureNotFound(err error) bool {
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		return respErr.StatusCode == http.StatusNotFound
	}

	return false
}
