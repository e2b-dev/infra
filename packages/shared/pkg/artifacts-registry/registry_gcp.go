package artifacts_registry

import (
	"context"
	"fmt"

	artifactregistry "cloud.google.com/go/artifactregistry/apiv1"
	"cloud.google.com/go/artifactregistry/apiv1/artifactregistrypb"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	containerregistry "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/gcpauth"
)

type GCPArtifactsRegistry struct {
	registry *artifactregistry.Client
	auth     authn.Authenticator
}

func NewGCPArtifactsRegistry(ctx context.Context) (*GCPArtifactsRegistry, error) {
	registry, err := artifactregistry.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("error creating artifact registry client: %w", err)
	}

	auth, err := gcpauth.NewRegistryAuthenticator(ctx)
	if err != nil {
		_ = registry.Close()

		return nil, fmt.Errorf("create ADC Artifact Registry authenticator: %w", err)
	}

	return &GCPArtifactsRegistry{registry: registry, auth: auth}, nil
}

func (g *GCPArtifactsRegistry) Delete(ctx context.Context, templateId string, buildId string) error {
	tagPath := g.getDockerImageTagPath(templateId, buildId)
	err := g.registry.DeleteTag(ctx, &artifactregistrypb.DeleteTagRequest{Name: tagPath})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return ErrImageNotExists
		}

		return fmt.Errorf("error deleting tag %s: %w", tagPath, err)
	}

	return nil
}

func (g *GCPArtifactsRegistry) GetTag(_ context.Context, templateId string, buildId string) (string, error) {
	return fmt.Sprintf("%s-docker.pkg.dev/%s/%s/%s:%s", consts.GCPRegion, consts.GCPProject, consts.DockerRegistry, templateId, buildId), nil
}

func (g *GCPArtifactsRegistry) GetImage(ctx context.Context, templateId string, buildId string, platform containerregistry.Platform) (containerregistry.Image, error) {
	imageUrl, err := g.GetTag(ctx, templateId, buildId)
	if err != nil {
		return nil, fmt.Errorf("failed to get image URL: %w", err)
	}

	ref, err := name.ParseReference(imageUrl)
	if err != nil {
		return nil, fmt.Errorf("invalid image reference: %w", err)
	}

	img, err := remote.Image(ref, remote.WithAuth(g.auth), remote.WithPlatform(platform), remote.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("error pulling image: %w", err)
	}

	return img, nil
}

func (g *GCPArtifactsRegistry) getDockerImagePath(templateId string) string {
	// DockerImagesURL is the URL to the docker images in the artifact registry
	return fmt.Sprintf("projects/%s/locations/%s/repositories/%s/packages/%s", consts.GCPProject, consts.GCPRegion, consts.DockerRegistry, templateId)
}

func (g *GCPArtifactsRegistry) getDockerImageTagPath(templateId string, buildId string) string {
	return fmt.Sprintf("%s/tags/%s", g.getDockerImagePath(templateId), buildId)
}
