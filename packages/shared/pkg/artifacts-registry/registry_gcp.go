package artifacts_registry

import (
	"context"
	"encoding/base64"
	"encoding/json"
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
)

type GCPArtifactsRegistry struct {
	registry *artifactregistry.Client
}

var gcpAuthConfig = authn.Basic{
	Username: "_json_key_base64",
	Password: consts.GoogleServiceAccountSecret,
}

func NewGCPArtifactsRegistry(ctx context.Context) (*GCPArtifactsRegistry, error) {
	registry, err := artifactregistry.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("error creating artifact registry client: %v", err)
	}

	return &GCPArtifactsRegistry{registry: registry}, nil
}

func (g *GCPArtifactsRegistry) Delete(ctx context.Context, templateId string, buildId string) error {
	tagPath := g.getDockerImageTagPath(templateId, buildId)
	err := g.registry.DeleteTag(ctx, &artifactregistrypb.DeleteTagRequest{Name: tagPath})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return ErrImageNotExists
		}

		return fmt.Errorf("error deleting tag %s: %v", tagPath, err)
	}

	return nil
}

func (g *GCPArtifactsRegistry) GetTag(ctx context.Context, templateId string, buildId string) (string, error) {
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

	auth, err := g.getAuthToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get auth: %w", err)
	}

	img, err := remote.Image(ref, remote.WithAuth(auth), remote.WithPlatform(platform))
	if err != nil {
		return nil, fmt.Errorf("error pulling image: %w", err)
	}

	return img, nil
}

func (g *GCPArtifactsRegistry) GetLayer(ctx context.Context, buildId string, layerHash string, platform containerregistry.Platform) (containerregistry.Image, error) {
	imageUrl, err := g.GetTag(ctx, buildId, layerHash)
	if err != nil {
		return nil, fmt.Errorf("failed to get image URL: %w", err)
	}

	ref, err := name.ParseReference(imageUrl)
	if err != nil {
		return nil, fmt.Errorf("invalid image reference: %w", err)
	}

	auth, err := g.getAuthToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get auth: %w", err)
	}

	img, err := remote.Image(ref, remote.WithAuth(auth), remote.WithPlatform(platform))
	if err != nil {
		return nil, fmt.Errorf("error pulling image: %w", err)
	}

	return img, nil
}

func (g *GCPArtifactsRegistry) PushLayer(ctx context.Context, buildId string, layerHash string, layer containerregistry.Image) error {
	imageUrl, err := g.GetTag(ctx, buildId, layerHash)
	if err != nil {
		return fmt.Errorf("failed to get image URL: %w", err)
	}

	ref, err := name.ParseReference(imageUrl)
	if err != nil {
		return fmt.Errorf("invalid image reference: %w", err)
	}

	auth, err := g.getAuthToken(ctx)
	if err != nil {
		return fmt.Errorf("failed to get auth: %w", err)
	}

	if err := remote.Write(ref, layer, remote.WithAuth(auth)); err != nil {
		return fmt.Errorf("error pushing layer: %w", err)
	}

	return nil
}

func (g *GCPArtifactsRegistry) getAuthToken(ctx context.Context) (*authn.Basic, error) {
	authCfg := consts.DockerAuthConfig
	if authCfg == "" {
		return &gcpAuthConfig, nil
	}

	decoded, err := base64.URLEncoding.DecodeString(authCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to decode auth config: %w", err)
	}

	var cfg struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}

	if err := json.Unmarshal(decoded, &cfg); err != nil {
		return nil, fmt.Errorf("invalid JSON auth config: %w", err)
	}

	return &authn.Basic{
		Username: cfg.Username,
		Password: cfg.Password,
	}, nil
}

func (g *GCPArtifactsRegistry) getDockerImagePath(templateId string) string {
	// DockerImagesURL is the URL to the docker images in the artifact registry
	return fmt.Sprintf("projects/%s/locations/%s/repositories/%s/packages/%s", consts.GCPProject, consts.GCPRegion, consts.DockerRegistry, templateId)
}

func (g *GCPArtifactsRegistry) getDockerImageTagPath(templateId string, buildId string) string {
	return fmt.Sprintf("%s/tags/%s", g.getDockerImagePath(templateId), buildId)
}
