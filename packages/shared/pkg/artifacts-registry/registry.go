package artifacts_registry

import (
	"context"
	"errors"
	"fmt"
	"time"

	containerregistry "github.com/google/go-containerregistry/pkg/v1"

	"github.com/e2b-dev/infra/packages/shared/pkg/env"
)

type RegistryProvider string

const (
	GCPStorageProvider   RegistryProvider = "GCP_ARTIFACTS"
	AWSStorageProvider   RegistryProvider = "AWS_ECR"
	LocalStorageProvider RegistryProvider = "Local"

	DefaultRegistryProvider RegistryProvider = GCPStorageProvider

	storageProviderEnv = "ARTIFACTS_REGISTRY_PROVIDER"
)

var ErrImageNotExists = errors.New("image does not exist")

type ArtifactsRegistry interface {
	GetTag(ctx context.Context, templateId string, buildId string) (string, error)
	GetImage(ctx context.Context, templateId string, buildId string, platform containerregistry.Platform) (containerregistry.Image, error)
	GetLayer(ctx context.Context, buildId string, layerHash string, platform containerregistry.Platform) (containerregistry.Image, error)
	PushLayer(ctx context.Context, buildId string, layerHash string, layer containerregistry.Image) error
	Delete(ctx context.Context, templateId string, buildId string) error
}

func GetArtifactsRegistryProvider() (ArtifactsRegistry, error) {
	provider := RegistryProvider(env.GetEnv(storageProviderEnv, string(DefaultRegistryProvider)))

	setupCtx, setupCtxCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer setupCtxCancel()

	switch provider {
	case AWSStorageProvider:
		return NewAWSArtifactsRegistry(setupCtx)
	case GCPStorageProvider:
		return NewGCPArtifactsRegistry(setupCtx)
	case LocalStorageProvider:
		return NewLocalArtifactsRegistry()
	}

	return nil, fmt.Errorf("unknown artifacts registry provider: %s", provider)
}
